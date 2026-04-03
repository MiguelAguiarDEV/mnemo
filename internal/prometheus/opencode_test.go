package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeBackend_CreateSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "test-session-123"})
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "user", "pass", 5*time.Second)
	id, err := backend.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if id != "test-session-123" {
		t.Errorf("session ID = %q, want %q", id, "test-session-123")
	}
}

func TestOpenCodeBackend_Send_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/session/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := openCodeResponse{}
		resp.Info.Tokens.Input = 100
		resp.Info.Tokens.Output = 50
		resp.Info.ModelID = "claude-sonnet-4-6"
		resp.Parts = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{Type: "text", Text: "Hello, world!"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	resp, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages:  []Message{{Role: "user", Content: "Hi"}},
		Model:     ModelConfig{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6"},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if resp.Text != "Hello, world!" {
		t.Errorf("text = %q, want %q", resp.Text, "Hello, world!")
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("input tokens = %d, want 100", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("output tokens = %d, want 50", resp.Usage.OutputTokens)
	}
}

func TestOpenCodeBackend_Send_WithToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openCodeResponse{}
		resp.Info.Tokens.Input = 200
		resp.Info.Tokens.Output = 100
		toolCallJSON := `I'll load that skill for you. {"tool_call": {"name": "load_skill", "arguments": {"name": "go-testing"}}}`
		resp.Parts = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{Type: "text", Text: toolCallJSON},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	resp, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages:  []Message{{Role: "user", Content: "load go testing"}},
		Model:     ModelConfig{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6"},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "load_skill" {
		t.Errorf("tool call name = %q, want %q", resp.ToolCalls[0].Name, "load_skill")
	}

	var args map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("failed to parse tool call arguments: %v", err)
	}
	if args["name"] != "go-testing" {
		t.Errorf("tool call arg name = %q, want %q", args["name"], "go-testing")
	}
}

func TestOpenCodeBackend_Send_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	_, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Model:     ModelConfig{ProviderID: "anthropic", ModelID: "test"},
	})
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "status 500")
	}
}

func TestOpenCodeBackend_Send_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Very short timeout
	backend := NewOpenCodeBackend(server.URL, "", "", 50*time.Millisecond)
	_, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Model:     ModelConfig{ProviderID: "anthropic", ModelID: "test"},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestOpenCodeBackend_CreateSession_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	_, err := backend.CreateSession(context.Background())
	if err == nil {
		t.Fatal("expected error for 403 status")
	}
}

func TestOpenCodeBackend_Send_WithSystemPrompt(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload openCodePayload
		json.NewDecoder(r.Body).Decode(&payload)
		if len(payload.Parts) > 0 {
			receivedBody = payload.Parts[0].Text
		}
		resp := openCodeResponse{}
		resp.Parts = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: "ok"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	_, err := backend.Send(context.Background(), Request{
		SessionID:    "s1",
		SystemPrompt: "You are a helper.",
		Messages:     []Message{{Role: "user", Content: "hello"}},
		Model:        ModelConfig{ProviderID: "test", ModelID: "test"},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if !strings.Contains(receivedBody, "You are a helper.") {
		t.Error("system prompt not included in request")
	}
	if !strings.Contains(receivedBody, "hello") {
		t.Error("user message not included in request")
	}
}

func TestOpenCodeBackend_Send_BasicAuth(t *testing.T) {
	var gotUser, gotPass string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		resp := openCodeResponse{}
		resp.Parts = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: "ok"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "myuser", "mypass", 5*time.Second)
	_, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Model:     ModelConfig{ProviderID: "test", ModelID: "test"},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotUser != "myuser" || gotPass != "mypass" {
		t.Errorf("auth = %s:%s, want myuser:mypass", gotUser, gotPass)
	}
}

func TestParseToolCalls(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "no tool calls",
			input: "Just plain text response.",
			want:  0,
		},
		{
			name:  "single tool call",
			input: `Here's my response. {"tool_call": {"name": "load_skill", "arguments": {"name": "go-testing"}}}`,
			want:  1,
		},
		{
			name:  "multiple tool calls",
			input: `{"tool_call": {"name": "load_skill", "arguments": {"name": "a"}}} text {"tool_call": {"name": "notify", "arguments": {"msg": "done"}}}`,
			want:  2,
		},
		{
			name:  "invalid json ignored",
			input: `{not valid json} and {"tool_call": {"name": "test", "arguments": {}}}`,
			want:  1,
		},
		{
			name:  "json without tool_call ignored",
			input: `{"key": "value"} some text`,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := ParseToolCalls(tt.input)
			if len(calls) != tt.want {
				t.Errorf("got %d tool calls, want %d", len(calls), tt.want)
			}
		})
	}
}

func TestNewOpenCodeBackend_DefaultTimeout(t *testing.T) {
	backend := NewOpenCodeBackend("http://localhost:4096", "", "", 0)
	if backend.httpClient.Timeout != 5*time.Minute {
		t.Errorf("timeout = %v, want 5m", backend.httpClient.Timeout)
	}
}

func TestOpenCodeBackend_CreateSession_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	_, err := backend.CreateSession(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestOpenCodeBackend_Send_InvalidResponseJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	_, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Model:     ModelConfig{ProviderID: "test", ModelID: "test"},
	})
	if err == nil {
		t.Fatal("expected error for invalid response JSON")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error = %q, want to contain 'decode response'", err.Error())
	}
}

func TestOpenCodeBackend_Send_MultipleMessages(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload openCodePayload
		json.NewDecoder(r.Body).Decode(&payload)
		if len(payload.Parts) > 0 {
			receivedBody = payload.Parts[0].Text
		}
		resp := openCodeResponse{}
		resp.Parts = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: "response"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	backend := NewOpenCodeBackend(server.URL, "", "", 5*time.Second)
	_, err := backend.Send(context.Background(), Request{
		SessionID: "s1",
		Messages: []Message{
			{Role: "user", Content: "first message"},
			{Role: "assistant", Content: "first reply"},
			{Role: "user", Content: "second message"},
		},
		Model: ModelConfig{ProviderID: "test", ModelID: "test"},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if !strings.Contains(receivedBody, "[User]") {
		t.Error("expected [User] role prefix")
	}
	if !strings.Contains(receivedBody, "[Assistant]") {
		t.Error("expected [Assistant] role prefix")
	}
}

func TestNewOpenCodeBackend_TrailingSlash(t *testing.T) {
	backend := NewOpenCodeBackend("http://localhost:4096/", "", "", time.Second)
	if strings.HasSuffix(backend.baseURL, "/") {
		t.Errorf("baseURL should not end with /: %q", backend.baseURL)
	}
}
