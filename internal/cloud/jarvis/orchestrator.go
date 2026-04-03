package jarvis

import (
	"bufio"
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

	"github.com/Gentleman-Programming/engram/internal/cloud/notifications"
	"github.com/Gentleman-Programming/engram/internal/morpheus"
	"github.com/Gentleman-Programming/engram/internal/prometheus"
	"github.com/Gentleman-Programming/engram/internal/atlas"
	"github.com/Gentleman-Programming/engram/internal/sentinel"
	"github.com/Gentleman-Programming/engram/internal/athena"
)

// ─── Store Interface ───────────────────────────────────────────────────────

type StoreInterface interface {
	AddMessage(conversationID int64, role, content, model string, tokensIn, tokensOut *int, costUSD *float64) (int64, error)
	GetMessages(conversationID int64, limit int) ([]StoreMessage, error)
	BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*StoreBudgetReport, error)
	CreateTask(userID string, title, description, project, priority string) (int64, error)
	ListTasks(userID string, status string, limit int) ([]StoreTask, error)
	UpdateTaskStatus(userID string, taskID int64, status string) error
	UpdateTask(userID string, taskID int64, fields athena.UpdateTaskFields) error
}

type StoreMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

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

type OrchestratorConfig struct {
	Store            StoreInterface
	Notifier         notifications.Notifier // optional; Discord DM, etc.
	OpenCodeURL      string                 // URL of OpenCode serve; defaults to OPENCODE_SERVE_URL or http://172.18.0.1:4096
	OpenCodePassword string                 // defaults to OPENCODE_SERVER_PASSWORD env var
	BudgetClaude     float64
	BudgetOpenAI     float64
}

// ─── Orchestrator ──────────────────────────────────────────────────────────

type Orchestrator struct {
	store            StoreInterface
	notifier         notifications.Notifier
	openCodeURL      string
	openCodePassword string
	budgetClaude     float64
	budgetOpenAI     float64
	client           *http.Client
	sessionID        string // reused OpenCode session for lower latency

	// Skills architecture components
	registry   *atlas.Registry
	loader     *atlas.Loader
	dispatcher *athena.Dispatcher
	logger     *slog.Logger

	// Async delegation (Tasks 27-29)
	jobTracker *athena.JobTracker
	worker     prometheus.WorkerExecutor

	// Direct Claude API client for ATHENA chat (PROMETHEUS v2)
	claudeClient *prometheus.ClaudeClient
}

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
		client:           &http.Client{Timeout: 5 * time.Minute},
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
	engramBin := os.Getenv("ENGRAM_BIN")
	if engramBin == "" {
		engramBin = "engram"
	}
	o.dispatcher.Register(athena.NewSearchMemoryTool(engramBin, nil))
	o.dispatcher.Register(athena.NewSaveMemoryTool(engramBin, nil))

	slog.Info("SKILLS_V2 initialized",
		"registry_skills", len(o.registry.AlwaysSkills()),
		"tools", 11,
	)
}

// ─── Memory Consolidation (autoDream) ─────────────────────────────────────

// StartDream launches the background memory consolidation loop.
// It runs in a goroutine and checks every hour if consolidation is needed.
// Call this from the cloud server startup after creating the orchestrator.
func (o *Orchestrator) StartDream(ctx context.Context) {
	engramBin := os.Getenv("ENGRAM_BIN")
	if engramBin == "" {
		engramBin = "engram"
	}

	lockPath := filepath.Join(os.TempDir(), "engram-consolidate.lock")
	if d := os.Getenv("JARVIS_DREAM_LOCK"); d != "" {
		lockPath = d
	}

	consolidator := morpheus.New(
		morpheus.WithEngramBin(engramBin),
		morpheus.WithLockPath(lockPath),
		morpheus.WithLogger(o.logger),
	)

	go consolidator.RunBackground(ctx)
	o.logger.Info("dream: background memory consolidation started",
		"lock_path", lockPath,
		"engram_bin", engramBin,
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
	engramBin := os.Getenv("ENGRAM_BIN")
	if engramBin == "" {
		engramBin = "engram"
	}
	lockPath := filepath.Join(os.TempDir(), "engram-consolidate.lock")
	if d := os.Getenv("JARVIS_DREAM_LOCK"); d != "" {
		lockPath = d
	}
	consolidator := morpheus.New(
		morpheus.WithEngramBin(engramBin),
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

// orchestratorDelegateHandler delegates via the orchestrator's session mechanism.
type orchestratorDelegateHandler struct {
	o *Orchestrator
}

func (h *orchestratorDelegateHandler) Delegate(ctx context.Context, task, project, skillContext string) (string, error) {
	slog.Info("delegate: creating sub-agent session", "task", task, "project", project)

	sessionID, err := h.o.createSession()
	if err != nil {
		return "", fmt.Errorf("failed to create delegate session: %w", err)
	}

	subPrompt := task
	if project != "" {
		subPrompt = fmt.Sprintf("[Project: %s] %s", project, subPrompt)
	}
	if skillContext != "" {
		subPrompt = skillContext + "\n\n" + subPrompt
	}

	delegateModel := SelectModel(ComplexityComplex, 0)
	result, _, err := h.o.sendPrompt(sessionID, subPrompt, delegateModel, func(s string) {})
	if err != nil {
		return "", fmt.Errorf("delegate sub-agent failed: %w", err)
	}

	if len(result) > 2000 {
		result = result[:2000] + "\n... (truncated)"
	}
	return result, nil
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
			Tools:        toolDefs,
			Model:        model.Model,
			MaxTokens:    model.MaxTokens,
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

		// If stop_reason is "end_turn", we're done
		if resp.StopReason != "tool_use" {
			// Persist total token usage
			modelName := resp.Model
			if modelName == "" {
				modelName = model.Model
			}
			cost := (float64(totalInputTokens) / 1000.0 * model.CostPer1KIn) +
				(float64(totalOutputTokens) / 1000.0 * model.CostPer1KOut)
			o.store.AddMessage(conversationID, "assistant", textContent, modelName, &totalInputTokens, &totalOutputTokens, &cost)

			return textContent, nil
		}

		// stop_reason == "tool_use": dispatch tools and continue the loop
		toolBlocks := resp.ToolUseBlocks()
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

			result, err := o.dispatcher.Dispatch(context.Background(), tb.Name, tb.Input)
			isError := false
			content := ""
			if err != nil {
				isError = true
				content = "tool error: " + err.Error()
				slog.Error("tool dispatch error", "tool", tb.Name, "err", err)
			} else if result.IsError {
				isError = true
				content = result.Content
				slog.Warn("tool returned error", "tool", tb.Name, "content", content)
			} else {
				content = result.Content
				slog.Debug("tool result", "tool", tb.Name, "result_len", len(content))
			}

			toolResultBlocks = append(toolResultBlocks, prometheus.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tb.ID,
				Content:   content,
				IsError:   isError,
			})
		}

		// Add all tool results as a single user message
		toolResultContent, _ := json.Marshal(toolResultBlocks)
		messages = append(messages, prometheus.ChatMessage{
			Role:    "user",
			Content: toolResultContent,
		})

		// If this is the last iteration, log a warning
		if i == maxIterations-1 {
			slog.Warn("tool-call loop reached max iterations", "max", maxIterations)
			// Persist what we have
			modelName := resp.Model
			cost := (float64(totalInputTokens) / 1000.0 * model.CostPer1KIn) +
				(float64(totalOutputTokens) / 1000.0 * model.CostPer1KOut)
			o.store.AddMessage(conversationID, "assistant", textContent, modelName, &totalInputTokens, &totalOutputTokens, &cost)
			return textContent, nil
		}
	}

	return "", fmt.Errorf("jarvis: tool-call loop exhausted without final response")
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

// ─── OpenCode Serve HTTP Call (kept for workers/delegation) ──────────────

// tokenInfo holds token usage from a response.
type tokenInfo struct {
	Input     int
	Output    int
	Total     int
	Reasoning int
	Model     string
	Cost      float64
}

// callOpenCodeServe calls OpenCode serve's session/prompt API via HTTP.
// Reuses a persistent session for lower latency.
func (o *Orchestrator) callOpenCodeServe(prompt string, model ModelConfig, onToken func(string)) (string, *tokenInfo, error) {
	// Reuse existing session or create one
	if o.sessionID == "" {
		sessionID, err := o.createSession()
		if err != nil {
			slog.Warn("failed to create session, falling back to direct", "err", err)
			text, ti, err := o.callOpenCodeDirect(prompt, model, onToken)
			return text, ti, err
		}
		o.sessionID = sessionID
		slog.Info("created OpenCode session", "session_id", sessionID)
	}

	result, ti, err := o.sendPrompt(o.sessionID, prompt, model, onToken)
	if err != nil {
		slog.Warn("session failed, creating new", "err", err)
		o.sessionID = ""
		sessionID, err2 := o.createSession()
		if err2 != nil {
			return "", nil, fmt.Errorf("retry failed: %w", err)
		}
		o.sessionID = sessionID
		return o.sendPrompt(o.sessionID, prompt, model, onToken)
	}

	return result, ti, nil
}

func (o *Orchestrator) createSession() (string, error) {
	body := strings.NewReader(`{}`)
	req, err := http.NewRequest("POST", o.openCodeURL+"/session", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if o.openCodePassword != "" {
		req.SetBasicAuth("opencode", o.openCodePassword)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("opencode serve unreachable at %s: %w", o.openCodeURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("opencode create session: status %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.ID, nil
}

// openCodeResponse is the JSON response from OpenCode serve's message endpoint.
type openCodeResponse struct {
	Info struct {
		Role   string `json:"role"`
		Tokens struct {
			Total     int `json:"total"`
			Input     int `json:"input"`
			Output    int `json:"output"`
			Reasoning int `json:"reasoning"`
		} `json:"tokens"`
		Cost    float64 `json:"cost"`
		ModelID string  `json:"modelID"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

func (o *Orchestrator) sendPrompt(sessionID, prompt string, model ModelConfig, onToken func(string)) (string, *tokenInfo, error) {
	payload := map[string]any{
		"parts": []map[string]string{{"type": "text", "text": prompt}},
		"model": map[string]string{
			"providerID": model.Provider,
			"modelID":    model.Model,
		},
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", o.openCodeURL+"/session/"+sessionID+"/message", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if o.openCodePassword != "" {
		req.SetBasicAuth("opencode", o.openCodePassword)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("opencode prompt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("opencode prompt: status %d (model: %s/%s)", resp.StatusCode, model.Provider, model.Model)
	}

	// OpenCode serve returns a complete JSON response with parts array
	var ocResp openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ocResp); err != nil {
		return "", nil, fmt.Errorf("opencode: decode response: %w", err)
	}

	// Send real status events from parts, then the text
	var fullText strings.Builder
	for _, part := range ocResp.Parts {
		switch part.Type {
		case "step-start":
			onToken("\n") // signal activity started
		case "reasoning":
			if part.Text != "" {
				// Send reasoning as a status event the frontend can display
				statusJSON, _ := json.Marshal(map[string]string{"status": "reasoning", "detail": part.Text})
				onToken("__STATUS__" + string(statusJSON))
			}
		case "text":
			if part.Text != "" {
				onToken(part.Text)
				fullText.WriteString(part.Text)
			}
		case "step-finish":
			// Send token info
			tokensJSON, _ := json.Marshal(map[string]any{
				"status":    "complete",
				"tokens_in": ocResp.Info.Tokens.Input,
				"tokens_out": ocResp.Info.Tokens.Output,
				"model":     ocResp.Info.ModelID,
			})
			onToken("__STATUS__" + string(tokensJSON))
		}
	}

	ti := &tokenInfo{
		Input:     ocResp.Info.Tokens.Input,
		Output:    ocResp.Info.Tokens.Output,
		Total:     ocResp.Info.Tokens.Total,
		Reasoning: ocResp.Info.Tokens.Reasoning,
		Model:     ocResp.Info.ModelID,
		Cost:      ocResp.Info.Cost,
	}

	return fullText.String(), ti, nil
}

// callOpenCodeDirect is a fallback that uses `opencode run` CLI style endpoint
func (o *Orchestrator) callOpenCodeDirect(prompt string, model ModelConfig, onToken func(string)) (string, *tokenInfo, error) {
	modelArg := model.Provider + "/" + model.Model
	payload := map[string]any{
		"message": prompt,
		"model":   modelArg,
		"format":  "json",
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", o.openCodeURL+"/run", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if o.openCodePassword != "" {
		req.SetBasicAuth("opencode", o.openCodePassword)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("opencode direct: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("opencode direct: status %d", resp.StatusCode)
	}

	text, err := o.readSSEResponse(resp, onToken)
	return text, nil, err
}

func (o *Orchestrator) readSSEResponse(resp *http.Response, onToken func(string)) (string, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	var fullText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line == ": keepalive" {
			continue
		}

		// SSE format: "data: {...}"
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data == "[DONE]" {
				break
			}

			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				// Not JSON — treat as raw text
				onToken(data)
				fullText.WriteString(data)
				continue
			}

			// Extract text content from various event formats
			text := extractText(event)
			if text != "" {
				onToken(text)
				fullText.WriteString(text)
			}
			continue
		}

		// Non-SSE: plain text or JSON lines
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Plain text line
			if line != "" {
				onToken(line)
				fullText.WriteString(line)
			}
			continue
		}

		text := extractText(event)
		if text != "" {
			onToken(text)
			fullText.WriteString(text)
		}
	}

	return fullText.String(), nil
}

// extractText pulls text content from various OpenCode event formats.
func extractText(event map[string]any) string {
	// Check common fields in order of likelihood
	for _, key := range []string{"content", "text", "part", "delta"} {
		if v, ok := event[key]; ok {
			switch val := v.(type) {
			case string:
				return val
			case map[string]any:
				if t, ok := val["text"]; ok {
					if s, ok := t.(string); ok {
						return s
					}
				}
			}
		}
	}
	return ""
}
