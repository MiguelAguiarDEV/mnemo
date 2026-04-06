// Package prometheus — ClaudeClient implements direct Claude API access
// using OAuth tokens from Claude Code's credentials file.
//
// This bypasses OpenCode serve for the ATHENA chat path, enabling native
// tool definitions and proper tool_use/tool_result message flow.
package prometheus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	claudeAPIURL          = "http://host.docker.internal:9876/v1/messages" // PROMETHEUS bridge (claude-agent-sdk)
	claudeAPIVersion      = "2023-06-01"
	claudeBetaHeaders     = "oauth-2025-04-20,interleaved-thinking-2025-05-14"
	claudeUserAgent       = "claude-code/2.1.88"
	claudeTokenRefreshURL = "https://console.anthropic.com/v1/oauth/token"
	claudeOAuthClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	tokenRefreshBuffer    = 5 * time.Minute
	defaultMaxTokens      = 8192
	defaultCredentialPath = "/.claude/.credentials.json"
)

// ─── Types ───────────────────────────────────────────────────────────────────

// ChatRequest is the request to the Claude API.
type ChatRequest struct {
	SystemPrompt   string              `json:"system_prompt"`
	Messages       []ChatMessage       `json:"messages"`
	Tools          []ChatToolDef       `json:"tools,omitempty"`
	Model          string              `json:"model"`
	MaxTokens      int                 `json:"max_tokens"`
	MaxTurns       int                 `json:"max_turns,omitempty"` // max agent turns for bridge (0 = bridge default)
	tokenRefreshed bool                // internal: prevent infinite refresh loop
}

// ChatMessage is a message in the Claude API conversation.
type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []ContentBlock
}

// NewTextMessage creates a ChatMessage with a plain text content.
func NewTextMessage(role, text string) ChatMessage {
	content, _ := json.Marshal(text)
	return ChatMessage{Role: role, Content: content}
}

// NewBlocksMessage creates a ChatMessage with an array of content blocks.
func NewBlocksMessage(role string, blocks []ContentBlock) ChatMessage {
	content, _ := json.Marshal(blocks)
	return ChatMessage{Role: role, Content: content}
}

// NewToolResultMessage creates an assistant-to-user tool_result message.
func NewToolResultMessage(toolUseID, result string, isError bool) ChatMessage {
	block := ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content:   result,
		IsError:   isError,
	}
	blocks := []ContentBlock{block}
	content, _ := json.Marshal(blocks)
	return ChatMessage{Role: "user", Content: content}
}

// ChatToolDef is a tool definition in Anthropic's native format.
type ChatToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ChatResponse is the parsed response from the Claude API.
type ChatResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      ChatUsage      `json:"usage"`
	Model      string         `json:"model"`
}

// ContentBlock is a block in the Claude API response content array.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	// tool_result fields (used in request messages, not responses)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ChatUsage tracks token consumption.
type ChatUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ToolUseBlocks returns only tool_use content blocks from the response.
func (r *ChatResponse) ToolUseBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, b := range r.Content {
		if b.Type == "tool_use" {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// TextContent returns the concatenated text from all text blocks.
func (r *ChatResponse) TextContent() string {
	var text string
	for _, b := range r.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text
}

// ─── Credentials ─────────────────────────────────────────────────────────────

// claudeCredentials mirrors the structure of ~/.claude/.credentials.json.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"` // unix ms
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

// ─── Client ──────────────────────────────────────────────────────────────────

// ClaudeClient is a direct client for the Anthropic Messages API.
type ClaudeClient struct {
	credentialsPath string
	cachedToken     string
	tokenExpiresAt  int64 // unix ms
	mu              sync.Mutex
	httpClient      *http.Client
	logger          *slog.Logger
}

// ClaudeOption configures a ClaudeClient.
type ClaudeOption func(*ClaudeClient)

// WithCredentialsPath sets the path to the credentials file.
func WithCredentialsPath(path string) ClaudeOption {
	return func(c *ClaudeClient) {
		c.credentialsPath = path
	}
}

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(client *http.Client) ClaudeOption {
	return func(c *ClaudeClient) {
		c.httpClient = client
	}
}

// WithLogger sets the logger.
func WithLogger(logger *slog.Logger) ClaudeOption {
	return func(c *ClaudeClient) {
		c.logger = logger
	}
}

// NewClaudeClient creates a new Claude API client.
func NewClaudeClient(opts ...ClaudeOption) *ClaudeClient {
	c := &ClaudeClient{
		credentialsPath: defaultCredentialsPath(),
		httpClient:      &http.Client{Timeout: 5 * time.Minute},
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.logger.Info("claude client created", "credentials_path", c.credentialsPath)
	return c
}

func defaultCredentialsPath() string {
	if p := os.Getenv("CLAUDE_CREDENTIALS_PATH"); p != "" {
		return p
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = ""
	}
	return home + defaultCredentialPath
}

// ─── Token Management ────────────────────────────────────────────────────────

// refreshToken reads the credentials file and updates the cached token.
// Thread-safe: caller must hold c.mu or call via getToken().
func (c *ClaudeClient) refreshToken() error {
	data, err := os.ReadFile(c.credentialsPath)
	if err != nil {
		return fmt.Errorf("read credentials: %w", err)
	}

	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("parse credentials: %w", err)
	}

	if creds.ClaudeAiOauth.AccessToken == "" {
		return fmt.Errorf("no access token in credentials file")
	}

	c.cachedToken = creds.ClaudeAiOauth.AccessToken
	c.tokenExpiresAt = creds.ClaudeAiOauth.ExpiresAt
	c.logger.Debug("token refreshed from file",
		"expires_at", time.UnixMilli(c.tokenExpiresAt).Format(time.RFC3339),
	)
	return nil
}

// getToken returns a valid access token.
// Priority: CLAUDE_API_TOKEN env var > credentials file.
func (c *ClaudeClient) getToken() (string, error) {
	// 1. Check env var first (setup-token, long-lived, no refresh needed)
	if envToken := os.Getenv("CLAUDE_API_TOKEN"); envToken != "" {
		return envToken, nil
	}

	// 2. Fall back to credentials file
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UnixMilli()
	bufferMs := int64(tokenRefreshBuffer / time.Millisecond)

	if c.cachedToken != "" && c.tokenExpiresAt > now+bufferMs {
		return c.cachedToken, nil
	}

	if err := c.refreshToken(); err != nil {
		if c.cachedToken != "" {
			c.logger.Warn("token refresh failed, using cached", "err", err)
			return c.cachedToken, nil
		}
		return "", err
	}

	return c.cachedToken, nil
}

// ─── API Call ────────────────────────────────────────────────────────────────

// apiRequest is the JSON body sent to the Claude API.
type apiRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	MaxTurns  int             `json:"maxTurns,omitempty"` // bridge field: max agent turns
	System    []apiTextBlock  `json:"system,omitempty"`
	Messages  []ChatMessage   `json:"messages"`
	Tools     []ChatToolDef   `json:"tools,omitempty"`
}

type apiTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// apiResponse is the raw JSON response from the Claude API.
type apiResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      ChatUsage      `json:"usage"`
}

// apiError is the error response from the Claude API.
type apiError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Send sends a request to the Claude Messages API and returns the response.
func (c *ClaudeClient) Send(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	token, err := c.getToken()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	model := req.Model
	if model == "" {
		model = "claude-opus-4-6"
	}

	// Build API request body
	apiReq := apiRequest{
		Model:     model,
		MaxTokens: maxTokens,
		MaxTurns:  req.MaxTurns,
		Messages:  req.Messages,
		Tools:     req.Tools,
	}

	if req.SystemPrompt != "" {
		apiReq.System = []apiTextBlock{
			{Type: "text", Text: req.SystemPrompt},
		}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	c.logger.Debug("sending claude API request",
		"model", model,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"body_size", len(body),
	)

	// Build HTTP request
	apiURL := claudeAPIURL + "?beta=true"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+token)
	httpReq.Header.Set("anthropic-version", claudeAPIVersion)
	httpReq.Header.Set("anthropic-beta", claudeBetaHeaders)
	httpReq.Header.Set("user-agent", claudeUserAgent)

	// Execute
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Handle rate limit: fall back to next model in chain.
	// opus → sonnet → haiku
	if resp.StatusCode == http.StatusTooManyRequests {
		fallbacks := map[string]string{
			"claude-opus-4-6":          "claude-sonnet-4-6",
			"claude-sonnet-4-6":        "claude-haiku-4-5-20251001",
		}
		if next, ok := fallbacks[model]; ok {
			c.logger.Warn("rate limited, falling back to next model",
				"original_model", model,
				"fallback_model", next,
			)
			req.Model = next
			req.tokenRefreshed = false // allow refresh attempt with new model
			return c.Send(ctx, req)
		}
	}

	// Handle errors
	if resp.StatusCode != http.StatusOK {
		var apiErr apiError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("claude API error (%d): %s: %s",
				resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("claude API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	c.logger.Info("claude API response",
		"model", apiResp.Model,
		"stop_reason", apiResp.StopReason,
		"input_tokens", apiResp.Usage.InputTokens,
		"output_tokens", apiResp.Usage.OutputTokens,
		"content_blocks", len(apiResp.Content),
	)

	return &ChatResponse{
		Content:    apiResp.Content,
		StopReason: apiResp.StopReason,
		Usage:      apiResp.Usage,
		Model:      apiResp.Model,
	}, nil
}

// ConvertToolDefs converts prometheus.ToolDefinitions to Claude API format.
func ConvertToolDefs(defs []ToolDefinition) []ChatToolDef {
	result := make([]ChatToolDef, len(defs))
	for i, d := range defs {
		result[i] = ChatToolDef{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Parameters,
		}
	}
	return result
}

// NewChatToolDef is a convenience constructor for building tool definitions directly.
func NewChatToolDef(name, description string, inputSchema json.RawMessage) ChatToolDef {
	return ChatToolDef{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
	}
}

// ─── SSE Streaming ───────────────────────────────────────────────────────────

// SSEEventHandler is invoked for each SSE event received from the bridge.
// event is the SSE event name (text|tool_start|tool_result|done|error).
// data is the raw JSON payload of the event.
type SSEEventHandler func(event string, data json.RawMessage) error

// sseDoneEvent is the payload of the "done" SSE event.
type sseDoneEvent struct {
	Text  string    `json:"text"`
	Usage ChatUsage `json:"usage"`
	Cost  float64   `json:"cost"`
	Model string    `json:"model"`
}

// sseTextEvent is the payload of the "text" SSE event.
type sseTextEvent struct {
	Text string `json:"text"`
}

// SendStreaming sends a request to the bridge with Accept: text/event-stream
// and parses the SSE stream. It invokes onEvent for each event and returns the
// final ChatResponse assembled from accumulated text events plus the "done"
// event payload.
//
// Use this when you need real-time tool_use / tool_result notifications. For
// non-streaming use cases, prefer Send.
func (c *ClaudeClient) SendStreaming(ctx context.Context, req ChatRequest, onEvent SSEEventHandler) (*ChatResponse, error) {
	token, err := c.getToken()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	model := req.Model
	if model == "" {
		model = "claude-opus-4-6"
	}

	apiReq := apiRequest{
		Model:     model,
		MaxTokens: maxTokens,
		MaxTurns:  req.MaxTurns,
		Messages:  req.Messages,
		Tools:     req.Tools,
	}

	if req.SystemPrompt != "" {
		apiReq.System = []apiTextBlock{
			{Type: "text", Text: req.SystemPrompt},
		}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	c.logger.Debug("sending claude API streaming request",
		"model", model,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
		"body_size", len(body),
	)

	apiURL := claudeAPIURL + "?beta=true"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+token)
	httpReq.Header.Set("anthropic-version", claudeAPIVersion)
	httpReq.Header.Set("anthropic-beta", claudeBetaHeaders)
	httpReq.Header.Set("user-agent", claudeUserAgent)
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		// Rate limit fallback
		if resp.StatusCode == http.StatusTooManyRequests {
			fallbacks := map[string]string{
				"claude-opus-4-6":   "claude-sonnet-4-6",
				"claude-sonnet-4-6": "claude-haiku-4-5-20251001",
			}
			if next, ok := fallbacks[model]; ok {
				c.logger.Warn("rate limited (sse), falling back",
					"original_model", model,
					"fallback_model", next,
				)
				req.Model = next
				return c.SendStreaming(ctx, req, onEvent)
			}
		}
		var apiErr apiError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("claude API error (%d): %s: %s",
				resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("claude API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Parse SSE stream
	scanner := bufio.NewScanner(resp.Body)
	// 1MB max line buffer (some tool results can be large)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		curEvent     string
		curDataLines []string
		accumText    strings.Builder
		finalUsage   ChatUsage
		finalModel   string
		gotDone      bool
	)

	flushEvent := func() {
		if curEvent == "" && len(curDataLines) == 0 {
			return
		}
		dataStr := strings.Join(curDataLines, "\n")
		curDataLines = curDataLines[:0]
		ev := curEvent
		curEvent = ""
		if dataStr == "" {
			return
		}
		raw := json.RawMessage(dataStr)

		c.logger.Debug("sse event", "event", ev, "size", len(dataStr))

		switch ev {
		case "text":
			var t sseTextEvent
			if err := json.Unmarshal(raw, &t); err != nil {
				c.logger.Warn("sse: parse text event failed", "err", err)
			} else {
				accumText.WriteString(t.Text)
			}
		case "done":
			var d sseDoneEvent
			if err := json.Unmarshal(raw, &d); err != nil {
				c.logger.Warn("sse: parse done event failed", "err", err)
			} else {
				finalUsage = d.Usage
				finalModel = d.Model
				if d.Text != "" && accumText.Len() == 0 {
					// Bridge may also send full text in done payload as fallback
					accumText.WriteString(d.Text)
				}
				gotDone = true
			}
		}

		// Forward all events (including text/done) to the handler so callers
		// can react in real time. Errors from handler are logged but
		// non-fatal — we keep streaming.
		if onEvent != nil {
			if err := onEvent(ev, raw); err != nil {
				c.logger.Warn("sse: handler error", "event", ev, "err", err)
			}
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line = end of event
		if line == "" {
			flushEvent()
			continue
		}

		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			curDataLines = append(curDataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment, ignore
			continue
		}
		c.logger.Debug("sse: unrecognized line", "line", line)
	}
	// Flush any trailing event
	flushEvent()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sse read: %w", err)
	}

	if !gotDone {
		c.logger.Warn("sse stream ended without done event",
			"text_len", accumText.Len(),
		)
	}

	if finalModel == "" {
		finalModel = model
	}

	c.logger.Info("claude API sse response",
		"model", finalModel,
		"input_tokens", finalUsage.InputTokens,
		"output_tokens", finalUsage.OutputTokens,
		"text_len", accumText.Len(),
	)

	return &ChatResponse{
		Content: []ContentBlock{
			{Type: "text", Text: accumText.String()},
		},
		StopReason: "end_turn",
		Usage:      finalUsage,
		Model:      finalModel,
	}, nil
}
