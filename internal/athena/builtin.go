package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// ─── Interfaces for tool dependencies ─────────────────────────────────────

// SkillLoader loads skill content by name.
type SkillLoader interface {
	Load(name string) (string, error)
}

// UpdateTaskFields holds optional fields for updating a task.
type UpdateTaskFields struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	Status      *string `json:"status,omitempty"`
}

// TaskStore provides task CRUD operations.
type TaskStore interface {
	CreateTask(userID string, title, description, project, priority string) (int64, error)
	ListTasks(userID string, status string, limit int) ([]TaskEntry, error)
	UpdateTaskStatus(userID string, taskID int64, status string) error
	UpdateTask(userID string, taskID int64, fields UpdateTaskFields) error
}

// TaskEntry is a lightweight task representation returned by TaskStore.
type TaskEntry struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Project  string `json:"project"`
}

// DelegateHandler handles task delegation to sub-agents.
type DelegateHandler interface {
	Delegate(ctx context.Context, task, project, skillContext string) (string, error)
}

// AsyncDelegateHandler handles async task delegation -- returns immediately with a job ID.
type AsyncDelegateHandler interface {
	DelegateAsync(ctx context.Context, task, project, workingDir, context string) (jobID string, err error)
}

// Notifier sends notifications through a channel.
type Notifier interface {
	Send(message, channel, level string) error
}

// CommandRunner abstracts exec.Command for testing.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

// defaultCommandRunner uses os/exec.
type defaultCommandRunner struct{}

func (r *defaultCommandRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// DefaultCommandRunner returns the real exec-based runner.
func DefaultCommandRunner() CommandRunner {
	return &defaultCommandRunner{}
}

// ─── LoadSkillTool ────────────────────────────────────────────────────────

// LoadSkillTool implements Tool for loading skills by name.
type LoadSkillTool struct {
	loader SkillLoader
}

// NewLoadSkillTool creates a LoadSkillTool backed by the given loader.
func NewLoadSkillTool(loader SkillLoader) *LoadSkillTool {
	return &LoadSkillTool{loader: loader}
}

func (t *LoadSkillTool) Name() string        { return "load_skill" }
func (t *LoadSkillTool) Description() string  { return "Load a skill by name into the conversation context" }
func (t *LoadSkillTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Skill name to load"}},"required":["name"]}`)
}

func (t *LoadSkillTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("load_skill: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Name == "" {
		slog.Warn("load_skill: missing name parameter")
		return ToolResult{Content: "missing required parameter: name", IsError: true}, nil
	}

	content, err := t.loader.Load(p.Name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			slog.Warn("load_skill: skill not found", "name", p.Name)
		} else {
			slog.Error("load_skill: failed to load skill", "name", p.Name, "err", err)
		}
		return ToolResult{Content: err.Error(), IsError: true}, nil
	}

	slog.Info("load_skill: skill loaded", "name", p.Name, "bytes", len(content))
	return ToolResult{Content: content}, nil
}

// ─── CreateTaskTool ───────────────────────────────────────────────────────

// CreateTaskTool implements Tool for creating tasks.
type CreateTaskTool struct {
	store  TaskStore
	userID string
}

// NewCreateTaskTool creates a CreateTaskTool.
func NewCreateTaskTool(store TaskStore, userID string) *CreateTaskTool {
	return &CreateTaskTool{store: store, userID: userID}
}

func (t *CreateTaskTool) Name() string        { return "create_task" }
func (t *CreateTaskTool) Description() string  { return "Create a new task" }
func (t *CreateTaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"Task title"},"description":{"type":"string","description":"Task description"},"project":{"type":"string","description":"Project name"},"priority":{"type":"string","enum":["low","medium","high","critical"],"description":"Task priority"}},"required":["title"]}`)
}

func (t *CreateTaskTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Project     string `json:"project"`
		Priority    string `json:"priority"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("create_task: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Title == "" {
		slog.Warn("create_task: missing title")
		return ToolResult{Content: "missing required parameter: title", IsError: true}, nil
	}
	if p.Priority == "" {
		p.Priority = "medium"
	}

	id, err := t.store.CreateTask(t.userID, p.Title, p.Description, p.Project, p.Priority)
	if err != nil {
		slog.Error("create_task: failed", "err", err)
		return ToolResult{Content: "failed to create task: " + err.Error(), IsError: true}, nil
	}

	result := map[string]interface{}{
		"id":     id,
		"title":  p.Title,
		"status": "open",
	}
	data, _ := json.Marshal(result)
	slog.Info("create_task: task created", "id", id, "title", p.Title)
	return ToolResult{Content: string(data)}, nil
}

// ─── ListTasksTool ────────────────────────────────────────────────────────

// ListTasksTool implements Tool for listing tasks.
type ListTasksTool struct {
	store  TaskStore
	userID string
}

// NewListTasksTool creates a ListTasksTool.
func NewListTasksTool(store TaskStore, userID string) *ListTasksTool {
	return &ListTasksTool{store: store, userID: userID}
}

func (t *ListTasksTool) Name() string        { return "list_tasks" }
func (t *ListTasksTool) Description() string  { return "List tasks with optional filters" }
func (t *ListTasksTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["open","done","all"],"description":"Filter by status"},"project":{"type":"string","description":"Filter by project"},"limit":{"type":"integer","description":"Max tasks to return"}}}`)
}

func (t *ListTasksTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Status  string `json:"status"`
		Project string `json:"project"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("list_tasks: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Status == "" {
		p.Status = "open"
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}

	tasks, err := t.store.ListTasks(t.userID, p.Status, p.Limit)
	if err != nil {
		slog.Error("list_tasks: failed", "err", err)
		return ToolResult{Content: "failed to list tasks: " + err.Error(), IsError: true}, nil
	}

	// Filter by project if specified
	if p.Project != "" {
		filtered := make([]TaskEntry, 0)
		for _, task := range tasks {
			if task.Project == p.Project {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}

	data, _ := json.Marshal(tasks)
	slog.Info("list_tasks: listed", "count", len(tasks), "status", p.Status)
	return ToolResult{Content: string(data)}, nil
}

// ─── CompleteTaskTool ─────────────────────────────────────────────────────

// CompleteTaskTool implements Tool for completing a task.
type CompleteTaskTool struct {
	store  TaskStore
	userID string
}

// NewCompleteTaskTool creates a CompleteTaskTool.
func NewCompleteTaskTool(store TaskStore, userID string) *CompleteTaskTool {
	return &CompleteTaskTool{store: store, userID: userID}
}

func (t *CompleteTaskTool) Name() string        { return "complete_task" }
func (t *CompleteTaskTool) Description() string  { return "Mark a task as done" }
func (t *CompleteTaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","description":"Task ID to complete"}},"required":["id"]}`)
}

func (t *CompleteTaskTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("complete_task: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.ID == 0 {
		slog.Warn("complete_task: missing id")
		return ToolResult{Content: "missing required parameter: id", IsError: true}, nil
	}

	// Auto-transition open → in_progress before marking done.
	// The state machine requires open → in_progress → done; skip if already in_progress.
	_ = t.store.UpdateTaskStatus(t.userID, p.ID, "in_progress")

	if err := t.store.UpdateTaskStatus(t.userID, p.ID, "done"); err != nil {
		slog.Error("complete_task: failed", "id", p.ID, "err", err)
		return ToolResult{Content: fmt.Sprintf("failed to complete task %d: %s", p.ID, err.Error()), IsError: true}, nil
	}

	result := map[string]interface{}{
		"id":     p.ID,
		"status": "done",
	}
	data, _ := json.Marshal(result)
	slog.Info("complete_task: task completed", "id", p.ID)
	return ToolResult{Content: string(data)}, nil
}

// ─── UpdateTaskTool ──────────────────────────────────────────────────────

// UpdateTaskTool implements Tool for updating a task's fields.
type UpdateTaskTool struct {
	store  TaskStore
	userID string
}

// NewUpdateTaskTool creates an UpdateTaskTool.
func NewUpdateTaskTool(store TaskStore, userID string) *UpdateTaskTool {
	return &UpdateTaskTool{store: store, userID: userID}
}

func (t *UpdateTaskTool) Name() string        { return "update_task" }
func (t *UpdateTaskTool) Description() string {
	return "Update a task's title, description, priority, or status"
}
func (t *UpdateTaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","description":"Task ID to update"},"title":{"type":"string","description":"New title"},"description":{"type":"string","description":"New description"},"priority":{"type":"string","enum":["low","medium","high","critical"],"description":"New priority"},"status":{"type":"string","enum":["open","in_progress","done","blocked","cancelled"],"description":"New status"}},"required":["id"]}`)
}

func (t *UpdateTaskTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		ID          int64   `json:"id"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Priority    *string `json:"priority"`
		Status      *string `json:"status"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("update_task: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.ID == 0 {
		slog.Warn("update_task: missing id")
		return ToolResult{Content: "missing required parameter: id", IsError: true}, nil
	}

	// Check that at least one field is being updated.
	if p.Title == nil && p.Description == nil && p.Priority == nil && p.Status == nil {
		return ToolResult{Content: "at least one field (title, description, priority, status) must be provided", IsError: true}, nil
	}

	fields := UpdateTaskFields{
		Title:       p.Title,
		Description: p.Description,
		Priority:    p.Priority,
		Status:      p.Status,
	}

	if err := t.store.UpdateTask(t.userID, p.ID, fields); err != nil {
		slog.Error("update_task: failed", "id", p.ID, "err", err)
		return ToolResult{Content: fmt.Sprintf("failed to update task %d: %s", p.ID, err.Error()), IsError: true}, nil
	}

	result := map[string]interface{}{
		"id":      p.ID,
		"updated": true,
	}
	if p.Title != nil {
		result["title"] = *p.Title
	}
	if p.Priority != nil {
		result["priority"] = *p.Priority
	}
	if p.Status != nil {
		result["status"] = *p.Status
	}
	data, _ := json.Marshal(result)
	slog.Info("update_task: task updated", "id", p.ID)
	return ToolResult{Content: string(data)}, nil
}

// ─── DelegateTool ─────────────────────────────────────────────────────────

// DelegateTool implements Tool for delegating tasks to sub-agents.
// If an AsyncDelegateHandler is set, it delegates asynchronously and returns
// immediately with a job ID. Otherwise it falls back to the sync DelegateHandler.
type DelegateTool struct {
	handler      DelegateHandler
	asyncHandler AsyncDelegateHandler
}

// NewDelegateTool creates a DelegateTool with a synchronous handler.
func NewDelegateTool(handler DelegateHandler) *DelegateTool {
	return &DelegateTool{handler: handler}
}

// NewAsyncDelegateTool creates a DelegateTool with an async handler.
// The tool returns immediately with a job ID instead of blocking.
func NewAsyncDelegateTool(asyncHandler AsyncDelegateHandler) *DelegateTool {
	return &DelegateTool{asyncHandler: asyncHandler}
}

func (t *DelegateTool) Name() string        { return "delegate" }
func (t *DelegateTool) Description() string  { return "Delegate a task to a background worker. Returns immediately with a job ID. Use list_jobs to check progress." }
func (t *DelegateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"Task description for the sub-agent"},"project":{"type":"string","description":"Project context"},"working_dir":{"type":"string","description":"Working directory for the task"},"context":{"type":"string","description":"Additional context for the sub-agent"}},"required":["task"]}`)
}

func (t *DelegateTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Task       string `json:"task"`
		Project    string `json:"project"`
		WorkingDir string `json:"working_dir"`
		Context    string `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("delegate: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Task == "" {
		slog.Warn("delegate: missing task")
		return ToolResult{Content: "missing required parameter: task", IsError: true}, nil
	}

	// Prefer async delegation if available.
	if t.asyncHandler != nil {
		slog.Info("delegate: async delegation", "task", p.Task, "project", p.Project)
		jobID, err := t.asyncHandler.DelegateAsync(ctx, p.Task, p.Project, p.WorkingDir, p.Context)
		if err != nil {
			slog.Error("delegate: async failed", "err", err)
			return ToolResult{Content: "delegation failed: " + err.Error(), IsError: true}, nil
		}

		result := fmt.Sprintf("Delegado. Job #%s creado. Te aviso cuando termine.", jobID)
		slog.Info("delegate: job created", "job_id", jobID)
		return ToolResult{Content: result}, nil
	}

	// Fallback to sync delegation.
	slog.Info("delegate: sync delegation", "task", p.Task, "project", p.Project)
	result, err := t.handler.Delegate(ctx, p.Task, p.Project, p.Context)
	if err != nil {
		slog.Error("delegate: failed", "err", err)
		return ToolResult{Content: "delegation failed: " + err.Error(), IsError: true}, nil
	}

	slog.Info("delegate: task completed", "result_len", len(result))
	return ToolResult{Content: result}, nil
}

// ─── ListJobsTool ─────────────────────────────────────────────────────

// JobLister provides read access to jobs for the LLM tools.
type JobLister interface {
	List() []*Job
	Get(id string) (*Job, bool)
}

// ListJobsTool implements Tool for listing delegation jobs.
type ListJobsTool struct {
	lister JobLister
}

// NewListJobsTool creates a ListJobsTool.
func NewListJobsTool(lister JobLister) *ListJobsTool {
	return &ListJobsTool{lister: lister}
}

func (t *ListJobsTool) Name() string        { return "list_jobs" }
func (t *ListJobsTool) Description() string  { return "List all delegation jobs and their status" }
func (t *ListJobsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","description":"Filter by status (pending, running, success, error)"}}}`)
}

func (t *ListJobsTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Status string `json:"status"`
	}
	if params != nil && len(params) > 0 {
		_ = json.Unmarshal(params, &p) // optional params, ignore errors
	}

	jobs := t.lister.List()

	// Filter by status if specified.
	if p.Status != "" {
		filtered := make([]*Job, 0)
		for _, j := range jobs {
			if j.Status == p.Status {
				filtered = append(filtered, j)
			}
		}
		jobs = filtered
	}

	data, _ := json.Marshal(jobs)
	slog.Info("list_jobs: listed", "count", len(jobs))
	return ToolResult{Content: string(data)}, nil
}

// ─── GetJobTool ───────────────────────────────────────────────────────

// GetJobTool implements Tool for getting a specific job's details.
type GetJobTool struct {
	lister JobLister
}

// NewGetJobTool creates a GetJobTool.
func NewGetJobTool(lister JobLister) *GetJobTool {
	return &GetJobTool{lister: lister}
}

func (t *GetJobTool) Name() string        { return "get_job" }
func (t *GetJobTool) Description() string  { return "Get details of a specific delegation job" }
func (t *GetJobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"Job ID"}},"required":["id"]}`)
}

func (t *GetJobTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("get_job: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.ID == "" {
		slog.Warn("get_job: missing id")
		return ToolResult{Content: "missing required parameter: id", IsError: true}, nil
	}

	job, ok := t.lister.Get(p.ID)
	if !ok {
		return ToolResult{Content: fmt.Sprintf("job %q not found", p.ID), IsError: true}, nil
	}

	data, _ := json.Marshal(job)
	return ToolResult{Content: string(data)}, nil
}

// ─── NotifyTool ───────────────────────────────────────────────────────────

// NotifyTool implements Tool for sending notifications.
type NotifyTool struct {
	notifier Notifier
}

// NewNotifyTool creates a NotifyTool.
func NewNotifyTool(notifier Notifier) *NotifyTool {
	return &NotifyTool{notifier: notifier}
}

func (t *NotifyTool) Name() string        { return "notify" }
func (t *NotifyTool) Description() string  { return "Send a notification to a channel" }
func (t *NotifyTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","description":"Notification message"},"channel":{"type":"string","enum":["discord","log"],"description":"Notification channel"},"level":{"type":"string","enum":["info","warn","error"],"description":"Notification level"}},"required":["message"]}`)
}

func (t *NotifyTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Message string `json:"message"`
		Channel string `json:"channel"`
		Level   string `json:"level"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("notify: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Message == "" {
		slog.Warn("notify: missing message")
		return ToolResult{Content: "missing required parameter: message", IsError: true}, nil
	}
	if p.Channel == "" {
		p.Channel = "discord"
	}
	if p.Level == "" {
		p.Level = "info"
	}

	slog.Info("notify: sending notification", "channel", p.Channel, "level", p.Level)
	if err := t.notifier.Send(p.Message, p.Channel, p.Level); err != nil {
		slog.Error("notify: failed", "err", err)
		return ToolResult{Content: "notification failed: " + err.Error(), IsError: true}, nil
	}

	slog.Info("notify: notification sent", "channel", p.Channel)
	return ToolResult{Content: fmt.Sprintf("notification sent to %s", p.Channel)}, nil
}

// ─── SearchMemoryTool ─────────────────────────────────────────────────────

// SearchMemoryTool implements Tool for searching mnemo memory.
type SearchMemoryTool struct {
	runner    CommandRunner
	mnemoBin string
}

// NewSearchMemoryTool creates a SearchMemoryTool.
func NewSearchMemoryTool(mnemoBin string, runner CommandRunner) *SearchMemoryTool {
	if runner == nil {
		runner = DefaultCommandRunner()
	}
	return &SearchMemoryTool{runner: runner, mnemoBin: mnemoBin}
}

func (t *SearchMemoryTool) Name() string        { return "search_memory" }
func (t *SearchMemoryTool) Description() string  { return "Search persistent memory for past observations" }
func (t *SearchMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"project":{"type":"string","description":"Filter by project"},"type":{"type":"string","description":"Filter by observation type"}},"required":["query"]}`)
}

func (t *SearchMemoryTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Query   string `json:"query"`
		Project string `json:"project"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("search_memory: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Query == "" {
		slog.Warn("search_memory: missing query")
		return ToolResult{Content: "missing required parameter: query", IsError: true}, nil
	}

	args := []string{"search", p.Query}
	if p.Project != "" {
		args = append(args, "--project", p.Project)
	}
	if p.Type != "" {
		args = append(args, "--type", p.Type)
	}

	slog.Info("search_memory: searching", "query", p.Query, "project", p.Project)
	output, err := t.runner.Run(t.mnemoBin, args...)
	if err != nil {
		slog.Error("search_memory: CLI failed", "err", err, "output", string(output))
		return ToolResult{Content: "memory search failed: " + err.Error(), IsError: true}, nil
	}

	slog.Debug("search_memory: results", "bytes", len(output))
	return ToolResult{Content: string(output)}, nil
}

// ─── SaveMemoryTool ───────────────────────────────────────────────────────

// SaveMemoryTool implements Tool for saving to mnemo memory.
type SaveMemoryTool struct {
	runner    CommandRunner
	mnemoBin string
}

// NewSaveMemoryTool creates a SaveMemoryTool.
func NewSaveMemoryTool(mnemoBin string, runner CommandRunner) *SaveMemoryTool {
	if runner == nil {
		runner = DefaultCommandRunner()
	}
	return &SaveMemoryTool{runner: runner, mnemoBin: mnemoBin}
}

func (t *SaveMemoryTool) Name() string        { return "save_memory" }
func (t *SaveMemoryTool) Description() string  { return "Save an observation to persistent memory" }
func (t *SaveMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"Observation title"},"content":{"type":"string","description":"Observation content"},"type":{"type":"string","enum":["decision","bugfix","discovery","pattern"],"description":"Observation type"},"project":{"type":"string","description":"Project name"}},"required":["title","content","type"]}`)
}

func (t *SaveMemoryTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Type    string `json:"type"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("save_memory: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Title == "" {
		slog.Warn("save_memory: missing title")
		return ToolResult{Content: "missing required parameter: title", IsError: true}, nil
	}
	if p.Content == "" {
		slog.Warn("save_memory: missing content")
		return ToolResult{Content: "missing required parameter: content", IsError: true}, nil
	}
	if p.Type == "" {
		slog.Warn("save_memory: missing type")
		return ToolResult{Content: "missing required parameter: type", IsError: true}, nil
	}

	args := []string{"save", p.Title, p.Content, "--type", p.Type}
	if p.Project != "" {
		args = append(args, "--project", p.Project)
	}

	slog.Info("save_memory: saving", "title", p.Title, "type", p.Type)
	output, err := t.runner.Run(t.mnemoBin, args...)
	if err != nil {
		slog.Error("save_memory: CLI failed", "err", err, "output", string(output))
		return ToolResult{Content: "memory save failed: " + err.Error(), IsError: true}, nil
	}

	slog.Info("save_memory: saved", "title", p.Title)
	return ToolResult{Content: strings.TrimSpace(string(output))}, nil
}

// ─── Compile-time interface checks ────────────────────────────────────────

var (
	_ Tool = (*LoadSkillTool)(nil)
	_ Tool = (*CreateTaskTool)(nil)
	_ Tool = (*ListTasksTool)(nil)
	_ Tool = (*CompleteTaskTool)(nil)
	_ Tool = (*UpdateTaskTool)(nil)
	_ Tool = (*DelegateTool)(nil)
	_ Tool = (*ListJobsTool)(nil)
	_ Tool = (*GetJobTool)(nil)
	_ Tool = (*NotifyTool)(nil)
	_ Tool = (*SearchMemoryTool)(nil)
	_ Tool = (*SaveMemoryTool)(nil)
)
