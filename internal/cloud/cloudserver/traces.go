package cloudserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/cloudstore"
)

func (s *CloudServer) handleAddToolCall(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	var req cloudstore.AddToolCallParams
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	if req.SessionID == "" || req.Agent == "" || req.ToolName == "" {
		jsonError(w, http.StatusBadRequest, "session_id, agent, and tool_name are required")
		return
	}

	// Truncate output if >50KB
	if len(req.OutputText) > 50000 {
		req.OutputText = req.OutputText[:50000] + "\n[truncated]"
	}

	resp, err := s.store.AddToolCall(userID, req)
	if err != nil {
		writeStoreError(w, err, "failed to insert tool call")
		return
	}

	jsonResponse(w, http.StatusCreated, resp)
}

func (s *CloudServer) handleSessionTraces(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	sessionID := r.PathValue("id")

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	resp, err := s.store.SessionToolCalls(userID, sessionID, limit, offset)
	if err != nil {
		writeStoreError(w, err, "failed to query session traces")
		return
	}

	jsonResponse(w, http.StatusOK, resp)
}

func (s *CloudServer) handleTraceStats(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	project := r.URL.Query().Get("project")
	sinceStr := r.URL.Query().Get("since")

	var since *time.Time
	if sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err == nil {
			since = &t
		}
	}

	resp, err := s.store.ToolCallStats(userID, project, since)
	if err != nil {
		writeStoreError(w, err, "failed to query trace stats")
		return
	}

	jsonResponse(w, http.StatusOK, resp)
}
