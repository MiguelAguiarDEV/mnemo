package prometheus

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ─── Mock Backend ────────────────────────────────────────────────────────────

type mockBackend struct {
	sessionID  string
	sessionErr error
	response   Response
	sendErr    error

	lastRequest Request
	createCalls int
	sendCalls   int
}

func (m *mockBackend) CreateSession(ctx context.Context) (string, error) {
	m.createCalls++
	return m.sessionID, m.sessionErr
}

func (m *mockBackend) Send(ctx context.Context, req Request) (Response, error) {
	m.sendCalls++
	m.lastRequest = req
	return m.response, m.sendErr
}

// ─── Worker.Execute Tests ────────────────────────────────────────────────────

func TestWorkerExecute_Success(t *testing.T) {
	backend := &mockBackend{
		sessionID: "sess-123",
		response: Response{
			Text: "Task completed successfully. Files updated.",
			Usage: TokenUsage{
				InputTokens:  500,
				OutputTokens: 200,
			},
		},
	}

	worker := NewWorker(backend, nil)

	req := DelegateRequest{
		Task:       "list files in the current directory",
		Project:    "personal-knowledgebase",
		WorkingDir: "/home/mx/projects",
	}

	result, err := worker.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("status = %q, want %q", result.Status, "success")
	}
	if result.Output != "Task completed successfully. Files updated." {
		t.Errorf("output = %q", result.Output)
	}
	if result.TokensUsed.InputTokens != 500 {
		t.Errorf("input tokens = %d, want 500", result.TokensUsed.InputTokens)
	}
	if result.TokensUsed.OutputTokens != 200 {
		t.Errorf("output tokens = %d, want 200", result.TokensUsed.OutputTokens)
	}
	if result.Duration <= 0 {
		t.Error("duration should be positive")
	}
	if result.Error != "" {
		t.Errorf("error should be empty, got %q", result.Error)
	}

	// Verify backend was called correctly.
	if backend.createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", backend.createCalls)
	}
	if backend.sendCalls != 1 {
		t.Errorf("sendCalls = %d, want 1", backend.sendCalls)
	}
	if backend.lastRequest.SessionID != "sess-123" {
		t.Errorf("session_id = %q, want %q", backend.lastRequest.SessionID, "sess-123")
	}
}

func TestWorkerExecute_SessionError(t *testing.T) {
	backend := &mockBackend{
		sessionErr: fmt.Errorf("connection refused"),
	}

	worker := NewWorker(backend, nil)

	result, err := worker.Execute(context.Background(), DelegateRequest{
		Task: "do something",
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if result.Error == "" {
		t.Error("error message should be set")
	}
	if result.Duration <= 0 {
		t.Error("duration should be positive even on error")
	}
}

func TestWorkerExecute_SendError(t *testing.T) {
	backend := &mockBackend{
		sessionID: "sess-456",
		sendErr:   fmt.Errorf("model overloaded"),
	}

	worker := NewWorker(backend, nil)

	result, err := worker.Execute(context.Background(), DelegateRequest{
		Task:    "analyze code",
		Project: "jarvis",
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if result.Error == "" {
		t.Error("error message should be set")
	}
}

func TestWorkerExecute_ContextTimeout(t *testing.T) {
	backend := &mockBackend{
		sessionID: "sess-789",
		sendErr:   context.DeadlineExceeded,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	worker := NewWorker(backend, nil)

	result, err := worker.Execute(ctx, DelegateRequest{
		Task: "long running task",
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != "timeout" {
		t.Errorf("status = %q, want %q", result.Status, "timeout")
	}
}

func TestWorkerExecute_ModelSelection(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantModel string
	}{
		{"default", "", "claude-sonnet-4-6"},
		{"opus", "opus", "claude-opus-4-6"},
		{"sonnet", "sonnet", "claude-sonnet-4-6"},
		{"unknown", "gpt-999", "claude-sonnet-4-6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &mockBackend{
				sessionID: "sess-model",
				response:  Response{Text: "ok"},
			}

			worker := NewWorker(backend, nil)
			_, err := worker.Execute(context.Background(), DelegateRequest{
				Task:  "test",
				Model: tt.model,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if backend.lastRequest.Model.ModelID != tt.wantModel {
				t.Errorf("model = %q, want %q", backend.lastRequest.Model.ModelID, tt.wantModel)
			}
		})
	}
}

// ─── buildDelegatePrompt Tests ───────────────────────────────────────────────

func TestBuildDelegatePrompt(t *testing.T) {
	tests := []struct {
		name    string
		req     DelegateRequest
		wantHas []string
		wantNot []string
	}{
		{
			name:    "task only",
			req:     DelegateRequest{Task: "fix the bug"},
			wantHas: []string{"Task: fix the bug"},
			wantNot: []string{"project", "Working directory", "context"},
		},
		{
			name: "full request",
			req: DelegateRequest{
				Task:       "fix login",
				Project:    "comparador-seguro",
				WorkingDir: "/home/mx/projects/cs",
				Context:    "The login form throws 500",
			},
			wantHas: []string{
				"comparador-seguro",
				"/home/mx/projects/cs",
				"Task: fix login",
				"The login form throws 500",
			},
		},
		{
			name: "project without workdir",
			req: DelegateRequest{
				Task:    "review code",
				Project: "jarvis",
			},
			wantHas: []string{"project", "jarvis", "Task: review code"},
			wantNot: []string{"Working directory"},
		},
		{
			name: "workdir without project",
			req: DelegateRequest{
				Task:       "run tests",
				WorkingDir: "/tmp/test",
			},
			wantHas: []string{"Working directory: /tmp/test", "Task: run tests"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildDelegatePrompt(tt.req)

			for _, want := range tt.wantHas {
				if !containsSubstring(prompt, want) {
					t.Errorf("prompt should contain %q, got:\n%s", want, prompt)
				}
			}
			for _, notWant := range tt.wantNot {
				if containsSubstring(prompt, notWant) {
					t.Errorf("prompt should NOT contain %q, got:\n%s", notWant, prompt)
				}
			}
		})
	}
}

// ─── resolveModel Tests ─────────────────────────────────────────────────────

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantModel string
	}{
		{"opus", "opus", "claude-opus-4-6"},
		{"sonnet", "sonnet", "claude-sonnet-4-6"},
		{"unknown defaults to sonnet", "gpt-99", "claude-sonnet-4-6"},
		{"empty defaults to sonnet", "", "claude-sonnet-4-6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := resolveModel(tt.input)
			if m.ModelID != tt.wantModel {
				t.Errorf("resolveModel(%q) = %q, want %q", tt.input, m.ModelID, tt.wantModel)
			}
		})
	}
}

// ─── truncateLog Tests ──────────────────────────────────────────────────────

func TestTruncateLog(t *testing.T) {
	if got := truncateLog("short", 100); got != "short" {
		t.Errorf("got %q, want %q", got, "short")
	}
	if got := truncateLog("exactly-ten", 10); got != "exactly-ten" {
		// 11 chars, should truncate
	}
	long := "abcdefghijklmnopqrstuvwxyz"
	got := truncateLog(long, 5)
	if got != "abcde..." {
		t.Errorf("got %q, want %q", got, "abcde...")
	}
}

// ─── NewWorker Tests ─────────────────────────────────────────────────────────

func TestNewWorker_NilLogger(t *testing.T) {
	w := NewWorker(&mockBackend{}, nil)
	if w == nil {
		t.Fatal("expected non-nil worker")
	}
	if w.logger == nil {
		t.Fatal("expected default logger, got nil")
	}
}

func TestNewWorker_WithLogger(t *testing.T) {
	w := NewWorker(&mockBackend{}, nil)
	if w.backend == nil {
		t.Fatal("expected backend to be set")
	}
}

// ─── Timing Test ─────────────────────────────────────────────────────────────

func TestWorkerExecute_DurationTracking(t *testing.T) {
	backend := &mockBackend{
		sessionID: "sess-timing",
		response:  Response{Text: "done"},
	}

	worker := NewWorker(backend, nil)
	before := time.Now()

	result, err := worker.Execute(context.Background(), DelegateRequest{Task: "quick"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := time.Now()
	if result.Duration < 0 || result.Duration > after.Sub(before)+time.Millisecond {
		t.Errorf("duration %v seems wrong (wall time: %v)", result.Duration, after.Sub(before))
	}
}

// containsSubstring is a helper for readability.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
