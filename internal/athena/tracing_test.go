package athena

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockTool is a simple tool for testing.
type mockTracingTool struct {
	name   string
	result ToolResult
	err    error
	delay  time.Duration
}

func (t *mockTracingTool) Name() string              { return t.name }
func (t *mockTracingTool) Description() string        { return "mock tool for tracing tests" }
func (t *mockTracingTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *mockTracingTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
	return t.result, t.err
}

func TestTracingDispatcher_CallsUnderlyingDispatcher(t *testing.T) {
	dispatcher := NewDispatcher(slog.Default())
	dispatcher.Register(&mockTracingTool{
		name:   "test_tool",
		result: ToolResult{Content: "hello from tool"},
	})

	td := NewTracingDispatcher(dispatcher, TracingConfig{
		// No TraceURL = trace recording skipped, but dispatch still works.
		SessionID: "test-session",
	})

	result, err := td.Dispatch(context.Background(), "test_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.Content != "hello from tool" {
		t.Fatalf("expected 'hello from tool', got %q", result.Content)
	}
}

func TestTracingDispatcher_UnknownToolReturnsError(t *testing.T) {
	dispatcher := NewDispatcher(slog.Default())
	td := NewTracingDispatcher(dispatcher, TracingConfig{SessionID: "s1"})

	_, err := td.Dispatch(context.Background(), "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestTracingDispatcher_PostsTrace(t *testing.T) {
	var mu sync.Mutex
	var receivedBody []byte
	var receivedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"occurred_at":"2026-04-03T00:00:00Z"}`))
	}))
	defer server.Close()

	dispatcher := NewDispatcher(slog.Default())
	dispatcher.Register(&mockTracingTool{
		name:   "my_tool",
		result: ToolResult{Content: "tool output"},
	})

	td := NewTracingDispatcher(dispatcher, TracingConfig{
		TraceURL:  server.URL,
		AuthToken: "eng_test_token",
		SessionID: "session-123",
		Project:   "jarvis-dashboard",
		Agent:     "jarvis",
	})

	result, err := td.Dispatch(context.Background(), "my_tool", json.RawMessage(`{"key":"value"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "tool output" {
		t.Fatalf("unexpected result: %q", result.Content)
	}

	// Wait for the goroutine to complete.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if receivedAuth != "Bearer eng_test_token" {
		t.Fatalf("expected auth header 'Bearer eng_test_token', got %q", receivedAuth)
	}

	var payload tracePayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal trace payload: %v", err)
	}

	if payload.SessionID != "session-123" {
		t.Errorf("session_id = %q, want 'session-123'", payload.SessionID)
	}
	if payload.Project != "jarvis-dashboard" {
		t.Errorf("project = %q, want 'jarvis-dashboard'", payload.Project)
	}
	if payload.Agent != "jarvis" {
		t.Errorf("agent = %q, want 'jarvis'", payload.Agent)
	}
	if payload.ToolName != "my_tool" {
		t.Errorf("tool_name = %q, want 'my_tool'", payload.ToolName)
	}
	if payload.OutputText != "tool output" {
		t.Errorf("output_text = %q, want 'tool output'", payload.OutputText)
	}
	if payload.DurationMs == nil || *payload.DurationMs < 0 {
		t.Errorf("duration_ms should be >= 0, got %v", payload.DurationMs)
	}
	if string(payload.InputJSON) != `{"key":"value"}` {
		t.Errorf("input_json = %q, want '{\"key\":\"value\"}'", string(payload.InputJSON))
	}
}

func TestTracingDispatcher_TraceFailureDoesNotBlockTool(t *testing.T) {
	// Server that always returns 500.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dispatcher := NewDispatcher(slog.Default())
	dispatcher.Register(&mockTracingTool{
		name:   "fast_tool",
		result: ToolResult{Content: "fast result"},
	})

	td := NewTracingDispatcher(dispatcher, TracingConfig{
		TraceURL:  server.URL,
		SessionID: "s1",
	})

	start := time.Now()
	result, err := td.Dispatch(context.Background(), "fast_tool", json.RawMessage(`{}`))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("tool should not fail due to trace error: %v", err)
	}
	if result.Content != "fast result" {
		t.Fatalf("unexpected result: %q", result.Content)
	}

	// Dispatch should return nearly instantly (trace is fire-and-forget).
	if elapsed > 1*time.Second {
		t.Fatalf("dispatch took too long (%s), trace may be blocking", elapsed)
	}
}

func TestTracingDispatcher_ConnectionRefusedDoesNotBlockTool(t *testing.T) {
	dispatcher := NewDispatcher(slog.Default())
	dispatcher.Register(&mockTracingTool{
		name:   "ok_tool",
		result: ToolResult{Content: "ok"},
	})

	// Point to a port that's not listening.
	td := NewTracingDispatcher(dispatcher, TracingConfig{
		TraceURL:  "http://127.0.0.1:19999/traces/tool-call",
		SessionID: "s1",
	})

	start := time.Now()
	result, err := td.Dispatch(context.Background(), "ok_tool", json.RawMessage(`{}`))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("unexpected result: %q", result.Content)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("dispatch took too long (%s), should not block on trace failure", elapsed)
	}
}

func TestTracingDispatcher_TruncatesLargeOutput(t *testing.T) {
	var mu sync.Mutex
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"occurred_at":"2026-04-03T00:00:00Z"}`))
	}))
	defer server.Close()

	// Create output larger than 10KB.
	largeOutput := strings.Repeat("x", 20*1024)

	dispatcher := NewDispatcher(slog.Default())
	dispatcher.Register(&mockTracingTool{
		name:   "big_tool",
		result: ToolResult{Content: largeOutput},
	})

	td := NewTracingDispatcher(dispatcher, TracingConfig{
		TraceURL:  server.URL,
		SessionID: "s1",
		Agent:     "jarvis",
	})

	result, err := td.Dispatch(context.Background(), "big_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The result returned to the caller should NOT be truncated.
	if len(result.Content) != 20*1024 {
		t.Fatalf("result should not be truncated, got %d bytes", len(result.Content))
	}

	// Wait for trace goroutine.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	var payload tracePayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Trace output should be truncated to ~10KB + "[truncated]".
	if len(payload.OutputText) > maxTraceOutputBytes+50 {
		t.Errorf("trace output should be truncated, got %d bytes", len(payload.OutputText))
	}
	if !strings.HasSuffix(payload.OutputText, "[truncated]") {
		t.Error("truncated output should end with '[truncated]'")
	}
}

func TestTracingDispatcher_DefaultAgent(t *testing.T) {
	td := NewTracingDispatcher(NewDispatcher(slog.Default()), TracingConfig{
		SessionID: "s1",
	})
	if td.cfg.Agent != "jarvis" {
		t.Errorf("default agent should be 'jarvis', got %q", td.cfg.Agent)
	}
}

func TestTracingDispatcher_ToolErrorRecordsErrorInTrace(t *testing.T) {
	var mu sync.Mutex
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"occurred_at":"2026-04-03T00:00:00Z"}`))
	}))
	defer server.Close()

	dispatcher := NewDispatcher(slog.Default())
	// No tools registered, so dispatching "missing" returns error.

	td := NewTracingDispatcher(dispatcher, TracingConfig{
		TraceURL:  server.URL,
		SessionID: "s1",
	})

	_, err := td.Dispatch(context.Background(), "missing", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	var payload tracePayload
	if unmarshalErr := json.Unmarshal(receivedBody, &payload); unmarshalErr != nil {
		t.Fatalf("failed to unmarshal: %v", unmarshalErr)
	}

	if !strings.Contains(payload.OutputText, "error:") {
		t.Errorf("trace output should contain error, got %q", payload.OutputText)
	}
}
