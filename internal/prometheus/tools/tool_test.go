package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry("/tmp/test", nil)

	// Should have all 6 standard tools.
	all := r.All()
	if len(all) != 6 {
		t.Errorf("expected 6 tools, got %d", len(all))
	}

	// Verify each tool is registered.
	expectedTools := []string{"bash", "read", "write", "edit", "glob", "grep"}
	for _, name := range expectedTools {
		if _, ok := r.Get(name); !ok {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestRegistryGet_NotFound(t *testing.T) {
	r := NewRegistry("/tmp/test", nil)
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for nonexistent tool")
	}
}

func TestRegistryToolDefs(t *testing.T) {
	r := NewRegistry("/tmp/test", nil)
	tools := r.ToolDefs()

	if len(tools) != 6 {
		t.Errorf("expected 6 anthropic tools, got %d", len(tools))
	}

	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("tool name should not be empty")
		}
		if tool.Description == "" {
			t.Errorf("tool %q description should not be empty", tool.Name)
		}
		if len(tool.InputSchema) == 0 {
			t.Errorf("tool %q input schema should not be empty", tool.Name)
		}

		// Verify schema is valid JSON.
		var schema map[string]interface{}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Errorf("tool %q schema is not valid JSON: %v", tool.Name, err)
		}
	}
}

func TestRegistryRegister_Custom(t *testing.T) {
	r := NewRegistry("/tmp/test", nil)

	// Register a custom tool.
	custom := &mockTool{name: "custom"}
	r.Register(custom)

	got, ok := r.Get("custom")
	if !ok {
		t.Fatal("expected custom tool to be registered")
	}
	if got.Name() != "custom" {
		t.Errorf("got name %q, want %q", got.Name(), "custom")
	}

	// Should now have 7 tools.
	if len(r.All()) != 7 {
		t.Errorf("expected 7 tools, got %d", len(r.All()))
	}
}

// mockTool is a minimal ToolExecutor for testing registration.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string                                                    { return m.name }
func (m *mockTool) Description() string                                              { return "mock tool" }
func (m *mockTool) InputSchema() json.RawMessage                                     { return json.RawMessage(`{}`) }
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "mock result", nil
}
