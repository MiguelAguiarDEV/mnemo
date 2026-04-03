package cloudserver

import (
	"encoding/json"
	"net/http"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/notifications"
)

// handleSendNotification processes POST /api/notifications.
// It decodes a Notification payload and sends it via the configured notifier.
func (s *CloudServer) handleSendNotification(w http.ResponseWriter, r *http.Request) {
	if s.notifier == nil {
		jsonError(w, http.StatusServiceUnavailable, "notifications not configured")
		return
	}

	var body notifications.Notification
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Title == "" || body.Message == "" {
		jsonError(w, http.StatusBadRequest, "title and message are required")
		return
	}
	// Default type to info if not provided.
	if body.Type == "" {
		body.Type = notifications.Info
	}

	if err := s.notifier.Send(body); err != nil {
		jsonError(w, http.StatusBadGateway, "failed to send notification: "+err.Error())
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "sent"})
}
