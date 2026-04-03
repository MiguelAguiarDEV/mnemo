package cloudstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// ─── Types ──────────────────────────────────────────────────────────────────

// Task represents a task row.
type Task struct {
	ID              int64    `json:"id"`
	UserID          string   `json:"user_id"`
	ParentID        *int64   `json:"parent_id,omitempty"`
	Title           string   `json:"title"`
	Description     *string  `json:"description,omitempty"`
	Status          string   `json:"status"`
	Priority        string   `json:"priority"`
	AssigneeType    string   `json:"assignee_type"`
	Assignee        *string  `json:"assignee,omitempty"`
	Source          string   `json:"source"`
	SourceSessionID *string  `json:"source_session_id,omitempty"`
	Project         *string  `json:"project,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	DueAt           *string  `json:"due_at,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	CompletedAt     *string  `json:"completed_at,omitempty"`
}

// CreateTaskParams holds parameters for creating a new task.
type CreateTaskParams struct {
	Title           string          `json:"title"`
	Description     string          `json:"description,omitempty"`
	Priority        string          `json:"priority,omitempty"`
	AssigneeType    string          `json:"assignee_type,omitempty"`
	Assignee        string          `json:"assignee,omitempty"`
	Source          string          `json:"source,omitempty"`
	SourceSessionID string          `json:"source_session_id,omitempty"`
	Project         string          `json:"project,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	DueAt           string          `json:"due_at,omitempty"`
	Metadata        json.RawMessage `json:"-"` // optional event payload on creation
}

// ListTasksOpts holds filter and pagination options for listing tasks.
type ListTasksOpts struct {
	Project  string `json:"project,omitempty"`
	Status   string `json:"status,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

// UpdateTaskParams holds fields that can be updated on a task.
type UpdateTaskParams struct {
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Status      *string  `json:"status,omitempty"`
	Priority    *string  `json:"priority,omitempty"`
	Assignee    *string  `json:"assignee,omitempty"`
	Project     *string  `json:"project,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DueAt       *string  `json:"due_at,omitempty"`
}

// ─── Status Transitions ────────────────────────────────────────────────────

// validTransitions defines allowed status transitions per TE-3.
// open → in_progress, blocked, cancelled
// in_progress → done, blocked, cancelled
// blocked → open, cancelled
// done and cancelled are terminal.
var validTransitions = map[string]map[string]bool{
	"open":        {"in_progress": true, "blocked": true, "cancelled": true},
	"in_progress": {"done": true, "blocked": true, "cancelled": true},
	"blocked":     {"open": true, "cancelled": true},
}

// ValidateTransition checks whether moving from oldStatus to newStatus is allowed.
func ValidateTransition(oldStatus, newStatus string) error {
	if oldStatus == newStatus {
		return nil
	}
	allowed, ok := validTransitions[oldStatus]
	if !ok {
		return fmt.Errorf("cloudstore: invalid current status %q", oldStatus)
	}
	if !allowed[newStatus] {
		return fmt.Errorf("cloudstore: invalid transition %s -> %s", oldStatus, newStatus)
	}
	return nil
}

// ─── Task CRUD ──────────────────────────────────────────────────────────────

// taskColumns is the SELECT column list for task queries.
const taskColumns = `id, user_id, parent_id, title, description, status, priority,
	assignee_type, assignee, source, source_session_id, project, tags,
	due_at, created_at, updated_at, completed_at`

// scanTask scans a single task row into a Task struct.
func scanTask(scanner interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	var dueAt, completedAt *time.Time
	err := scanner.Scan(
		&t.ID, &t.UserID, &t.ParentID, &t.Title, &t.Description,
		&t.Status, &t.Priority, &t.AssigneeType, &t.Assignee,
		&t.Source, &t.SourceSessionID, &t.Project, pq.Array(&t.Tags),
		&dueAt, &t.CreatedAt, &t.UpdatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}
	if dueAt != nil {
		s := dueAt.Format(time.RFC3339)
		t.DueAt = &s
	}
	if completedAt != nil {
		s := completedAt.Format(time.RFC3339)
		t.CompletedAt = &s
	}
	return &t, nil
}

// CreateTask inserts a new task and logs a "created" event.
func (cs *CloudStore) CreateTask(userID string, p CreateTaskParams) (int64, error) {
	priority := p.Priority
	if priority == "" {
		priority = "medium"
	}
	assigneeType := p.AssigneeType
	if assigneeType == "" {
		assigneeType = "user"
	}
	source := p.Source
	if source == "" {
		source = "user"
	}

	var dueAt *time.Time
	if p.DueAt != "" {
		t, err := time.Parse(time.RFC3339, p.DueAt)
		if err != nil {
			return 0, fmt.Errorf("cloudstore: parse due_at: %w", err)
		}
		dueAt = &t
	}

	var id int64
	err := cs.db.QueryRow(
		`INSERT INTO tasks
		 (user_id, title, description, status, priority, assignee_type, assignee,
		  source, source_session_id, project, tags, due_at)
		 VALUES ($1, $2, $3, 'open', $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id`,
		userID, p.Title, nullableString(p.Description), priority, assigneeType,
		nullableString(p.Assignee), source, nullableString(p.SourceSessionID),
		nullableString(p.Project), pq.Array(p.Tags), dueAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("cloudstore: create task: %w", err)
	}

	_ = cs.AddTaskEvent(id, userID, "created", nil)
	return id, nil
}

// GetTask returns a single task by ID, scoped to the user.
func (cs *CloudStore) GetTask(userID string, id int64) (*Task, error) {
	row := cs.db.QueryRow(
		fmt.Sprintf(`SELECT %s FROM tasks WHERE id = $1 AND user_id = $2`, taskColumns),
		id, userID,
	)
	t, err := scanTask(row)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: get task: %w", err)
	}
	return t, nil
}

// ListTasks returns tasks matching the given filters with pagination.
// Returns the task list and total count.
func (cs *CloudStore) ListTasks(userID string, opts ListTasksOpts) ([]Task, int, error) {
	where := "WHERE user_id = $1 AND parent_id IS NULL"
	args := []any{userID}
	argN := 2

	if opts.Project != "" {
		where += fmt.Sprintf(" AND project = $%d", argN)
		args = append(args, opts.Project)
		argN++
	}
	if opts.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, opts.Status)
		argN++
	}
	if opts.Assignee != "" {
		where += fmt.Sprintf(" AND assignee = $%d", argN)
		args = append(args, opts.Assignee)
		argN++
	}

	var total int
	err := cs.db.QueryRow(
		fmt.Sprintf("SELECT COUNT(*) FROM tasks %s", where), args...,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudstore: count tasks: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	orderAndPage := fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argN, argN+1)
	args = append(args, limit, opts.Offset)

	rows, err := cs.db.Query(
		fmt.Sprintf("SELECT %s FROM tasks %s%s", taskColumns, where, orderAndPage), args...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudstore: list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("cloudstore: scan task: %w", err)
		}
		tasks = append(tasks, *t)
	}

	return tasks, total, nil
}

// UpdateTask updates a task, validating status transitions per TE-3.
func (cs *CloudStore) UpdateTask(userID string, id int64, p UpdateTaskParams) error {
	// Fetch current task to validate status transition.
	current, err := cs.GetTask(userID, id)
	if err != nil {
		return fmt.Errorf("cloudstore: update task: %w", err)
	}

	if p.Status != nil && *p.Status != current.Status {
		if err := ValidateTransition(current.Status, *p.Status); err != nil {
			return err
		}
	}

	// Build dynamic SET clause.
	sets := []string{"updated_at = NOW()"}
	args := []any{userID, id}
	argN := 3

	if p.Title != nil {
		sets = append(sets, fmt.Sprintf("title = $%d", argN))
		args = append(args, *p.Title)
		argN++
	}
	if p.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", argN))
		args = append(args, *p.Description)
		argN++
	}
	if p.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", argN))
		args = append(args, *p.Status)
		argN++
		if *p.Status == "done" {
			sets = append(sets, "completed_at = NOW()")
		}
	}
	if p.Priority != nil {
		sets = append(sets, fmt.Sprintf("priority = $%d", argN))
		args = append(args, *p.Priority)
		argN++
	}
	if p.Assignee != nil {
		sets = append(sets, fmt.Sprintf("assignee = $%d", argN))
		args = append(args, *p.Assignee)
		argN++
	}
	if p.Project != nil {
		sets = append(sets, fmt.Sprintf("project = $%d", argN))
		args = append(args, *p.Project)
		argN++
	}
	if p.Tags != nil {
		sets = append(sets, fmt.Sprintf("tags = $%d", argN))
		args = append(args, pq.Array(p.Tags))
		argN++
	}
	if p.DueAt != nil {
		if *p.DueAt == "" {
			sets = append(sets, "due_at = NULL")
		} else {
			t, err := time.Parse(time.RFC3339, *p.DueAt)
			if err != nil {
				return fmt.Errorf("cloudstore: parse due_at: %w", err)
			}
			sets = append(sets, fmt.Sprintf("due_at = $%d", argN))
			args = append(args, t)
			argN++
		}
	}

	_, err = cs.db.Exec(
		fmt.Sprintf("UPDATE tasks SET %s WHERE id = $2 AND user_id = $1", strings.Join(sets, ", ")),
		args...,
	)
	if err != nil {
		return fmt.Errorf("cloudstore: update task: %w", err)
	}

	// Log status change event.
	if p.Status != nil && *p.Status != current.Status {
		_ = cs.AddTaskEvent(id, userID, "status_changed", map[string]string{
			"from": current.Status,
			"to":   *p.Status,
		})
	}

	return nil
}

// DeleteTask hard-deletes a task by ID, scoped to the user.
func (cs *CloudStore) DeleteTask(userID string, id int64) error {
	res, err := cs.db.Exec(
		"DELETE FROM tasks WHERE id = $1 AND user_id = $2",
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("cloudstore: delete task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cloudstore: delete task: not found")
	}
	return nil
}

// CreateSubtask inserts a child task under the given parent.
func (cs *CloudStore) CreateSubtask(userID string, parentID int64, p CreateTaskParams) (int64, error) {
	// Verify parent exists and belongs to user.
	_, err := cs.GetTask(userID, parentID)
	if err != nil {
		return 0, fmt.Errorf("cloudstore: create subtask: parent: %w", err)
	}

	priority := p.Priority
	if priority == "" {
		priority = "medium"
	}
	assigneeType := p.AssigneeType
	if assigneeType == "" {
		assigneeType = "user"
	}
	source := p.Source
	if source == "" {
		source = "user"
	}

	var dueAt *time.Time
	if p.DueAt != "" {
		t, err := time.Parse(time.RFC3339, p.DueAt)
		if err != nil {
			return 0, fmt.Errorf("cloudstore: parse due_at: %w", err)
		}
		dueAt = &t
	}

	var id int64
	err = cs.db.QueryRow(
		`INSERT INTO tasks
		 (user_id, parent_id, title, description, status, priority, assignee_type,
		  assignee, source, source_session_id, project, tags, due_at)
		 VALUES ($1, $2, $3, $4, 'open', $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id`,
		userID, parentID, p.Title, nullableString(p.Description), priority,
		assigneeType, nullableString(p.Assignee), source,
		nullableString(p.SourceSessionID), nullableString(p.Project),
		pq.Array(p.Tags), dueAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("cloudstore: create subtask: %w", err)
	}

	_ = cs.AddTaskEvent(id, userID, "created", map[string]string{"parent_id": fmt.Sprintf("%d", parentID)})
	return id, nil
}

// GetSubtasks returns all direct children of the given parent task.
func (cs *CloudStore) GetSubtasks(userID string, parentID int64) ([]Task, error) {
	rows, err := cs.db.Query(
		fmt.Sprintf("SELECT %s FROM tasks WHERE parent_id = $1 AND user_id = $2 ORDER BY created_at ASC", taskColumns),
		parentID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: get subtasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("cloudstore: scan subtask: %w", err)
		}
		tasks = append(tasks, *t)
	}
	return tasks, nil
}

// ─── Task Events ────────────────────────────────────────────────────────────

// AddTaskEvent inserts a task event record for audit/history.
func (cs *CloudStore) AddTaskEvent(taskID int64, userID, eventType string, payload any) error {
	var payloadJSON interface{}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("cloudstore: marshal task event payload: %w", err)
		}
		payloadJSON = string(b)
	}

	_, err := cs.db.Exec(
		`INSERT INTO task_events (task_id, user_id, event_type, payload)
		 VALUES ($1, $2, $3, $4)`,
		taskID, userID, eventType, payloadJSON,
	)
	if err != nil {
		return fmt.Errorf("cloudstore: add task event: %w", err)
	}
	return nil
}
