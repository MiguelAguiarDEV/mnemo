package cloudserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/cloudstore"
)

// CostQuerier defines the store methods used by cost handlers.
// Satisfied by *cloudstore.CloudStore; enables mock-based testing.
type CostQuerier interface {
	CostByModel(userID string, since *time.Time) ([]cloudstore.ModelCost, error)
	CostByDay(userID string, since *time.Time) ([]cloudstore.DayCost, error)
	CostBySessions(userID string, since *time.Time, limit int) ([]cloudstore.SessionCost, error)
	BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*cloudstore.BudgetReport, error)
}

// costStore returns the CostQuerier for cost handlers.
// If a dedicated costQuerier is set (for testing), it takes precedence.
func (s *CloudServer) costStore() CostQuerier {
	if s.costQuerier != nil {
		return s.costQuerier
	}
	return s.store
}

// handleCosts returns aggregated cost data for the authenticated user.
// Query params:
//   - period: "day" | "week" | "month" (default "month")
//   - since: RFC3339 date override (e.g. "2025-01-01T00:00:00Z")
//   - until: RFC3339 date override (e.g. "2025-12-31T23:59:59Z")
//   - claude_budget: monthly Claude budget in USD (default 200)
//   - openai_budget: monthly OpenAI budget in USD (default 200)
func (s *CloudServer) handleCosts(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}

	now := s.now()
	since, until := parseDateRange(r, now, period)

	slog.Info("cost query",
		"handler", "handleCosts",
		"user_id", userID,
		"period", period,
		"since", since,
		"until", until,
	)

	store := s.costStore()

	// Use "since" for the query. If "until" is set, we use "since" only
	// (the store queries filter by >= since; until filtering happens below).
	byModel, err := store.CostByModel(userID, &since)
	if err != nil {
		slog.Error("failed to fetch cost by model", "handler", "handleCosts", "user_id", userID, "error", err)
		writeStoreError(w, err, "failed to fetch cost by model")
		return
	}

	byDay, err := store.CostByDay(userID, &since)
	if err != nil {
		slog.Error("failed to fetch cost by day", "handler", "handleCosts", "user_id", userID, "error", err)
		writeStoreError(w, err, "failed to fetch cost by day")
		return
	}

	// Filter by "until" if provided.
	if !until.IsZero() {
		byDay = filterDaysByUntil(byDay, until)
	}

	var totalCost float64
	for _, mc := range byModel {
		totalCost += mc.CostUSD
	}

	claudeBudget := float64(queryInt(r, "claude_budget", 200))
	openAIBudget := float64(queryInt(r, "openai_budget", 200))
	budget, err := store.BudgetUsage(userID, now, claudeBudget, openAIBudget)
	if err != nil {
		slog.Error("failed to fetch budget", "handler", "handleCosts", "user_id", userID, "error", err)
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

	slog.Info("cost query complete",
		"handler", "handleCosts",
		"user_id", userID,
		"total_cost", totalCost,
		"models", len(byModel),
		"days", len(byDay),
	)

	jsonResponse(w, http.StatusOK, resp)
}

// handleCostSessions returns per-session cost breakdown.
// Query params:
//   - period: "day" | "week" | "month" (default "month")
//   - since: RFC3339 date override
//   - limit: max sessions to return (default 50)
func (s *CloudServer) handleCostSessions(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}

	now := s.now()
	since, _ := parseDateRange(r, now, period)
	limit := queryInt(r, "limit", 50)

	slog.Info("cost sessions query",
		"handler", "handleCostSessions",
		"user_id", userID,
		"period", period,
		"since", since,
		"limit", limit,
	)

	sessions, err := s.costStore().CostBySessions(userID, &since, limit)
	if err != nil {
		slog.Error("failed to fetch session costs", "handler", "handleCostSessions", "user_id", userID, "error", err)
		writeStoreError(w, err, "failed to fetch session costs")
		return
	}

	slog.Info("cost sessions query complete",
		"handler", "handleCostSessions",
		"user_id", userID,
		"sessions", len(sessions),
	)

	jsonResponse(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"period":   period,
	})
}

// BudgetResponse is the JSON response for GET /api/costs/budget.
type BudgetResponse struct {
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd"`
	SpentThisMonth   float64 `json:"spent_this_month"`
	Remaining        float64 `json:"remaining"`
	PercentUsed      float64 `json:"percent_used"`
	ProjectedMonthly float64 `json:"projected_monthly"`
}

// handleCostBudget returns budget status with monthly projection.
// Query params:
//   - claude_budget: monthly Claude budget in USD (default 200)
//   - openai_budget: monthly OpenAI budget in USD (default 200)
func (s *CloudServer) handleCostBudget(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	now := s.now()

	claudeBudget := float64(queryInt(r, "claude_budget", 200))
	openAIBudget := float64(queryInt(r, "openai_budget", 200))
	totalBudget := claudeBudget + openAIBudget

	slog.Info("budget query",
		"handler", "handleCostBudget",
		"user_id", userID,
		"claude_budget", claudeBudget,
		"openai_budget", openAIBudget,
	)

	budget, err := s.costStore().BudgetUsage(userID, now, claudeBudget, openAIBudget)
	if err != nil {
		slog.Error("failed to fetch budget usage", "handler", "handleCostBudget", "user_id", userID, "error", err)
		writeStoreError(w, err, "failed to fetch budget usage")
		return
	}

	spent := budget.ClaudeUsed + budget.OpenAIUsed
	remaining := totalBudget - spent
	if remaining < 0 {
		remaining = 0
	}

	var percentUsed float64
	if totalBudget > 0 {
		percentUsed = (spent / totalBudget) * 100
	}

	projected := projectMonthlySpend(spent, now)
	budget.BudgetProjection = projected

	resp := BudgetResponse{
		MonthlyBudgetUSD: totalBudget,
		SpentThisMonth:   spent,
		Remaining:        remaining,
		PercentUsed:      percentUsed,
		ProjectedMonthly: projected,
	}

	slog.Info("budget query complete",
		"handler", "handleCostBudget",
		"user_id", userID,
		"spent", spent,
		"projected", projected,
		"percent_used", percentUsed,
	)

	jsonResponse(w, http.StatusOK, resp)
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// parseDateRange extracts since/until from query params, falling back to
// period-based calculation.
func parseDateRange(r *http.Request, now time.Time, period string) (since, until time.Time) {
	if s := r.URL.Query().Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			since = t
		}
	}
	if u := r.URL.Query().Get("until"); u != "" {
		if t, err := time.Parse(time.RFC3339, u); err == nil {
			until = t
		}
	}

	// If since was not provided via query param, derive from period.
	if since.IsZero() {
		switch period {
		case "day":
			since = now.Add(-24 * time.Hour)
		case "week":
			since = now.Add(-7 * 24 * time.Hour)
		default:
			since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}

	return since, until
}

// filterDaysByUntil removes DayCost entries whose date is after the until time.
func filterDaysByUntil(days []cloudstore.DayCost, until time.Time) []cloudstore.DayCost {
	cutoff := until.Format("2006-01-02")
	var filtered []cloudstore.DayCost
	for _, d := range days {
		if d.Date <= cutoff {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// projectMonthlySpend extrapolates current spend to the full month.
// projected = (spent / days_elapsed) * days_in_month
func projectMonthlySpend(spent float64, now time.Time) float64 {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	daysInMonth := monthEnd.Sub(monthStart).Hours() / 24

	elapsed := now.Sub(monthStart).Hours() / 24
	if elapsed < 1 {
		elapsed = 1 // avoid division by zero on day 1
	}

	return (spent / elapsed) * daysInMonth
}
