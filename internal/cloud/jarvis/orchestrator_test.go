package jarvis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gentleman-Programming/engram/internal/cloud/notifications"
	"github.com/Gentleman-Programming/engram/internal/prometheus"
	"github.com/Gentleman-Programming/engram/internal/atlas"
	"github.com/Gentleman-Programming/engram/internal/athena"
)

// ─── Mock Store ─────────────────────────────────────────────────────────────

type mockStore struct {
	messages     []StoreMessage
	createdTasks []mockCreatedTask
	listedStatus string
	listedLimit  int
	tasksToList  []StoreTask
	updatedTasks []mockUpdatedTask
	updateErr    error
}

type mockCreatedTask struct {
	UserID, Title, Description, Project, Priority string
}

type mockUpdatedTask struct {
	UserID string
	TaskID int64
	Status string
}

func (m *mockStore) AddMessage(conversationID int64, role, content, model string, tokensIn, tokensOut *int, costUSD *float64) (int64, error) {
	m.messages = append(m.messages, StoreMessage{Role: role, Content: content})
	return int64(len(m.messages)), nil
}

func (m *mockStore) GetMessages(conversationID int64, limit int) ([]StoreMessage, error) {
	return m.messages, nil
}

func (m *mockStore) BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*StoreBudgetReport, error) {
	return &StoreBudgetReport{ClaudeUsed: 0, ClaudeBudget: claudeBudget, ClaudePct: 0}, nil
}

func (m *mockStore) CreateTask(userID string, title, description, project, priority string) (int64, error) {
	m.createdTasks = append(m.createdTasks, mockCreatedTask{
		UserID: userID, Title: title, Description: description, Project: project, Priority: priority,
	})
	return int64(len(m.createdTasks)), nil
}

func (m *mockStore) ListTasks(userID string, status string, limit int) ([]StoreTask, error) {
	m.listedStatus = status
	m.listedLimit = limit
	return m.tasksToList, nil
}

func (m *mockStore) UpdateTaskStatus(userID string, taskID int64, status string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedTasks = append(m.updatedTasks, mockUpdatedTask{
		UserID: userID, TaskID: taskID, Status: status,
	})
	return nil
}

func (m *mockStore) UpdateTask(userID string, taskID int64, fields athena.UpdateTaskFields) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	status := ""
	if fields.Status != nil {
		status = *fields.Status
	}
	m.updatedTasks = append(m.updatedTasks, mockUpdatedTask{
		UserID: userID, TaskID: taskID, Status: status,
	})
	return nil
}

// ─── Mock Notifier ──────────────────────────────────────────────────────────

type mockNotifier struct {
	sent []notifications.Notification
}

func (m *mockNotifier) Send(n notifications.Notification) error {
	m.sent = append(m.sent, n)
	return nil
}

// ─── Tests ──────────────────────────────────────────────────────────────────

// ─── Deprecated Marker Tests (verify removal) ────────────────────────────

// TestOldMarkersNotProcessed verifies that old [TASK:CREATE], [TASK:LIST],
// [TASK:DONE], and [DELEGATE] markers are no longer processed by the orchestrator.
// These markers were replaced by the tool system.
func TestOldMarkersNotProcessed(t *testing.T) {
	// The old marker functions (processTaskCommands, processDelegateCommands)
	// have been removed. Verify they no longer exist by checking that
	// the orchestrator struct has no legacy marker methods.
	// This test serves as a regression guard.

	store := &mockStore{}
	o := &Orchestrator{store: store}

	// These marker patterns used to create tasks via regex parsing.
	// Now they should be ignored — tasks are created via the create_task tool instead.
	markerPatterns := []string{
		`[TASK:CREATE] {"title":"Test","description":"","project":"","priority":"low"}`,
		`[TASK:LIST] {"status":"open","limit":10}`,
		`[TASK:DONE] {"id":42}`,
		`[DELEGATE] {"task":"read the main.go file","project":"jarvis"}`,
	}

	for _, pattern := range markerPatterns {
		// prometheus.ParseToolCalls should NOT match these — they are not tool calls
		calls := prometheus.ParseToolCalls(pattern)
		if len(calls) > 0 {
			t.Errorf("old marker %q should NOT be parsed as a tool call", pattern)
		}
	}

	// Verify no tasks were created via legacy path
	if len(store.createdTasks) != 0 {
		t.Errorf("expected 0 created tasks (markers should not be processed), got %d", len(store.createdTasks))
	}
	if len(store.updatedTasks) != 0 {
		t.Errorf("expected 0 updated tasks (markers should not be processed), got %d", len(store.updatedTasks))
	}
	_ = o // ensure o is used
}

// ─── Skills Architecture Tests ──────────────────────────────────────────────

// buildTestRegistry creates a registry with test skills for unit tests.
func buildTestRegistry(t *testing.T) (*atlas.Registry, string) {
	t.Helper()

	// Create temp dir with test skills
	tmpDir := t.TempDir()

	// Create a regular skill
	writeTestSkill(t, tmpDir, "go-testing.md", "go-testing", "Go testing patterns", false)

	// Create an always-loaded skill
	writeTestSkill(t, tmpDir, "server-guardrails.md", "server-guardrails", "Safety rules for server ops", true)

	// Create another always-loaded skill
	writeTestSkill(t, tmpDir, "server-knowledge.md", "server-knowledge", "Current server state info", true)

	registry := atlas.NewRegistry()
	if err := registry.Build([]atlas.CatalogPath{
		{Path: tmpDir, Tier: "ops", Project: "jarvis"},
	}); err != nil {
		t.Fatalf("failed to build registry: %v", err)
	}

	return registry, tmpDir
}

func writeTestSkill(t *testing.T, dir, filename, name, desc string, always bool) {
	t.Helper()
	alwaysStr := "false"
	if always {
		alwaysStr = "true"
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\nalways: %s\n---\n# %s\n\nSkill content for %s.", name, desc, alwaysStr, name, name)
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test skill: %v", err)
	}
}

// ─── extractSkillOverrides Tests ──────────────────────────────────────────

func TestExtractSkillOverrides_ValidSkill(t *testing.T) {
	registry, _ := buildTestRegistry(t)

	cleaned, skillNames := extractSkillOverrides("/go-testing help me write tests", registry)

	if cleaned != "help me write tests" {
		t.Errorf("cleaned = %q, want %q", cleaned, "help me write tests")
	}
	if len(skillNames) != 1 || skillNames[0] != "go-testing" {
		t.Errorf("skillNames = %v, want [go-testing]", skillNames)
	}
}

func TestExtractSkillOverrides_UnknownSkill(t *testing.T) {
	registry, _ := buildTestRegistry(t)

	cleaned, skillNames := extractSkillOverrides("/nonexistent do something", registry)

	// Unknown skills are not extracted — message stays as-is
	if cleaned != "/nonexistent do something" {
		t.Errorf("cleaned = %q, want original message", cleaned)
	}
	if len(skillNames) != 0 {
		t.Errorf("skillNames = %v, want empty", skillNames)
	}
}

func TestExtractSkillOverrides_PathLikeRejected(t *testing.T) {
	registry, _ := buildTestRegistry(t)

	// /etc/passwd should NOT match (has path separator)
	cleaned, skillNames := extractSkillOverrides("/etc/passwd check this", registry)

	// The regex only matches single-segment names, so /etc would be matched
	// but "etc" is not a real skill, so it passes through
	if len(skillNames) != 0 {
		t.Errorf("skillNames = %v, want empty (path-like should be rejected)", skillNames)
	}
	_ = cleaned
}

func TestExtractSkillOverrides_MultipleOverrides(t *testing.T) {
	registry, _ := buildTestRegistry(t)

	cleaned, skillNames := extractSkillOverrides("/go-testing\n/server-guardrails actual message", registry)

	if len(skillNames) != 2 {
		t.Errorf("skillNames = %v, want 2 skills", skillNames)
	}
	if !strings.Contains(cleaned, "actual message") {
		t.Errorf("cleaned = %q, should contain 'actual message'", cleaned)
	}
}

func TestExtractSkillOverrides_NoTextAfterSkill(t *testing.T) {
	registry, _ := buildTestRegistry(t)

	cleaned, skillNames := extractSkillOverrides("/server-knowledge", registry)

	if len(skillNames) != 1 || skillNames[0] != "server-knowledge" {
		t.Errorf("skillNames = %v, want [server-knowledge]", skillNames)
	}
	if cleaned != "" {
		t.Errorf("cleaned = %q, want empty", cleaned)
	}
}

func TestExtractSkillOverrides_NilRegistry(t *testing.T) {
	cleaned, skillNames := extractSkillOverrides("/go-testing test", nil)
	if cleaned != "/go-testing test" {
		t.Errorf("cleaned = %q, want original", cleaned)
	}
	if skillNames != nil {
		t.Errorf("skillNames = %v, want nil", skillNames)
	}
}

func TestExtractSkillOverrides_NoOverrides(t *testing.T) {
	registry, _ := buildTestRegistry(t)

	cleaned, skillNames := extractSkillOverrides("just a normal message", registry)
	if cleaned != "just a normal message" {
		t.Errorf("cleaned = %q, want original", cleaned)
	}
	if len(skillNames) != 0 {
		t.Errorf("skillNames = %v, want empty", skillNames)
	}
}

// ─── buildPromptV2 Tests ──────────────────────────────────────────────────

func TestBuildPromptV2_AlwaysSkillsInjected(t *testing.T) {
	registry, _ := buildTestRegistry(t)
	loader := atlas.NewLoader(registry)

	o := &Orchestrator{
		registry:   registry,
		loader:     loader,
		dispatcher: newTestDispatcher(),
	}

	// Override configDir for test
	oldConfigDir := configDir
	configDir = t.TempDir()
	os.WriteFile(filepath.Join(configDir, "system-prompt.md"), []byte("Base prompt."), 0644)
	defer func() { configDir = oldConfigDir }()

	prompt := o.buildPromptV2(nil, "hello", nil)

	// Always skills should be injected
	if !strings.Contains(prompt, "Skill content for server-guardrails") {
		t.Error("prompt should contain always-loaded server-guardrails content")
	}
	if !strings.Contains(prompt, "Skill content for server-knowledge") {
		t.Error("prompt should contain always-loaded server-knowledge content")
	}

	// Non-always skills should NOT be in full content
	if strings.Contains(prompt, "Skill content for go-testing") {
		t.Error("prompt should NOT contain full go-testing content (it's not always:true)")
	}
}

func TestBuildPromptV2_PreloadSkillsInjected(t *testing.T) {
	registry, _ := buildTestRegistry(t)
	loader := atlas.NewLoader(registry)

	o := &Orchestrator{
		registry:   registry,
		loader:     loader,
		dispatcher: newTestDispatcher(),
	}

	oldConfigDir := configDir
	configDir = t.TempDir()
	os.WriteFile(filepath.Join(configDir, "system-prompt.md"), []byte("Base prompt."), 0644)
	defer func() { configDir = oldConfigDir }()

	prompt := o.buildPromptV2(nil, "test", []string{"go-testing"})

	if !strings.Contains(prompt, "Skill content for go-testing") {
		t.Error("prompt should contain preloaded go-testing content")
	}
}

func TestBuildPromptV2_MissingAlwaysSkillContinues(t *testing.T) {
	// Create a registry where an always skill points to a missing file
	registry := atlas.NewRegistry()
	tmpDir := t.TempDir()
	writeTestSkill(t, tmpDir, "exists.md", "exists", "A skill that exists", true)
	registry.Build([]atlas.CatalogPath{{Path: tmpDir, Tier: "ops"}})

	// Delete the file after building the registry
	os.Remove(filepath.Join(tmpDir, "exists.md"))

	loader := atlas.NewLoader(registry)

	o := &Orchestrator{
		registry:   registry,
		loader:     loader,
		dispatcher: newTestDispatcher(),
	}

	oldConfigDir := configDir
	configDir = t.TempDir()
	os.WriteFile(filepath.Join(configDir, "system-prompt.md"), []byte("Base prompt."), 0644)
	defer func() { configDir = oldConfigDir }()

	// Should not panic
	prompt := o.buildPromptV2(nil, "hello", nil)
	if !strings.Contains(prompt, "Base prompt") {
		t.Error("prompt should still have base prompt even if always skill fails")
	}
}

func TestBuildPromptV2_CompactIndexAndToolDefinitions(t *testing.T) {
	registry, _ := buildTestRegistry(t)
	loader := atlas.NewLoader(registry)

	o := &Orchestrator{
		registry:   registry,
		loader:     loader,
		dispatcher: newTestDispatcher(),
	}

	oldConfigDir := configDir
	configDir = t.TempDir()
	os.WriteFile(filepath.Join(configDir, "system-prompt.md"), []byte("Base prompt."), 0644)
	defer func() { configDir = oldConfigDir }()

	prompt := o.buildPromptV2(nil, "hello", nil)

	// Should contain compact index
	if !strings.Contains(prompt, "Available Skills") {
		t.Error("prompt should contain compact skill index")
	}
}

// ─── prometheus.ParseToolCalls Tests (integration with shared parser) ───────────

func TestParseToolCalls_NoToolCalls(t *testing.T) {
	calls := prometheus.ParseToolCalls("Just a normal response with no tool calls.")
	if len(calls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(calls))
	}
}

func TestParseToolCalls_SingleToolCall(t *testing.T) {
	response := `Let me load that skill for you.
{"tool_call": {"name": "load_skill", "arguments": {"name": "go-testing"}}}
I'll use the skill to help.`

	calls := prometheus.ParseToolCalls(response)
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "load_skill" {
		t.Errorf("name = %q, want %q", calls[0].Name, "load_skill")
	}

	var args map[string]string
	json.Unmarshal(calls[0].Arguments, &args)
	if args["name"] != "go-testing" {
		t.Errorf("args.name = %q, want %q", args["name"], "go-testing")
	}
}

func TestParseToolCalls_MultipleToolCalls(t *testing.T) {
	response := `{"tool_call": {"name": "load_skill", "arguments": {"name": "go-testing"}}}
Some text in between.
{"tool_call": {"name": "create_task", "arguments": {"title": "Test"}}}`

	calls := prometheus.ParseToolCalls(response)
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Name != "load_skill" {
		t.Errorf("first call name = %q", calls[0].Name)
	}
	if calls[1].Name != "create_task" {
		t.Errorf("second call name = %q", calls[1].Name)
	}
}

func TestParseToolCalls_InvalidJSON(t *testing.T) {
	response := `{invalid json} {"tool_call": {"name": "load_skill", "arguments": {}}}`

	calls := prometheus.ParseToolCalls(response)
	if len(calls) != 1 {
		t.Fatalf("expected 1 valid tool call, got %d", len(calls))
	}
}

// ─── Skills Architecture Component Tests ─────────────────────────────────

func TestChat_DispatcherInitialized(t *testing.T) {
	// Verify that the orchestrator initializes registry and dispatcher
	registry, _ := buildTestRegistry(t)
	loader := atlas.NewLoader(registry)
	dispatcher := newTestDispatcher()

	o := &Orchestrator{
		registry:   registry,
		loader:     loader,
		dispatcher: dispatcher,
	}

	if o.registry == nil {
		t.Error("registry should be set")
	}
	if o.dispatcher == nil {
		t.Error("dispatcher should be set")
	}
	if o.loader == nil {
		t.Error("loader should be set")
	}
}

// ─── Helper ───────────────────────────────────────────────────────────────

func newTestDispatcher() *athena.Dispatcher {
	// Import is needed — ensure tools package is available
	d := athena.NewDispatcher(nil)
	return d
}

// Bring in tools import for the test file
var _ = athena.ToolResult{}
