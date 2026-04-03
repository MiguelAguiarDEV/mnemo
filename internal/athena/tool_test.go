package athena

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// mockTool implements Tool for testing.
type mockTool struct {
	name        string
	description string
	params      json.RawMessage
	execFn      func(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

func (m *mockTool) Name() string                { return m.name }
func (m *mockTool) Description() string          { return m.description }
func (m *mockTool) Parameters() json.RawMessage  { return m.params }
func (m *mockTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	return m.execFn(ctx, params)
}

func TestDispatcher_Register(t *testing.T) {
	d := NewDispatcher(slog.Default())
	tool := &mockTool{
		name:        "test-tool",
		description: "A test tool",
		params:      json.RawMessage(`{"type": "object"}`),
		execFn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: "ok"}, nil
		},
	}

	d.Register(tool)

	defs := d.ToolDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].Name != "test-tool" {
		t.Errorf("name = %q, want %q", defs[0].Name, "test-tool")
	}
	if defs[0].Description != "A test tool" {
		t.Errorf("description = %q, want %q", defs[0].Description, "A test tool")
	}
}

func TestDispatcher_Dispatch_Known(t *testing.T) {
	d := NewDispatcher(slog.Default())
	tool := &mockTool{
		name:        "echo",
		description: "Echoes input",
		params:      json.RawMessage(`{}`),
		execFn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: "hello"}, nil
		},
	}
	d.Register(tool)

	result, err := d.Dispatch(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "hello" {
		t.Errorf("content = %q, want %q", result.Content, "hello")
	}
	if result.IsError {
		t.Error("expected IsError = false")
	}
}

func TestDispatcher_Dispatch_Unknown(t *testing.T) {
	d := NewDispatcher(slog.Default())

	_, err := d.Dispatch(context.Background(), "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool: nonexistent") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "unknown tool: nonexistent")
	}
}

func TestDispatcher_Dispatch_ToolError(t *testing.T) {
	d := NewDispatcher(slog.Default())
	tool := &mockTool{
		name:        "failing",
		description: "Always fails",
		params:      json.RawMessage(`{}`),
		execFn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			return ToolResult{IsError: true, Content: "something broke"}, nil
		},
	}
	d.Register(tool)

	result, err := d.Dispatch(context.Background(), "failing", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError = true")
	}
	if result.Content != "something broke" {
		t.Errorf("content = %q, want %q", result.Content, "something broke")
	}
}

func TestDispatcher_ToolDefinitions_Multiple(t *testing.T) {
	d := NewDispatcher(slog.Default())
	for _, name := range []string{"alpha", "beta", "gamma"} {
		n := name
		d.Register(&mockTool{
			name:        n,
			description: n + " tool",
			params:      json.RawMessage(`{"type":"object"}`),
			execFn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
				return ToolResult{Content: n}, nil
			},
		})
	}

	defs := d.ToolDefinitions()
	if len(defs) != 3 {
		t.Fatalf("expected 3 definitions, got %d", len(defs))
	}
}

func TestDispatcher_ToolDefinitions_Serialization(t *testing.T) {
	d := NewDispatcher(slog.Default())
	d.Register(&mockTool{
		name:        "test",
		description: "Test tool",
		params:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		execFn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			return ToolResult{}, nil
		},
	})

	defs := d.ToolDefinitions()
	data, err := json.Marshal(defs)
	if err != nil {
		t.Fatalf("failed to marshal definitions: %v", err)
	}
	if !strings.Contains(string(data), `"name":"test"`) {
		t.Errorf("serialized JSON doesn't contain tool name")
	}
}

func TestDispatcher_ToolDefinitionsBlock_Empty(t *testing.T) {
	d := NewDispatcher(slog.Default())
	block := d.ToolDefinitionsBlock()
	if block != "" {
		t.Errorf("expected empty block, got %q", block)
	}
}

func TestDispatcher_ToolDefinitionsBlock_WithTools(t *testing.T) {
	d := NewDispatcher(slog.Default())
	d.Register(&mockTool{
		name:        "my-tool",
		description: "Does things",
		params:      json.RawMessage(`{}`),
		execFn: func(ctx context.Context, params json.RawMessage) (ToolResult, error) {
			return ToolResult{}, nil
		},
	})

	block := d.ToolDefinitionsBlock()
	if !strings.Contains(block, "## Available Tools") {
		t.Error("block missing header")
	}
	if !strings.Contains(block, "my-tool") {
		t.Error("block missing tool name")
	}
	if !strings.Contains(block, "Does things") {
		t.Error("block missing tool description")
	}
}

func TestNewDispatcher_NilLogger(t *testing.T) {
	d := NewDispatcher(nil)
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	if d.logger == nil {
		t.Fatal("expected non-nil logger")
	}
}
