package cloudstore

import (
	"fmt"
	"time"
)

// ─── Types ──────────────────────────────────────────────────────────────────

// ProjectCost aggregates token usage and cost for a single project.
type ProjectCost struct {
	Project   string  `json:"project"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
	Calls     int     `json:"calls"`
}

// DayCost aggregates cost for a single day.
type DayCost struct {
	Date    string  `json:"date"`
	CostUSD float64 `json:"cost_usd"`
	Calls   int     `json:"calls"`
}

// BudgetReport shows spending vs budget for each provider this month.
type BudgetReport struct {
	ClaudeUsed   float64 `json:"claude_used"`
	ClaudeBudget float64 `json:"claude_budget"`
	OpenAIUsed   float64 `json:"openai_used"`
	OpenAIBudget float64 `json:"openai_budget"`
	ClaudePct    float64 `json:"claude_pct"`
	OpenAIPct    float64 `json:"openai_pct"`
}

// ─── Queries ────────────────────────────────────────────────────────────────

// CostByProject returns token usage and cost aggregated by project for a user.
// If since is nil, all data is included. Results combine agent_tool_calls and
// messages tables to capture both agent activity and chat costs.
func (cs *CloudStore) CostByProject(userID string, since *time.Time) ([]ProjectCost, error) {
	query := `
		SELECT
			COALESCE(project, 'unknown') AS project,
			COALESCE(SUM(tokens_in), 0)  AS tokens_in,
			COALESCE(SUM(tokens_out), 0) AS tokens_out,
			COALESCE(SUM(cost_usd), 0)   AS cost_usd,
			COUNT(*)                     AS calls
		FROM agent_tool_calls
		WHERE user_id = $1
	`
	args := []interface{}{userID}
	if since != nil {
		query += " AND occurred_at >= $2"
		args = append(args, *since)
	}
	query += " GROUP BY COALESCE(project, 'unknown') ORDER BY cost_usd DESC"

	rows, err := cs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: cost by project: %w", err)
	}
	defer rows.Close()

	var results []ProjectCost
	for rows.Next() {
		var pc ProjectCost
		if err := rows.Scan(&pc.Project, &pc.TokensIn, &pc.TokensOut, &pc.CostUSD, &pc.Calls); err != nil {
			return nil, fmt.Errorf("cloudstore: scan project cost: %w", err)
		}
		results = append(results, pc)
	}
	return results, rows.Err()
}

// CostByDay returns daily cost breakdown for a user. If since is nil, returns
// all available data.
func (cs *CloudStore) CostByDay(userID string, since *time.Time) ([]DayCost, error) {
	query := `
		SELECT
			TO_CHAR(occurred_at, 'YYYY-MM-DD') AS date,
			COALESCE(SUM(cost_usd), 0)          AS cost_usd,
			COUNT(*)                            AS calls
		FROM agent_tool_calls
		WHERE user_id = $1
	`
	args := []interface{}{userID}
	if since != nil {
		query += " AND occurred_at >= $2"
		args = append(args, *since)
	}
	query += " GROUP BY TO_CHAR(occurred_at, 'YYYY-MM-DD') ORDER BY date DESC"

	rows, err := cs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: cost by day: %w", err)
	}
	defer rows.Close()

	var results []DayCost
	for rows.Next() {
		var dc DayCost
		if err := rows.Scan(&dc.Date, &dc.CostUSD, &dc.Calls); err != nil {
			return nil, fmt.Errorf("cloudstore: scan day cost: %w", err)
		}
		results = append(results, dc)
	}
	return results, rows.Err()
}

// ─── Cost By Model ─────────────────────────────────────────────────────────

// ModelCost aggregates token usage and cost for a single model.
type ModelCost struct {
	Model     string  `json:"model"`
	TokensIn  int64   `json:"input_tokens"`
	TokensOut int64   `json:"output_tokens"`
	CostUSD   float64 `json:"cost"`
	Calls     int     `json:"calls"`
}

// CostByModel returns token usage and cost aggregated by model for a user.
func (cs *CloudStore) CostByModel(userID string, since *time.Time) ([]ModelCost, error) {
	query := `
		SELECT
			COALESCE(model, 'unknown') AS model,
			COALESCE(SUM(tokens_in), 0)  AS tokens_in,
			COALESCE(SUM(tokens_out), 0) AS tokens_out,
			COALESCE(SUM(cost_usd), 0)   AS cost_usd,
			COUNT(*)                     AS calls
		FROM agent_tool_calls
		WHERE user_id = $1
	`
	args := []interface{}{userID}
	if since != nil {
		query += " AND occurred_at >= $2"
		args = append(args, *since)
	}
	query += " GROUP BY COALESCE(model, 'unknown') ORDER BY cost_usd DESC"

	rows, err := cs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: cost by model: %w", err)
	}
	defer rows.Close()

	var results []ModelCost
	for rows.Next() {
		var mc ModelCost
		if err := rows.Scan(&mc.Model, &mc.TokensIn, &mc.TokensOut, &mc.CostUSD, &mc.Calls); err != nil {
			return nil, fmt.Errorf("cloudstore: scan model cost: %w", err)
		}
		results = append(results, mc)
	}
	return results, rows.Err()
}

// ─── Cost By Session ────────────────────────────────────────────────────────

// SessionCost aggregates cost data for a single session.
type SessionCost struct {
	SessionID string  `json:"session_id"`
	Project   string  `json:"project"`
	Model     string  `json:"model"`
	TokensIn  int64   `json:"input_tokens"`
	TokensOut int64   `json:"output_tokens"`
	CostUSD   float64 `json:"cost"`
	Calls     int     `json:"calls"`
	FirstCall string  `json:"first_call"`
	LastCall  string  `json:"last_call"`
}

// CostBySessions returns per-session cost breakdown for a user.
func (cs *CloudStore) CostBySessions(userID string, since *time.Time, limit int) ([]SessionCost, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT
			session_id,
			COALESCE(project, 'unknown') AS project,
			COALESCE(model, 'unknown')   AS model,
			COALESCE(SUM(tokens_in), 0)  AS tokens_in,
			COALESCE(SUM(tokens_out), 0) AS tokens_out,
			COALESCE(SUM(cost_usd), 0)   AS cost_usd,
			COUNT(*)                     AS calls,
			MIN(occurred_at)             AS first_call,
			MAX(occurred_at)             AS last_call
		FROM agent_tool_calls
		WHERE user_id = $1
	`
	args := []interface{}{userID}
	if since != nil {
		query += " AND occurred_at >= $2"
		args = append(args, *since)
	}
	query += ` GROUP BY session_id, COALESCE(project, 'unknown'), COALESCE(model, 'unknown')
		ORDER BY MAX(occurred_at) DESC
		LIMIT ` + fmt.Sprintf("%d", limit)

	rows, err := cs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: cost by sessions: %w", err)
	}
	defer rows.Close()

	var results []SessionCost
	for rows.Next() {
		var sc SessionCost
		if err := rows.Scan(&sc.SessionID, &sc.Project, &sc.Model,
			&sc.TokensIn, &sc.TokensOut, &sc.CostUSD, &sc.Calls,
			&sc.FirstCall, &sc.LastCall); err != nil {
			return nil, fmt.Errorf("cloudstore: scan session cost: %w", err)
		}
		results = append(results, sc)
	}
	return results, rows.Err()
}

// ─── Aggregated Cost Response ───────────────────────────────────────────────

// CostSummary is the combined response for the /api/costs endpoint.
type CostSummary struct {
	TotalCost float64        `json:"total_cost"`
	Period    string         `json:"period"`
	ByModel   []ModelCost    `json:"by_model"`
	ByDay     []DayCost      `json:"by_day"`
	Budget    *BudgetReport  `json:"budget"`
}

// BudgetUsage calculates total spend for a given month per provider.
// Provider is inferred from the model field: models containing "claude" or
// "sonnet" or "opus" or "haiku" are Claude; models containing "gpt" or
// "o1" are OpenAI. Budget limits must be passed in.
func (cs *CloudStore) BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*BudgetReport, error) {
	// Start and end of the month in UTC.
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	// Aggregate cost grouped by provider using CASE expression on model name.
	query := `
		SELECT
			CASE
				WHEN LOWER(COALESCE(model, '')) ~ '(claude|sonnet|opus|haiku)' THEN 'claude'
				WHEN LOWER(COALESCE(model, '')) ~ '(gpt|o1|o3|o4)' THEN 'openai'
				ELSE 'other'
			END AS provider,
			COALESCE(SUM(cost_usd), 0) AS total_cost
		FROM agent_tool_calls
		WHERE user_id = $1
		  AND occurred_at >= $2
		  AND occurred_at < $3
		GROUP BY provider
	`

	rows, err := cs.db.Query(query, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: budget usage: %w", err)
	}
	defer rows.Close()

	report := &BudgetReport{
		ClaudeBudget: claudeBudget,
		OpenAIBudget: openAIBudget,
	}

	for rows.Next() {
		var provider string
		var cost float64
		if err := rows.Scan(&provider, &cost); err != nil {
			return nil, fmt.Errorf("cloudstore: scan budget: %w", err)
		}
		switch provider {
		case "claude":
			report.ClaudeUsed = cost
		case "openai":
			report.OpenAIUsed = cost
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: budget rows: %w", err)
	}

	// Calculate percentages, guarding against zero budgets.
	if report.ClaudeBudget > 0 {
		report.ClaudePct = (report.ClaudeUsed / report.ClaudeBudget) * 100
	}
	if report.OpenAIBudget > 0 {
		report.OpenAIPct = (report.OpenAIUsed / report.OpenAIBudget) * 100
	}

	return report, nil
}
