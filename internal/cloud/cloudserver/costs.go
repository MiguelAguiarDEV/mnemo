package cloudserver

import (
	"net/http"
	"time"

	"github.com/Gentleman-Programming/engram/internal/cloud/cloudstore"
)

// handleCosts returns aggregated cost data for the authenticated user.
// Query params:
//   - period: "day" | "week" | "month" (default "month")
//   - claude_budget: monthly Claude budget in USD (default 200)
//   - openai_budget: monthly OpenAI budget in USD (default 200)
func (s *CloudServer) handleCosts(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}

	// Calculate the "since" time based on period.
	now := s.now()
	var since time.Time
	switch period {
	case "day":
		since = now.Add(-24 * time.Hour)
	case "week":
		since = now.Add(-7 * 24 * time.Hour)
	default:
		period = "month"
		since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	// Fetch cost by model.
	byModel, err := s.store.CostByModel(userID, &since)
	if err != nil {
		writeStoreError(w, err, "failed to fetch cost by model")
		return
	}

	// Fetch cost by day.
	byDay, err := s.store.CostByDay(userID, &since)
	if err != nil {
		writeStoreError(w, err, "failed to fetch cost by day")
		return
	}

	// Calculate total cost.
	var totalCost float64
	for _, mc := range byModel {
		totalCost += mc.CostUSD
	}

	// Fetch budget.
	claudeBudget := float64(queryInt(r, "claude_budget", 200))
	openAIBudget := float64(queryInt(r, "openai_budget", 200))
	budget, err := s.store.BudgetUsage(userID, now, claudeBudget, openAIBudget)
	if err != nil {
		writeStoreError(w, err, "failed to fetch budget")
		return
	}

	resp := cloudstore.CostSummary{
		TotalCost: totalCost,
		Period:    period,
		ByModel:   byModel,
		ByDay:     byDay,
		Budget:    budget,
	}

	jsonResponse(w, http.StatusOK, resp)
}

// handleCostSessions returns per-session cost breakdown.
// Query params:
//   - period: "day" | "week" | "month" (default "month")
//   - limit: max sessions to return (default 50)
func (s *CloudServer) handleCostSessions(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}

	now := s.now()
	var since time.Time
	switch period {
	case "day":
		since = now.Add(-24 * time.Hour)
	case "week":
		since = now.Add(-7 * 24 * time.Hour)
	default:
		since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	limit := queryInt(r, "limit", 50)

	sessions, err := s.store.CostBySessions(userID, &since, limit)
	if err != nil {
		writeStoreError(w, err, "failed to fetch session costs")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"period":   period,
	})
}
