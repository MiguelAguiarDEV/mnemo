// Package tools implements the tool interface, dispatcher, and tool definitions
// for the JARVIS orchestrator tool system.
package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Tool is the interface that all JARVIS tools must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage // JSON Schema
	Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

// ToolDefinition is a serializable representation of a tool for LLM consumption.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Dispatcher manages tool registration and dispatch.
type Dispatcher struct {
	tools  map[string]Tool
	logger *slog.Logger
}

// NewDispatcher creates a new Dispatcher with the given logger.
func NewDispatcher(logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	slog.Debug("dispatcher created")
	return &Dispatcher{
		tools:  make(map[string]Tool),
		logger: logger,
	}
}

// Register adds a tool to the dispatcher. If a tool with the same name
// already exists, it is overwritten.
func (d *Dispatcher) Register(tool Tool) {
	d.tools[tool.Name()] = tool
	d.logger.Info("tool registered", "tool", tool.Name())
}

// Dispatch executes a tool by name with the given parameters.
// Returns an error if the tool is not found.
func (d *Dispatcher) Dispatch(ctx context.Context, name string, params json.RawMessage) (ToolResult, error) {
	d.logger.Info("dispatching tool", "tool", name)

	tool, ok := d.tools[name]
	if !ok {
		d.logger.Warn("unknown tool requested", "tool", name)
		return ToolResult{}, fmt.Errorf("unknown tool: %s", name)
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		d.logger.Error("tool execution failed", "tool", name, "err", err)
		return result, err
	}

	d.logger.Debug("tool executed successfully", "tool", name, "is_error", result.IsError)
	return result, nil
}

// ToolDefinitions returns the definitions of all registered tools.
func (d *Dispatcher) ToolDefinitions() []ToolDefinition {
	d.logger.Debug("generating tool definitions", "count", len(d.tools))
	defs := make([]ToolDefinition, 0, len(d.tools))
	for _, tool := range d.tools {
		defs = append(defs, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}
	return defs
}

// ToolDefinitionsBlock returns a formatted text block of tool definitions
// suitable for injection into system prompts.
func (d *Dispatcher) ToolDefinitionsBlock() string {
	defs := d.ToolDefinitions()
	if len(defs) == 0 {
		d.logger.Debug("no tool definitions to render")
		return ""
	}

	var b strings.Builder
	b.WriteString("## Available Tools\n\n")
	b.WriteString("You can call these tools by outputting JSON:\n")
	b.WriteString("```json\n{\"tool\": \"<name>\", \"args\": {<parameters>}}\n```\n\n")

	for _, def := range defs {
		b.WriteString(fmt.Sprintf("### %s\n%s\n\n", def.Name, def.Description))
	}

	d.logger.Debug("tool definitions block generated", "tools", len(defs))
	return b.String()
}
