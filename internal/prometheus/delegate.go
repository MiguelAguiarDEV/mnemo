// Package prometheus — Worker implements async delegation to LLM backends.
// Workers can use either:
//   - OpenCode backend (legacy): creates session, sends prompt, returns text
//   - Native executor (PROMETHEUS v2): uses Claude API directly with Go-native tools
package prometheus

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Gentleman-Programming/engram/internal/prometheus/tools"
)

// DelegateRequest describes a task to delegate to an OpenCode worker.
type DelegateRequest struct {
	Task       string // what to do
	Project    string // which project/repo
	Context    string // additional context
	WorkingDir string // where to execute
	Model      string // model preference (optional, e.g. "opus", "sonnet")
}

// DelegateResult holds the outcome of a delegated task execution.
type DelegateResult struct {
	JobID      string        `json:"job_id"`
	Status     string        `json:"status"` // "success", "error", "timeout"
	Output     string        `json:"output"`
	TokensUsed TokenUsage    `json:"tokens_used"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
}

// WorkerExecutor is the interface for task execution backends.
type WorkerExecutor interface {
	Execute(ctx context.Context, req DelegateRequest) (*DelegateResult, error)
}

// Worker executes delegated tasks through an OpenCode backend.
type Worker struct {
	backend Backend
	logger  *slog.Logger
}

// NewWorker creates a Worker backed by the given LLM backend.
func NewWorker(backend Backend, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		backend: backend,
		logger:  logger,
	}
}

// Execute runs a delegated task synchronously: creates a session, sends the
// prompt, collects the response, and returns a DelegateResult.
func (w *Worker) Execute(ctx context.Context, req DelegateRequest) (*DelegateResult, error) {
	start := time.Now()
	w.logger.Info("worker: starting delegated task",
		"task", truncateLog(req.Task, 100),
		"project", req.Project,
	)

	// 1. Create a fresh session for isolation.
	sessionID, err := w.backend.CreateSession(ctx)
	if err != nil {
		w.logger.Error("worker: failed to create session", "err", err)
		return &DelegateResult{
			Status:   "error",
			Error:    fmt.Sprintf("create session: %v", err),
			Duration: time.Since(start),
		}, err
	}
	w.logger.Debug("worker: session created", "session_id", sessionID)

	// 2. Build the prompt.
	prompt := buildDelegatePrompt(req)

	// 3. Resolve model config (defaults to sonnet for delegation).
	model := ModelConfig{
		ProviderID: "anthropic",
		ModelID:    "claude-sonnet-4-6",
	}
	if req.Model != "" {
		model = resolveModel(req.Model)
	}

	// 4. Send the prompt.
	request := Request{
		SessionID: sessionID,
		Messages:  []Message{{Role: "user", Content: prompt}},
		Model:     model,
	}

	resp, err := w.backend.Send(ctx, request)
	if err != nil {
		duration := time.Since(start)
		w.logger.Error("worker: send failed", "err", err, "duration", duration)

		// Check for context timeout/cancellation.
		status := "error"
		if ctx.Err() != nil {
			status = "timeout"
		}

		return &DelegateResult{
			Status:   status,
			Error:    fmt.Sprintf("send prompt: %v", err),
			Duration: duration,
		}, err
	}

	duration := time.Since(start)
	w.logger.Info("worker: task completed",
		"duration", duration,
		"tokens_in", resp.Usage.InputTokens,
		"tokens_out", resp.Usage.OutputTokens,
		"output_len", len(resp.Text),
	)

	return &DelegateResult{
		Status:     "success",
		Output:     resp.Text,
		TokensUsed: resp.Usage,
		Duration:   duration,
	}, nil
}

// buildDelegatePrompt constructs the worker prompt from a DelegateRequest.
func buildDelegatePrompt(req DelegateRequest) string {
	var prompt string

	if req.Project != "" {
		prompt += fmt.Sprintf("You are working on project %q", req.Project)
		if req.WorkingDir != "" {
			prompt += fmt.Sprintf(" in %s", req.WorkingDir)
		}
		prompt += ".\n\n"
	} else if req.WorkingDir != "" {
		prompt += fmt.Sprintf("Working directory: %s\n\n", req.WorkingDir)
	}

	prompt += fmt.Sprintf("Task: %s", req.Task)

	if req.Context != "" {
		prompt += fmt.Sprintf("\n\nAdditional context:\n%s", req.Context)
	}

	return prompt
}

// resolveModel maps a model name shorthand to a ModelConfig.
func resolveModel(name string) ModelConfig {
	switch name {
	case "opus":
		return ModelConfig{ProviderID: "anthropic", ModelID: "claude-opus-4-6"}
	case "sonnet":
		return ModelConfig{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6"}
	default:
		// Default to sonnet for unknown models.
		return ModelConfig{ProviderID: "anthropic", ModelID: "claude-sonnet-4-6"}
	}
}

// truncateLog truncates a string for log output.
func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ─── Native Worker (PROMETHEUS v2) ─────────────────────────────────────────

const (
	maxToolIterations      = 25 // max tool_use loop iterations per worker (raised from 10 for Haiku compatibility)
	warnToolIterations     = 20 // log a warning when approaching the limit
	nativeWorkerMaxToks    = 16384
)

// NativeWorker executes delegated tasks using the Claude API directly with
// Go-native tool implementations. This replaces the OpenCode backend path.
type NativeWorker struct {
	client *ClaudeClient
	logger *slog.Logger
}

// NewNativeWorker creates a NativeWorker backed by the given Claude API client.
func NewNativeWorker(client *ClaudeClient, logger *slog.Logger) *NativeWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &NativeWorker{
		client: client,
		logger: logger,
	}
}

// Execute runs a delegated task using the Claude API with native tool execution.
// It implements the tool_use loop: send request -> execute tools -> send results -> repeat.
func (w *NativeWorker) Execute(ctx context.Context, req DelegateRequest) (*DelegateResult, error) {
	start := time.Now()
	w.logger.Info("native worker: starting task",
		"task", truncateLog(req.Task, 100),
		"project", req.Project,
		"working_dir", req.WorkingDir,
	)

	// 1. Create tool registry for the working directory.
	workDir := req.WorkingDir
	if workDir == "" {
		workDir = "/tmp"
	}
	registry := tools.NewRegistry(workDir, w.logger)

	// 2. Resolve model.
	model := "claude-sonnet-4-6"
	if req.Model != "" {
		model = resolveModel(req.Model).ModelID
	}

	// 3. Build initial request.
	systemPrompt := buildNativeWorkerPrompt(req)
	chatReq := ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     []ChatMessage{NewTextMessage("user", req.Task)},
		Tools:        toolDefsToChat(registry.ToolDefs()),
		Model:        model,
		MaxTokens:    nativeWorkerMaxToks,
	}

	var totalUsage TokenUsage

	// 4. Tool loop.
	var lastTextContent string
	for i := 0; i < maxToolIterations; i++ {
		if i+1 == warnToolIterations {
			w.logger.Warn("native worker: approaching max iterations",
				"iteration", i+1,
				"max", maxToolIterations,
			)
		}
		w.logger.Debug("native worker: sending request", "iteration", i+1)

		resp, err := w.client.Send(ctx, chatReq)
		if err != nil {
			duration := time.Since(start)
			w.logger.Error("native worker: API error", "err", err, "iteration", i+1)

			status := "error"
			if ctx.Err() != nil {
				status = "timeout"
			}

			return &DelegateResult{
				Status:     status,
				Error:      fmt.Sprintf("claude API: %v", err),
				Duration:   duration,
				TokensUsed: totalUsage,
			}, err
		}

		// Accumulate usage.
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens

		// Capture latest text for partial result if we hit max iterations.
		if text := resp.TextContent(); text != "" {
			lastTextContent = text
		}

		// Check if done (no more tool calls).
		if resp.StopReason != "tool_use" {
			text := resp.TextContent()
			duration := time.Since(start)
			w.logger.Info("native worker: task completed",
				"duration", duration,
				"iterations", i+1,
				"tokens_in", totalUsage.InputTokens,
				"tokens_out", totalUsage.OutputTokens,
			)

			return &DelegateResult{
				Status:     "success",
				Output:     text,
				TokensUsed: totalUsage,
				Duration:   duration,
			}, nil
		}

		// Execute tool calls and build result messages.
		toolBlocks := resp.ToolUseBlocks()
		w.logger.Info("native worker: executing tools",
			"count", len(toolBlocks),
			"iteration", i+1,
		)

		// Add the assistant's response to messages.
		chatReq.Messages = append(chatReq.Messages, NewBlocksMessage("assistant", resp.Content))

		// Execute each tool and collect results.
		var resultBlocks []ContentBlock
		for _, block := range toolBlocks {
			tool, ok := registry.Get(block.Name)
			if !ok {
				w.logger.Warn("native worker: unknown tool", "name", block.Name)
				resultBlocks = append(resultBlocks, ContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   fmt.Sprintf("unknown tool: %s", block.Name),
					IsError:   true,
				})
				continue
			}

			w.logger.Info("native worker: executing tool",
				"tool", block.Name,
				"input_size", len(block.Input),
			)

			result, execErr := tool.Execute(ctx, block.Input)
			if execErr != nil {
				w.logger.Error("native worker: tool error",
					"tool", block.Name,
					"err", execErr,
				)
				resultBlocks = append(resultBlocks, ContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   fmt.Sprintf("error: %v", execErr),
					IsError:   true,
				})
				continue
			}

			// Truncate large results.
			if len(result) > 100*1024 {
				result = result[:100*1024] + "\n... (output truncated at 100KB)"
			}

			resultBlocks = append(resultBlocks, ContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result,
			})
		}

		// Add tool results as a user message.
		chatReq.Messages = append(chatReq.Messages, NewBlocksMessage("user", resultBlocks))
	}

	// Max iterations reached -- return partial result instead of hard error.
	duration := time.Since(start)
	w.logger.Warn("native worker: max iterations reached, returning partial result",
		"max", maxToolIterations,
		"has_partial", lastTextContent != "",
	)

	output := lastTextContent
	if output == "" {
		output = fmt.Sprintf("(max tool iterations reached: %d -- no text output captured)", maxToolIterations)
	}

	return &DelegateResult{
		Status:     "success",
		Output:     output,
		Error:      fmt.Sprintf("max tool iterations reached (%d), returning partial result", maxToolIterations),
		TokensUsed: totalUsage,
		Duration:   duration,
	}, nil
}

// buildNativeWorkerPrompt constructs a system prompt for native worker execution.
func buildNativeWorkerPrompt(req DelegateRequest) string {
	var prompt string

	prompt += "You are a task worker. Complete the given task efficiently using the available tools.\n\n"
	prompt += "Rules:\n"
	prompt += "- Use tools to inspect the filesystem and execute commands as needed.\n"
	prompt += "- Be thorough but concise.\n"
	prompt += "- Report results clearly when done.\n"
	prompt += "- Do NOT ask questions — complete the task with your best judgment.\n\n"

	if req.Project != "" {
		prompt += fmt.Sprintf("Project: %s\n", req.Project)
	}
	if req.WorkingDir != "" {
		prompt += fmt.Sprintf("Working directory: %s\n", req.WorkingDir)
	}
	if req.Context != "" {
		prompt += fmt.Sprintf("\nAdditional context:\n%s\n", req.Context)
	}

	return prompt
}

// toolDefsToChat converts tools.ToolDef to ChatToolDef (breaks import cycle).
func toolDefsToChat(defs []tools.ToolDef) []ChatToolDef {
	result := make([]ChatToolDef, len(defs))
	for i, d := range defs {
		result[i] = ChatToolDef{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	return result
}

// ─── DelegateHandler adapter for NativeWorker ──────────────────────────────

// NativeAsyncDelegateAdapter wraps a NativeWorker to satisfy the DelegateRequest
// interface used by JobTracker.RunAsync. It allows gradual migration from
// OpenCode backend to native execution.
type NativeAsyncDelegateAdapter struct {
	Worker *NativeWorker
}

// DelegateAsync creates a job-compatible execution. The actual async handling
// is done by JobTracker.RunAsync -- this adapter just ensures compatibility.
func (a *NativeAsyncDelegateAdapter) DelegateAsync(ctx context.Context, task, project, workingDir, context string) (string, error) {
	result, err := a.Worker.Execute(ctx, DelegateRequest{
		Task:       task,
		Project:    project,
		WorkingDir: workingDir,
		Context:    context,
	})
	if err != nil {
		return "", err
	}
	return result.Output, nil
}
