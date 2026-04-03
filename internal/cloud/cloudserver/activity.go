package cloudserver

import (
	"net/http"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/cloudstore"
)

// ─── Activity Feed Handler ─────────────────────────────────────────────────

func (s *CloudServer) handleActivity(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	project := r.URL.Query().Get("project")
	limit := queryInt(r, "limit", 50)

	var since *time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		t, err := time.Parse(time.RFC3339, sinceStr)
		if err == nil {
			since = &t
		}
	}

	entries, err := s.store.ActivityFeed(userID, project, since, limit)
	if err != nil {
		writeStoreError(w, err, "failed to query activity feed")
		return
	}

	if entries == nil {
		entries = []cloudstore.ActivityEntry{}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"entries": entries,
	})
}
