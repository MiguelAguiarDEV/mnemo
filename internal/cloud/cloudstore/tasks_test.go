package cloudstore

import (
	"strings"
	"testing"
)

// testUser creates a store and a test user, returning both plus the user ID.
func testUser(t *testing.T) (*CloudStore, string) {
	t.Helper()
	cs := newTestStore(t)
	u, err := cs.CreateUser("taskuser", "taskuser@example.com", "pass123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return cs, u.ID
}

func strPtr(s string) *string { return &s }

// ── ValidateTransition (unit, no DB) ──────────────────────────────────────

func TestValidateTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		// Valid transitions.
		{"open->in_progress", "open", "in_progress", false},
		{"open->blocked", "open", "blocked", false},
		{"open->cancelled", "open", "cancelled", false},
		{"in_progress->done", "in_progress", "done", false},
		{"in_progress->blocked", "in_progress", "blocked", false},
		{"in_progress->cancelled", "in_progress", "cancelled", false},
		{"blocked->open", "blocked", "open", false},
		{"blocked->cancelled", "blocked", "cancelled", false},
		// Same status (no-op).
		{"open->open", "open", "open", false},
		{"done->done", "done", "done", false},
		// Invalid transitions.
		{"open->done", "open", "done", true},
		{"done->open", "done", "open", true},
		{"done->in_progress", "done", "in_progress", true},
		{"cancelled->open", "cancelled", "open", true},
		{"cancelled->in_progress", "cancelled", "in_progress", true},
		{"blocked->in_progress", "blocked", "in_progress", true},
		{"blocked->done", "blocked", "done", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransition(tt.from, tt.to)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %s -> %s, got nil", tt.from, tt.to)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %s -> %s: %v", tt.from, tt.to, err)
			}
		})
	}
}

// ── CreateTask ────────────────────────────────────────────────────────────

func TestCreateTask(t *testing.T) {
	cs, userID := testUser(t)

	id, err := cs.CreateTask(userID, CreateTaskParams{
		Title:   "My first task",
		Project: "test-project",
		Tags:    []string{"backend", "mvp"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected ID > 0, got %d", id)
	}

	// Verify defaults.
	task, err := cs.GetTask(userID, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != "open" {
		t.Errorf("status = %q, want open", task.Status)
	}
	if task.Priority != "medium" {
		t.Errorf("priority = %q, want medium", task.Priority)
	}
	if task.AssigneeType != "user" {
		t.Errorf("assignee_type = %q, want user", task.AssigneeType)
	}
	if task.Source != "user" {
		t.Errorf("source = %q, want user", task.Source)
	}
}

// ── GetTask ───────────────────────────────────────────────────────────────

func TestGetTaskNotFound(t *testing.T) {
	cs, userID := testUser(t)

	_, err := cs.GetTask(userID, 99999)
	if err == nil {
		t.Fatal("expected error for non-existent task, got nil")
	}
}

func TestGetTaskWrongUser(t *testing.T) {
	cs, userID := testUser(t)

	id, err := cs.CreateTask(userID, CreateTaskParams{Title: "private task"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Create a second user.
	u2, err := cs.CreateUser("other", "other@example.com", "pass456")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err = cs.GetTask(u2.ID, id)
	if err == nil {
		t.Fatal("expected error when querying another user's task")
	}
}

// ── ListTasks ─────────────────────────────────────────────────────────────

func TestListTasksFilterByStatus(t *testing.T) {
	cs, userID := testUser(t)

	id1, _ := cs.CreateTask(userID, CreateTaskParams{Title: "task1"})
	cs.CreateTask(userID, CreateTaskParams{Title: "task2"})

	// Move task1 to in_progress.
	cs.UpdateTask(userID, id1, UpdateTaskParams{Status: strPtr("in_progress")})

	tasks, total, err := cs.ListTasks(userID, ListTasksOpts{Status: "open"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(tasks) != 1 {
		t.Errorf("len(tasks) = %d, want 1", len(tasks))
	}
}

func TestListTasksFilterByProject(t *testing.T) {
	cs, userID := testUser(t)

	cs.CreateTask(userID, CreateTaskParams{Title: "a", Project: "alpha"})
	cs.CreateTask(userID, CreateTaskParams{Title: "b", Project: "beta"})
	cs.CreateTask(userID, CreateTaskParams{Title: "c", Project: "alpha"})

	tasks, total, err := cs.ListTasks(userID, ListTasksOpts{Project: "alpha"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(tasks) != 2 {
		t.Errorf("len(tasks) = %d, want 2", len(tasks))
	}
}

func TestListTasksPagination(t *testing.T) {
	cs, userID := testUser(t)

	for i := 0; i < 5; i++ {
		cs.CreateTask(userID, CreateTaskParams{Title: "task"})
	}

	tasks, total, err := cs.ListTasks(userID, ListTasksOpts{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListTasks page 1: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(tasks) != 2 {
		t.Errorf("len(tasks) = %d, want 2", len(tasks))
	}

	tasks2, _, err := cs.ListTasks(userID, ListTasksOpts{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("ListTasks page 3: %v", err)
	}
	if len(tasks2) != 1 {
		t.Errorf("len(tasks) offset=4 = %d, want 1", len(tasks2))
	}
}

func TestListTasksExcludesSubtasks(t *testing.T) {
	cs, userID := testUser(t)

	parentID, _ := cs.CreateTask(userID, CreateTaskParams{Title: "parent"})
	cs.CreateSubtask(userID, parentID, CreateTaskParams{Title: "child"})

	tasks, total, err := cs.ListTasks(userID, ListTasksOpts{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (subtasks should be excluded)", total)
	}
	if len(tasks) != 1 {
		t.Errorf("len(tasks) = %d, want 1", len(tasks))
	}
}

// ── UpdateTask — status transitions ───────────────────────────────────────

func TestUpdateTaskStatusTransitions(t *testing.T) {
	tests := []struct {
		name      string
		setup     []string // transitions to reach start state
		from      string   // expected start state (for documentation)
		to        string
		wantErr   bool
		errSubstr string
	}{
		// Valid transitions.
		{"open->in_progress", nil, "open", "in_progress", false, ""},
		{"open->blocked", nil, "open", "blocked", false, ""},
		{"open->cancelled", nil, "open", "cancelled", false, ""},
		{"in_progress->done", []string{"in_progress"}, "in_progress", "done", false, ""},
		{"in_progress->blocked", []string{"in_progress"}, "in_progress", "blocked", false, ""},
		{"in_progress->cancelled", []string{"in_progress"}, "in_progress", "cancelled", false, ""},
		{"blocked->open", []string{"blocked"}, "blocked", "open", false, ""},
		{"blocked->cancelled", []string{"blocked"}, "blocked", "cancelled", false, ""},
		// Invalid transitions.
		{"open->done", nil, "open", "done", true, "invalid transition"},
		{"done->open", []string{"in_progress", "done"}, "done", "open", true, "invalid current status"},
		{"done->in_progress", []string{"in_progress", "done"}, "done", "in_progress", true, "invalid current status"},
		{"cancelled->open", []string{"cancelled"}, "cancelled", "open", true, "invalid current status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, userID := testUser(t)

			id, err := cs.CreateTask(userID, CreateTaskParams{Title: tt.name})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}

			// Walk through setup transitions to reach the desired start state.
			for _, status := range tt.setup {
				if err := cs.UpdateTask(userID, id, UpdateTaskParams{Status: strPtr(status)}); err != nil {
					t.Fatalf("setup transition to %s: %v", status, err)
				}
			}

			// Attempt the transition under test.
			err = cs.UpdateTask(userID, id, UpdateTaskParams{Status: strPtr(tt.to)})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s -> %s, got nil", tt.from, tt.to)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for %s -> %s: %v", tt.from, tt.to, err)
				}
				// Verify the task actually changed status.
				task, err := cs.GetTask(userID, id)
				if err != nil {
					t.Fatalf("GetTask after transition: %v", err)
				}
				if task.Status != tt.to {
					t.Errorf("status = %q, want %q", task.Status, tt.to)
				}
			}
		})
	}
}

func TestUpdateTaskSetsCompletedAt(t *testing.T) {
	cs, userID := testUser(t)

	id, _ := cs.CreateTask(userID, CreateTaskParams{Title: "will complete"})
	cs.UpdateTask(userID, id, UpdateTaskParams{Status: strPtr("in_progress")})
	cs.UpdateTask(userID, id, UpdateTaskParams{Status: strPtr("done")})

	task, err := cs.GetTask(userID, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.CompletedAt == nil {
		t.Error("completed_at should be set after transitioning to done")
	}
}

func TestUpdateTaskNonStatusFields(t *testing.T) {
	cs, userID := testUser(t)

	id, _ := cs.CreateTask(userID, CreateTaskParams{Title: "original"})

	err := cs.UpdateTask(userID, id, UpdateTaskParams{
		Title:    strPtr("updated title"),
		Priority: strPtr("high"),
		Project:  strPtr("new-project"),
		Tags:     []string{"tag1", "tag2"},
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	task, err := cs.GetTask(userID, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Title != "updated title" {
		t.Errorf("title = %q, want 'updated title'", task.Title)
	}
	if task.Priority != "high" {
		t.Errorf("priority = %q, want high", task.Priority)
	}
	if task.Project == nil || *task.Project != "new-project" {
		t.Errorf("project = %v, want 'new-project'", task.Project)
	}
}

// ── DeleteTask ────────────────────────────────────────────────────────────

func TestDeleteTask(t *testing.T) {
	cs, userID := testUser(t)

	id, _ := cs.CreateTask(userID, CreateTaskParams{Title: "to delete"})

	if err := cs.DeleteTask(userID, id); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err := cs.GetTask(userID, id)
	if err == nil {
		t.Fatal("expected error after deleting task, got nil")
	}
}

func TestDeleteTaskCascadesToChildren(t *testing.T) {
	cs, userID := testUser(t)

	parentID, _ := cs.CreateTask(userID, CreateTaskParams{Title: "parent"})
	childID, _ := cs.CreateSubtask(userID, parentID, CreateTaskParams{Title: "child"})

	if err := cs.DeleteTask(userID, parentID); err != nil {
		t.Fatalf("DeleteTask parent: %v", err)
	}

	// Child should be gone too (ON DELETE CASCADE).
	_, err := cs.GetTask(userID, childID)
	if err == nil {
		t.Fatal("expected child to be deleted after parent deletion")
	}
}

func TestDeleteTaskCascadesToEvents(t *testing.T) {
	cs, userID := testUser(t)

	id, _ := cs.CreateTask(userID, CreateTaskParams{Title: "with events"})
	// CreateTask already logs a "created" event. Add one more.
	cs.AddTaskEvent(id, userID, "test_event", map[string]string{"key": "val"})

	if err := cs.DeleteTask(userID, id); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// Verify events are gone.
	var count int
	err := cs.db.QueryRow("SELECT COUNT(*) FROM task_events WHERE task_id = $1", id).Scan(&count)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 events after delete, got %d", count)
	}
}

func TestDeleteTaskNotFound(t *testing.T) {
	cs, userID := testUser(t)

	err := cs.DeleteTask(userID, 99999)
	if err == nil {
		t.Fatal("expected error deleting non-existent task")
	}
}

// ── Subtasks ──────────────────────────────────────────────────────────────

func TestCreateSubtask(t *testing.T) {
	cs, userID := testUser(t)

	parentID, _ := cs.CreateTask(userID, CreateTaskParams{Title: "parent"})
	childID, err := cs.CreateSubtask(userID, parentID, CreateTaskParams{Title: "child"})
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}
	if childID <= 0 {
		t.Errorf("expected child ID > 0, got %d", childID)
	}

	child, err := cs.GetTask(userID, childID)
	if err != nil {
		t.Fatalf("GetTask child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parentID {
		t.Errorf("parent_id = %v, want %d", child.ParentID, parentID)
	}
}

func TestGetSubtasks(t *testing.T) {
	cs, userID := testUser(t)

	parentID, _ := cs.CreateTask(userID, CreateTaskParams{Title: "parent"})
	cs.CreateSubtask(userID, parentID, CreateTaskParams{Title: "child1"})
	cs.CreateSubtask(userID, parentID, CreateTaskParams{Title: "child2"})

	subs, err := cs.GetSubtasks(userID, parentID)
	if err != nil {
		t.Fatalf("GetSubtasks: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("expected 2 subtasks, got %d", len(subs))
	}
	// Should be ordered by created_at ASC.
	if subs[0].Title != "child1" {
		t.Errorf("first subtask title = %q, want child1", subs[0].Title)
	}
}

func TestCreateSubtaskInvalidParent(t *testing.T) {
	cs, userID := testUser(t)

	_, err := cs.CreateSubtask(userID, 99999, CreateTaskParams{Title: "orphan"})
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
}

// ── AddTaskEvent ──────────────────────────────────────────────────────────

func TestAddTaskEvent(t *testing.T) {
	cs, userID := testUser(t)

	id, _ := cs.CreateTask(userID, CreateTaskParams{Title: "with event"})

	err := cs.AddTaskEvent(id, userID, "custom_event", map[string]string{"action": "test"})
	if err != nil {
		t.Fatalf("AddTaskEvent: %v", err)
	}

	// Verify event was recorded (created event + our custom event = 2).
	var count int
	err = cs.db.QueryRow("SELECT COUNT(*) FROM task_events WHERE task_id = $1", id).Scan(&count)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 events (created + custom), got %d", count)
	}
}
