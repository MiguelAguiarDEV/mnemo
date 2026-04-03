package cloudstore

import (
	"encoding/json"
	"fmt"
	"time"
)

// ToolCall represents a single agent tool call trace.
type ToolCall struct {
	ID         int64   `json:"id"`
	UserID     string  `json:"user_id"`
	SessionID  string  `json:"session_id"`
	Project    *string `json:"project,omitempty"`
	Agent      string  `json:"agent"`
	ToolName   string  `json:"tool_name"`
	InputJSON  *string `json:"input_json,omitempty"`
	OutputText *string `json:"output_text,omitempty"`
	DurationMs *int    `json:"duration_ms,omitempty"`
	TokensIn   *int    `json:"tokens_in,omitempty"`
	TokensOut  *int    `json:"tokens_out,omitempty"`
	Model      *string `json:"model,omitempty"`
	CostUSD    *string `json:"cost_usd,omitempty"`
	IsEngram   bool    `json:"is_engram"`
	OccurredAt string  `json:"occurred_at"`
}

// AddToolCallParams holds parameters for inserting a tool call trace.
type AddToolCallParams struct {
	SessionID  string          `json:"session_id"`
	Project    string          `json:"project,omitempty"`
	Agent      string          `json:"agent"`
	ToolName   string          `json:"tool_name"`
	InputJSON  json.RawMessage `json:"input_json,omitempty"`
	OutputText string          `json:"output_text,omitempty"`
	DurationMs *int            `json:"duration_ms,omitempty"`
	TokensIn   *int            `json:"tokens_in,omitempty"`
	TokensOut  *int            `json:"tokens_out,omitempty"`
	Model      string          `json:"model,omitempty"`
	CostUSD    *float64        `json:"cost_usd,omitempty"`
	IsEngram   bool            `json:"is_engram"`
}

// AddToolCallResult is returned after inserting a tool call trace.
type AddToolCallResult struct {
	ID         int64  `json:"id"`
	OccurredAt string `json:"occurred_at"`
}

// AddToolCall inserts a new tool call trace.
func (cs *CloudStore) AddToolCall(userID string, p AddToolCallParams) (*AddToolCallResult, error) {
	var inputJSON interface{}
	if len(p.InputJSON) > 0 {
		inputJSON = string(p.InputJSON)
	}

	var r AddToolCallResult
	err := cs.db.QueryRow(
		`INSERT INTO agent_tool_calls
		 (user_id, session_id, project, agent, tool_name, input_json, output_text,
		  duration_ms, tokens_in, tokens_out, model, cost_usd, is_engram)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id, occurred_at`,
		userID, p.SessionID, nullableString(p.Project), p.Agent, p.ToolName,
		inputJSON, nullableString(p.OutputText), p.DurationMs, p.TokensIn,
		p.TokensOut, nullableString(p.Model), p.CostUSD, p.IsEngram,
	).Scan(&r.ID, &r.OccurredAt)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: add tool call: %w", err)
	}
	return &r, nil
}

// RecentToolCalls returns the most recent tool calls across all sessions for a user.
func (cs *CloudStore) RecentToolCalls(userID string, limit int) ([]ToolCall, int, error) {
	var total int
	err := cs.db.QueryRow(
		`SELECT COUNT(*) FROM agent_tool_calls WHERE user_id = $1`,
		userID,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudstore: count recent tool calls: %w", err)
	}

	rows, err := cs.db.Query(
		`SELECT id, user_id, session_id, project, agent, tool_name, input_json::text, output_text,
		        duration_ms, tokens_in, tokens_out, model, cost_usd::text, is_engram, occurred_at
		 FROM agent_tool_calls
		 WHERE user_id = $1
		 ORDER BY occurred_at DESC
		 LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudstore: query recent tool calls: %w", err)
	}
	defer rows.Close()

	var calls []ToolCall
	for rows.Next() {
		var tc ToolCall
		if err := rows.Scan(&tc.ID, &tc.UserID, &tc.SessionID, &tc.Project, &tc.Agent,
			&tc.ToolName, &tc.InputJSON, &tc.OutputText, &tc.DurationMs, &tc.TokensIn,
			&tc.TokensOut, &tc.Model, &tc.CostUSD, &tc.IsEngram, &tc.OccurredAt); err != nil {
			return nil, 0, fmt.Errorf("cloudstore: scan tool call: %w", err)
		}
		calls = append(calls, tc)
	}

	return calls, total, nil
}

// SessionToolCallsResult contains paginated tool calls for a session.
type SessionToolCallsResult struct {
	SessionID string     `json:"session_id"`
	ToolCalls []ToolCall `json:"tool_calls"`
	Total     int        `json:"total"`
}

// SessionToolCalls returns paginated tool calls for a session, ordered by occurred_at ASC.
func (cs *CloudStore) SessionToolCalls(userID, sessionID string, limit, offset int) (*SessionToolCallsResult, error) {
	var total int
	err := cs.db.QueryRow(
		`SELECT COUNT(*) FROM agent_tool_calls WHERE user_id = $1 AND session_id = $2`,
		userID, sessionID,
	).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: count session tool calls: %w", err)
	}

	rows, err := cs.db.Query(
		`SELECT id, user_id, session_id, project, agent, tool_name, input_json::text, output_text,
		        duration_ms, tokens_in, tokens_out, model, cost_usd::text, is_engram, occurred_at
		 FROM agent_tool_calls
		 WHERE user_id = $1 AND session_id = $2
		 ORDER BY occurred_at ASC
		 LIMIT $3 OFFSET $4`,
		userID, sessionID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: query session tool calls: %w", err)
	}
	defer rows.Close()

	var calls []ToolCall
	for rows.Next() {
		var tc ToolCall
		if err := rows.Scan(&tc.ID, &tc.UserID, &tc.SessionID, &tc.Project, &tc.Agent,
			&tc.ToolName, &tc.InputJSON, &tc.OutputText, &tc.DurationMs, &tc.TokensIn,
			&tc.TokensOut, &tc.Model, &tc.CostUSD, &tc.IsEngram, &tc.OccurredAt); err != nil {
			return nil, fmt.Errorf("cloudstore: scan tool call: %w", err)
		}
		calls = append(calls, tc)
	}

	return &SessionToolCallsResult{
		SessionID: sessionID,
		ToolCalls: calls,
		Total:     total,
	}, nil
}

// ToolCallStatsResult contains aggregate tool usage statistics.
type ToolCallStatsResult struct {
	TotalCalls      int           `json:"total_calls"`
	UniqueTools     int           `json:"unique_tools"`
	TotalDurationMs int64         `json:"total_duration_ms"`
	ByTool          []ToolStat    `json:"by_tool"`
	BySession       []SessionStat `json:"by_session"`
	ByDay           []DayStat     `json:"by_day"`
}

// ToolStat holds per-tool aggregate counts.
type ToolStat struct {
	ToolName      string `json:"tool_name"`
	Count         int    `json:"count"`
	AvgDurationMs int    `json:"avg_duration_ms"`
}

// SessionStat holds per-session tool call counts.
type SessionStat struct {
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Count     int    `json:"count"`
}

// DayStat holds per-day tool call counts.
type DayStat struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ToolCallStats returns aggregate statistics for tool calls.
func (cs *CloudStore) ToolCallStats(userID, project string, since *time.Time) (*ToolCallStatsResult, error) {
	where := "WHERE user_id = $1"
	args := []any{userID}
	argN := 2

	if project != "" {
		where += fmt.Sprintf(" AND project = $%d", argN)
		args = append(args, project)
		argN++
	}
	if since != nil {
		where += fmt.Sprintf(" AND occurred_at >= $%d", argN)
		args = append(args, *since)
	}

	resp := &ToolCallStatsResult{}

	// Totals
	err := cs.db.QueryRow(
		fmt.Sprintf(`SELECT COUNT(*), COUNT(DISTINCT tool_name), COALESCE(SUM(duration_ms), 0)
		 FROM agent_tool_calls %s`, where),
		args...,
	).Scan(&resp.TotalCalls, &resp.UniqueTools, &resp.TotalDurationMs)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: tool call stats totals: %w", err)
	}

	// By tool (top 20)
	rows, err := cs.db.Query(
		fmt.Sprintf(`SELECT tool_name, COUNT(*), COALESCE(AVG(duration_ms)::int, 0)
		 FROM agent_tool_calls %s
		 GROUP BY tool_name ORDER BY COUNT(*) DESC LIMIT 20`, where),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: tool call stats by tool: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ts ToolStat
		rows.Scan(&ts.ToolName, &ts.Count, &ts.AvgDurationMs)
		resp.ByTool = append(resp.ByTool, ts)
	}

	// By session (top 20)
	rows2, err := cs.db.Query(
		fmt.Sprintf(`SELECT session_id, COALESCE(project, ''), COUNT(*)
		 FROM agent_tool_calls %s
		 GROUP BY session_id, project ORDER BY COUNT(*) DESC LIMIT 20`, where),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: tool call stats by session: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var ss SessionStat
		rows2.Scan(&ss.SessionID, &ss.Project, &ss.Count)
		resp.BySession = append(resp.BySession, ss)
	}

	// By day (last 30)
	rows3, err := cs.db.Query(
		fmt.Sprintf(`SELECT occurred_at::date::text, COUNT(*)
		 FROM agent_tool_calls %s
		 GROUP BY occurred_at::date ORDER BY occurred_at::date DESC LIMIT 30`, where),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: tool call stats by day: %w", err)
	}
	defer rows3.Close()
	for rows3.Next() {
		var ds DayStat
		rows3.Scan(&ds.Date, &ds.Count)
		resp.ByDay = append(resp.ByDay, ds)
	}

	return resp, nil
}
