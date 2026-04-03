package cloudserver

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
)

// ─── SSE Events Handler ────────────────────────────────────────────────────

func (s *CloudServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.dsn == "" {
		jsonError(w, http.StatusServiceUnavailable, "SSE not configured: missing database DSN")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	projectFilter := r.URL.Query().Get("project")

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Create a pq listener for NOTIFY channels.
	reportProblem := func(ev pq.ListenerEventType, err error) {
		if err != nil {
			log.Printf("[engram-cloud] SSE listener error: %v", err)
		}
	}

	listener := pq.NewListener(s.dsn, 10*time.Second, time.Minute, reportProblem)
	defer listener.Close()

	if err := listener.Listen("jarvis_tasks"); err != nil {
		log.Printf("[engram-cloud] SSE: failed to LISTEN jarvis_tasks: %v", err)
		return
	}
	if err := listener.Listen("jarvis_activity"); err != nil {
		log.Printf("[engram-cloud] SSE: failed to LISTEN jarvis_activity: %v", err)
		return
	}

	ctx := r.Context()
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case n := <-listener.Notify:
			if n == nil {
				// Reconnect notification — no data.
				continue
			}
			if !s.matchesProjectFilter(n.Extra, projectFilter) {
				continue
			}

			eventType := channelToEventType(n.Channel)
			evt := map[string]any{
				"type": eventType,
				"data": json.RawMessage(n.Extra),
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// channelToEventType maps PG channel names to SSE event types.
func channelToEventType(channel string) string {
	switch channel {
	case "jarvis_tasks":
		return "task_update"
	case "jarvis_activity":
		return "activity"
	default:
		return channel
	}
}

// matchesProjectFilter checks if a NOTIFY payload matches the requested project filter.
// Returns true if no filter is set or if the payload contains the project name.
func (s *CloudServer) matchesProjectFilter(payload, projectFilter string) bool {
	if projectFilter == "" {
		return true
	}
	// NOTIFY payloads are JSON — check if project field matches.
	// This is a best-effort filter; payloads may not always have a project field.
	return strings.Contains(payload, projectFilter)
}
