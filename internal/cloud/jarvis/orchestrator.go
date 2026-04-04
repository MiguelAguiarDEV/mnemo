package jarvis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/notifications"
	"github.com/MiguelAguiarDEV/mnemo/internal/morpheus"
	"github.com/MiguelAguiarDEV/mnemo/internal/prometheus"
	"github.com/MiguelAguiarDEV/mnemo/internal/atlas"
	"github.com/MiguelAguiarDEV/mnemo/internal/sentinel"
	"github.com/MiguelAguiarDEV/mnemo/internal/athena"
)

// ─── Store Interface ───────────────────────────────────────────────────────

// StoreInterface abstracts the persistence layer used by the orchestrator
// for messages, budget tracking, and task management.
type StoreInterface interface {
	AddMessage(conversationID int64, role, content, model string, tokensIn, tokensOut *int, costUSD *float64) (int64, error)
	GetMessages(conversationID int64, limit int) ([]StoreMessage, error)
	BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*StoreBudgetReport, error)
	CreateTask(userID string, title, description, project, priority string) (int64, error)
	ListTasks(userID string, status string, limit int) ([]StoreTask, error)
	UpdateTaskStatus(userID string, taskID int64, status string) error
	UpdateTask(userID string, taskID int64, fields athena.UpdateTaskFields) error
}

// StoreMessage is a lightweight role+content pair from conversation history.
type StoreMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StoreBudgetReport summarises Claude cost usage against the configured budget.
type StoreBudgetReport struct {
	ClaudeUsed   float64
	ClaudeBudget float64
	ClaudePct    float64
}

// StoreTask is a lightweight task representation for JARVIS responses.
type StoreTask struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Project  string `json:"project"`
}

// ─── Configuration ─────────────────────────────────────────────────────────

// OrchestratorConfig holds the dependencies and settings for creating an Orchestrator.
type OrchestratorConfig struct {
	Store            StoreInterface
	Notifier         notifications.Notifier // optional; Discord DM, etc.
	OpenCodeURL      string                 // URL of OpenCode serve; defaults to OPENCODE_SERVE_URL or http://172.18.0.1:4096
	OpenCodePassword string                 // defaults to OPENCODE_SERVER_PASSWORD env var
	BudgetClaude     float64
	BudgetOpenAI     float64
}

// ─── Orchestrator ──────────────────────────────────────────────────────────

// Orchestrator is the JARVIS brain -- it receives chat messages, selects an LLM
// model based on complexity and budget, dispatches tool calls, and tracks costs.
type Orchestrator struct {
	store            StoreInterface
	notifier         notifications.Notifier
	openCodeURL      string
	openCodePassword string
	budgetClaude     float64
	budgetOpenAI     float64
	client           *http.Client
	// Skills architecture components
	registry           *atlas.Registry
	loader             *atlas.Loader
	dispatcher         *athena.Dispatcher
	tracingDispatcher  *athena.TracingDispatcher
	logger             *slog.Logger

	// Async delegation (Tasks 27-29)
	jobTracker *athena.JobTracker
	worker     prometheus.WorkerExecutor

	// Direct Claude API client for ATHENA chat (PROMETHEUS v2)
	claudeClient *prometheus.ClaudeClient
}

// New creates an Orchestrator with the given config, initialising the Claude
// client, async delegation, and the full ATHENA tool registry.
func New(cfg OrchestratorConfig) *Orchestrator {
	url := cfg.OpenCodeURL
	if url == "" {
		url = os.Getenv("OPENCODE_SERVE_URL")
	}
	if url == "" {
		url = "http://172.18.0.1:4096" // Docker host gateway to OpenCode serve
	}

	password := cfg.OpenCodePassword
	if password == "" {
		password = os.Getenv("OPENCODE_SERVER_PASSWORD")
	}

	claudeBudget := cfg.BudgetClaude
	openAIBudget := cfg.BudgetOpenAI
	if claudeBudget == 0 || openAIBudget == 0 {
		cb, ob := BudgetFromEnv()
		if claudeBudget == 0 {
			claudeBudget = cb
		}
		if openAIBudget == 0 {
			openAIBudget = ob
		}
	}

	logger := slog.Default()

	o := &Orchestrator{
		store:            cfg.Store,
		notifier:         cfg.Notifier,
		openCodeURL:      strings.TrimRight(url, "/"),
		openCodePassword: password,
		budgetClaude:     claudeBudget,
		budgetOpenAI:     openAIBudget,
		client:           &http.Client{Timeout: 15 * time.Minute},
		logger:           logger,
	}

	// Initialize direct Claude API client (PROMETHEUS v2) — must be before initAsyncDelegation
	o.claudeClient = prometheus.NewClaudeClient(
		prometheus.WithLogger(logger),
	)

	// Initialize async delegation (Tasks 27-29)
	o.initAsyncDelegation()

	// Initialize skills architecture
	o.initSkillsV2(logger)

	return o
}

// initAsyncDelegation sets up the PROMETHEUS worker, job tracker, and HERMES callback.
func (o *Orchestrator) initAsyncDelegation() {
	// Create OpenCode backend for the worker.
	// PROMETHEUS v2 Phase C: use NativeWorker (our own tool executor)
	// instead of OpenCode. Falls back to OpenCode if PROMETHEUS_USE_OPENCODE=true.
	if os.Getenv("PROMETHEUS_USE_OPENCODE") == "true" {
		backend := prometheus.NewOpenCodeBackend(
			o.openCodeURL,
			"opencode",
			o.openCodePassword,
			10*time.Minute,
		)
		o.worker = prometheus.NewWorker(backend, o.logger)
		o.logger.Info("PROMETHEUS: using OpenCode backend for workers")
	} else {
		o.worker = prometheus.NewNativeWorker(o.claudeClient, o.logger)
		o.logger.Info("PROMETHEUS: using native tool executor for workers (no OpenCode dependency)")
	}

	// HERMES callback: send Discord DM when a job completes.
	onComplete := func(job *athena.Job) {
		if o.notifier == nil {
			o.logger.Info("job completed but no notifier configured",
				"job_id", job.ID, "status", job.Status)
			return
		}

		var msg string
		if job.Status == athena.JobStatusSuccess && job.Result != nil {
			duration := "unknown"
			if job.StartedAt != nil && job.CompletedAt != nil {
				duration = job.CompletedAt.Sub(*job.StartedAt).Round(time.Second).String()
			}
			output := job.Result.Output
			if len(output) > 500 {
				output = output[:500] + "\n... (truncado)"
			}
			msg = fmt.Sprintf("Job #%s completado\nTask: %s\nProject: %s\nDuracion: %s\n\nResultado:\n%s",
				job.ID, job.Task, job.Project, duration, output)
		} else {
			msg = fmt.Sprintf("Job #%s FALLO\nTask: %s\nError: %s",
				job.ID, job.Task, job.Error)
		}

		nType := notifications.TaskComplete
		title := "JARVIS Job completado"
		if job.Status != athena.JobStatusSuccess {
			nType = notifications.Alert
			title = "JARVIS Job fallido"
		}

		if err := o.notifier.Send(notifications.Notification{
			Type:    nType,
			Title:   title,
			Message: msg,
		}); err != nil {
			o.logger.Error("failed to send job notification", "job_id", job.ID, "err", err)
		}
	}

	o.jobTracker = athena.NewJobTracker(o.logger, onComplete)
	o.logger.Info("async delegation initialized")
}

// JobTracker returns the orchestrator's job tracker for API handlers.
func (o *Orchestrator) JobTracker() *athena.JobTracker {
	return o.jobTracker
}

// Worker returns the orchestrator's PROMETHEUS worker for API handlers.
func (o *Orchestrator) Worker() prometheus.WorkerExecutor {
	return o.worker
}

// Notifier returns the orchestrator's notifier (for Discord DMs, etc.).
func (o *Orchestrator) Notifier() notifications.Notifier {
	return o.notifier
}

// initSkillsV2 initializes the registry, loader, dispatcher, and registers all tools.
func (o *Orchestrator) initSkillsV2(logger *slog.Logger) {
	slog.Info("initializing skills architecture")

	// Build registry from config dir skills
	o.registry = atlas.NewRegistry()
	catalogPaths := []atlas.CatalogPath{
		{Path: filepath.Join(configDir, "skills"), Tier: "ops", Project: "jarvis"},
	}

	// Add additional catalog paths from environment
	if extraPaths := os.Getenv("JARVIS_SKILL_CATALOGS"); extraPaths != "" {
		for _, p := range strings.Split(extraPaths, ":") {
			p = strings.TrimSpace(p)
			if p != "" {
				catalogPaths = append(catalogPaths, atlas.CatalogPath{
					Path: p, Tier: "global", Project: "kb",
				})
			}
		}
	}

	if err := o.registry.Build(catalogPaths); err != nil {
		slog.Error("failed to build skill registry", "err", err)
	}

	o.loader = atlas.NewLoader(o.registry)
	o.dispatcher = athena.NewDispatcher(logger)

	// Register all 9 MVP tools (+ update_task)
	o.dispatcher.Register(athena.NewLoadSkillTool(o.loader))

	// Task tools — wrap the store to implement athena.TaskStore
	// Single-user system: use the primary user's UUID
	jarvisUserID := "2b8c5ccb-f82e-49f2-b8d7-9b9e7f4a4e03"
	taskStore := &orchestratorTaskStore{store: o.store, userID: jarvisUserID}
	o.dispatcher.Register(athena.NewCreateTaskTool(taskStore, jarvisUserID))
	o.dispatcher.Register(athena.NewListTasksTool(taskStore, jarvisUserID))
	o.dispatcher.Register(athena.NewCompleteTaskTool(taskStore, jarvisUserID))
	o.dispatcher.Register(athena.NewUpdateTaskTool(taskStore, jarvisUserID))

	// Delegate tool (async via job tracker)
	o.dispatcher.Register(athena.NewAsyncDelegateTool(&orchestratorAsyncDelegateHandler{o: o}))

	// Job listing tools
	o.dispatcher.Register(athena.NewListJobsTool(o.jobTracker))
	o.dispatcher.Register(athena.NewGetJobTool(o.jobTracker))

	// Notify tool
	o.dispatcher.Register(athena.NewNotifyTool(&orchestratorNotifier{notifier: o.notifier}))

	// Memory tools
	mnemoBin := os.Getenv("MNEMO_BIN")
	if mnemoBin == "" {
		mnemoBin = "mnemo"
	}
	o.dispatcher.Register(athena.NewSearchMemoryTool(mnemoBin, nil))
	o.dispatcher.Register(athena.NewSaveMemoryTool(mnemoBin, nil))

	// Filesystem tools (read_file, write_file, edit_file)
	allowedDirs := []string{os.Getenv("HOME") + "/projects", "/tmp"}
	if extraDirs := os.Getenv("JARVIS_ALLOWED_DIRS"); extraDirs != "" {
		for _, d := range strings.Split(extraDirs, ":") {
			d = strings.TrimSpace(d)
			if d != "" {
				allowedDirs = append(allowedDirs, d)
			}
		}
	}
	pathValidator := athena.NewPathValidator(allowedDirs)
	o.dispatcher.Register(athena.NewReadFileTool(pathValidator))
	o.dispatcher.Register(athena.NewWriteFileTool(pathValidator))
	o.dispatcher.Register(athena.NewEditFileTool(pathValidator))

	// Shell tools (bash, grep, glob)
	o.dispatcher.Register(athena.NewBashTool(athena.BashToolConfig{}))
	o.dispatcher.Register(athena.NewGrepTool())
	o.dispatcher.Register(athena.NewGlobTool())

	// Web tools (fetch_url, web_search)
	o.dispatcher.Register(athena.NewFetchURLTool(athena.FetchURLConfig{
		AllowPrivateIPs: os.Getenv("JARVIS_ALLOW_PRIVATE_IPS") == "true",
	}))
	o.dispatcher.Register(athena.NewWebSearchTool())

	// Initialize tracing dispatcher — wraps the tool dispatcher with fire-and-forget traces.
	traceURL := os.Getenv("JARVIS_TRACE_URL")
	if traceURL == "" {
		traceURL = "http://127.0.0.1:8080/traces/tool-call"
	}
	traceToken := os.Getenv("MNEMO_CLOUD_API_KEY")
	if traceToken == "" {
		traceToken = os.Getenv("MNEMO_API_KEY")
	}

	o.tracingDispatcher = athena.NewTracingDispatcher(o.dispatcher, athena.TracingConfig{
		TraceURL:  traceURL,
		AuthToken: traceToken,
		Agent:     "jarvis",
		Project:   "jarvis-dashboard",
	})

	slog.Info("SKILLS_V2 initialized",
		"registry_skills", len(o.registry.AlwaysSkills()),
		"tools", 18,
		"tracing", traceURL != "",
	)
}

// ─── Memory Consolidation (autoDream) ─────────────────────────────────────

// StartDream launches the background memory consolidation loop.
// It runs in a goroutine and checks every hour if consolidation is needed.
// Call this from the cloud server startup after creating the orchestrator.
func (o *Orchestrator) StartDream(ctx context.Context) {
	mnemoBin := os.Getenv("MNEMO_BIN")
	if mnemoBin == "" {
		mnemoBin = "mnemo"
	}

	lockPath := filepath.Join(os.TempDir(), "mnemo-consolidate.lock")
	if d := os.Getenv("JARVIS_DREAM_LOCK"); d != "" {
		lockPath = d
	}

	consolidator := morpheus.New(
		morpheus.WithMnemoBin(mnemoBin),
		morpheus.WithLockPath(lockPath),
		morpheus.WithLogger(o.logger),
	)

	go consolidator.RunBackground(ctx)
	o.logger.Info("dream: background memory consolidation started",
		"lock_path", lockPath,
		"mnemo_bin", mnemoBin,
	)
}

// ─── Proactive Ticker ────────────────────────────────────────────────────

// StartTicker launches the background health-check sentinel.
// It runs periodic checks (server health, disk space, DB, OpenCode, memory
// consolidation) and sends Discord notifications when something needs attention.
// Call this from the cloud server startup alongside StartDream.
func (o *Orchestrator) StartTicker(ctx context.Context) {
	// Build the notifier adapter: sentinel.NotifyFunc -> notifications.Notifier.
	var notifyFn sentinel.NotifyFunc
	if o.notifier != nil {
		notifyFn = func(title, message string) error {
			return o.notifier.Send(notifications.Notification{
				Type:    notifications.Alert,
				Title:   title,
				Message: message,
			})
		}
	}

	// Build the dream consolidator for the memory check.
	mnemoBin := os.Getenv("MNEMO_BIN")
	if mnemoBin == "" {
		mnemoBin = "mnemo"
	}
	lockPath := filepath.Join(os.TempDir(), "mnemo-consolidate.lock")
	if d := os.Getenv("JARVIS_DREAM_LOCK"); d != "" {
		lockPath = d
	}
	consolidator := morpheus.New(
		morpheus.WithMnemoBin(mnemoBin),
		morpheus.WithLockPath(lockPath),
		morpheus.WithLogger(o.logger),
	)

	// Resolve the health URL from the server's own port (default 8080).
	healthURL := "http://127.0.0.1:8080/health"

	checks := sentinel.DefaultChecks(
		healthURL,
		o.openCodeURL,
		nil, // DB pinger: nil for now (CloudStore doesn't expose *sql.DB directly)
		consolidator.NeedsConsolidation,
		consolidator.Run,
	)

	t := sentinel.New(
		sentinel.WithLogger(o.logger),
		sentinel.WithNotifier(notifyFn),
		sentinel.WithChecks(checks),
	)

	go t.RunBackground(ctx)
	o.logger.Info("ticker: proactive health checks started",
		"checks", len(checks),
		"interval", sentinel.DefaultInterval,
	)
}

// ─── Tools Adapters ───────────────────────────────────────────────────────

// orchestratorTaskStore wraps StoreInterface to satisfy athena.TaskStore.
type orchestratorTaskStore struct {
	store  StoreInterface
	userID string // set per-request
}

func (s *orchestratorTaskStore) CreateTask(userID, title, description, project, priority string) (int64, error) {
	uid := userID
	if uid == "" {
		uid = s.userID
	}
	return s.store.CreateTask(uid, title, description, project, priority)
}

func (s *orchestratorTaskStore) ListTasks(userID, status string, limit int) ([]athena.TaskEntry, error) {
	uid := userID
	if uid == "" {
		uid = s.userID
	}
	tasks, err := s.store.ListTasks(uid, status, limit)
	if err != nil {
		return nil, err
	}
	result := make([]athena.TaskEntry, len(tasks))
	for i, t := range tasks {
		result[i] = athena.TaskEntry{
			ID:       t.ID,
			Title:    t.Title,
			Status:   t.Status,
			Priority: t.Priority,
			Project:  t.Project,
		}
	}
	return result, nil
}

func (s *orchestratorTaskStore) UpdateTaskStatus(userID string, taskID int64, status string) error {
	uid := userID
	if uid == "" {
		uid = s.userID
	}
	return s.store.UpdateTaskStatus(uid, taskID, status)
}

func (s *orchestratorTaskStore) UpdateTask(userID string, taskID int64, fields athena.UpdateTaskFields) error {
	uid := userID
	if uid == "" {
		uid = s.userID
	}
	return s.store.UpdateTask(uid, taskID, fields)
}

// ─── JobService adapter (for cloudserver API) ────────────────────────────────

// JobServiceAdapter wraps the orchestrator to implement cloudserver.JobService.
type JobServiceAdapter struct {
	o *Orchestrator
}

// NewJobServiceAdapter creates a JobServiceAdapter from an Orchestrator.
func NewJobServiceAdapter(o *Orchestrator) *JobServiceAdapter {
	return &JobServiceAdapter{o: o}
}

// CreateJob creates a new async delegation job and returns its ID.
func (a *JobServiceAdapter) CreateJob(task, project, workingDir, ctx string) string {
	job := a.o.jobTracker.Create(task, project)

	req := prometheus.DelegateRequest{
		Task:       task,
		Project:    project,
		Context:    ctx,
		WorkingDir: workingDir,
	}

	a.o.jobTracker.RunAsync(job, a.o.worker, req)
	return job.ID
}

// GetJob returns a job as a map (JSON-serializable).
func (a *JobServiceAdapter) GetJob(id string) (map[string]any, bool) {
	job, ok := a.o.jobTracker.Get(id)
	if !ok {
		return nil, false
	}
	return jobToMap(job), true
}

// ListJobs returns all jobs as maps (JSON-serializable).
func (a *JobServiceAdapter) ListJobs() []map[string]any {
	jobs := a.o.jobTracker.List()
	result := make([]map[string]any, len(jobs))
	for i, j := range jobs {
		result[i] = jobToMap(j)
	}
	return result
}

func jobToMap(j *athena.Job) map[string]any {
	m := map[string]any{
		"id":         j.ID,
		"task":       j.Task,
		"project":    j.Project,
		"status":     j.Status,
		"created_at": j.CreatedAt,
	}
	if j.StartedAt != nil {
		m["started_at"] = *j.StartedAt
	}
	if j.CompletedAt != nil {
		m["completed_at"] = *j.CompletedAt
	}
	if j.Error != "" {
		m["error"] = j.Error
	}
	if j.Result != nil {
		m["result"] = map[string]any{
			"status":     j.Result.Status,
			"output":     j.Result.Output,
			"duration":   j.Result.Duration.String(),
			"tokens_in":  j.Result.TokensUsed.InputTokens,
			"tokens_out": j.Result.TokensUsed.OutputTokens,
		}
	}
	return m
}

// orchestratorAsyncDelegateHandler implements athena.AsyncDelegateHandler using the job tracker.
type orchestratorAsyncDelegateHandler struct {
	o *Orchestrator
}

func (h *orchestratorAsyncDelegateHandler) DelegateAsync(ctx context.Context, task, project, workingDir, skillContext string) (string, error) {
	slog.Info("async delegate: creating job", "task", task, "project", project)

	job := h.o.jobTracker.Create(task, project)

	req := prometheus.DelegateRequest{
		Task:       task,
		Project:    project,
		Context:    skillContext,
		WorkingDir: workingDir,
	}

	h.o.jobTracker.RunAsync(job, h.o.worker, req)

	return job.ID, nil
}

// orchestratorNotifier wraps notifications.Notifier to satisfy athena.Notifier.
type orchestratorNotifier struct {
	notifier notifications.Notifier
}

func (n *orchestratorNotifier) Send(message, channel, level string) error {
	if n.notifier == nil {
		slog.Info("notify: no notifier configured, logging only", "message", message, "level", level)
		return nil
	}

	nType := notifications.Info
	switch level {
	case "warn":
		nType = notifications.Alert
	case "error":
		nType = notifications.Alert
	}

	return n.notifier.Send(notifications.Notification{
		Type:    nType,
		Title:   "JARVIS Notification",
		Message: message,
	})
}

// ─── System Prompt ─────────────────────────────────────────────────────────

// configDir is the path to JARVIS config files. Defaults to JARVIS_CONFIG_DIR env var.
var configDir = getConfigDir()

func getConfigDir() string {
	d := os.Getenv("JARVIS_CONFIG_DIR")
	if d != "" {
		return d
	}
	return "/config" // default for Docker container
}

// loadSystemPrompt reads system-prompt.md + all active skills from config dir.
// Called on EVERY chat message = hot reload without restart.
func loadSystemPrompt() string {
	var b strings.Builder

	// Read base prompt
	base, err := os.ReadFile(filepath.Join(configDir, "system-prompt.md"))
	if err != nil {
		slog.Warn("failed to read system-prompt.md, using fallback", "err", err)
		b.WriteString(fallbackPrompt)
	} else {
		b.Write(base)
	}

	// Read all skills
	skillsDir := filepath.Join(configDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				content, err := os.ReadFile(filepath.Join(skillsDir, entry.Name()))
				if err == nil {
					b.WriteString("\n\n")
					b.Write(content)
				}
			}
		}
	}

	return b.String()
}

// fallbackPrompt is used when config files are missing.
const fallbackPrompt = `You are JARVIS, a personal AI assistant. Be direct and concise.`

// ─── Chat ──────────────────────────────────────────────────────────────────

func (o *Orchestrator) Chat(userID string, conversationID int64, message string, onToken func(string)) (string, error) {
	// 1. Persist the user message.
	_, err := o.store.AddMessage(conversationID, "user", message, "", nil, nil, nil)
	if err != nil {
		return "", fmt.Errorf("jarvis: save user message: %w", err)
	}

	// 2. Load conversation history.
	history, err := o.store.GetMessages(conversationID, 50)
	if err != nil {
		slog.Warn("failed to load history", "err", err)
		history = nil
	}

	// 3. Budget check and model selection.
	budgetPct := 0.0
	if o.store != nil {
		report, err := o.store.BudgetUsage(userID, time.Now(), o.budgetClaude, o.budgetOpenAI)
		if err != nil {
			slog.Warn("budget check failed", "err", err)
		} else if report != nil {
			budgetPct = report.ClaudePct / 100.0
		}
	}

	complexity := ClassifyComplexity(message)
	model := SelectModel(complexity, budgetPct)

	return o.chatV2(userID, conversationID, message, history, model, onToken)
}

// ─── SKILLS_V2 Chat Path (PROMETHEUS v2: Direct Claude API) ──────────────

// chatV2 implements the ATHENA chat path with native tool dispatch via direct Claude API.
// Uses the Anthropic Messages API directly, bypassing OpenCode serve for the chat flow.
// Workers (delegation) still use OpenCode via the prometheus.Worker.
func (o *Orchestrator) chatV2(userID string, conversationID int64, message string, history []StoreMessage, model ModelConfig, onToken func(string)) (string, error) {
	// 1. Extract /skill-name overrides from message
	cleanedMessage, preloadSkills := extractSkillOverrides(message, o.registry)
	if len(preloadSkills) > 0 {
		slog.Info("skill overrides detected", "skills", preloadSkills)
	}

	// 2. Build system prompt with always-skills + preloaded skills + compact index
	systemPrompt := o.buildSystemPromptV2(preloadSkills)

	// 3. Convert ATHENA tool definitions to Claude API format
	athenaDefs := o.dispatcher.ToolDefinitions()
	toolDefs := make([]prometheus.ChatToolDef, len(athenaDefs))
	for i, d := range athenaDefs {
		toolDefs[i] = prometheus.NewChatToolDef(d.Name, d.Description, d.Parameters)
	}

	// 4. Build conversation messages (history + current)
	messages := o.buildChatMessages(history, cleanedMessage)

	// Bind tracing to this conversation.
	o.tracingDispatcher.SetSessionID(fmt.Sprintf("conv-%d", conversationID))

	slog.Debug("PROMETHEUS_V2 chat built",
		"system_len", len(systemPrompt),
		"messages", len(messages),
		"tools", len(toolDefs),
		"model", model.Model,
	)

	// 5. Tool-call loop (max 5 iterations for native tool use)
	const maxIterations = 5
	var totalInputTokens, totalOutputTokens int

	for i := 0; i < maxIterations; i++ {
		resp, err := o.claudeClient.Send(context.Background(), prometheus.ChatRequest{
			SystemPrompt: systemPrompt,
			Messages:     messages,
			// NOTE: Tools are NOT sent to the bridge. The bridge only handles text.
			// Tool definitions are in the system prompt; the orchestrator parses
			// tool_use JSON from Claude's text response and dispatches locally.
			Model:     model.Model,
			MaxTokens: model.MaxTokens,
			MaxTurns:  20,
		})
		if err != nil {
			return "", fmt.Errorf("jarvis: claude API (iteration %d): %w", i, err)
		}

		totalInputTokens += resp.Usage.InputTokens
		totalOutputTokens += resp.Usage.OutputTokens

		// Stream text blocks to the client
		textContent := resp.TextContent()
		if textContent != "" {
			onToken(textContent)
		}

		// Send status event with token info
		statusJSON, _ := json.Marshal(map[string]any{
			"status":     "complete",
			"tokens_in":  resp.Usage.InputTokens,
			"tokens_out": resp.Usage.OutputTokens,
			"model":      resp.Model,
		})
		onToken("__STATUS__" + string(statusJSON))

		// Check for native tool_use blocks first (if bridge supports it)
		hasNativeTools := resp.StopReason == "tool_use"
		toolBlocks := resp.ToolUseBlocks()

		// Check for text-based [TOOL:name] markers in Claude's response
		textToolRequests := parseToolRequests(textContent)

		if !hasNativeTools && len(textToolRequests) == 0 {
			// No tools needed — return response as-is
			modelName := resp.Model
			if modelName == "" {
				modelName = model.Model
			}
			cost := (float64(totalInputTokens) / 1000.0 * model.CostPer1KIn) +
				(float64(totalOutputTokens) / 1000.0 * model.CostPer1KOut)
			o.store.AddMessage(conversationID, "assistant", textContent, modelName, &totalInputTokens, &totalOutputTokens, &cost)

			return textContent, nil
		}

		// ── Path A: Native tool_use blocks (bridge supports tools) ──────
		if hasNativeTools && len(toolBlocks) > 0 {
			slog.Info("native tool_use detected",
				"count", len(toolBlocks),
				"iteration", i+1,
			)

			// Add assistant's response (with tool_use blocks) to conversation
			messages = append(messages, prometheus.NewBlocksMessage("assistant", resp.Content))

			// Execute each tool and build tool_result messages
			var toolResultBlocks []prometheus.ContentBlock
			for _, tb := range toolBlocks {
				slog.Info("dispatching tool", "name", tb.Name, "id", tb.ID)

				toolStartJSON, _ := json.Marshal(map[string]any{
					"event": "tool_start",
					"tool":  tb.Name,
				})
				onToken("__STATUS__" + string(toolStartJSON))

				toolStartTime := time.Now()
				result, dispErr := o.tracingDispatcher.Dispatch(context.Background(), tb.Name, tb.Input)
				toolDuration := time.Since(toolStartTime).Milliseconds()

				isError := false
				content := ""
				if dispErr != nil {
					isError = true
					content = "tool error: " + dispErr.Error()
					slog.Error("tool dispatch error", "tool", tb.Name, "err", dispErr)
				} else if result.IsError {
					isError = true
					content = result.Content
					slog.Warn("tool returned error", "tool", tb.Name, "content", content)
				} else {
					content = truncateResult(result.Content)
					slog.Debug("tool result", "tool", tb.Name, "result_len", len(content))
				}

				toolDoneJSON, _ := json.Marshal(map[string]any{
					"event":       "tool_done",
					"tool":        tb.Name,
					"duration_ms": toolDuration,
					"is_error":    isError,
				})
				onToken("__STATUS__" + string(toolDoneJSON))

				toolResultBlocks = append(toolResultBlocks, prometheus.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tb.ID,
					Content:   content,
					IsError:   isError,
				})
			}

			toolResultContent, _ := json.Marshal(toolResultBlocks)
			messages = append(messages, prometheus.ChatMessage{
				Role:    "user",
				Content: toolResultContent,
			})
		}

		// ── Path B: Text-based [TOOL:name] markers ──────────────────────
		if len(textToolRequests) > 0 {
			slog.Info("text-based tool requests detected",
				"count", len(textToolRequests),
				"iteration", i+1,
			)

			// Add assistant's text response to conversation
			messages = append(messages, prometheus.NewTextMessage("assistant", textContent))

			// Execute each tool and collect results
			var resultParts []string
			for _, tr := range textToolRequests {
				slog.Info("dispatching text tool", "name", tr.Name)

				toolStartJSON, _ := json.Marshal(map[string]any{
					"event": "tool_start",
					"tool":  tr.Name,
				})
				onToken("__STATUS__" + string(toolStartJSON))

				toolStartTime := time.Now()
				result, dispErr := o.tracingDispatcher.Dispatch(context.Background(), tr.Name, tr.Params)
				toolDuration := time.Since(toolStartTime).Milliseconds()

				isError := false
				content := ""
				if dispErr != nil {
					isError = true
					content = "ERROR: " + dispErr.Error()
					slog.Error("text tool dispatch error", "tool", tr.Name, "err", dispErr)
				} else if result.IsError {
					isError = true
					content = "ERROR: " + result.Content
					slog.Warn("text tool returned error", "tool", tr.Name, "content", content)
				} else {
					content = truncateResult(result.Content)
					slog.Info("text tool result", "tool", tr.Name, "result_len", len(content))
				}

				toolDoneJSON, _ := json.Marshal(map[string]any{
					"event":       "tool_done",
					"tool":        tr.Name,
					"duration_ms": toolDuration,
					"is_error":    isError,
				})
				onToken("__STATUS__" + string(toolDoneJSON))

				resultParts = append(resultParts, fmt.Sprintf("[RESULT:%s] %s", tr.Name, content))
			}

			// Send tool results back to Claude as a user message
			toolResultsText := "Tool results:\n" + strings.Join(resultParts, "\n\n")
			messages = append(messages, prometheus.NewTextMessage("user", toolResultsText))
		}

		// If this is the last iteration, log a warning and return what we have
		if i == maxIterations-1 {
			slog.Warn("tool-call loop reached max iterations", "max", maxIterations)
			modelName := resp.Model
			cost := (float64(totalInputTokens) / 1000.0 * model.CostPer1KIn) +
				(float64(totalOutputTokens) / 1000.0 * model.CostPer1KOut)
			o.store.AddMessage(conversationID, "assistant", textContent, modelName, &totalInputTokens, &totalOutputTokens, &cost)
			return textContent, nil
		}
	}

	return "", fmt.Errorf("jarvis: tool-call loop exhausted without final response")
}

// ─── Text-Based Tool Parsing ─────────────────────────────────────────────

// ToolRequest represents a tool call parsed from Claude's text response.
type ToolRequest struct {
	Name   string
	Params json.RawMessage
}

// toolRequestRe matches [TOOL:tool_name] followed by a JSON object.
// The JSON object may span multiple lines.
var toolRequestRe = regexp.MustCompile(`(?m)^\[TOOL:([a-z_]+)\]\s*(\{.*)`)

// parseToolRequests extracts [TOOL:name] {"params"} patterns from text.
// It handles multi-line JSON by finding the balanced closing brace.
func parseToolRequests(text string) []ToolRequest {
	var requests []ToolRequest

	matches := toolRequestRe.FindAllStringSubmatchIndex(text, -1)
	for _, loc := range matches {
		name := text[loc[2]:loc[3]]
		jsonStart := loc[4] // start of the JSON portion

		// Find balanced closing brace for multi-line JSON
		jsonStr := extractJSON(text[jsonStart:])
		if jsonStr == "" {
			slog.Warn("parseToolRequests: could not extract JSON", "tool", name)
			continue
		}

		// Validate it's actually JSON
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
			slog.Warn("parseToolRequests: invalid JSON", "tool", name, "err", err)
			continue
		}

		requests = append(requests, ToolRequest{
			Name:   name,
			Params: raw,
		})
	}

	return requests
}

// extractJSON finds the balanced JSON object starting from the first '{'.
func extractJSON(s string) string {
	if len(s) == 0 || s[0] != '{' {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}

	return "" // unbalanced
}

// maxToolResultSize is the maximum size of a tool result before truncation.
const maxToolResultSize = 10 * 1024 // 10 KB

// truncateResult truncates a tool result to maxToolResultSize.
func truncateResult(s string) string {
	if len(s) <= maxToolResultSize {
		return s
	}
	return s[:maxToolResultSize] + "\n... (truncated)"
}

// ─── Quick / Long Chat Modes ──────────────────────────────────────────────

// QuickResponse holds a quick-mode response with metadata about whether background work is needed.
type QuickResponse struct {
	Text      string // response text from the single turn
	NeedsMore bool   // true if Claude's stop_reason != "end_turn" (i.e., it wanted to use tools / continue)
}

// ChatQuick sends a message with maxTurns=1 for fast responses.
// It returns immediately after the first Claude turn. If NeedsMore is true,
// the caller should launch ChatLong in a goroutine for the full autonomous run.
func (o *Orchestrator) ChatQuick(userID string, conversationID int64, message string) (QuickResponse, error) {
	// 1. Persist the user message.
	_, err := o.store.AddMessage(conversationID, "user", message, "", nil, nil, nil)
	if err != nil {
		return QuickResponse{}, fmt.Errorf("jarvis: save user message: %w", err)
	}

	// 2. Load conversation history.
	history, err := o.store.GetMessages(conversationID, 50)
	if err != nil {
		slog.Warn("failed to load history", "err", err)
		history = nil
	}

	// 3. Budget check and model selection.
	budgetPct := 0.0
	if o.store != nil {
		report, bErr := o.store.BudgetUsage(userID, time.Now(), o.budgetClaude, o.budgetOpenAI)
		if bErr != nil {
			slog.Warn("budget check failed", "err", bErr)
		} else if report != nil {
			budgetPct = report.ClaudePct / 100.0
		}
	}

	complexity := ClassifyComplexity(message)
	model := SelectModel(complexity, budgetPct)

	// 4. Build system prompt and messages (same as chatV2).
	cleanedMessage, preloadSkills := extractSkillOverrides(message, o.registry)
	systemPrompt := o.buildSystemPromptV2(preloadSkills)
	messages := o.buildChatMessages(history, cleanedMessage)

	// 5. Quick call with MaxTurns=5 — enough for Claude to think and respond.
	// claude-agent-sdk query() counts the initial message as a turn, so 1 is never enough.
	resp, err := o.claudeClient.Send(context.Background(), prometheus.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Model:        model.Model,
		MaxTokens:    model.MaxTokens,
		MaxTurns:     20,
	})
	if err != nil {
		return QuickResponse{}, fmt.Errorf("jarvis: quick chat: %w", err)
	}

	text := resp.TextContent()

	// Persist the response.
	cost := (float64(resp.Usage.InputTokens) / 1000.0 * model.CostPer1KIn) +
		(float64(resp.Usage.OutputTokens) / 1000.0 * model.CostPer1KOut)
	inTok := resp.Usage.InputTokens
	outTok := resp.Usage.OutputTokens
	o.store.AddMessage(conversationID, "assistant", text, resp.Model, &inTok, &outTok, &cost)

	needsMore := resp.StopReason != "end_turn"

	slog.Info("ChatQuick completed",
		"model", resp.Model,
		"stop_reason", resp.StopReason,
		"needs_more", needsMore,
		"text_len", len(text),
	)

	return QuickResponse{Text: text, NeedsMore: needsMore}, nil
}

// ChatLong sends a message with maxTurns=50 for autonomous work.
// Designed to run in a goroutine — uses the provided context for cancellation.
// The onToken callback is called with text chunks as they arrive.
func (o *Orchestrator) ChatLong(ctx context.Context, userID string, conversationID int64, message string, onToken func(string)) (string, error) {
	// NOTE: ChatQuick already persisted the user message and one assistant response.
	// ChatLong continues the conversation from where Quick left off.

	// 1. Load conversation history (includes the quick response).
	history, err := o.store.GetMessages(conversationID, 50)
	if err != nil {
		slog.Warn("failed to load history for long chat", "err", err)
		history = nil
	}

	// 2. Budget check and model selection.
	budgetPct := 0.0
	if o.store != nil {
		report, bErr := o.store.BudgetUsage(userID, time.Now(), o.budgetClaude, o.budgetOpenAI)
		if bErr != nil {
			slog.Warn("budget check failed", "err", bErr)
		} else if report != nil {
			budgetPct = report.ClaudePct / 100.0
		}
	}

	complexity := ClassifyComplexity(message)
	model := SelectModel(complexity, budgetPct)

	// 3. Build system prompt and messages.
	cleanedMessage, preloadSkills := extractSkillOverrides(message, o.registry)
	systemPrompt := o.buildSystemPromptV2(preloadSkills)

	// Use a continuation prompt so Claude knows it should now fully complete the task.
	continuationMsg := "Continue with the task. You now have full autonomy — use all tools needed to complete: " + cleanedMessage
	messages := o.buildChatMessages(history, continuationMsg)

	// 4. Convert tool definitions for native tool dispatch.
	athenaDefs := o.dispatcher.ToolDefinitions()
	toolDefs := make([]prometheus.ChatToolDef, len(athenaDefs))
	for i, d := range athenaDefs {
		toolDefs[i] = prometheus.NewChatToolDef(d.Name, d.Description, d.Parameters)
	}

	// Bind tracing to this conversation.
	o.tracingDispatcher.SetSessionID(fmt.Sprintf("conv-%d-long", conversationID))

	slog.Info("ChatLong starting",
		"model", model.Model,
		"messages", len(messages),
		"tools", len(toolDefs),
	)

	// 5. Extended tool-call loop (50 iterations for autonomous work).
	const maxIterations = 50
	var totalInputTokens, totalOutputTokens int
	var lastText string

	for i := 0; i < maxIterations; i++ {
		// Check context cancellation.
		if ctx.Err() != nil {
			return lastText, fmt.Errorf("jarvis: long chat cancelled: %w", ctx.Err())
		}

		resp, err := o.claudeClient.Send(ctx, prometheus.ChatRequest{
			SystemPrompt: systemPrompt,
			Messages:     messages,
			Model:        model.Model,
			MaxTokens:    model.MaxTokens,
			MaxTurns:     50,
		})
		if err != nil {
			return lastText, fmt.Errorf("jarvis: claude API (long iteration %d): %w", i, err)
		}

		totalInputTokens += resp.Usage.InputTokens
		totalOutputTokens += resp.Usage.OutputTokens

		textContent := resp.TextContent()
		if textContent != "" {
			lastText = textContent
			if onToken != nil {
				onToken(textContent)
			}
		}

		// If done, persist and return.
		if resp.StopReason != "tool_use" {
			modelName := resp.Model
			if modelName == "" {
				modelName = model.Model
			}
			cost := (float64(totalInputTokens) / 1000.0 * model.CostPer1KIn) +
				(float64(totalOutputTokens) / 1000.0 * model.CostPer1KOut)
			o.store.AddMessage(conversationID, "assistant", lastText, modelName, &totalInputTokens, &totalOutputTokens, &cost)

			slog.Info("ChatLong completed",
				"iterations", i+1,
				"total_input_tokens", totalInputTokens,
				"total_output_tokens", totalOutputTokens,
			)
			return lastText, nil
		}

		// Tool dispatch (same as chatV2).
		toolBlocks := resp.ToolUseBlocks()
		slog.Info("ChatLong tool_use",
			"count", len(toolBlocks),
			"iteration", i+1,
		)

		messages = append(messages, prometheus.NewBlocksMessage("assistant", resp.Content))

		var toolResultBlocks []prometheus.ContentBlock
		for _, tb := range toolBlocks {
			slog.Info("dispatching tool (long)", "name", tb.Name, "id", tb.ID)

			result, tErr := o.tracingDispatcher.Dispatch(ctx, tb.Name, tb.Input)
			isError := false
			content := ""
			if tErr != nil {
				isError = true
				content = "tool error: " + tErr.Error()
			} else if result.IsError {
				isError = true
				content = result.Content
			} else {
				content = result.Content
			}

			toolResultBlocks = append(toolResultBlocks, prometheus.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tb.ID,
				Content:   content,
				IsError:   isError,
			})
		}

		toolResultContent, _ := json.Marshal(toolResultBlocks)
		messages = append(messages, prometheus.ChatMessage{
			Role:    "user",
			Content: toolResultContent,
		})
	}

	// Max iterations reached.
	slog.Warn("ChatLong reached max iterations", "max", maxIterations)
	return lastText, nil
}

// ─── /skill-name Interception ─────────────────────────────────────────────

// skillOverrideRe matches /skill-name at the start of a message or after whitespace.
// Only single-segment kebab-case names are valid (no path separators).
var skillOverrideRe = regexp.MustCompile(`(?m)^/([a-z][a-z0-9-]*)(?:\s|$)`)

// extractSkillOverrides extracts /skill-name prefixes from a message.
// Returns the cleaned message (without skill prefixes) and the list of valid skill names.
// Path-like patterns (e.g., /etc/passwd) are rejected by the regex (no path separators).
func extractSkillOverrides(message string, registry *atlas.Registry) (string, []string) {
	if registry == nil {
		return message, nil
	}

	matches := skillOverrideRe.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return message, nil
	}

	var skillNames []string
	cleaned := message
	for _, m := range matches {
		name := m[1]
		if registry.Has(name) {
			skillNames = append(skillNames, name)
			cleaned = strings.Replace(cleaned, m[0], "", 1)
			slog.Info("skill override extracted", "name", name)
		} else {
			slog.Warn("unknown skill in override, passing through", "name", name)
		}
	}

	return strings.TrimSpace(cleaned), skillNames
}

// ─── SKILLS_V2 Prompt Building ────────────────────────────────────────────

// buildPromptV2 constructs the prompt with always-skills, preloaded skills,
// compact skill index, and tool definitions injected.
func (o *Orchestrator) buildPromptV2(history []StoreMessage, currentMessage string, preloadSkills []string) string {
	var b strings.Builder
	b.WriteString(loadSystemPrompt())

	// 1. Inject always:true skills
	alwaysSkills := o.registry.AlwaysSkills()
	if len(alwaysSkills) > 0 {
		slog.Debug("injecting always-loaded skills", "count", len(alwaysSkills))
		for _, s := range alwaysSkills {
			content, err := o.loader.Load(s.Name)
			if err != nil {
				slog.Error("failed to load always skill", "name", s.Name, "err", err)
				continue
			}
			b.WriteString("\n\n")
			b.WriteString(content)
		}
	}

	// 2. Inject pre-loaded skills (/skill-name overrides)
	for _, name := range preloadSkills {
		content, err := o.loader.Load(name)
		if err != nil {
			slog.Warn("failed to preload skill", "name", name, "err", err)
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(content)
	}

	// 3. Inject compact skill index + tool definitions
	compactIndex := o.registry.CompactIndex()
	if compactIndex != "" {
		b.WriteString("\n\n")
		b.WriteString(compactIndex)
	}

	toolBlock := o.dispatcher.ToolDefinitionsBlock()
	if toolBlock != "" {
		b.WriteString("\n\n")
		b.WriteString(toolBlock)
	}

	b.WriteString("\n\n")

	// 4. Conversation history + current message
	for _, m := range history {
		if m.Role == "user" && m.Content == currentMessage {
			continue
		}
		b.WriteString("[")
		if m.Role == "user" {
			b.WriteString("User")
		} else {
			b.WriteString("Assistant")
		}
		b.WriteString("]\n")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}

	b.WriteString("[User]\n")
	b.WriteString(currentMessage)

	return b.String()
}

// ─── PROMETHEUS v2: System Prompt + Messages ─────────────────────────────

// buildSystemPromptV2 constructs the system prompt with skills injected.
// Unlike buildPromptV2, this does NOT include conversation history or tool
// definitions in text form — those go through native API parameters.
func (o *Orchestrator) buildSystemPromptV2(preloadSkills []string) string {
	var b strings.Builder
	b.WriteString(loadSystemPrompt())

	// 1. Inject always:true skills
	alwaysSkills := o.registry.AlwaysSkills()
	if len(alwaysSkills) > 0 {
		slog.Debug("injecting always-loaded skills", "count", len(alwaysSkills))
		for _, s := range alwaysSkills {
			content, err := o.loader.Load(s.Name)
			if err != nil {
				slog.Error("failed to load always skill", "name", s.Name, "err", err)
				continue
			}
			b.WriteString("\n\n")
			b.WriteString(content)
		}
	}

	// 2. Inject pre-loaded skills (/skill-name overrides)
	for _, name := range preloadSkills {
		content, err := o.loader.Load(name)
		if err != nil {
			slog.Warn("failed to preload skill", "name", name, "err", err)
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(content)
	}

	// 3. Inject compact skill index (so Claude knows what skills exist to load)
	compactIndex := o.registry.CompactIndex()
	if compactIndex != "" {
		b.WriteString("\n\n")
		b.WriteString(compactIndex)
	}

	// NOTE: Tool definitions are NOT injected as text here.
	// They go through the native tools API parameter.

	return b.String()
}

// buildChatMessages converts conversation history + current message into Claude API messages.
func (o *Orchestrator) buildChatMessages(history []StoreMessage, currentMessage string) []prometheus.ChatMessage {
	var messages []prometheus.ChatMessage

	for _, m := range history {
		// Skip the current message from history (it will be added at the end)
		if m.Role == "user" && m.Content == currentMessage {
			continue
		}
		messages = append(messages, prometheus.NewTextMessage(m.Role, m.Content))
	}

	// Add current user message
	messages = append(messages, prometheus.NewTextMessage("user", currentMessage))

	return messages
}

