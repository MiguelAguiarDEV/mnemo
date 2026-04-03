// Package llm defines the LLM backend abstraction for the JARVIS orchestrator.
// It provides interfaces and types for communicating with LLM providers.
package prometheus

import (
	"context"
	"encoding/json"
)

// Backend is the interface for LLM provider implementations.
type Backend interface {
	// Send sends a request to the LLM and returns the response.
	Send(ctx context.Context, req Request) (Response, error)
	// CreateSession creates a new conversation session and returns the session ID.
	CreateSession(ctx context.Context) (string, error)
}

// Request represents a request to an LLM backend.
type Request struct {
	SessionID    string      `json:"session_id"`
	SystemPrompt string      `json:"system_prompt"`
	Messages     []Message   `json:"messages"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
	Model        ModelConfig `json:"model"`
}

// Response represents a response from an LLM backend.
type Response struct {
	Text      string      `json:"text"`
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
	Usage     TokenUsage  `json:"usage"`
}

// ToolCall represents a tool invocation requested by the LLM.
type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// TokenUsage tracks token consumption for a request.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ModelConfig describes the LLM model to use.
type ModelConfig struct {
	ProviderID     string  `json:"provider_id"`
	ModelID        string  `json:"model_id"`
	InputCostPer1K  float64 `json:"input_cost_per_1k"`
	OutputCostPer1K float64 `json:"output_cost_per_1k"`
}

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolDefinition describes a tool for the LLM. Mirrors the athena package's ToolDefinition
// but lives in the prometheus package to avoid circular imports.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Compile-time interface check is in opencode.go:
// var _ Backend = (*OpenCodeBackend)(nil)
