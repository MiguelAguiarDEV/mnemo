package cloudserver

import (
	"net/http"
	"strconv"
)

func (s *CloudServer) handleGraph(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	project := r.URL.Query().Get("project")
	maxNodes := queryInt(r, "max_nodes", 100)

	graph, err := s.store.BuildGraph(userID, project, maxNodes)
	if err != nil {
		writeStoreError(w, err, "failed to build knowledge graph")
		return
	}

	jsonResponse(w, http.StatusOK, graph)
}

func (s *CloudServer) handleSessionObservations(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	sessionID := r.PathValue("id")

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	obs, err := s.store.SessionObservations(userID, sessionID, limit)
	if err != nil {
		writeStoreError(w, err, "failed to query session observations")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"session_id":   sessionID,
		"observations": obs,
		"total":        len(obs),
	})
}

func (s *CloudServer) handleGetObservation(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	idStr := r.PathValue("id")

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid observation id", http.StatusBadRequest)
		return
	}

	obs, err := s.store.GetObservation(userID, id)
	if err != nil {
		writeStoreError(w, err, "failed to get observation")
		return
	}
	if obs == nil {
		http.Error(w, "observation not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, http.StatusOK, obs)
}
