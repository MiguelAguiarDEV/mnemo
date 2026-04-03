package cloudserver

import (
	"encoding/json"
	"net/http"
)

// ─── Jobs API Handlers ──────────────────────────────────────────────────────

// handleListJobs returns all delegation jobs.
// GET /api/jobs
func (s *CloudServer) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobSvc == nil {
		jsonError(w, http.StatusServiceUnavailable, "jobs service not configured")
		return
	}

	jobs := s.jobSvc.ListJobs()
	jsonResponse(w, http.StatusOK, map[string]any{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// handleGetJob returns a single job by ID.
// GET /api/jobs/{id}
func (s *CloudServer) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if s.jobSvc == nil {
		jsonError(w, http.StatusServiceUnavailable, "jobs service not configured")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "job id is required")
		return
	}

	job, ok := s.jobSvc.GetJob(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"job": job,
	})
}

// handleCreateJob creates a new async delegation job.
// POST /api/jobs
func (s *CloudServer) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if s.jobSvc == nil {
		jsonError(w, http.StatusServiceUnavailable, "jobs service not configured")
		return
	}

	var body struct {
		Task       string `json:"task"`
		Project    string `json:"project"`
		WorkingDir string `json:"working_dir"`
		Context    string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Task == "" {
		jsonError(w, http.StatusBadRequest, "task is required")
		return
	}

	jobID := s.jobSvc.CreateJob(body.Task, body.Project, body.WorkingDir, body.Context)

	jsonResponse(w, http.StatusCreated, map[string]any{
		"job_id":  jobID,
		"status":  "pending",
		"message": "Job created. It will run in the background.",
	})
}
