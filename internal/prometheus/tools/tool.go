// Package tools implements Go-native tool executors for the PROMETHEUS worker.
// These tools replace the OpenCode runtime, allowing workers to execute
// bash commands, read/write/edit files, and search the filesystem directly.
package tools

import (
	"context"
	"encoding/json"
	"log/slog"
)

// ToolExecutor is the interface for a tool that can be executed by the worker.
type ToolExecutor interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolDef is a serializable tool definition for API consumption.
// This avoids importing the prometheus package (which would create a cycle).
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Registry manages tool registration and lookup.
type Registry struct {
	tools  map[string]ToolExecutor
	logger *slog.Logger
}

// NewRegistry creates a Registry with all standard tools for the given working directory.
func NewRegistry(workingDir string, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Registry{
		tools:  make(map[string]ToolExecutor),
		logger: logger,
	}

	// Register all standard tools.
	r.Register(NewBashTool(workingDir, logger))
	r.Register(NewFileReadTool(logger))
	r.Register(NewFileWriteTool(logger))
	r.Register(NewFileEditTool(logger))
	r.Register(NewGlobTool(logger))
	r.Register(NewGrepTool(logger))

	logger.Info("tool registry created", "tools", len(r.tools), "working_dir", workingDir)
	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool ToolExecutor) {
	r.tools[tool.Name()] = tool
	r.logger.Debug("tool registered", "name", tool.Name())
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (ToolExecutor, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// All returns all registered tools.
func (r *Registry) All() []ToolExecutor {
	result := make([]ToolExecutor, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToolDefs returns all registered tools as serializable definitions.
func (r *Registry) ToolDefs() []ToolDef {
	result := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return result
}
