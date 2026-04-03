package cloudserver

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"
)

// handleRecordMetric accepts POST /api/metrics with system stats and persists them.
func (s *CloudServer) handleRecordMetric(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CpuPct     float32 `json:"cpu_pct"`
		MemPct     float32 `json:"mem_pct"`
		DiskPct    float32 `json:"disk_pct"`
		Load1m     float32 `json:"load_1m"`
		MemUsedMB  int     `json:"mem_used_mb"`
		MemTotalMB int     `json:"mem_total_mb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := s.store.RecordMetric(body.CpuPct, body.MemPct, body.DiskPct, body.Load1m, body.MemUsedMB, body.MemTotalMB); err != nil {
		writeStoreError(w, err, "failed to record metric")
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// handleGetMetrics returns GET /api/metrics?limit=N recent metrics snapshots.
func (s *CloudServer) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 60
	}

	metrics, err := s.store.RecentMetrics(limit)
	if err != nil {
		writeStoreError(w, err, "failed to get metrics")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{"metrics": metrics})
}

// handleSystemInfo returns GET /api/system with live host metrics.
// This runs inside the engram container but reads /proc for host-level data.
func (s *CloudServer) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	info := map[string]any{
		"go_routines": runtime.NumGoroutine(),
		"go_mem_mb":   m.Alloc / 1024 / 1024,
		"uptime":      time.Since(s.startTime).Seconds(),
		"hostname":    getHostname(),
		"cores":       runtime.NumCPU(),
	}

	jsonResponse(w, http.StatusOK, info)
}

func getHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
