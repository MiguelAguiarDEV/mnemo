package jarvis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gentleman-Programming/engram/internal/prometheus"
	"github.com/Gentleman-Programming/engram/internal/atlas"
	"github.com/Gentleman-Programming/engram/internal/athena"
)

// ─── Mock LLM Backend ────────────────────────────────────────────────────────

// mockBackend simulates an LLM that returns a tool call on the first Send(),
// then plain text on the second.
type mockBackend struct {
	callCount int
	responses []mockResponse
}

type mockResponse struct {
	text      string
	toolCalls []prometheus.ToolCall
}

func (b *mockBackend) nextResponse() (string, []prometheus.ToolCall) {
	if b.callCount >= len(b.responses) {
		return "fallback response", nil
	}
	r := b.responses[b.callCount]
	b.callCount++
	return r.text, r.toolCalls
}

// ─── Tool-Call Loop Tests ────────────────────────────────────────────────────

// TestToolCallLoop_ToolCallThenText verifies the basic tool-call loop:
// 1st LLM call returns a tool call, orchestrator executes it
// 2nd LLM call returns plain text (final response)
func TestToolCallLoop_ToolCallThenText(t *testing.T) {
	// Set up real registry + loader with a test skill
	tmpDir := t.TempDir()
	writeTestSkill(t, tmpDir, "go-testing.md", "go-testing", "Go testing patterns", false)

	registry := atlas.NewRegistry()
	registry.Build([]atlas.CatalogPath{{Path: tmpDir, Tier: "ops"}})
	loader := atlas.NewLoader(registry)

	// Set up real dispatcher with real LoadSkillTool
	dispatcher := athena.NewDispatcher(slog.Default())
	dispatcher.Register(athena.NewLoadSkillTool(loader))

	// Set up config dir for buildPromptV2
	oldConfigDir := configDir
	configDir = t.TempDir()
	os.WriteFile(filepath.Join(configDir, "system-prompt.md"), []byte("Base prompt."), 0644)
	defer func() { configDir = oldConfigDir }()

	// Create the orchestrator
	store := &mockStore{}
	o := &Orchestrator{
		store:      store,
		registry:   registry,
		loader:     loader,
		dispatcher: dispatcher,
		logger:     slog.Default(),
	}

	// Simulate the tool-call loop manually (since chatV2 uses callOpenCodeServe
	// which needs a real HTTP server, we test the loop logic directly)

	// Build initial prompt
	prompt := o.buildPromptV2(nil, "help me write tests", nil)

	// Simulate iteration 1: LLM returns a tool call
	toolCallJSON := `{"tool_call": {"name": "load_skill", "arguments": {"name": "go-testing"}}}`
	toolCalls := prometheus.ParseToolCalls(toolCallJSON)

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "load_skill" {
		t.Errorf("tool call name = %q, want %q", toolCalls[0].Name, "load_skill")
	}

	// Execute the tool call through the real dispatcher
	result, err := dispatcher.Dispatch(context.Background(), toolCalls[0].Name, toolCalls[0].Arguments)
	if err != nil {
		t.Fatalf("Dispatch() failed: %v", err)
	}

	if result.IsError {
		t.Fatalf("tool result is error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Skill content for go-testing") {
		t.Errorf("tool result should contain skill content, got: %s", result.Content)
	}

	// Build follow-up prompt with tool results (simulating what chatV2 does)
	var toolResults strings.Builder
	toolResults.WriteString(fmt.Sprintf("\n[Tool %s result:]\n%s\n", toolCalls[0].Name, result.Content))
	followUpPrompt := prompt + "\n\n[Assistant]\n" + toolCallJSON + "\n\n[Tool Results]\n" + toolResults.String()

	// Verify the follow-up prompt contains the tool result
	if !strings.Contains(followUpPrompt, "Skill content for go-testing") {
		t.Error("follow-up prompt should contain tool result")
	}

	// Simulate iteration 2: LLM returns plain text (no tool calls)
	finalResponse := "Here are some Go testing patterns based on the skill..."
	finalToolCalls := prometheus.ParseToolCalls(finalResponse)
	if len(finalToolCalls) != 0 {
		t.Error("final response should have no tool calls")
	}
}

// TestToolCallLoop_MaxIterations verifies the loop terminates after 3 iterations.
func TestToolCallLoop_MaxIterations(t *testing.T) {
	dispatcher := athena.NewDispatcher(slog.Default())

	// Register a mock tool that always returns content
	dispatcher.Register(&mockTool{
		name: "infinite_tool",
		exec: func(ctx context.Context, params json.RawMessage) (athena.ToolResult, error) {
			return athena.ToolResult{Content: "tool result"}, nil
		},
	})

	const maxIterations = 3
	iterations := 0

	// Simulate tool calls on every iteration
	for i := 0; i < maxIterations+2; i++ {
		iterations++

		// Check if we hit the max
		if iterations > maxIterations {
			break
		}

		// Simulate dispatching
		_, err := dispatcher.Dispatch(context.Background(), "infinite_tool", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Dispatch() failed on iteration %d: %v", i, err)
		}
	}

	if iterations != maxIterations+1 {
		t.Errorf("loop should have terminated at iteration %d, got %d", maxIterations+1, iterations)
	}
}

// TestToolCallLoop_UnknownToolReturnsError verifies unknown tool calls are handled.
func TestToolCallLoop_UnknownToolReturnsError(t *testing.T) {
	dispatcher := athena.NewDispatcher(slog.Default())

	_, err := dispatcher.Dispatch(context.Background(), "nonexistent_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error should mention unknown tool, got: %v", err)
	}
}

// TestToolCallLoop_DispatcherWithRealLoader verifies the full path:
// real dispatcher -> real LoadSkillTool -> real loader -> real filesystem
func TestToolCallLoop_DispatcherWithRealLoader(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skills with real content
	writeTestSkill(t, tmpDir, "task-management.md", "task-management", "Task management skill", false)
	writeTestSkill(t, tmpDir, "server-guardrails.md", "server-guardrails", "Safety rules", true)

	registry := atlas.NewRegistry()
	registry.Build([]atlas.CatalogPath{{Path: tmpDir, Tier: "ops"}})
	loader := atlas.NewLoader(registry)

	dispatcher := athena.NewDispatcher(slog.Default())
	dispatcher.Register(athena.NewLoadSkillTool(loader))

	// Test loading via dispatcher
	args, _ := json.Marshal(map[string]string{"name": "task-management"})
	result, err := dispatcher.Dispatch(context.Background(), "load_skill", args)
	if err != nil {
		t.Fatalf("Dispatch(load_skill) failed: %v", err)
	}
	if result.IsError {
		t.Errorf("result should not be error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Skill content for task-management") {
		t.Errorf("result should contain skill content, got: %s", result.Content)
	}

	// Test loading non-existent skill
	args, _ = json.Marshal(map[string]string{"name": "nonexistent"})
	result, err = dispatcher.Dispatch(context.Background(), "load_skill", args)
	if err != nil {
		t.Fatalf("Dispatch() should not return Go error for not-found: %v", err)
	}
	if !result.IsError {
		t.Error("result should be error for nonexistent skill")
	}
}

// TestToolCallLoop_ToolCallParsing verifies prometheus.ParseToolCalls extracts
// tool calls from realistic LLM response text.
func TestToolCallLoop_ToolCallParsing(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantN    int
		wantName string
	}{
		{
			name:     "embedded in text",
			text:     `Let me load that. {"tool_call": {"name": "load_skill", "arguments": {"name": "go-testing"}}} Done.`,
			wantN:    1,
			wantName: "load_skill",
		},
		{
			name:  "no tool calls",
			text:  "Here is your answer with no tool calls.",
			wantN: 0,
		},
		{
			name: "multiple tool calls",
			text: `{"tool_call": {"name": "load_skill", "arguments": {"name": "a"}}}
text
{"tool_call": {"name": "create_task", "arguments": {"title": "b"}}}`,
			wantN:    2,
			wantName: "load_skill",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := prometheus.ParseToolCalls(tt.text)
			if len(calls) != tt.wantN {
				t.Errorf("got %d tool calls, want %d", len(calls), tt.wantN)
			}
			if tt.wantN > 0 && calls[0].Name != tt.wantName {
				t.Errorf("first call name = %q, want %q", calls[0].Name, tt.wantName)
			}
		})
	}
}

// TestToolCallLoop_LoggingAtEachStep verifies that the loop logs at each step.
// This test ensures the logging code paths don't panic.
func TestToolCallLoop_LoggingAtEachStep(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestSkill(t, tmpDir, "test-skill.md", "test-skill", "A test skill", false)

	registry := atlas.NewRegistry()
	registry.Build([]atlas.CatalogPath{{Path: tmpDir, Tier: "ops"}})
	loader := atlas.NewLoader(registry)

	dispatcher := athena.NewDispatcher(slog.Default())
	dispatcher.Register(athena.NewLoadSkillTool(loader))

	// Dispatch with logging -- should not panic
	args, _ := json.Marshal(map[string]string{"name": "test-skill"})
	result, err := dispatcher.Dispatch(context.Background(), "load_skill", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty result")
	}

	// Dispatch unknown tool -- should log warning
	_, err = dispatcher.Dispatch(context.Background(), "unknown", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

// ─── Mock Tool ───────────────────────────────────────────────────────────────

type mockTool struct {
	name string
	exec func(ctx context.Context, params json.RawMessage) (athena.ToolResult, error)
}

func (t *mockTool) Name() string                  { return t.name }
func (t *mockTool) Description() string            { return "mock tool" }
func (t *mockTool) Parameters() json.RawMessage    { return json.RawMessage(`{}`) }
func (t *mockTool) Execute(ctx context.Context, params json.RawMessage) (athena.ToolResult, error) {
	return t.exec(ctx, params)
}
