package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ─── Mock implementations ─────────────────────────────────────────────────

type mockSkillLoader struct {
	skills map[string]string
	err    error
}

func (m *mockSkillLoader) Load(name string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	content, ok := m.skills[name]
	if !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	return content, nil
}

type mockTaskStore struct {
	tasks      []TaskEntry
	createdID  int64
	createErr  error
	listErr    error
	updateErr  error
	updateTaskErr error
	lastCreate struct {
		userID, title, description, project, priority string
	}
	lastUpdate struct {
		userID string
		taskID int64
		status string
	}
	lastUpdateTask struct {
		userID string
		taskID int64
		fields UpdateTaskFields
	}
}

func (m *mockTaskStore) CreateTask(userID, title, description, project, priority string) (int64, error) {
	m.lastCreate.userID = userID
	m.lastCreate.title = title
	m.lastCreate.description = description
	m.lastCreate.project = project
	m.lastCreate.priority = priority
	if m.createErr != nil {
		return 0, m.createErr
	}
	m.createdID++
	return m.createdID, nil
}

func (m *mockTaskStore) ListTasks(userID, status string, limit int) ([]TaskEntry, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.tasks, nil
}

func (m *mockTaskStore) UpdateTaskStatus(userID string, taskID int64, status string) error {
	m.lastUpdate.userID = userID
	m.lastUpdate.taskID = taskID
	m.lastUpdate.status = status
	return m.updateErr
}

func (m *mockTaskStore) UpdateTask(userID string, taskID int64, fields UpdateTaskFields) error {
	m.lastUpdateTask.userID = userID
	m.lastUpdateTask.taskID = taskID
	m.lastUpdateTask.fields = fields
	return m.updateTaskErr
}

type mockDelegateHandler struct {
	result string
	err    error
	lastTask, lastProject, lastContext string
}

func (m *mockDelegateHandler) Delegate(ctx context.Context, task, project, skillContext string) (string, error) {
	m.lastTask = task
	m.lastProject = project
	m.lastContext = skillContext
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

type mockAsyncDelegateHandler struct {
	jobID string
	err   error
	lastTask, lastProject, lastWorkingDir, lastContext string
}

func (m *mockAsyncDelegateHandler) DelegateAsync(ctx context.Context, task, project, workingDir, context string) (string, error) {
	m.lastTask = task
	m.lastProject = project
	m.lastWorkingDir = workingDir
	m.lastContext = context
	if m.err != nil {
		return "", m.err
	}
	return m.jobID, nil
}

type mockJobLister struct {
	jobs []*Job
}

func (m *mockJobLister) List() []*Job { return m.jobs }
func (m *mockJobLister) Get(id string) (*Job, bool) {
	for _, j := range m.jobs {
		if j.ID == id {
			return j, true
		}
	}
	return nil, false
}

type mockNotifier struct {
	lastMessage, lastChannel, lastLevel string
	err                                  error
}

func (m *mockNotifier) Send(message, channel, level string) error {
	m.lastMessage = message
	m.lastChannel = channel
	m.lastLevel = level
	return m.err
}

type mockCommandRunner struct {
	output []byte
	err    error
	lastArgs []string
}

func (m *mockCommandRunner) Run(name string, args ...string) ([]byte, error) {
	m.lastArgs = append([]string{name}, args...)
	return m.output, m.err
}

// ─── LoadSkillTool Tests ──────────────────────────────────────────────────

func TestLoadSkillTool_Success(t *testing.T) {
	loader := &mockSkillLoader{skills: map[string]string{
		"go-testing": "# Go Testing\nPatterns for Go tests.",
	}}
	tool := NewLoadSkillTool(loader)

	if tool.Name() != "load_skill" {
		t.Errorf("name = %q, want %q", tool.Name(), "load_skill")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"go-testing"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if result.Content != "# Go Testing\nPatterns for Go tests." {
		t.Errorf("content = %q, want skill content", result.Content)
	}
}

func TestLoadSkillTool_NotFound(t *testing.T) {
	loader := &mockSkillLoader{skills: map[string]string{}}
	tool := NewLoadSkillTool(loader)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"nonexistent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "not found") {
		t.Errorf("content = %q, should contain 'not found'", result.Content)
	}
}

func TestLoadSkillTool_ReadFailure(t *testing.T) {
	loader := &mockSkillLoader{err: fmt.Errorf("permission denied")}
	tool := NewLoadSkillTool(loader)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"broken"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "permission denied") {
		t.Errorf("content = %q, should contain error", result.Content)
	}
}

func TestLoadSkillTool_MissingName(t *testing.T) {
	loader := &mockSkillLoader{skills: map[string]string{}}
	tool := NewLoadSkillTool(loader)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: name") {
		t.Errorf("content = %q, should mention missing name", result.Content)
	}
}

func TestLoadSkillTool_InvalidParams(t *testing.T) {
	loader := &mockSkillLoader{skills: map[string]string{}}
	tool := NewLoadSkillTool(loader)

	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "invalid parameters") {
		t.Errorf("content = %q, should mention invalid params", result.Content)
	}
}

func TestLoadSkillTool_Parameters(t *testing.T) {
	tool := NewLoadSkillTool(&mockSkillLoader{})
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("invalid JSON schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties in schema")
	}
	if _, ok := props["name"]; !ok {
		t.Error("expected 'name' in properties")
	}
}

// ─── CreateTaskTool Tests ─────────────────────────────────────────────────

func TestCreateTaskTool_Success(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewCreateTaskTool(store, "user-1")

	if tool.Name() != "create_task" {
		t.Errorf("name = %q, want %q", tool.Name(), "create_task")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Fix bug","description":"Login fails","project":"jarvis","priority":"high"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	if store.lastCreate.title != "Fix bug" {
		t.Errorf("title = %q, want %q", store.lastCreate.title, "Fix bug")
	}
	if store.lastCreate.priority != "high" {
		t.Errorf("priority = %q, want %q", store.lastCreate.priority, "high")
	}
	if store.lastCreate.userID != "user-1" {
		t.Errorf("userID = %q, want %q", store.lastCreate.userID, "user-1")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result.Content), &parsed); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if parsed["status"] != "open" {
		t.Errorf("status = %v, want %q", parsed["status"], "open")
	}
}

func TestCreateTaskTool_DefaultPriority(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewCreateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Quick task"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if store.lastCreate.priority != "medium" {
		t.Errorf("priority = %q, want %q (default)", store.lastCreate.priority, "medium")
	}
}

func TestCreateTaskTool_MissingTitle(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewCreateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"description":"no title"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: title") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestCreateTaskTool_StoreError(t *testing.T) {
	store := &mockTaskStore{createErr: fmt.Errorf("db error")}
	tool := NewCreateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "db error") {
		t.Errorf("content = %q", result.Content)
	}
}

// ─── ListTasksTool Tests ──────────────────────────────────────────────────

func TestListTasksTool_Success(t *testing.T) {
	store := &mockTaskStore{tasks: []TaskEntry{
		{ID: 1, Title: "Task A", Status: "open", Priority: "high", Project: "jarvis"},
		{ID: 2, Title: "Task B", Status: "open", Priority: "medium", Project: "mnemo"},
	}}
	tool := NewListTasksTool(store, "user-1")

	if tool.Name() != "list_tasks" {
		t.Errorf("name = %q, want %q", tool.Name(), "list_tasks")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"status":"open"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var tasks []TaskEntry
	if err := json.Unmarshal([]byte(result.Content), &tasks); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("got %d tasks, want 2", len(tasks))
	}
}

func TestListTasksTool_FilterByProject(t *testing.T) {
	store := &mockTaskStore{tasks: []TaskEntry{
		{ID: 1, Title: "Task A", Status: "open", Priority: "high", Project: "jarvis"},
		{ID: 2, Title: "Task B", Status: "open", Priority: "medium", Project: "mnemo"},
	}}
	tool := NewListTasksTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"project":"jarvis"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var tasks []TaskEntry
	if err := json.Unmarshal([]byte(result.Content), &tasks); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("got %d tasks, want 1 (filtered by project)", len(tasks))
	}
}

func TestListTasksTool_DefaultParams(t *testing.T) {
	store := &mockTaskStore{tasks: []TaskEntry{}}
	tool := NewListTasksTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
}

func TestListTasksTool_StoreError(t *testing.T) {
	store := &mockTaskStore{listErr: fmt.Errorf("db error")}
	tool := NewListTasksTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
}

// ─── CompleteTaskTool Tests ───────────────────────────────────────────────

func TestCompleteTaskTool_Success(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewCompleteTaskTool(store, "user-1")

	if tool.Name() != "complete_task" {
		t.Errorf("name = %q, want %q", tool.Name(), "complete_task")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":42}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	if store.lastUpdate.taskID != 42 {
		t.Errorf("taskID = %d, want 42", store.lastUpdate.taskID)
	}
	if store.lastUpdate.status != "done" {
		t.Errorf("status = %q, want %q", store.lastUpdate.status, "done")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result.Content), &parsed); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if parsed["status"] != "done" {
		t.Errorf("status in result = %v, want %q", parsed["status"], "done")
	}
}

func TestCompleteTaskTool_MissingID(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewCompleteTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: id") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestCompleteTaskTool_NotFound(t *testing.T) {
	store := &mockTaskStore{updateErr: fmt.Errorf("task not found")}
	tool := NewCompleteTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":999}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "task not found") {
		t.Errorf("content = %q", result.Content)
	}
}

// ─── UpdateTaskTool Tests ────────────────────────────────────────────────

func TestUpdateTaskTool_Success(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewUpdateTaskTool(store, "user-1")

	if tool.Name() != "update_task" {
		t.Errorf("name = %q, want %q", tool.Name(), "update_task")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":42,"priority":"high","title":"new title"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if store.lastUpdateTask.taskID != 42 {
		t.Errorf("taskID = %d, want 42", store.lastUpdateTask.taskID)
	}
	if store.lastUpdateTask.userID != "user-1" {
		t.Errorf("userID = %q, want %q", store.lastUpdateTask.userID, "user-1")
	}
	if store.lastUpdateTask.fields.Priority == nil || *store.lastUpdateTask.fields.Priority != "high" {
		t.Error("expected priority to be 'high'")
	}
	if store.lastUpdateTask.fields.Title == nil || *store.lastUpdateTask.fields.Title != "new title" {
		t.Error("expected title to be 'new title'")
	}
	if !strings.Contains(result.Content, `"updated":true`) {
		t.Errorf("content = %q, should contain updated:true", result.Content)
	}
}

func TestUpdateTaskTool_MissingID(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewUpdateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"priority":"high"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: id") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestUpdateTaskTool_NoFields(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewUpdateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true for no fields")
	}
	if !strings.Contains(result.Content, "at least one field") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestUpdateTaskTool_StoreError(t *testing.T) {
	store := &mockTaskStore{updateTaskErr: fmt.Errorf("task not found")}
	tool := NewUpdateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":999,"priority":"low"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "task not found") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestUpdateTaskTool_InvalidParams(t *testing.T) {
	store := &mockTaskStore{}
	tool := NewUpdateTaskTool(store, "user-1")

	result, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
}

// ─── DelegateTool Tests ──────────────────────────────────────────────────

func TestDelegateTool_Success(t *testing.T) {
	handler := &mockDelegateHandler{result: "Sub-agent completed: analysis done."}
	tool := NewDelegateTool(handler)

	if tool.Name() != "delegate" {
		t.Errorf("name = %q, want %q", tool.Name(), "delegate")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"analyze logs","project":"jarvis","context":"check errors"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if result.Content != "Sub-agent completed: analysis done." {
		t.Errorf("content = %q", result.Content)
	}
	if handler.lastTask != "analyze logs" {
		t.Errorf("task = %q, want %q", handler.lastTask, "analyze logs")
	}
	if handler.lastProject != "jarvis" {
		t.Errorf("project = %q, want %q", handler.lastProject, "jarvis")
	}
}

func TestDelegateTool_MissingTask(t *testing.T) {
	handler := &mockDelegateHandler{}
	tool := NewDelegateTool(handler)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"project":"jarvis"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: task") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestDelegateTool_Error(t *testing.T) {
	handler := &mockDelegateHandler{err: fmt.Errorf("sub-agent unreachable")}
	tool := NewDelegateTool(handler)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "sub-agent unreachable") {
		t.Errorf("content = %q", result.Content)
	}
}

// ─── NotifyTool Tests ─────────────────────────────────────────────────────

func TestNotifyTool_Success(t *testing.T) {
	notifier := &mockNotifier{}
	tool := NewNotifyTool(notifier)

	if tool.Name() != "notify" {
		t.Errorf("name = %q, want %q", tool.Name(), "notify")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"message":"Deploy complete","channel":"discord","level":"info"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if notifier.lastMessage != "Deploy complete" {
		t.Errorf("message = %q", notifier.lastMessage)
	}
	if notifier.lastChannel != "discord" {
		t.Errorf("channel = %q", notifier.lastChannel)
	}
	if notifier.lastLevel != "info" {
		t.Errorf("level = %q", notifier.lastLevel)
	}
	if !strings.Contains(result.Content, "notification sent") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestNotifyTool_Defaults(t *testing.T) {
	notifier := &mockNotifier{}
	tool := NewNotifyTool(notifier)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"message":"Hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if notifier.lastChannel != "discord" {
		t.Errorf("channel = %q, want default %q", notifier.lastChannel, "discord")
	}
	if notifier.lastLevel != "info" {
		t.Errorf("level = %q, want default %q", notifier.lastLevel, "info")
	}
}

func TestNotifyTool_MissingMessage(t *testing.T) {
	notifier := &mockNotifier{}
	tool := NewNotifyTool(notifier)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"channel":"discord"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
}

func TestNotifyTool_Error(t *testing.T) {
	notifier := &mockNotifier{err: fmt.Errorf("discord down")}
	tool := NewNotifyTool(notifier)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"message":"Hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "discord down") {
		t.Errorf("content = %q", result.Content)
	}
}

// ─── SearchMemoryTool Tests ───────────────────────────────────────────────

func TestSearchMemoryTool_Success(t *testing.T) {
	runner := &mockCommandRunner{output: []byte(`[{"id":1,"title":"Found something"}]`)}
	tool := NewSearchMemoryTool("/usr/local/bin/mnemo", runner)

	if tool.Name() != "search_memory" {
		t.Errorf("name = %q, want %q", tool.Name(), "search_memory")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"architecture"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Found something") {
		t.Errorf("content = %q", result.Content)
	}
	// Verify CLI args
	if len(runner.lastArgs) < 3 {
		t.Fatalf("expected at least 3 args, got %v", runner.lastArgs)
	}
	if runner.lastArgs[0] != "/usr/local/bin/mnemo" {
		t.Errorf("binary = %q", runner.lastArgs[0])
	}
	if runner.lastArgs[1] != "search" {
		t.Errorf("subcommand = %q", runner.lastArgs[1])
	}
	if runner.lastArgs[2] != "architecture" {
		t.Errorf("query = %q", runner.lastArgs[2])
	}
}

func TestSearchMemoryTool_WithFilters(t *testing.T) {
	runner := &mockCommandRunner{output: []byte(`[]`)}
	tool := NewSearchMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"auth","project":"jarvis","type":"decision"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	// Verify --project and --type flags
	argsStr := strings.Join(runner.lastArgs, " ")
	if !strings.Contains(argsStr, "--project jarvis") {
		t.Errorf("args = %q, should contain --project jarvis", argsStr)
	}
	if !strings.Contains(argsStr, "--type decision") {
		t.Errorf("args = %q, should contain --type decision", argsStr)
	}
}

func TestSearchMemoryTool_CLIError(t *testing.T) {
	runner := &mockCommandRunner{output: []byte("command not found"), err: fmt.Errorf("exit status 1")}
	tool := NewSearchMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "memory search failed") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestSearchMemoryTool_MissingQuery(t *testing.T) {
	runner := &mockCommandRunner{}
	tool := NewSearchMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: query") {
		t.Errorf("content = %q", result.Content)
	}
}

// ─── SaveMemoryTool Tests ─────────────────────────────────────────────────

func TestSaveMemoryTool_Success(t *testing.T) {
	runner := &mockCommandRunner{output: []byte("observation saved: id=42")}
	tool := NewSaveMemoryTool("/usr/local/bin/mnemo", runner)

	if tool.Name() != "save_memory" {
		t.Errorf("name = %q, want %q", tool.Name(), "save_memory")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Auth decision","content":"Chose JWT","type":"decision","project":"jarvis"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "observation saved") {
		t.Errorf("content = %q", result.Content)
	}
	// Verify CLI args
	argsStr := strings.Join(runner.lastArgs, " ")
	if !strings.Contains(argsStr, "save") {
		t.Errorf("args = %q, should contain 'save'", argsStr)
	}
	if !strings.Contains(argsStr, "--type decision") {
		t.Errorf("args = %q, should contain --type decision", argsStr)
	}
	if !strings.Contains(argsStr, "--project jarvis") {
		t.Errorf("args = %q, should contain --project jarvis", argsStr)
	}
}

func TestSaveMemoryTool_MissingTitle(t *testing.T) {
	runner := &mockCommandRunner{}
	tool := NewSaveMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"content":"data","type":"decision"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: title") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestSaveMemoryTool_MissingContent(t *testing.T) {
	runner := &mockCommandRunner{}
	tool := NewSaveMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Test","type":"decision"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: content") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestSaveMemoryTool_MissingType(t *testing.T) {
	runner := &mockCommandRunner{}
	tool := NewSaveMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Test","content":"data"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: type") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestSaveMemoryTool_CLIError(t *testing.T) {
	runner := &mockCommandRunner{output: []byte("error"), err: fmt.Errorf("exit status 1")}
	tool := NewSaveMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Test","content":"data","type":"decision"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "memory save failed") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestSaveMemoryTool_NoProject(t *testing.T) {
	runner := &mockCommandRunner{output: []byte("saved")}
	tool := NewSaveMemoryTool("/usr/local/bin/mnemo", runner)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"title":"Test","content":"data","type":"bugfix"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	argsStr := strings.Join(runner.lastArgs, " ")
	if strings.Contains(argsStr, "--project") {
		t.Errorf("args = %q, should NOT contain --project when empty", argsStr)
	}
}

// ─── AsyncDelegateTool Tests ─────────────────────────────────────────

func TestAsyncDelegateTool_Success(t *testing.T) {
	handler := &mockAsyncDelegateHandler{jobID: "42"}
	tool := NewAsyncDelegateTool(handler)

	if tool.Name() != "delegate" {
		t.Errorf("name = %q, want %q", tool.Name(), "delegate")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"fix bug","project":"jarvis","working_dir":"/home/mx/projects"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "42") {
		t.Errorf("content should contain job ID, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "Delegado") {
		t.Errorf("content should confirm delegation, got %q", result.Content)
	}
	if handler.lastTask != "fix bug" {
		t.Errorf("task = %q, want %q", handler.lastTask, "fix bug")
	}
	if handler.lastProject != "jarvis" {
		t.Errorf("project = %q, want %q", handler.lastProject, "jarvis")
	}
	if handler.lastWorkingDir != "/home/mx/projects" {
		t.Errorf("working_dir = %q", handler.lastWorkingDir)
	}
}

func TestAsyncDelegateTool_Error(t *testing.T) {
	handler := &mockAsyncDelegateHandler{err: fmt.Errorf("worker pool full")}
	tool := NewAsyncDelegateTool(handler)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"something"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "worker pool full") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestDelegateTool_PreferAsync(t *testing.T) {
	// When both handlers are set, async should be preferred.
	syncHandler := &mockDelegateHandler{result: "sync result"}
	asyncHandler := &mockAsyncDelegateHandler{jobID: "99"}

	tool := &DelegateTool{handler: syncHandler, asyncHandler: asyncHandler}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "99") {
		t.Errorf("should use async handler, got %q", result.Content)
	}
	if syncHandler.lastTask != "" {
		t.Error("sync handler should not have been called")
	}
}

func TestDelegateTool_FallbackToSync(t *testing.T) {
	// When only sync handler is set, use it.
	syncHandler := &mockDelegateHandler{result: "sync result"}

	tool := NewDelegateTool(syncHandler)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "sync result" {
		t.Errorf("content = %q, want %q", result.Content, "sync result")
	}
}

// ─── ListJobsTool Tests ──────────────────────────────────────────────

func TestListJobsTool_Success(t *testing.T) {
	lister := &mockJobLister{jobs: []*Job{
		{ID: "1", Task: "fix bug", Project: "jarvis", Status: JobStatusSuccess},
		{ID: "2", Task: "review code", Project: "mnemo", Status: JobStatusRunning},
	}}
	tool := NewListJobsTool(lister)

	if tool.Name() != "list_jobs" {
		t.Errorf("name = %q, want %q", tool.Name(), "list_jobs")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var jobs []*Job
	if err := json.Unmarshal([]byte(result.Content), &jobs); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("got %d jobs, want 2", len(jobs))
	}
}

func TestListJobsTool_FilterByStatus(t *testing.T) {
	lister := &mockJobLister{jobs: []*Job{
		{ID: "1", Task: "done task", Status: JobStatusSuccess},
		{ID: "2", Task: "running task", Status: JobStatusRunning},
		{ID: "3", Task: "another done", Status: JobStatusSuccess},
	}}
	tool := NewListJobsTool(lister)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"status":"success"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var jobs []*Job
	if err := json.Unmarshal([]byte(result.Content), &jobs); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("got %d jobs, want 2 (filtered)", len(jobs))
	}
}

func TestListJobsTool_Empty(t *testing.T) {
	lister := &mockJobLister{jobs: []*Job{}}
	tool := NewListJobsTool(lister)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestListJobsTool_NilParams(t *testing.T) {
	lister := &mockJobLister{jobs: []*Job{
		{ID: "1", Task: "task", Status: JobStatusPending},
	}}
	tool := NewListJobsTool(lister)

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

// ─── GetJobTool Tests ────────────────────────────────────────────────

func TestGetJobTool_Found(t *testing.T) {
	lister := &mockJobLister{jobs: []*Job{
		{ID: "5", Task: "important task", Project: "jarvis", Status: JobStatusSuccess},
	}}
	tool := NewGetJobTool(lister)

	if tool.Name() != "get_job" {
		t.Errorf("name = %q, want %q", tool.Name(), "get_job")
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"5"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var job Job
	if err := json.Unmarshal([]byte(result.Content), &job); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if job.ID != "5" {
		t.Errorf("id = %q, want %q", job.ID, "5")
	}
	if job.Task != "important task" {
		t.Errorf("task = %q", job.Task)
	}
}

func TestGetJobTool_NotFound(t *testing.T) {
	lister := &mockJobLister{jobs: []*Job{}}
	tool := NewGetJobTool(lister)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"999"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "not found") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestGetJobTool_MissingID(t *testing.T) {
	lister := &mockJobLister{}
	tool := NewGetJobTool(lister)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if !strings.Contains(result.Content, "missing required parameter: id") {
		t.Errorf("content = %q", result.Content)
	}
}

func TestGetJobTool_InvalidJSON(t *testing.T) {
	lister := &mockJobLister{}
	tool := NewGetJobTool(lister)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{{{`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
}

// ─── DefaultCommandRunner Test ────────────────────────────────────────────

func TestDefaultCommandRunner(t *testing.T) {
	runner := DefaultCommandRunner()
	if runner == nil {
		t.Fatal("expected non-nil runner")
	}
}

// ─── Tool Metadata Tests (Description, Parameters, Name) ─────────────────

func TestAllTools_Metadata(t *testing.T) {
	tools := []struct {
		tool        Tool
		wantName    string
		wantDescNon string // substring that must appear in description
	}{
		{NewLoadSkillTool(&mockSkillLoader{}), "load_skill", "skill"},
		{NewCreateTaskTool(&mockTaskStore{}, "u"), "create_task", "task"},
		{NewListTasksTool(&mockTaskStore{}, "u"), "list_tasks", "task"},
		{NewCompleteTaskTool(&mockTaskStore{}, "u"), "complete_task", "task"},
		{NewAsyncDelegateTool(&mockAsyncDelegateHandler{}), "delegate", "worker"},
		{NewListJobsTool(&mockJobLister{}), "list_jobs", "job"},
		{NewGetJobTool(&mockJobLister{}), "get_job", "job"},
		{NewNotifyTool(&mockNotifier{}), "notify", "notification"},
		{NewSearchMemoryTool("/bin/e", &mockCommandRunner{}), "search_memory", "memory"},
		{NewSaveMemoryTool("/bin/e", &mockCommandRunner{}), "save_memory", "memory"},
	}

	for _, tt := range tools {
		t.Run(tt.wantName, func(t *testing.T) {
			if tt.tool.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", tt.tool.Name(), tt.wantName)
			}
			desc := tt.tool.Description()
			if desc == "" {
				t.Error("Description() is empty")
			}
			if !strings.Contains(strings.ToLower(desc), tt.wantDescNon) {
				t.Errorf("Description() = %q, should contain %q", desc, tt.wantDescNon)
			}
			params := tt.tool.Parameters()
			var schema map[string]interface{}
			if err := json.Unmarshal(params, &schema); err != nil {
				t.Fatalf("Parameters() invalid JSON: %v", err)
			}
			if schema["type"] != "object" {
				t.Errorf("Parameters schema type = %v, want 'object'", schema["type"])
			}
		})
	}
}

// ─── Memory Tool nil runner defaults ──────────────────────────────────────

func TestNewSearchMemoryTool_NilRunner(t *testing.T) {
	tool := NewSearchMemoryTool("/bin/mnemo", nil)
	if tool.runner == nil {
		t.Error("expected default runner, got nil")
	}
}

func TestNewSaveMemoryTool_NilRunner(t *testing.T) {
	tool := NewSaveMemoryTool("/bin/mnemo", nil)
	if tool.runner == nil {
		t.Error("expected default runner, got nil")
	}
}

// ─── Invalid JSON Tests (covers all tools) ────────────────────────────────

func TestAllTools_InvalidJSON(t *testing.T) {
	tools := []Tool{
		NewLoadSkillTool(&mockSkillLoader{}),
		NewCreateTaskTool(&mockTaskStore{}, "user-1"),
		NewListTasksTool(&mockTaskStore{}, "user-1"),
		NewCompleteTaskTool(&mockTaskStore{}, "user-1"),
		NewDelegateTool(&mockDelegateHandler{}),
		NewAsyncDelegateTool(&mockAsyncDelegateHandler{}),
		NewGetJobTool(&mockJobLister{}),
		NewNotifyTool(&mockNotifier{}),
		NewSearchMemoryTool("/bin/mnemo", &mockCommandRunner{}),
		NewSaveMemoryTool("/bin/mnemo", &mockCommandRunner{}),
	}

	for _, tool := range tools {
		t.Run(tool.Name(), func(t *testing.T) {
			result, err := tool.Execute(context.Background(), json.RawMessage(`{{{invalid`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Error("expected IsError = true for invalid JSON")
			}
			if !strings.Contains(result.Content, "invalid parameters") {
				t.Errorf("content = %q, should mention invalid parameters", result.Content)
			}
		})
	}
}
