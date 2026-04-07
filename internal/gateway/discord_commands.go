package gateway

// Slash commands for the DiscordChannel — fast-path actions that hit ATHENA
// endpoints directly or shell out, bypassing the LLM for instant, free,
// structured responses. Free-form DMs still route through the gateway/LLM via
// onMessageCreate in discord.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// jarvisCommands lists all guild slash commands registered on bot ready.
var jarvisCommands = []*discordgo.ApplicationCommand{
	{Name: "health", Description: "JARVIS stack health"},
	{Name: "usage", Description: "Claude rate limit + token usage"},
	{
		Name:        "stats",
		Description: "Daily usage stats",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "days",
				Description: "Days to show (default 7)",
				Required:    false,
			},
		},
	},
	{Name: "costs", Description: "Cost summary this month"},
	{
		Name:        "tasks",
		Description: "List tasks",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "status",
				Description: "pending|in_progress|done",
				Required:    false,
			},
		},
	},
	{
		Name:        "done",
		Description: "Mark task as done",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "id",
				Description: "Task ID",
				Required:    true,
			},
		},
	},
	{Name: "services", Description: "Docker containers status"},
	{
		Name:        "memory",
		Description: "Search MNEMO",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "query",
				Description: "Search query",
				Required:    true,
			},
		},
	},
	{Name: "help", Description: "Show available commands"},
}

// ─── Registration ───────────────────────────────────────────────────────────

// registerSlashCommands registers all jarvisCommands as guild commands for
// faster propagation (global commands can take up to 1h to appear). If
// guildID is empty, falls back to global registration.
func (dc *DiscordChannel) registerSlashCommands(s *discordgo.Session) {
	if s.State == nil || s.State.User == nil {
		dc.logger.Warn("discord register commands: no bot user in session state")
		return
	}
	appID := s.State.User.ID
	dc.appID = appID

	guildID := dc.guildID
	registered := 0
	for _, cmd := range jarvisCommands {
		if _, err := s.ApplicationCommandCreate(appID, guildID, cmd); err != nil {
			dc.logger.Error("discord register command failed",
				"command", cmd.Name,
				"guild_id", guildID,
				"error", err,
			)
			continue
		}
		registered++
	}
	dc.logger.Info("discord slash commands registered",
		"count", registered,
		"total", len(jarvisCommands),
		"guild_id", guildID,
		"app_id", appID,
	)
}

// ─── Interaction Dispatch ───────────────────────────────────────────────────

// handleInteraction dispatches incoming slash command interactions to the
// corresponding cmd* handler. It defers the Discord response first (giving
// 15 minutes instead of 3 seconds to respond) and sends the final result
// via FollowupMessageCreate.
func (dc *DiscordChannel) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	// Auth check: only allowed users can use slash commands.
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}
	if userID == "" && i.User != nil {
		userID = i.User.ID
	}
	if !dc.isUserAllowed(userID) {
		dc.logger.Warn("discord slash command from unauthorized user",
			"user_id", userID,
			"command", i.ApplicationCommandData().Name,
		)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Not authorized.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	cmd := i.ApplicationCommandData()
	dc.logger.Info("discord slash command received",
		"user_id", userID,
		"command", cmd.Name,
	)

	// Defer the response so we have up to 15 min to reply.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		dc.logger.Error("discord defer interaction failed", "command", cmd.Name, "error", err)
		return
	}

	// Dispatch.
	var result string
	var err error
	switch cmd.Name {
	case "health":
		result, err = dc.cmdHealth()
	case "usage":
		result, err = dc.cmdUsage()
	case "stats":
		days := 7
		for _, opt := range cmd.Options {
			if opt.Name == "days" {
				days = int(opt.IntValue())
			}
		}
		result, err = dc.cmdStats(days)
	case "costs":
		result, err = dc.cmdCosts()
	case "tasks":
		status := ""
		for _, opt := range cmd.Options {
			if opt.Name == "status" {
				status = opt.StringValue()
			}
		}
		result, err = dc.cmdTasks(status)
	case "done":
		var id int64
		for _, opt := range cmd.Options {
			if opt.Name == "id" {
				id = opt.IntValue()
			}
		}
		result, err = dc.cmdDone(id)
	case "services":
		result, err = dc.cmdServices()
	case "memory":
		query := ""
		for _, opt := range cmd.Options {
			if opt.Name == "query" {
				query = opt.StringValue()
			}
		}
		result, err = dc.cmdMemory(query)
	case "help":
		result = dc.cmdHelp()
	default:
		result = "Unknown command."
	}

	if err != nil {
		dc.logger.Error("discord slash command failed",
			"command", cmd.Name,
			"error", err,
		)
		result = "Error: " + err.Error()
	}

	// Discord messages cap at 2000 chars.
	if len(result) > 1900 {
		result = result[:1900] + "\n... (truncated)"
	}
	if strings.TrimSpace(result) == "" {
		result = "(empty response)"
	}

	if _, ferr := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: result,
	}); ferr != nil {
		dc.logger.Error("discord followup message failed",
			"command", cmd.Name,
			"error", ferr,
		)
	}
}

// ─── HTTP helper ────────────────────────────────────────────────────────────

// apiGet performs an authenticated GET against ATHENA and decodes the JSON
// body into out. Returns the status code and any error.
func (dc *DiscordChannel) apiGet(path string, out any) (int, error) {
	return dc.apiDo(http.MethodGet, path, nil, out)
}

func (dc *DiscordChannel) apiDo(method, path string, body []byte, out any) (int, error) {
	if dc.athenaURL == "" {
		return 0, fmt.Errorf("athena URL not configured")
	}
	client := dc.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, dc.athenaURL+path, bodyReader)
	if err != nil {
		return 0, err
	}
	if dc.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+dc.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("athena %s %s: %s", method, path, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// ─── Command handlers ───────────────────────────────────────────────────────

func (dc *DiscordChannel) cmdHealth() (string, error) {
	var h struct {
		Status   string `json:"status"`
		Service  string `json:"service"`
		Version  string `json:"version"`
		Database string `json:"database"`
	}
	status, err := dc.apiGet("/health", &h)
	if err != nil && status == 0 {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("## JARVIS Health\n\n")
	icon := "[OK]"
	if h.Status != "ok" {
		icon = "[DEGRADED]"
	}
	sb.WriteString(fmt.Sprintf("**ATHENA:** %s `%s`\n", icon, h.Status))
	if h.Service != "" {
		sb.WriteString(fmt.Sprintf("**Service:** `%s` v`%s`\n", h.Service, h.Version))
	}
	if h.Database != "" {
		sb.WriteString(fmt.Sprintf("**Database:** `%s`\n", h.Database))
	}

	// Container snapshot via local docker.
	out, derr := exec.Command("docker", "ps", "--format", "{{.Names}}\t{{.Status}}").Output()
	if derr == nil && len(out) > 0 {
		sb.WriteString("\n**Containers:**\n```\n")
		sb.WriteString(string(out))
		sb.WriteString("```")
	}
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdUsage() (string, error) {
	var data struct {
		BridgeAvailable bool `json:"bridge_available"`
		BridgeError     string `json:"bridge_error"`
		RateLimit       *struct {
			Status        string  `json:"status"`
			ResetsAt      int64   `json:"resetsAt"`
			RateLimitType string  `json:"rateLimitType"`
			Utilization   float64 `json:"utilization"`
		} `json:"rate_limit"`
		ModelUsageLastRequest map[string]struct {
			InputTokens  int     `json:"inputTokens"`
			OutputTokens int     `json:"outputTokens"`
			CostUSD      float64 `json:"costUSD"`
		} `json:"model_usage_last_request"`
	}
	if _, err := dc.apiGet("/api/usage/limits", &data); err != nil {
		return "", err
	}
	if !data.BridgeAvailable {
		msg := "Bridge unavailable"
		if data.BridgeError != "" {
			msg += ": " + data.BridgeError
		}
		return msg, nil
	}

	var sb strings.Builder
	sb.WriteString("## Claude Usage\n\n")
	if data.RateLimit != nil {
		sb.WriteString(fmt.Sprintf("**Status:** `%s`\n", data.RateLimit.Status))
		if data.RateLimit.RateLimitType != "" {
			sb.WriteString(fmt.Sprintf("**Type:** `%s`\n", data.RateLimit.RateLimitType))
		}
		if data.RateLimit.ResetsAt > 0 {
			sb.WriteString(fmt.Sprintf("**Resets:** <t:%d:R>\n", data.RateLimit.ResetsAt))
		}
		if data.RateLimit.Utilization > 0 {
			sb.WriteString(fmt.Sprintf("**Used:** `%.0f%%`\n", data.RateLimit.Utilization*100))
		}
	} else {
		sb.WriteString("_No rate limit event seen yet._\n")
	}
	if len(data.ModelUsageLastRequest) > 0 {
		sb.WriteString("\n**Last request:**\n")
		for model, u := range data.ModelUsageLastRequest {
			sb.WriteString(fmt.Sprintf("- `%s` in:%d out:%d $%.4f\n", model, u.InputTokens, u.OutputTokens, u.CostUSD))
		}
	}
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdStats(days int) (string, error) {
	if days <= 0 {
		days = 7
	}
	var data struct {
		Available        bool   `json:"available"`
		Error            string `json:"error"`
		LastComputedDate string `json:"last_computed_date"`
		DailyActivity    []struct {
			Date          string `json:"date"`
			MessageCount  int    `json:"messageCount"`
			SessionCount  int    `json:"sessionCount"`
			ToolCallCount int    `json:"toolCallCount"`
		} `json:"daily_activity"`
		TotalSessions int `json:"total_sessions"`
		TotalMessages int `json:"total_messages"`
	}
	if _, err := dc.apiGet("/api/usage/stats", &data); err != nil {
		return "", err
	}
	if !data.Available {
		return "Stats cache not available: " + data.Error, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Claude Stats (last %d days)\n\n", days))
	sb.WriteString(fmt.Sprintf("**Total sessions:** `%d`  **Total messages:** `%d`\n", data.TotalSessions, data.TotalMessages))
	if data.LastComputedDate != "" {
		sb.WriteString(fmt.Sprintf("**Last computed:** `%s`\n", data.LastComputedDate))
	}
	sb.WriteString("\n```\n")
	sb.WriteString("date        msgs  sess  tools\n")
	n := days
	if n > len(data.DailyActivity) {
		n = len(data.DailyActivity)
	}
	for i := 0; i < n; i++ {
		d := data.DailyActivity[i]
		sb.WriteString(fmt.Sprintf("%-10s  %4d  %4d  %5d\n", d.Date, d.MessageCount, d.SessionCount, d.ToolCallCount))
	}
	sb.WriteString("```")
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdCosts() (string, error) {
	var b struct {
		MonthlyBudgetUSD float64 `json:"monthly_budget_usd"`
		SpentThisMonth   float64 `json:"spent_this_month"`
		Remaining        float64 `json:"remaining"`
		PercentUsed      float64 `json:"percent_used"`
		ProjectedMonthly float64 `json:"projected_monthly"`
	}
	if _, err := dc.apiGet("/api/costs/budget", &b); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("## Cost Summary (this month)\n\n")
	sb.WriteString(fmt.Sprintf("**Budget:** `$%.2f`\n", b.MonthlyBudgetUSD))
	sb.WriteString(fmt.Sprintf("**Spent:** `$%.2f` (`%.1f%%`)\n", b.SpentThisMonth, b.PercentUsed))
	sb.WriteString(fmt.Sprintf("**Remaining:** `$%.2f`\n", b.Remaining))
	sb.WriteString(fmt.Sprintf("**Projected:** `$%.2f`\n", b.ProjectedMonthly))
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdTasks(status string) (string, error) {
	path := "/api/tasks"
	if status != "" {
		path += "?status=" + status
	}
	// Tasks endpoint may return either [] or {tasks:[]}
	var raw json.RawMessage
	if _, err := dc.apiGet(path, &raw); err != nil {
		return "", err
	}

	type task struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		Priority string `json:"priority"`
	}
	var tasks []task
	if err := json.Unmarshal(raw, &tasks); err != nil {
		var wrapped struct {
			Tasks []task `json:"tasks"`
		}
		if err2 := json.Unmarshal(raw, &wrapped); err2 != nil {
			return "", fmt.Errorf("decode tasks: %w", err)
		}
		tasks = wrapped.Tasks
	}

	var sb strings.Builder
	label := "all"
	if status != "" {
		label = status
	}
	sb.WriteString(fmt.Sprintf("## Tasks (%s)\n\n", label))
	if len(tasks) == 0 {
		sb.WriteString("_No tasks._")
		return sb.String(), nil
	}
	max := 20
	if len(tasks) < max {
		max = len(tasks)
	}
	for i := 0; i < max; i++ {
		t := tasks[i]
		sb.WriteString(fmt.Sprintf("- `#%d` **%s** — `%s`", t.ID, t.Title, t.Status))
		if t.Priority != "" {
			sb.WriteString(fmt.Sprintf(" (`%s`)", t.Priority))
		}
		sb.WriteString("\n")
	}
	if len(tasks) > max {
		sb.WriteString(fmt.Sprintf("\n_...and %d more._", len(tasks)-max))
	}
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdDone(id int64) (string, error) {
	if id <= 0 {
		return "Invalid task ID", nil
	}
	body := []byte(`{"status":"done"}`)
	var out map[string]any
	if _, err := dc.apiDo(http.MethodPatch, fmt.Sprintf("/api/tasks/%d", id), body, &out); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task `#%d` marked as **done**.", id), nil
}

func (dc *DiscordChannel) cmdServices() (string, error) {
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}\t{{.Status}}").Output()
	if err != nil {
		return "", fmt.Errorf("docker ps: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("## Docker Containers\n\n```\n")
	if len(out) == 0 {
		sb.WriteString("(no containers running)\n")
	} else {
		sb.Write(out)
	}
	sb.WriteString("```")
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdMemory(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "Usage: `/memory query:<text>`", nil
	}
	cmd := exec.Command("mnemo", "search", query)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mnemo search: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## MNEMO search: `%s`\n\n```\n", query))
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = "(no results)"
	}
	sb.WriteString(text)
	sb.WriteString("\n```")
	return sb.String(), nil
}

func (dc *DiscordChannel) cmdHelp() string {
	return strings.Join([]string{
		"## JARVIS Slash Commands",
		"",
		"- `/health` — JARVIS stack health + containers",
		"- `/usage` — Claude rate limit + last request tokens",
		"- `/stats [days]` — Daily usage stats (default 7)",
		"- `/costs` — Cost summary for current month",
		"- `/tasks [status]` — List tasks (pending|in_progress|done)",
		"- `/done <id>` — Mark task as done",
		"- `/services` — Docker containers status",
		"- `/memory <query>` — Search MNEMO knowledge base",
		"- `/help` — Show this message",
		"",
		"_Free-form DMs still route through Claude via the LLM._",
	}, "\n")
}
