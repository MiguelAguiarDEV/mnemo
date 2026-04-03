package athena

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	// maxTraceOutputBytes is the maximum size of tool output stored in a trace.
	maxTraceOutputBytes = 10 * 1024 // 10KB
)

// TracingConfig configures the TracingDispatcher.
type TracingConfig struct {
	TraceURL  string // e.g. "http://localhost:8080/traces/tool-call"
	AuthToken string // Bearer token or API key for the trace endpoint
	SessionID string // Chat session identifier
	Project   string // Project name (optional)
	Agent     string // Agent identifier (default: "jarvis")
}

// TracingDispatcher wraps a Dispatcher and records a trace after each tool dispatch.
// Trace recording is fire-and-forget: it never blocks or fails the tool execution.
type TracingDispatcher struct {
	inner     *Dispatcher
	cfg       TracingConfig
	client    *http.Client
	sessionID atomic.Value // stores string; overrides cfg.SessionID when set
}

// NewTracingDispatcher creates a TracingDispatcher that wraps the given Dispatcher.
// If cfg.Agent is empty, it defaults to "jarvis".
func NewTracingDispatcher(inner *Dispatcher, cfg TracingConfig) *TracingDispatcher {
	if cfg.Agent == "" {
		cfg.Agent = "jarvis"
	}
	slog.Debug("tracing dispatcher created",
		"trace_url", cfg.TraceURL,
		"session_id", cfg.SessionID,
		"agent", cfg.Agent,
	)
	return &TracingDispatcher{
		inner: inner,
		cfg:   cfg,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// SetSessionID updates the session ID used for subsequent traces.
// This is safe to call concurrently. Use it to bind traces to a specific
// chat conversation before dispatching tools.
func (td *TracingDispatcher) SetSessionID(sessionID string) {
	td.sessionID.Store(sessionID)
}

// currentSessionID returns the active session ID (atomic override or config default).
func (td *TracingDispatcher) currentSessionID() string {
	if v := td.sessionID.Load(); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return td.cfg.SessionID
}

// Dispatch executes the tool via the underlying Dispatcher and fires a trace POST.
func (td *TracingDispatcher) Dispatch(ctx context.Context, name string, params json.RawMessage) (ToolResult, error) {
	start := time.Now()

	result, err := td.inner.Dispatch(ctx, name, params)

	durationMs := int(time.Since(start).Milliseconds())

	// Fire-and-forget: record the trace in a goroutine.
	go td.recordTrace(name, params, result, err, durationMs)

	return result, err
}

// tracePayload matches cloudstore.AddToolCallParams JSON structure.
type tracePayload struct {
	SessionID  string          `json:"session_id"`
	Project    string          `json:"project,omitempty"`
	Agent      string          `json:"agent"`
	ToolName   string          `json:"tool_name"`
	InputJSON  json.RawMessage `json:"input_json,omitempty"`
	OutputText string          `json:"output_text,omitempty"`
	DurationMs *int            `json:"duration_ms,omitempty"`
}

func (td *TracingDispatcher) recordTrace(toolName string, params json.RawMessage, result ToolResult, toolErr error, durationMs int) {
	if td.cfg.TraceURL == "" {
		slog.Debug("tracing: no trace URL configured, skipping")
		return
	}

	output := result.Content
	if toolErr != nil {
		output = fmt.Sprintf("error: %s", toolErr.Error())
	}

	// Truncate output to maxTraceOutputBytes.
	if len(output) > maxTraceOutputBytes {
		output = output[:maxTraceOutputBytes] + "\n[truncated]"
	}

	payload := tracePayload{
		SessionID:  td.currentSessionID(),
		Project:    td.cfg.Project,
		Agent:      td.cfg.Agent,
		ToolName:   toolName,
		InputJSON:  params,
		OutputText: output,
		DurationMs: &durationMs,
	}

	body, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		slog.Warn("tracing: failed to marshal trace payload", "tool", toolName, "err", marshalErr)
		return
	}

	req, reqErr := http.NewRequest(http.MethodPost, td.cfg.TraceURL, bytes.NewReader(body))
	if reqErr != nil {
		slog.Warn("tracing: failed to create trace request", "tool", toolName, "err", reqErr)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if td.cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+td.cfg.AuthToken)
	}

	resp, postErr := td.client.Do(req)
	if postErr != nil {
		slog.Warn("tracing: trace POST failed", "tool", toolName, "err", postErr)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		slog.Warn("tracing: trace POST non-2xx", "tool", toolName, "status", resp.StatusCode)
		return
	}

	slog.Debug("tracing: trace recorded", "tool", toolName, "duration_ms", durationMs)
}
