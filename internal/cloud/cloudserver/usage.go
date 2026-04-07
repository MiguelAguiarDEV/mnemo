package cloudserver

// Usage handlers — Claude Code subscription rate limit + local stats cache.
//
// /api/usage/limits → proxies the PROMETHEUS bridge GET /v1/usage endpoint,
//                     which exposes the latest SDKRateLimitEvent payload + the
//                     last query()'s modelUsage breakdown.
//
// /api/usage/stats  → reads ~/.claude/stats-cache.json (mounted into the
//                     mnemo-cloud container at /claude-credentials/) and returns
//                     the last 30 days of dailyActivity + dailyModelTokens, plus
//                     aggregated modelUsage and totals.
//
// Both endpoints sit behind withAuth and are intended to power JARVIS answers
// to "cuánto me queda?" / "uso?" / "límites?" via the bash tool.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"
)

const (
	defaultClaudeStatsPath  = "/claude-credentials/stats-cache.json"
	defaultPrometheusUsage  = "http://host.docker.internal:9876/v1/usage"
	usageStatsDays          = 30
	usageProxyTimeout       = 4 * time.Second
)

// ─── /api/usage/limits ──────────────────────────────────────────────────────

// usageLimitsResponse mirrors the JSON shape returned by the PROMETHEUS bridge
// at GET /v1/usage. Fields are kept loose (json.RawMessage / interface{}) so we
// don't have to track every SDK field change in two places.
type usageLimitsResponse struct {
	RateLimit                    json.RawMessage `json:"rate_limit"`
	RateLimitSeenAt              *string         `json:"rate_limit_seen_at"`
	ModelUsageLastRequest        json.RawMessage `json:"model_usage_last_request"`
	PermissionDenialsLastRequest json.RawMessage `json:"permission_denials_last_request"`
	LastResultAt                 *string         `json:"last_result_at"`
}

func (s *CloudServer) handleUsageLimits(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	url := os.Getenv("PROMETHEUS_USAGE_URL")
	if url == "" {
		url = defaultPrometheusUsage
	}

	slog.Info("usage limits proxy", "handler", "handleUsageLimits", "user_id", userID, "url", url)

	client := &http.Client{Timeout: usageProxyTimeout}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		slog.Error("usage limits: build request failed", "error", err)
		jsonError(w, http.StatusInternalServerError, "failed to build bridge request")
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("usage limits: bridge unreachable", "error", err)
		jsonResponse(w, http.StatusOK, map[string]any{
			"rate_limit":              nil,
			"rate_limit_seen_at":      nil,
			"model_usage_last_request": nil,
			"last_result_at":          nil,
			"bridge_available":        false,
			"bridge_error":            err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("usage limits: read body failed", "error", err)
		jsonError(w, http.StatusBadGateway, "failed to read bridge response")
		return
	}

	if resp.StatusCode != http.StatusOK {
		slog.Warn("usage limits: bridge returned non-200", "status", resp.StatusCode, "body", string(body))
		jsonError(w, http.StatusBadGateway, "bridge returned status "+resp.Status)
		return
	}

	var parsed usageLimitsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		slog.Error("usage limits: parse json failed", "error", err)
		jsonError(w, http.StatusBadGateway, "failed to parse bridge response")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"rate_limit":               parsed.RateLimit,
		"rate_limit_seen_at":       parsed.RateLimitSeenAt,
		"model_usage_last_request": parsed.ModelUsageLastRequest,
		"permission_denials_last_request": parsed.PermissionDenialsLastRequest,
		"last_result_at":           parsed.LastResultAt,
		"bridge_available":         true,
	})
}

// ─── /api/usage/stats ───────────────────────────────────────────────────────

// claudeStatsCache mirrors the relevant subset of ~/.claude/stats-cache.json.
type claudeStatsCache struct {
	Version          int    `json:"version"`
	LastComputedDate string `json:"lastComputedDate"`

	DailyActivity []struct {
		Date          string `json:"date"`
		MessageCount  int    `json:"messageCount"`
		SessionCount  int    `json:"sessionCount"`
		ToolCallCount int    `json:"toolCallCount"`
	} `json:"dailyActivity"`

	DailyModelTokens []struct {
		Date          string         `json:"date"`
		TokensByModel map[string]int `json:"tokensByModel"`
	} `json:"dailyModelTokens"`

	ModelUsage map[string]struct {
		InputTokens              int     `json:"inputTokens"`
		OutputTokens             int     `json:"outputTokens"`
		CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
		CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
		WebSearchRequests        int     `json:"webSearchRequests"`
		CostUSD                  float64 `json:"costUSD"`
	} `json:"modelUsage"`

	TotalSessions int `json:"totalSessions"`
	TotalMessages int `json:"totalMessages"`
}

func (s *CloudServer) handleUsageStats(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)

	path := os.Getenv("CLAUDE_STATS_PATH")
	if path == "" {
		path = defaultClaudeStatsPath
	}

	slog.Info("usage stats query", "handler", "handleUsageStats", "user_id", userID, "path", path)

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("usage stats: file not found", "path", path)
			jsonResponse(w, http.StatusOK, map[string]any{
				"available":         false,
				"path":              path,
				"error":             "stats-cache.json not found",
				"daily_activity":    []any{},
				"daily_model_tokens": []any{},
			})
			return
		}
		slog.Error("usage stats: read failed", "path", path, "error", err)
		jsonError(w, http.StatusInternalServerError, "failed to read stats cache")
		return
	}

	var stats claudeStatsCache
	if err := json.Unmarshal(raw, &stats); err != nil {
		slog.Error("usage stats: parse failed", "error", err)
		jsonError(w, http.StatusInternalServerError, "failed to parse stats cache")
		return
	}

	// Sort newest first by date string (YYYY-MM-DD sorts lexically) and trim.
	sort.Slice(stats.DailyActivity, func(i, j int) bool {
		return stats.DailyActivity[i].Date > stats.DailyActivity[j].Date
	})
	if len(stats.DailyActivity) > usageStatsDays {
		stats.DailyActivity = stats.DailyActivity[:usageStatsDays]
	}

	sort.Slice(stats.DailyModelTokens, func(i, j int) bool {
		return stats.DailyModelTokens[i].Date > stats.DailyModelTokens[j].Date
	})
	if len(stats.DailyModelTokens) > usageStatsDays {
		stats.DailyModelTokens = stats.DailyModelTokens[:usageStatsDays]
	}

	slog.Info("usage stats query complete",
		"handler", "handleUsageStats",
		"user_id", userID,
		"days_activity", len(stats.DailyActivity),
		"days_tokens", len(stats.DailyModelTokens),
		"total_sessions", stats.TotalSessions,
		"total_messages", stats.TotalMessages,
	)

	jsonResponse(w, http.StatusOK, map[string]any{
		"available":          true,
		"path":               path,
		"version":            stats.Version,
		"last_computed_date": stats.LastComputedDate,
		"daily_activity":     stats.DailyActivity,
		"daily_model_tokens": stats.DailyModelTokens,
		"model_usage":        stats.ModelUsage,
		"total_sessions":     stats.TotalSessions,
		"total_messages":     stats.TotalMessages,
		"days_returned":      len(stats.DailyActivity),
	})
}
