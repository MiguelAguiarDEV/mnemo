package cloudserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// ─── Chat Handlers ─────────────────────────────────────────────────────────

// handleChatSSE handles POST /api/chat — accepts a message and streams
// the response via Server-Sent Events. This avoids WebSocket complexity
// through Cloudflare Tunnel.
//
// Request body: {"conversation_id": 123, "message": "..."}
// Response: text/event-stream with events:
//   data: {"token":"..."}\n\n      — streamed token
//   data: {"done":true,"message_id":456}\n\n — final event
func (s *CloudServer) handleChatSSE(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

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

	// Verify conversation belongs to this user.
	if _, err := s.store.GetConversation(userID, body.ConversationID); err != nil {
		jsonError(w, http.StatusNotFound, "conversation not found")
		return
	}

	// Check that orchestrator is configured.
	if s.jarvis == nil {
		jsonError(w, http.StatusServiceUnavailable, "chat not configured")
		return
	}

	// Route through Gateway when WebChannel is configured (Phase 2).
	// Gateway path collects the full response (no streaming) and returns it as SSE.
	// This proves the Gateway → WebChannel → Orchestrator flow works.
	// SSE streaming is preserved: the full response is sent as a single token event.
	if s.webChannel != nil {
		slog.Info("message routed through gateway", "channel", "web", "user_id", userID, "conversation_id", body.ConversationID)
		slog.Debug("gateway routing details", "text_len", len(body.Message), "user_id", userID)

		resp, err := s.webChannel.ProcessHTTPMessage(r.Context(), userID, body.ConversationID, body.Message)
		if err != nil {
			slog.Error("gateway routing failed", "channel", "web", "user_id", userID, "error", err)
			jsonError(w, http.StatusInternalServerError, "chat processing failed")
			return
		}

		// Return response as SSE stream (single token + done) to keep client compatibility.
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

	// Fallback: direct orchestrator call with SSE streaming (pre-Gateway path).
	slog.Warn("gateway not configured, falling back to direct orchestrator call", "user_id", userID)

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Stream tokens via SSE. Status events use __STATUS__ prefix.
	onToken := func(token string) {
		if strings.HasPrefix(token, "__STATUS__") {
			// Forward status event as-is (already JSON)
			fmt.Fprintf(w, "data: %s\n\n", token[10:])
			flusher.Flush()
			return
		}
		if token == "\n" {
			return // skip empty signals
		}
		data, _ := json.Marshal(map[string]string{"token": token})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	_, err := s.jarvis.Chat(userID, body.ConversationID, body.Message, onToken)
	if err != nil {
		// Send error as SSE event (client is already in streaming mode).
		errData, _ := json.Marshal(map[string]any{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
		return
	}

	// Send done event.
	doneData, _ := json.Marshal(map[string]any{"done": true})
	fmt.Fprintf(w, "data: %s\n\n", doneData)
	flusher.Flush()
}

// ─── Conversation Handlers ─────────────────────────────────────────────────

// handleCreateConversation handles POST /api/conversations.
// Request body: {"title": "optional title"}
func (s *CloudServer) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	id, err := s.store.CreateConversation(userID, body.Title)
	if err != nil {
		writeStoreError(w, err, "failed to create conversation")
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]any{
		"id": id,
	})
}

// handleListConversations handles GET /api/conversations.
func (s *CloudServer) handleListConversations(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	limit := queryInt(r, "limit", 20)

	convos, err := s.store.ListConversations(userID, limit)
	if err != nil {
		writeStoreError(w, err, "failed to list conversations")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"conversations": convos,
	})
}

// handleDeleteConversation handles DELETE /api/conversations/{id}.
func (s *CloudServer) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}

	if err := s.store.DeleteConversation(userID, id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			jsonError(w, http.StatusNotFound, "conversation not found")
			return
		}
		writeStoreError(w, err, "failed to delete conversation")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleRenameConversation handles PATCH /api/conversations/{id}.
func (s *CloudServer) handleRenameConversation(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}

	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	if err := s.store.RenameConversation(userID, id, body.Title); err != nil {
		if strings.Contains(err.Error(), "not found") {
			jsonError(w, http.StatusNotFound, "conversation not found")
			return
		}
		writeStoreError(w, err, "failed to rename conversation")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "renamed"})
}

// handleGetMessages handles GET /api/conversations/{id}/messages.
func (s *CloudServer) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid conversation id")
		return
	}

	// Verify conversation belongs to this user.
	if _, err := s.store.GetConversation(userID, id); err != nil {
		jsonError(w, http.StatusNotFound, "conversation not found")
		return
	}

	limit := queryInt(r, "limit", 100)
	msgs, err := s.store.GetMessages(id, limit)
	if err != nil {
		writeStoreError(w, err, "failed to get messages")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"messages": msgs,
	})
}
