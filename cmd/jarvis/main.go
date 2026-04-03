// JARVIS CLI — Task management and system control for JARVIS.
//
// Usage:
//
//	jarvis tasks <create|list|complete|delete> [args] [flags]
//	jarvis status                              Quick system status
//	jarvis dream <run|status>                  MORPHEUS memory consolidation
//	jarvis ticker <run|status>                 SENTINEL health checks
//	jarvis costs <show|by-model|by-day>        Cost tracking
//	jarvis version                             Show version
//
// Global flags:
//
//	--api-url    API URL (default: http://100.71.66.54:8080, env: JARVIS_API_URL)
//	--api-key    API key (env: JARVIS_API_KEY)
//	--format     Output format: text, json (default: text)
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

var version = "dev"

const (
	defaultAPIURL = "http://100.71.66.54:8080"
)

// ANSI color helpers.
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

// config holds resolved CLI configuration.
type config struct {
	apiURL string
	apiKey string
	format string // "text" or "json"
	logger *slog.Logger
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg := config{
		apiURL: envOrDefault("JARVIS_API_URL", defaultAPIURL),
		apiKey: os.Getenv("JARVIS_API_KEY"),
		format: "text",
		logger: logger,
	}

	// Parse global flags from args, extract remaining positional args.
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api-url":
			if i+1 < len(args) {
				i++
				cfg.apiURL = args[i]
			}
		case "--api-key":
			if i+1 < len(args) {
				i++
				cfg.apiKey = args[i]
			}
		case "--format":
			if i+1 < len(args) {
				i++
				cfg.format = args[i]
			}
		case "--help", "-h":
			printUsage(stdout)
			return 0
		default:
			positional = append(positional, args[i])
		}
	}

	// Resolve API key if not set.
	if cfg.apiKey == "" {
		cfg.apiKey = resolveAPIKey()
	}

	if len(positional) == 0 {
		printUsage(stdout)
		return 0
	}

	cmd := positional[0]
	subArgs := positional[1:]

	switch cmd {
	case "tasks":
		return cmdTasks(&cfg, subArgs, stdout, stderr)
	case "jobs":
		return cmdJobs(&cfg, subArgs, stdout, stderr)
	case "status":
		return cmdStatus(&cfg, stdout, stderr)
	case "dream":
		return cmdDream(&cfg, subArgs, stdout, stderr)
	case "ticker":
		return cmdTicker(&cfg, subArgs, stdout, stderr)
	case "costs":
		return cmdCosts(&cfg, subArgs, stdout, stderr)
	case "version":
		return cmdVersion(&cfg, stdout)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", cmd)
		printUsage(stderr)
		return 1
	}
}

// ─── Tasks ──────────────────────────────────────────────────────────────────

func cmdTasks(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis tasks <create|list|complete|delete> [args]")
		return 1
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "create":
		return tasksCreate(cfg, subArgs, stdout, stderr)
	case "list", "ls":
		return tasksList(cfg, subArgs, stdout, stderr)
	case "complete", "done":
		return tasksComplete(cfg, subArgs, stdout, stderr)
	case "delete", "rm":
		return tasksDelete(cfg, subArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown tasks subcommand: %s\n", sub)
		return 1
	}
}

func tasksCreate(cfg *config, args []string, stdout, stderr io.Writer) int {
	var title, description, priority, project, source string
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--description", "-d":
			if i+1 < len(args) {
				i++
				description = args[i]
			}
		case "--priority", "-p":
			if i+1 < len(args) {
				i++
				priority = args[i]
			}
		case "--project":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		case "--source":
			if i+1 < len(args) {
				i++
				source = args[i]
			}
		default:
			positional = append(positional, args[i])
		}
	}

	title = strings.Join(positional, " ")
	if title == "" {
		fmt.Fprintln(stderr, "usage: jarvis tasks create \"Title\" [--description desc] [--priority high|medium|low] [--project name]")
		return 1
	}

	if priority == "" {
		priority = "medium"
	}
	if source == "" {
		source = "cli"
	}

	body := map[string]any{
		"title":    title,
		"priority": priority,
		"source":   source,
	}
	if description != "" {
		body["description"] = description
	}
	if project != "" {
		body["project"] = project
	}

	resp, err := apiRequest(cfg, "POST", "/api/tasks", body)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	id := jsonFloat(resp, "id")
	fmt.Fprintf(stdout, "%s✓ Task created%s (id: %.0f)\n", colorGreen, colorReset, id)
	fmt.Fprintf(stdout, "  Title:    %s\n", jsonStr(resp, "title"))
	fmt.Fprintf(stdout, "  Priority: %s\n", jsonStr(resp, "priority"))
	if p := jsonStr(resp, "project"); p != "" {
		fmt.Fprintf(stdout, "  Project:  %s\n", p)
	}
	return 0
}

func tasksList(cfg *config, args []string, stdout, stderr io.Writer) int {
	var status, project string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--status", "-s":
			if i+1 < len(args) {
				i++
				status = args[i]
			}
		case "--project":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		}
	}

	params := url.Values{}
	if status != "" && status != "all" {
		params.Set("status", status)
	}
	if project != "" {
		params.Set("project", project)
	}
	params.Set("limit", "100")

	path := "/api/tasks"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := apiRequest(cfg, "GET", path, nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	tasks, ok := resp["tasks"].([]any)
	if !ok || len(tasks) == 0 {
		fmt.Fprintln(stdout, "No tasks found.")
		return 0
	}

	total := jsonFloat(resp, "total")
	fmt.Fprintf(stdout, "%sTasks%s (%d total)\n\n", colorBold, colorReset, int(total))

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%sID\tSTATUS\tPRIORITY\tTITLE\tPROJECT%s\n", colorDim, colorReset)
	for _, t := range tasks {
		task, ok := t.(map[string]any)
		if !ok {
			continue
		}
		statusStr := mapStr(task, "status")
		statusColor := statusColorCode(statusStr)
		priorityStr := mapStr(task, "priority")
		priorityColor := priorityColorCode(priorityStr)

		proj := mapStr(task, "project")
		if proj == "" {
			proj = "-"
		}
		fmt.Fprintf(tw, "%.0f\t%s%s%s\t%s%s%s\t%s\t%s\n",
			mapFloat(task, "id"),
			statusColor, statusStr, colorReset,
			priorityColor, priorityStr, colorReset,
			mapStr(task, "title"),
			proj,
		)
	}
	tw.Flush()
	return 0
}

func tasksComplete(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis tasks complete <id>")
		return 1
	}

	id := args[0]

	// First, get the current task to check status.
	task, err := apiRequest(cfg, "GET", "/api/tasks/"+id, nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	// The GET /api/tasks/{id} returns {"task": {...}, "subtasks": [...]}.
	taskData, _ := task["task"].(map[string]any)
	currentStatus := mapStr(taskData, "status")

	// Handle state machine: open -> in_progress -> done.
	if currentStatus == "open" {
		_, err := apiRequest(cfg, "PATCH", "/api/tasks/"+id, map[string]any{"status": "in_progress"})
		if err != nil {
			fmt.Fprintf(stderr, "error transitioning to in_progress: %v\n", err)
			return 1
		}
	}

	resp, err := apiRequest(cfg, "PATCH", "/api/tasks/"+id, map[string]any{"status": "done"})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	fmt.Fprintf(stdout, "%s✓ Task %s marked as done%s\n", colorGreen, id, colorReset)
	return 0
}

func tasksDelete(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis tasks delete <id>")
		return 1
	}

	id := args[0]
	resp, err := apiRequest(cfg, "DELETE", "/api/tasks/"+id, nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	fmt.Fprintf(stdout, "%s✓ Task %s deleted%s\n", colorGreen, id, colorReset)
	return 0
}

// ─── Jobs ───────────────────────────────────────────────────────────────────

func cmdJobs(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis jobs <list|get|create> [args]")
		return 1
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "list", "ls":
		return jobsList(cfg, stdout, stderr)
	case "get":
		return jobsGet(cfg, subArgs, stdout, stderr)
	case "create":
		return jobsCreate(cfg, subArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown jobs subcommand: %s\n", sub)
		return 1
	}
}

func jobsList(cfg *config, stdout, stderr io.Writer) int {
	resp, err := apiRequest(cfg, "GET", "/api/jobs", nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	jobs, ok := resp["jobs"].([]any)
	if !ok || len(jobs) == 0 {
		fmt.Fprintln(stdout, "No jobs found.")
		return 0
	}

	total := jsonFloat(resp, "total")
	fmt.Fprintf(stdout, "%sJobs%s (%d total)\n\n", colorBold, colorReset, int(total))

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%sID\tSTATUS\tTASK\tPROJECT%s\n", colorDim, colorReset)
	for _, j := range jobs {
		job, ok := j.(map[string]any)
		if !ok {
			continue
		}
		statusStr := mapStr(job, "status")
		statusColor := jobStatusColor(statusStr)
		task := mapStr(job, "task")
		if len(task) > 60 {
			task = task[:60] + "..."
		}
		proj := mapStr(job, "project")
		if proj == "" {
			proj = "-"
		}
		fmt.Fprintf(tw, "%s\t%s%s%s\t%s\t%s\n",
			mapStr(job, "id"),
			statusColor, statusStr, colorReset,
			task,
			proj,
		)
	}
	tw.Flush()
	return 0
}

func jobsGet(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis jobs get <id>")
		return 1
	}

	id := args[0]
	resp, err := apiRequest(cfg, "GET", "/api/jobs/"+id, nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	job, ok := resp["job"].(map[string]any)
	if !ok {
		fmt.Fprintln(stderr, "invalid response format")
		return 1
	}

	statusStr := mapStr(job, "status")
	statusColor := jobStatusColor(statusStr)

	fmt.Fprintf(stdout, "%sJob #%s%s\n", colorBold, mapStr(job, "id"), colorReset)
	fmt.Fprintf(stdout, "  Status:  %s%s%s\n", statusColor, statusStr, colorReset)
	fmt.Fprintf(stdout, "  Task:    %s\n", mapStr(job, "task"))
	if p := mapStr(job, "project"); p != "" {
		fmt.Fprintf(stdout, "  Project: %s\n", p)
	}
	fmt.Fprintf(stdout, "  Created: %s\n", mapStr(job, "created_at"))
	if s := mapStr(job, "started_at"); s != "" {
		fmt.Fprintf(stdout, "  Started: %s\n", s)
	}
	if c := mapStr(job, "completed_at"); c != "" {
		fmt.Fprintf(stdout, "  Done:    %s\n", c)
	}
	if e := mapStr(job, "error"); e != "" {
		fmt.Fprintf(stdout, "  Error:   %s%s%s\n", colorRed, e, colorReset)
	}

	// Show result if available.
	if result, ok := job["result"].(map[string]any); ok {
		fmt.Fprintf(stdout, "\n  %sDuration:%s %s\n", colorDim, colorReset, mapStr(result, "duration"))
		fmt.Fprintf(stdout, "  %sTokens:%s  in=%s out=%s\n", colorDim, colorReset,
			mapStr(result, "tokens_in"), mapStr(result, "tokens_out"))
		if output := mapStr(result, "output"); output != "" {
			if len(output) > 1000 {
				output = output[:1000] + "\n... (truncated)"
			}
			fmt.Fprintf(stdout, "\n  %sOutput:%s\n%s\n", colorDim, colorReset, output)
		}
	}

	return 0
}

func jobsCreate(cfg *config, args []string, stdout, stderr io.Writer) int {
	var task, project, workingDir string
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				i++
				project = args[i]
			}
		case "--working-dir", "--dir":
			if i+1 < len(args) {
				i++
				workingDir = args[i]
			}
		default:
			positional = append(positional, args[i])
		}
	}

	task = strings.Join(positional, " ")
	if task == "" {
		fmt.Fprintln(stderr, "usage: jarvis jobs create \"task description\" [--project name] [--working-dir path]")
		return 1
	}

	body := map[string]any{
		"task": task,
	}
	if project != "" {
		body["project"] = project
	}
	if workingDir != "" {
		body["working_dir"] = workingDir
	}

	resp, err := apiRequest(cfg, "POST", "/api/jobs", body)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	jobID := jsonStr(resp, "job_id")
	fmt.Fprintf(stdout, "%s✓ Job #%s created%s — running in background\n", colorGreen, jobID, colorReset)
	fmt.Fprintf(stdout, "  Task: %s\n", task)
	if project != "" {
		fmt.Fprintf(stdout, "  Project: %s\n", project)
	}
	fmt.Fprintf(stdout, "\nCheck status: jarvis jobs get %s\n", jobID)
	return 0
}

func jobStatusColor(status string) string {
	switch status {
	case "success":
		return colorGreen
	case "error":
		return colorRed
	case "running":
		return colorCyan
	case "pending":
		return colorYellow
	default:
		return colorReset
	}
}

// ─── Status ─────────────────────────────────────────────────────────────────

func cmdStatus(cfg *config, stdout, stderr io.Writer) int {
	if cfg.format == "json" {
		return cmdStatusJSON(cfg, stdout, stderr)
	}

	fmt.Fprintf(stdout, "\n%sJARVIS System Status%s\n", colorBold, colorReset)
	fmt.Fprintln(stdout, "====================")

	// Health
	health, err := apiRequest(cfg, "GET", "/health", nil)
	if err != nil {
		fmt.Fprintf(stdout, "API:        %s✗ unreachable%s (%v)\n", colorRed, colorReset, err)
	} else {
		status := jsonStr(health, "status")
		ver := jsonStr(health, "version")
		if status == "ok" {
			fmt.Fprintf(stdout, "API:        %s✓ healthy%s (v%s)\n", colorGreen, colorReset, ver)
		} else {
			fmt.Fprintf(stdout, "API:        %s⚠ %s%s (v%s)\n", colorYellow, status, colorReset, ver)
		}
	}

	// Postgres (inferred from health)
	if health != nil {
		dbStatus := jsonStr(health, "database")
		if dbStatus == "unavailable" {
			fmt.Fprintf(stdout, "Postgres:   %s✗ unavailable%s\n", colorRed, colorReset)
		} else {
			fmt.Fprintf(stdout, "Postgres:   %s✓ connected%s\n", colorGreen, colorReset)
		}
	}

	// Service port checks
	checkPort(stdout, "OpenCode", "localhost", 4096)
	checkPort(stdout, "Dashboard", "100.71.66.54", 3001)

	// Tasks summary
	tasksResp, err := apiRequest(cfg, "GET", "/api/tasks?limit=1000", nil)
	if err == nil {
		tasks, _ := tasksResp["tasks"].([]any)
		pending, done := 0, 0
		for _, t := range tasks {
			task, ok := t.(map[string]any)
			if !ok {
				continue
			}
			switch mapStr(task, "status") {
			case "done":
				done++
			default:
				pending++
			}
		}
		fmt.Fprintf(stdout, "Tasks:      %d pending, %d done\n", pending, done)
	}

	// Memory (observations)
	searchResp, err := apiRequest(cfg, "GET", "/sync/search?q=*&limit=1", nil)
	if err == nil {
		// The search endpoint doesn't return total count easily.
		// Use the graph endpoint instead.
		_ = searchResp
	}
	graphResp, err := apiRequest(cfg, "GET", "/api/graph", nil)
	if err == nil {
		if sessions, ok := graphResp["sessions"].([]any); ok {
			totalObs := 0
			for _, s := range sessions {
				sess, ok := s.(map[string]any)
				if !ok {
					continue
				}
				totalObs += int(mapFloat(sess, "observation_count"))
			}
			fmt.Fprintf(stdout, "Memory:     %d observations\n", totalObs)
		}
	}

	// Costs
	costsResp, err := apiRequest(cfg, "GET", "/api/costs?period=month", nil)
	if err == nil {
		total := jsonFloat(costsResp, "total_cost")
		fmt.Fprintf(stdout, "Costs:      $%.2f this month\n", total)
	}

	fmt.Fprintln(stdout)
	return 0
}

func cmdStatusJSON(cfg *config, stdout, stderr io.Writer) int {
	result := map[string]any{}

	health, err := apiRequest(cfg, "GET", "/health", nil)
	if err != nil {
		result["api"] = map[string]any{"status": "unreachable", "error": err.Error()}
	} else {
		result["api"] = health
	}

	tasksResp, err := apiRequest(cfg, "GET", "/api/tasks?limit=1000", nil)
	if err == nil {
		tasks, _ := tasksResp["tasks"].([]any)
		pending, done := 0, 0
		for _, t := range tasks {
			task, ok := t.(map[string]any)
			if !ok {
				continue
			}
			switch mapStr(task, "status") {
			case "done":
				done++
			default:
				pending++
			}
		}
		result["tasks"] = map[string]int{"pending": pending, "done": done}
	}

	costsResp, err := apiRequest(cfg, "GET", "/api/costs?period=month", nil)
	if err == nil {
		result["costs"] = costsResp
	}

	printJSON(stdout, result)
	return 0
}

// ─── Dream (MORPHEUS) ───────────────────────────────────────────────────────

func cmdDream(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis dream <run|status>")
		return 1
	}

	switch args[0] {
	case "status":
		return dreamStatus(cfg, stdout, stderr)
	case "run":
		return dreamRun(cfg, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown dream subcommand: %s\n", args[0])
		return 1
	}
}

func dreamStatus(cfg *config, stdout, stderr io.Writer) int {
	// Check for MORPHEUS lock file.
	lockFile := os.ExpandEnv("$HOME/.jarvis/morpheus.lock")
	info, err := os.Stat(lockFile)

	result := map[string]any{
		"running": false,
	}

	if err == nil {
		result["running"] = true
		result["started_at"] = info.ModTime().Format(time.RFC3339)
	}

	// Check last consolidation log.
	logFile := os.ExpandEnv("$HOME/.jarvis/morpheus-last.log")
	logInfo, err := os.Stat(logFile)
	if err == nil {
		result["last_run"] = logInfo.ModTime().Format(time.RFC3339)
	}

	if cfg.format == "json" {
		printJSON(stdout, result)
		return 0
	}

	fmt.Fprintf(stdout, "%sMORPHEUS Status%s\n", colorBold, colorReset)
	if result["running"].(bool) {
		fmt.Fprintf(stdout, "Status:   %srunning%s (since %s)\n", colorYellow, colorReset, result["started_at"])
	} else {
		fmt.Fprintf(stdout, "Status:   %sidle%s\n", colorGreen, colorReset)
	}
	if lastRun, ok := result["last_run"].(string); ok {
		fmt.Fprintf(stdout, "Last run: %s\n", lastRun)
	} else {
		fmt.Fprintf(stdout, "Last run: %snever%s\n", colorDim, colorReset)
	}
	return 0
}

func dreamRun(cfg *config, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "%sMORPHEUS%s — triggering memory consolidation...\n", colorBold, colorReset)
	// Stub: in future this calls the consolidator directly or via API.
	fmt.Fprintf(stdout, "%s⚠ Not yet implemented%s — will call MORPHEUS consolidator\n", colorYellow, colorReset)
	return 0
}

// ─── Ticker (SENTINEL) ─────────────────────────────────────────────────────

func cmdTicker(cfg *config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jarvis ticker <run|status>")
		return 1
	}

	switch args[0] {
	case "status":
		return tickerStatus(cfg, stdout, stderr)
	case "run":
		return tickerRun(cfg, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown ticker subcommand: %s\n", args[0])
		return 1
	}
}

func tickerStatus(cfg *config, stdout, stderr io.Writer) int {
	logFile := os.ExpandEnv("$HOME/.jarvis/sentinel-last.log")
	info, err := os.Stat(logFile)

	result := map[string]any{}
	if err == nil {
		result["last_check"] = info.ModTime().Format(time.RFC3339)
	}

	if cfg.format == "json" {
		printJSON(stdout, result)
		return 0
	}

	fmt.Fprintf(stdout, "%sSENTINEL Status%s\n", colorBold, colorReset)
	if lastCheck, ok := result["last_check"].(string); ok {
		fmt.Fprintf(stdout, "Last check: %s\n", lastCheck)
	} else {
		fmt.Fprintf(stdout, "Last check: %snever%s\n", colorDim, colorReset)
	}
	return 0
}

func tickerRun(cfg *config, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "%sSENTINEL%s — running health checks...\n", colorBold, colorReset)
	// Stub: in future this runs all SENTINEL checks.
	fmt.Fprintf(stdout, "%s⚠ Not yet implemented%s — will run SENTINEL checks\n", colorYellow, colorReset)
	return 0
}

// ─── Costs ──────────────────────────────────────────────────────────────────

func cmdCosts(cfg *config, args []string, stdout, stderr io.Writer) int {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "show":
		return costsShow(cfg, stdout, stderr)
	case "by-model":
		return costsByModel(cfg, stdout, stderr)
	case "by-day":
		return costsByDay(cfg, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown costs subcommand: %s\n", sub)
		return 1
	}
}

func costsShow(cfg *config, stdout, stderr io.Writer) int {
	resp, err := apiRequest(cfg, "GET", "/api/costs?period=month", nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	total := jsonFloat(resp, "total_cost")
	period := jsonStr(resp, "period")
	fmt.Fprintf(stdout, "%sCost Summary%s (%s)\n", colorBold, colorReset, period)
	fmt.Fprintf(stdout, "Total: %s$%.2f%s\n", colorCyan, total, colorReset)

	// Budget info if available.
	if budget, ok := resp["budget"].(map[string]any); ok && budget != nil {
		if claude, ok := budget["claude"].(map[string]any); ok {
			spent := mapFloat(claude, "spent")
			limit := mapFloat(claude, "limit")
			pct := mapFloat(claude, "percent_used")
			fmt.Fprintf(stdout, "Claude:  $%.2f / $%.0f (%.1f%%)\n", spent, limit, pct)
		}
		if openai, ok := budget["openai"].(map[string]any); ok {
			spent := mapFloat(openai, "spent")
			limit := mapFloat(openai, "limit")
			pct := mapFloat(openai, "percent_used")
			fmt.Fprintf(stdout, "OpenAI:  $%.2f / $%.0f (%.1f%%)\n", spent, limit, pct)
		}
	}
	return 0
}

func costsByModel(cfg *config, stdout, stderr io.Writer) int {
	resp, err := apiRequest(cfg, "GET", "/api/costs?period=month", nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	models, ok := resp["by_model"].([]any)
	if !ok || len(models) == 0 {
		fmt.Fprintln(stdout, "No cost data by model.")
		return 0
	}

	fmt.Fprintf(stdout, "%sCosts by Model%s\n\n", colorBold, colorReset)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%sMODEL\tCOST\tINPUT TOKENS\tOUTPUT TOKENS%s\n", colorDim, colorReset)
	for _, m := range models {
		model, ok := m.(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(tw, "%s\t$%.4f\t%.0f\t%.0f\n",
			mapStr(model, "model"),
			mapFloat(model, "cost_usd"),
			mapFloat(model, "total_input_tokens"),
			mapFloat(model, "total_output_tokens"),
		)
	}
	tw.Flush()
	return 0
}

func costsByDay(cfg *config, stdout, stderr io.Writer) int {
	resp, err := apiRequest(cfg, "GET", "/api/costs?period=month", nil)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if cfg.format == "json" {
		printJSON(stdout, resp)
		return 0
	}

	days, ok := resp["by_day"].([]any)
	if !ok || len(days) == 0 {
		fmt.Fprintln(stdout, "No cost data by day.")
		return 0
	}

	fmt.Fprintf(stdout, "%sCosts by Day%s\n\n", colorBold, colorReset)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "%sDATE\tCOST\tSESSIONS%s\n", colorDim, colorReset)
	for _, d := range days {
		day, ok := d.(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(tw, "%s\t$%.4f\t%.0f\n",
			mapStr(day, "date"),
			mapFloat(day, "cost_usd"),
			mapFloat(day, "session_count"),
		)
	}
	tw.Flush()
	return 0
}

// ─── Version ────────────────────────────────────────────────────────────────

func cmdVersion(cfg *config, stdout io.Writer) int {
	if cfg.format == "json" {
		printJSON(stdout, map[string]string{"version": version})
		return 0
	}
	fmt.Fprintf(stdout, "jarvis %s\n", version)
	return 0
}

// ─── API Client ─────────────────────────────────────────────────────────────

func apiRequest(cfg *config, method, path string, body any) (map[string]any, error) {
	fullURL := strings.TrimRight(cfg.apiURL, "/") + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cfg.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response (status %d): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode >= 400 {
		errMsg := jsonStr(result, "error")
		if errMsg == "" {
			errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, errMsg)
	}

	return result, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func resolveAPIKey() string {
	// Try 1Password CLI.
	cmd := exec.Command("op", "read", "op://Desarrollo/jarvis-dashboard/api_key")
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func envOrDefault(key, defaultVal string) string {
	v := os.Getenv(key)
	if v != "" {
		return v
	}
	return defaultVal
}

func jsonStr(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func jsonFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func mapStr(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func mapFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch f := v.(type) {
		case float64:
			return f
		case int:
			return float64(f)
		case string:
			n, _ := strconv.ParseFloat(f, 64)
			return n
		}
	}
	return 0
}

func printJSON(w io.Writer, data any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

func checkPort(w io.Writer, name, host string, port int) {
	addr := fmt.Sprintf("%s:%d", host, port)
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintf(w, "%-12s%s✗ port %d not responding%s\n", name+":", colorRed, port, colorReset)
		return
	}
	conn.Close()
	fmt.Fprintf(w, "%-12s%s✓ port %d responding%s\n", name+":", colorGreen, port, colorReset)
}

func statusColorCode(status string) string {
	switch status {
	case "done":
		return colorGreen
	case "in_progress":
		return colorCyan
	case "blocked":
		return colorRed
	case "cancelled":
		return colorDim
	default:
		return colorYellow
	}
}

func priorityColorCode(priority string) string {
	switch priority {
	case "high":
		return colorRed
	case "medium":
		return colorYellow
	case "low":
		return colorGreen
	default:
		return colorReset
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `%sjarvis%s — JARVIS system CLI (v%s)

%sUsage:%s
  jarvis <command> [args] [flags]

%sCommands:%s
  tasks     Manage tasks (create, list, complete, delete)
  jobs      Manage delegation jobs (list, get, create)
  status    Quick system status (health + services)
  dream     MORPHEUS memory consolidation (run, status)
  ticker    SENTINEL health checks (run, status)
  costs     Cost tracking (show, by-model, by-day)
  version   Show version

%sGlobal flags:%s
  --api-url    API URL (default: %s, env: JARVIS_API_URL)
  --api-key    API key (env: JARVIS_API_KEY)
  --format     Output format: text, json (default: text)
`, colorBold, colorReset, version,
		colorBold, colorReset,
		colorBold, colorReset,
		colorBold, colorReset,
		defaultAPIURL)
}
