package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// OpenCodeBackend implements Backend for the OpenCode serve HTTP API.
type OpenCodeBackend struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewOpenCodeBackend creates a new OpenCode backend.
func NewOpenCodeBackend(baseURL, username, password string, timeout time.Duration) *OpenCodeBackend {
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	slog.Info("opencode backend created", "url", baseURL)
	return &OpenCodeBackend{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: slog.Default(),
	}
}

// openCodePayload is the request body for OpenCode serve.
type openCodePayload struct {
	Parts []openCodePart    `json:"parts"`
	Model openCodeModelSpec `json:"model"`
}

type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openCodeModelSpec struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// openCodeResponse is the JSON response from OpenCode serve.
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

// CreateSession creates a new OpenCode session and returns the session ID.
func (b *OpenCodeBackend) CreateSession(ctx context.Context) (string, error) {
	b.logger.Debug("creating opencode session")

	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+"/session", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		b.logger.Error("failed to create session request", "err", err)
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if b.password != "" {
		req.SetBasicAuth(b.username, b.password)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		b.logger.Error("opencode session request failed", "err", err)
		return "", fmt.Errorf("opencode serve unreachable at %s: %w", b.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b.logger.Error("opencode session unexpected status", "status", resp.StatusCode)
		return "", fmt.Errorf("opencode create session: status %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		b.logger.Error("failed to decode session response", "err", err)
		return "", err
	}

	b.logger.Info("opencode session created", "id", result.ID)
	return result.ID, nil
}

// Send sends a request to OpenCode serve and returns the response.
func (b *OpenCodeBackend) Send(ctx context.Context, r Request) (Response, error) {
	b.logger.Debug("sending request to opencode",
		"session_id", r.SessionID,
		"provider", r.Model.ProviderID,
		"model", r.Model.ModelID,
	)

	// Build the full prompt: system prompt + messages
	var prompt strings.Builder
	if r.SystemPrompt != "" {
		prompt.WriteString(r.SystemPrompt)
		prompt.WriteString("\n\n")
	}
	for _, msg := range r.Messages {
		if msg.Role == "user" {
			prompt.WriteString("[User]\n")
		} else {
			prompt.WriteString("[Assistant]\n")
		}
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n\n")
	}

	payload := openCodePayload{
		Parts: []openCodePart{{Type: "text", Text: prompt.String()}},
		Model: openCodeModelSpec{
			ProviderID: r.Model.ProviderID,
			ModelID:    r.Model.ModelID,
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		b.logger.Error("failed to marshal request payload", "err", err)
		return Response{}, fmt.Errorf("marshal request: %w", err)
	}

	url := b.baseURL + "/session/" + r.SessionID + "/message"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		b.logger.Error("failed to create HTTP request", "err", err)
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if b.password != "" {
		httpReq.SetBasicAuth(b.username, b.password)
	}

	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		b.logger.Error("opencode request failed", "err", err)
		return Response{}, fmt.Errorf("opencode prompt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b.logger.Error("opencode unexpected status", "status", resp.StatusCode)
		return Response{}, fmt.Errorf("opencode prompt: status %d (model: %s/%s)",
			resp.StatusCode, r.Model.ProviderID, r.Model.ModelID)
	}

	var ocResp openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ocResp); err != nil {
		b.logger.Error("failed to decode opencode response", "err", err)
		return Response{}, fmt.Errorf("opencode: decode response: %w", err)
	}

	// Extract text and tool calls from parts
	var fullText strings.Builder
	var toolCalls []ToolCall

	for _, part := range ocResp.Parts {
		if part.Type == "text" && part.Text != "" {
			fullText.WriteString(part.Text)
		}
	}

	// Parse tool calls from response text
	// Tool calls appear as JSON blocks: {"tool_call": {"name": "...", "arguments": {...}}}
	text := fullText.String()
	toolCalls = ParseToolCalls(text)

	b.logger.Debug("opencode response parsed",
		"text_len", len(text),
		"tool_calls", len(toolCalls),
		"tokens_in", ocResp.Info.Tokens.Input,
		"tokens_out", ocResp.Info.Tokens.Output,
	)

	return Response{
		Text:      text,
		ToolCalls: toolCalls,
		Usage: TokenUsage{
			InputTokens:  ocResp.Info.Tokens.Input,
			OutputTokens: ocResp.Info.Tokens.Output,
		},
	}, nil
}

// toolCallWrapper is used to parse tool call JSON from response text.
type toolCallWrapper struct {
	ToolCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"tool_call"`
}

// ParseToolCalls extracts tool call JSON blocks from response text.
// Exported so other packages (e.g. orchestrator) can reuse the same parsing logic.
func ParseToolCalls(text string) []ToolCall {
	slog.Debug("parsing tool calls from response text")
	var calls []ToolCall

	// Look for JSON objects containing "tool_call"
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}

		// Try to find matching closing brace
		depth := 0
		end := -1
		for j := i; j < len(text); j++ {
			if text[j] == '{' {
				depth++
			} else if text[j] == '}' {
				depth--
				if depth == 0 {
					end = j + 1
					break
				}
			}
		}
		if end == -1 {
			continue
		}

		candidate := text[i:end]
		var wrapper toolCallWrapper
		if err := json.Unmarshal([]byte(candidate), &wrapper); err == nil && wrapper.ToolCall.Name != "" {
			calls = append(calls, ToolCall{
				Name:      wrapper.ToolCall.Name,
				Arguments: wrapper.ToolCall.Arguments,
			})
			slog.Debug("tool call found", "name", wrapper.ToolCall.Name)
			i = end - 1 // skip past this JSON block
		}
	}

	return calls
}

// Compile-time check that OpenCodeBackend implements Backend.
var _ Backend = (*OpenCodeBackend)(nil)
