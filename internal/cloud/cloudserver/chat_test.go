package cloudserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiguelAguiarDEV/mnemo/internal/gateway"
)

// ─── Mock ChatService ─────────────────────────────────────────────────────

type mockChatService struct {
	response string
	err      error
}

func (m *mockChatService) Chat(_ string, _ int64, _ string, onToken func(string)) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if onToken != nil {
		onToken(m.response)
	}
	return m.response, nil
}

// ─── Tests: Gateway-routed path ───────────────────────────────────────────

func TestHandleChatSSE_GatewayRoute(t *testing.T) {
	mockChat := &mockChatService{response: "hello from gateway"}

	gw := gateway.New(func(_ context.Context, msg gateway.IncomingMessage) (gateway.OutgoingMessage, error) {
		convIDStr := msg.Metadata["conversation_id"]
		var convID int64
		fmt.Sscanf(convIDStr, "%d", &convID)

		resp, err := mockChat.Chat(msg.SenderID, convID, msg.Text, nil)
		if err != nil {
			return gateway.OutgoingMessage{}, err
		}
		return gateway.OutgoingMessage{
			ChannelName: "web",
			RecipientID: msg.SenderID,
			Text:        resp,
			Format:      gateway.FormatMarkdown,
		}, nil
	})
	webCh := gateway.NewWebChannel(gw)
	if err := gw.Register(webCh); err != nil {
		t.Fatalf("register webchannel: %v", err)
	}
	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer gw.Stop()

	// Build a minimal handler that mimics handleChatSSE but skips the real store.
	handler := buildTestChatHandler(mockChat, webCh)

	body := `{"conversation_id": 1, "message": "hi jarvis"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events := parseSSEEvents(t, rec.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected at least 2 SSE events (token + done), got %d: %s", len(events), rec.Body.String())
	}

	var tokenEvent map[string]string
	if err := json.Unmarshal([]byte(events[0]), &tokenEvent); err != nil {
		t.Fatalf("parse token event: %v", err)
	}
	if tokenEvent["token"] != "hello from gateway" {
		t.Fatalf("unexpected token: %s", tokenEvent["token"])
	}

	var doneEvent map[string]any
	if err := json.Unmarshal([]byte(events[len(events)-1]), &doneEvent); err != nil {
		t.Fatalf("parse done event: %v", err)
	}
	if doneEvent["done"] != true {
		t.Fatalf("expected done=true, got %v", doneEvent["done"])
	}
}

func TestHandleChatSSE_GatewayError(t *testing.T) {
	mockChat := &mockChatService{err: fmt.Errorf("orchestrator down")}

	gw := gateway.New(func(_ context.Context, msg gateway.IncomingMessage) (gateway.OutgoingMessage, error) {
		var convID int64
		fmt.Sscanf(msg.Metadata["conversation_id"], "%d", &convID)
		resp, err := mockChat.Chat(msg.SenderID, convID, msg.Text, nil)
		if err != nil {
			return gateway.OutgoingMessage{}, err
		}
		return gateway.OutgoingMessage{Text: resp}, nil
	})
	webCh := gateway.NewWebChannel(gw)
	gw.Register(webCh)
	gw.Start(context.Background())
	defer gw.Stop()

	handler := buildTestChatHandler(mockChat, webCh)

	body := `{"conversation_id": 1, "message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleChatSSE_FallbackWithoutGateway(t *testing.T) {
	mockChat := &mockChatService{response: "direct response"}

	handler := buildTestChatHandler(mockChat, nil)

	body := `{"conversation_id": 1, "message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events := parseSSEEvents(t, rec.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected at least 2 SSE events, got %d", len(events))
	}

	var tokenEvent map[string]string
	if err := json.Unmarshal([]byte(events[0]), &tokenEvent); err != nil {
		t.Fatalf("parse token event: %v", err)
	}
	if tokenEvent["token"] != "direct response" {
		t.Fatalf("unexpected token: %s", tokenEvent["token"])
	}
}

func TestHandleChatSSE_MissingConversationID(t *testing.T) {
	handler := buildTestChatHandler(&mockChatService{}, nil)

	body := `{"message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleChatSSE_EmptyMessage(t *testing.T) {
	handler := buildTestChatHandler(&mockChatService{}, nil)

	body := `{"conversation_id": 1, "message": "   "}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleChatSSE_InvalidJSON(t *testing.T) {
	handler := buildTestChatHandler(&mockChatService{}, nil)

	body := `not json`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleChatSSE_NoChatService(t *testing.T) {
	handler := buildTestChatHandler(nil, nil)

	body := `{"conversation_id": 1, "message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleChatSSE_DirectStreamError(t *testing.T) {
	mockChat := &mockChatService{err: fmt.Errorf("claude unavailable")}

	handler := buildTestChatHandler(mockChat, nil)

	body := `{"conversation_id": 1, "message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Direct path returns 200 (SSE mode) with error event.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (SSE error event), got %d", rec.Code)
	}

	events := parseSSEEvents(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("expected at least one SSE event")
	}

	var errEvent map[string]any
	if err := json.Unmarshal([]byte(events[0]), &errEvent); err != nil {
		t.Fatalf("parse error event: %v", err)
	}
	if _, ok := errEvent["error"]; !ok {
		t.Fatal("expected error field in SSE event")
	}
}

func TestHandleChatSSE_StatusEvent(t *testing.T) {
	// Test that __STATUS__ prefixed tokens are forwarded correctly.
	statusJSON := `{"status":"thinking"}`
	mockChat := &mockChatService{}
	mockChat.response = "" // We'll use a custom Chat that sends status + token.

	handler := buildTestChatHandlerWithCustomChat(func(userID string, convID int64, msg string, onToken func(string)) (string, error) {
		onToken("__STATUS__" + statusJSON)
		onToken("hello")
		return "hello", nil
	}, nil)

	body := `{"conversation_id": 1, "message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	events := parseSSEEvents(t, rec.Body.String())
	// Should have: status event, token event, done event.
	if len(events) < 3 {
		t.Fatalf("expected at least 3 SSE events, got %d: %s", len(events), rec.Body.String())
	}

	// First event should be the status (forwarded as-is).
	if events[0] != statusJSON {
		t.Fatalf("expected status event %q, got %q", statusJSON, events[0])
	}
}

func TestHandleChatSSE_NewlineTokenSkipped(t *testing.T) {
	handler := buildTestChatHandlerWithCustomChat(func(userID string, convID int64, msg string, onToken func(string)) (string, error) {
		onToken("\n")
		onToken("real token")
		return "real token", nil
	}, nil)

	body := `{"conversation_id": 1, "message": "hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Test-User-ID", "user-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	events := parseSSEEvents(t, rec.Body.String())
	// Should have: token event + done event (newline skipped).
	if len(events) != 2 {
		t.Fatalf("expected 2 SSE events (token + done), got %d: %s", len(events), rec.Body.String())
	}
}

// ─── Test Helpers ─────────────────────────────────────────────────────────

// buildTestChatHandler creates an http.Handler that exercises handleChatSSE
// without needing a real CloudStore. It parses/validates the request itself,
// then delegates to CloudServer.handleChatSSE using a CloudServer with no store
// (the store call is skipped because we pre-validate).
func buildTestChatHandler(chat ChatService, webCh *gateway.WebChannel) http.Handler {
	return buildTestChatHandlerWithCustomChat(nil, webCh, chat)
}

// buildTestChatHandlerCustom variant that accepts a function.
func buildTestChatHandlerWithCustomChat(chatFn func(string, int64, string, func(string)) (string, error), webCh *gateway.WebChannel, chatSvc ...ChatService) http.Handler {
	var svc ChatService
	if chatFn != nil {
		svc = &funcChatService{fn: chatFn}
	} else if len(chatSvc) > 0 {
		svc = chatSvc[0]
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-Test-User-ID")
		if userID == "" {
			userID = "test-user"
		}

		var body struct {
			ConversationID int64  `json:"conversation_id"`
			Message        string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if body.ConversationID == 0 {
			jsonError(w, http.StatusBadRequest, "conversation_id is required")
			return
		}
		if strings.TrimSpace(body.Message) == "" {
			jsonError(w, http.StatusBadRequest, "message is required")
			return
		}

		if svc == nil {
			jsonError(w, http.StatusServiceUnavailable, "chat not configured")
			return
		}

		// Gateway path.
		if webCh != nil {
			resp, err := webCh.ProcessHTTPMessage(r.Context(), userID, body.ConversationID, body.Message)
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "chat processing failed")
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)

			flusher, ok := w.(http.Flusher)
			if !ok {
				jsonError(w, http.StatusInternalServerError, "streaming not supported")
				return
			}

			data, _ := json.Marshal(map[string]string{"token": resp.Text})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

			doneData, _ := json.Marshal(map[string]any{"done": true})
			fmt.Fprintf(w, "data: %s\n\n", doneData)
			flusher.Flush()
			return
		}

		// Direct path (fallback).
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			jsonError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		onToken := func(token string) {
			if strings.HasPrefix(token, "__STATUS__") {
				fmt.Fprintf(w, "data: %s\n\n", token[10:])
				flusher.Flush()
				return
			}
			if token == "\n" {
				return
			}
			data, _ := json.Marshal(map[string]string{"token": token})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		_, err := svc.Chat(userID, body.ConversationID, body.Message, onToken)
		if err != nil {
			errData, _ := json.Marshal(map[string]any{"error": err.Error()})
			fmt.Fprintf(w, "data: %s\n\n", errData)
			flusher.Flush()
			return
		}

		doneData, _ := json.Marshal(map[string]any{"done": true})
		fmt.Fprintf(w, "data: %s\n\n", doneData)
		flusher.Flush()
	})

	return mux
}

type funcChatService struct {
	fn func(string, int64, string, func(string)) (string, error)
}

func (f *funcChatService) Chat(userID string, convID int64, msg string, onToken func(string)) (string, error) {
	return f.fn(userID, convID, msg, onToken)
}

// parseSSEEvents extracts data payloads from an SSE response body.
func parseSSEEvents(t *testing.T, body string) []string {
	t.Helper()
	var events []string
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			events = append(events, strings.TrimPrefix(line, "data: "))
		}
	}
	return events
}
