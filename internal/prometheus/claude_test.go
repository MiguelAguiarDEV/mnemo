package prometheus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── Token Reading ───────────────────────────────────────────────────────────

func TestRefreshToken_ValidFile(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")

	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "sk-ant-oat01-test-token",
			"refreshToken": "sk-ant-ort01-test-refresh",
			"expiresAt":    time.Now().Add(1 * time.Hour).UnixMilli(),
		},
	}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeClient(WithCredentialsPath(credPath))
	token, err := c.getToken()
	if err != nil {
		t.Fatalf("getToken() error: %v", err)
	}
	if token != "sk-ant-oat01-test-token" {
		t.Errorf("got token %q, want %q", token, "sk-ant-oat01-test-token")
	}
}

func TestRefreshToken_MissingFile(t *testing.T) {
	c := NewClaudeClient(WithCredentialsPath("/nonexistent/path/.credentials.json"))
	_, err := c.getToken()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRefreshToken_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(credPath, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeClient(WithCredentialsPath(credPath))
	_, err := c.getToken()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRefreshToken_NoAccessToken(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")

	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"refreshToken": "sk-ant-ort01-test-refresh",
			"expiresAt":    time.Now().Add(1 * time.Hour).UnixMilli(),
		},
	}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeClient(WithCredentialsPath(credPath))
	_, err := c.getToken()
	if err == nil {
		t.Fatal("expected error for missing access token")
	}
}

func TestRefreshToken_ExpiredToken_RefreshFromFile(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")

	// Write initial expired token
	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "sk-ant-oat01-expired",
			"refreshToken": "sk-ant-ort01-refresh",
			"expiresAt":    time.Now().Add(-1 * time.Hour).UnixMilli(), // expired
		},
	}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeClient(WithCredentialsPath(credPath))
	// First call caches the expired token
	token1, err := c.getToken()
	if err != nil {
		t.Fatalf("getToken() error: %v", err)
	}

	// Now update file with fresh token (simulates Claude Code refresh)
	creds["claudeAiOauth"] = map[string]any{
		"accessToken":  "sk-ant-oat01-fresh",
		"refreshToken": "sk-ant-ort01-refresh",
		"expiresAt":    time.Now().Add(1 * time.Hour).UnixMilli(),
	}
	data, _ = json.Marshal(creds)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Second call should re-read from file since token is expired
	token2, err := c.getToken()
	if err != nil {
		t.Fatalf("getToken() error: %v", err)
	}

	// The token should have been re-read (first was expired, re-read on that call too)
	_ = token1
	if token2 != "sk-ant-oat01-fresh" {
		t.Errorf("got token %q, want %q", token2, "sk-ant-oat01-fresh")
	}
}

func TestGetToken_CachesValidToken(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")

	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "sk-ant-oat01-cached",
			"refreshToken": "sk-ant-ort01-refresh",
			"expiresAt":    time.Now().Add(1 * time.Hour).UnixMilli(),
		},
	}
	data, _ := json.Marshal(creds)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeClient(WithCredentialsPath(credPath))

	// First call reads from file
	t1, _ := c.getToken()
	// Second call should use cache
	t2, _ := c.getToken()

	if t1 != t2 {
		t.Errorf("tokens differ: %q vs %q", t1, t2)
	}
}

// ─── API Request Format ──────────────────────────────────────────────────────

func TestSend_RequestFormat(t *testing.T) {
	// Mock API server
	var receivedBody []byte
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{
			ID:         "msg_test",
			Type:       "message",
			Role:       "assistant",
			Content:    []ContentBlock{{Type: "text", Text: "Hello"}},
			Model:      "claude-sonnet-4-6",
			StopReason: "end_turn",
			Usage:      ChatUsage{InputTokens: 10, OutputTokens: 5},
		})
	}))
	defer server.Close()

	// Create client with mock credentials and custom HTTP client that redirects to mock
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")
	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": "sk-ant-oat01-test",
			"expiresAt":   time.Now().Add(1 * time.Hour).UnixMilli(),
		},
	}
	data, _ := json.Marshal(creds)
	os.WriteFile(credPath, data, 0600)

	// Override the API URL by using a custom transport
	client := &http.Client{
		Transport: &rewriteTransport{target: server.URL},
		Timeout:   10 * time.Second,
	}

	c := NewClaudeClient(
		WithCredentialsPath(credPath),
		WithHTTPClient(client),
	)

	req := ChatRequest{
		SystemPrompt: "You are JARVIS",
		Messages:     []ChatMessage{NewTextMessage("user", "Hello")},
		Tools: []ChatToolDef{
			{
				Name:        "list_tasks",
				Description: "List tasks",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`),
			},
		},
		Model:     "claude-sonnet-4-6",
		MaxTokens: 4096,
	}

	resp, err := c.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	// Verify response
	if resp.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want %q", resp.StopReason, "end_turn")
	}
	if resp.TextContent() != "Hello" {
		t.Errorf("text = %q, want %q", resp.TextContent(), "Hello")
	}

	// Verify headers
	if auth := receivedHeaders.Get("Authorization"); auth != "Bearer sk-ant-oat01-test" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer sk-ant-oat01-test")
	}
	if v := receivedHeaders.Get("Anthropic-Version"); v != claudeAPIVersion {
		t.Errorf("anthropic-version = %q, want %q", v, claudeAPIVersion)
	}
	if beta := receivedHeaders.Get("Anthropic-Beta"); beta != claudeBetaHeaders {
		t.Errorf("anthropic-beta = %q, want %q", beta, claudeBetaHeaders)
	}

	// Verify request body structure
	var apiReq apiRequest
	if err := json.Unmarshal(receivedBody, &apiReq); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if apiReq.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want %q", apiReq.Model, "claude-sonnet-4-6")
	}
	if apiReq.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want %d", apiReq.MaxTokens, 4096)
	}
	if len(apiReq.System) != 1 || apiReq.System[0].Text != "You are JARVIS" {
		t.Errorf("system = %+v, want [{text: 'You are JARVIS'}]", apiReq.System)
	}
	if len(apiReq.Tools) != 1 || apiReq.Tools[0].Name != "list_tasks" {
		t.Errorf("tools = %+v, want 1 tool named 'list_tasks'", apiReq.Tools)
	}
}

// ─── Response Parsing ────────────────────────────────────────────────────────

func TestSend_ParseTextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{
			Content: []ContentBlock{
				{Type: "text", Text: "Tienes 7 tasks pendientes."},
			},
			StopReason: "end_turn",
			Usage:      ChatUsage{InputTokens: 100, OutputTokens: 20},
			Model:      "claude-sonnet-4-6",
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	resp, err := c.Send(context.Background(), ChatRequest{
		Messages: []ChatMessage{NewTextMessage("user", "cuantas tasks?")},
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if resp.TextContent() != "Tienes 7 tasks pendientes." {
		t.Errorf("text = %q", resp.TextContent())
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 20 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestSend_ParseToolUseResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{
			Content: []ContentBlock{
				{Type: "text", Text: "Let me check your tasks."},
				{
					Type:  "tool_use",
					ID:    "toolu_01abc",
					Name:  "list_tasks",
					Input: json.RawMessage(`{"status":"open"}`),
				},
			},
			StopReason: "tool_use",
			Usage:      ChatUsage{InputTokens: 50, OutputTokens: 30},
			Model:      "claude-sonnet-4-6",
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	resp, err := c.Send(context.Background(), ChatRequest{
		Messages: []ChatMessage{NewTextMessage("user", "cuantas tasks?")},
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want %q", resp.StopReason, "tool_use")
	}

	toolBlocks := resp.ToolUseBlocks()
	if len(toolBlocks) != 1 {
		t.Fatalf("expected 1 tool_use block, got %d", len(toolBlocks))
	}
	if toolBlocks[0].Name != "list_tasks" {
		t.Errorf("tool name = %q, want %q", toolBlocks[0].Name, "list_tasks")
	}
	if toolBlocks[0].ID != "toolu_01abc" {
		t.Errorf("tool id = %q, want %q", toolBlocks[0].ID, "toolu_01abc")
	}

	var args map[string]string
	json.Unmarshal(toolBlocks[0].Input, &args)
	if args["status"] != "open" {
		t.Errorf("tool args = %v", args)
	}
}

func TestSend_MixedContentBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{
			Content: []ContentBlock{
				{Type: "text", Text: "First "},
				{Type: "text", Text: "Second"},
				{Type: "tool_use", ID: "toolu_01", Name: "t1", Input: json.RawMessage(`{}`)},
				{Type: "tool_use", ID: "toolu_02", Name: "t2", Input: json.RawMessage(`{}`)},
			},
			StopReason: "tool_use",
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	resp, err := c.Send(context.Background(), ChatRequest{
		Messages: []ChatMessage{NewTextMessage("user", "test")},
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if resp.TextContent() != "First Second" {
		t.Errorf("text = %q", resp.TextContent())
	}
	if len(resp.ToolUseBlocks()) != 2 {
		t.Errorf("expected 2 tool_use blocks, got %d", len(resp.ToolUseBlocks()))
	}
}

// ─── Error Handling ──────────────────────────────────────────────────────────

func TestSend_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(apiError{
			Type: "error",
			Error: struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}{
				Type:    "invalid_request_error",
				Message: "max_tokens must be positive",
			},
		})
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, err := c.Send(context.Background(), ChatRequest{
		Messages: []ChatMessage{NewTextMessage("user", "test")},
	})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if got := err.Error(); got == "" {
		t.Error("error message should not be empty")
	}
}

func TestSend_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"too many requests"}}`))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)
	_, err := c.Send(context.Background(), ChatRequest{
		Messages: []ChatMessage{NewTextMessage("user", "test")},
	})
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
}

func TestSend_ContextCancellation(t *testing.T) {
	// Cancel context immediately before sending
	c := newTestClient(t, "http://127.0.0.1:1") // won't connect
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.Send(ctx, ChatRequest{
		Messages: []ChatMessage{NewTextMessage("user", "test")},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ─── Tool Call Loop Simulation ───────────────────────────────────────────────

func TestToolCallLoop(t *testing.T) {
	// Simulates: user asks -> Claude returns tool_use -> we send tool_result -> Claude answers
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req apiRequest
		json.Unmarshal(body, &req)

		callCount++
		w.Header().Set("Content-Type", "application/json")

		if callCount == 1 {
			// First call: Claude wants to use a tool
			json.NewEncoder(w).Encode(apiResponse{
				Content: []ContentBlock{
					{Type: "text", Text: "Voy a revisar tus tasks."},
					{Type: "tool_use", ID: "toolu_abc", Name: "list_tasks", Input: json.RawMessage(`{"status":"open"}`)},
				},
				StopReason: "tool_use",
				Usage:      ChatUsage{InputTokens: 50, OutputTokens: 25},
			})
		} else {
			// Second call: with tool_result, Claude gives final answer
			// Verify the tool_result is in the messages
			lastMsg := req.Messages[len(req.Messages)-1]
			var blocks []ContentBlock
			json.Unmarshal(lastMsg.Content, &blocks)

			if len(blocks) == 0 || blocks[0].Type != "tool_result" {
				t.Errorf("expected tool_result in last message, got %+v", blocks)
			}

			json.NewEncoder(w).Encode(apiResponse{
				Content: []ContentBlock{
					{Type: "text", Text: "Tienes 3 tasks pendientes."},
				},
				StopReason: "end_turn",
				Usage:      ChatUsage{InputTokens: 80, OutputTokens: 15},
			})
		}
	}))
	defer server.Close()

	c := newTestClient(t, server.URL)

	// Step 1: Initial request
	messages := []ChatMessage{NewTextMessage("user", "cuantas tasks pendientes?")}
	resp, err := c.Send(context.Background(), ChatRequest{
		Messages: messages,
		Tools: []ChatToolDef{{
			Name:        "list_tasks",
			Description: "List tasks",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("first Send() error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("expected tool_use, got %q", resp.StopReason)
	}

	// Step 2: Build tool result and re-send
	toolBlocks := resp.ToolUseBlocks()
	if len(toolBlocks) != 1 {
		t.Fatalf("expected 1 tool block, got %d", len(toolBlocks))
	}

	// Simulate tool execution result
	toolResult := `[{"id":1,"title":"Task A","status":"open"},{"id":2,"title":"Task B","status":"open"},{"id":3,"title":"Task C","status":"open"}]`

	// Build the conversation with tool result
	// Add assistant's response with tool_use
	assistantBlocks := resp.Content
	messages = append(messages, NewBlocksMessage("assistant", assistantBlocks))
	messages = append(messages, NewToolResultMessage(toolBlocks[0].ID, toolResult, false))

	resp2, err := c.Send(context.Background(), ChatRequest{
		Messages: messages,
		Tools: []ChatToolDef{{
			Name:        "list_tasks",
			Description: "List tasks",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("second Send() error: %v", err)
	}

	if resp2.StopReason != "end_turn" {
		t.Errorf("expected end_turn, got %q", resp2.StopReason)
	}
	if resp2.TextContent() != "Tienes 3 tasks pendientes." {
		t.Errorf("text = %q", resp2.TextContent())
	}

	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}

// ─── ConvertToolDefs ─────────────────────────────────────────────────────────

func TestConvertToolDefs(t *testing.T) {
	defs := []ToolDefinition{
		{
			Name:        "list_tasks",
			Description: "List tasks",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}}}`),
		},
		{
			Name:        "create_task",
			Description: "Create a task",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
		},
	}

	result := ConvertToolDefs(defs)
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}

	if result[0].Name != "list_tasks" {
		t.Errorf("tool 0 name = %q", result[0].Name)
	}
	if result[1].Name != "create_task" {
		t.Errorf("tool 1 name = %q", result[1].Name)
	}

	// Verify input_schema is the same as parameters
	if string(result[0].InputSchema) != string(defs[0].Parameters) {
		t.Errorf("input_schema mismatch")
	}
}

// ─── Message Constructors ────────────────────────────────────────────────────

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage("user", "hello")
	if msg.Role != "user" {
		t.Errorf("role = %q", msg.Role)
	}
	var text string
	json.Unmarshal(msg.Content, &text)
	if text != "hello" {
		t.Errorf("content = %q", text)
	}
}

func TestNewToolResultMessage(t *testing.T) {
	msg := NewToolResultMessage("toolu_01", "result data", false)
	if msg.Role != "user" {
		t.Errorf("role = %q, want %q", msg.Role, "user")
	}

	var blocks []ContentBlock
	json.Unmarshal(msg.Content, &blocks)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != "tool_result" {
		t.Errorf("type = %q", blocks[0].Type)
	}
	if blocks[0].ToolUseID != "toolu_01" {
		t.Errorf("tool_use_id = %q", blocks[0].ToolUseID)
	}
	if blocks[0].Content != "result data" {
		t.Errorf("content = %q", blocks[0].Content)
	}
}

func TestNewBlocksMessage(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "tool_use", ID: "toolu_01", Name: "test", Input: json.RawMessage(`{}`)},
	}
	msg := NewBlocksMessage("assistant", blocks)
	if msg.Role != "assistant" {
		t.Errorf("role = %q", msg.Role)
	}

	var parsed []ContentBlock
	json.Unmarshal(msg.Content, &parsed)
	if len(parsed) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(parsed))
	}
}

// ─── Default Credentials Path ────────────────────────────────────────────────

func TestDefaultCredentialsPath_EnvVar(t *testing.T) {
	t.Setenv("CLAUDE_CREDENTIALS_PATH", "/custom/path/.credentials.json")
	path := defaultCredentialsPath()
	if path != "/custom/path/.credentials.json" {
		t.Errorf("path = %q", path)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// rewriteTransport redirects all requests to a test server.
type rewriteTransport struct {
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.target[7:] // strip "http://"
	req.URL.Path = "/v1/messages"
	req.URL.RawQuery = ""
	return http.DefaultTransport.RoundTrip(req)
}

// newTestClient creates a ClaudeClient pointing to a test server.
func newTestClient(t *testing.T, serverURL string) *ClaudeClient {
	t.Helper()
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".credentials.json")
	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": "sk-ant-oat01-test",
			"expiresAt":   time.Now().Add(1 * time.Hour).UnixMilli(),
		},
	}
	data, _ := json.Marshal(creds)
	os.WriteFile(credPath, data, 0600)

	return NewClaudeClient(
		WithCredentialsPath(credPath),
		WithHTTPClient(&http.Client{
			Transport: &rewriteTransport{target: serverURL},
			Timeout:   10 * time.Second,
		}),
	)
}
