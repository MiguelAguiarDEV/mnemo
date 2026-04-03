package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ─── ReadFileTool ────────────────────────────────────────────────────────────

// ReadFileTool reads file content with optional line-based offset and limit.
type ReadFileTool struct {
	validator *PathValidator
}

// NewReadFileTool creates a ReadFileTool with the given path validator.
func NewReadFileTool(validator *PathValidator) *ReadFileTool {
	slog.Debug("read_file: tool created")
	return &ReadFileTool{validator: validator}
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string { return "Read file content. Supports optional offset and limit for reading specific line ranges." }
func (t *ReadFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path to read"},"offset":{"type":"integer","description":"Starting line number (0-based)"},"limit":{"type":"integer","description":"Max number of lines to read"}},"required":["path"]}`)
}

func (t *ReadFileTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Path   string `json:"path"`
		Offset *int   `json:"offset"`
		Limit  *int   `json:"limit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("read_file: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Path == "" {
		slog.Warn("read_file: missing path")
		return ToolResult{Content: "missing required parameter: path", IsError: true}, nil
	}

	resolvedPath, err := t.validator.Validate(p.Path)
	if err != nil {
		slog.Error("read_file: path validation failed", "path", p.Path, "err", err)
		return ToolResult{Content: err.Error(), IsError: true}, nil
	}

	slog.Info("read_file: reading", "path", resolvedPath)

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("read_file: file not found", "path", resolvedPath)
			return ToolResult{Content: fmt.Sprintf("file not found: %s", resolvedPath), IsError: true}, nil
		}
		if os.IsPermission(err) {
			slog.Error("read_file: permission denied", "path", resolvedPath)
			return ToolResult{Content: fmt.Sprintf("permission denied: %s", resolvedPath), IsError: true}, nil
		}
		slog.Error("read_file: failed to read", "path", resolvedPath, "err", err)
		return ToolResult{Content: "failed to read file: " + err.Error(), IsError: true}, nil
	}

	content := string(data)

	// Apply offset/limit if specified.
	if p.Offset != nil || p.Limit != nil {
		lines := strings.Split(content, "\n")
		offset := 0
		if p.Offset != nil {
			offset = *p.Offset
			if offset < 0 {
				offset = 0
			}
			if offset >= len(lines) {
				slog.Debug("read_file: offset beyond file length", "offset", offset, "lines", len(lines))
				return ToolResult{Content: ""}, nil
			}
		}

		limit := len(lines) - offset
		if p.Limit != nil && *p.Limit > 0 && *p.Limit < limit {
			limit = *p.Limit
		}

		end := offset + limit
		if end > len(lines) {
			end = len(lines)
		}

		slog.Debug("read_file: applying offset/limit", "offset", offset, "limit", limit, "total_lines", len(lines))
		content = strings.Join(lines[offset:end], "\n")
	}

	slog.Info("read_file: success", "path", resolvedPath, "bytes", len(content))
	return ToolResult{Content: content}, nil
}

// ─── WriteFileTool ───────────────────────────────────────────────────────────

// WriteFileTool creates or overwrites a file, creating parent directories as needed.
type WriteFileTool struct {
	validator *PathValidator
}

// NewWriteFileTool creates a WriteFileTool with the given path validator.
func NewWriteFileTool(validator *PathValidator) *WriteFileTool {
	slog.Debug("write_file: tool created")
	return &WriteFileTool{validator: validator}
}

func (t *WriteFileTool) Name() string        { return "write_file" }
func (t *WriteFileTool) Description() string { return "Create or overwrite a file. Creates parent directories if needed." }
func (t *WriteFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path to write"},"content":{"type":"string","description":"Content to write"}},"required":["path","content"]}`)
}

func (t *WriteFileTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("write_file: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Path == "" {
		slog.Warn("write_file: missing path")
		return ToolResult{Content: "missing required parameter: path", IsError: true}, nil
	}

	resolvedPath, err := t.validator.Validate(p.Path)
	if err != nil {
		slog.Error("write_file: path validation failed", "path", p.Path, "err", err)
		return ToolResult{Content: err.Error(), IsError: true}, nil
	}

	// Check if file already exists (for logging).
	if _, statErr := os.Stat(resolvedPath); statErr == nil {
		slog.Warn("write_file: overwriting existing file", "path", resolvedPath)
	}

	// Create parent directories.
	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("write_file: failed to create parent dirs", "dir", dir, "err", err)
		return ToolResult{Content: "failed to create directories: " + err.Error(), IsError: true}, nil
	}

	slog.Info("write_file: writing", "path", resolvedPath, "bytes", len(p.Content))
	if err := os.WriteFile(resolvedPath, []byte(p.Content), 0o644); err != nil {
		slog.Error("write_file: failed to write", "path", resolvedPath, "err", err)
		return ToolResult{Content: "failed to write file: " + err.Error(), IsError: true}, nil
	}

	slog.Info("write_file: success", "path", resolvedPath, "bytes", len(p.Content))
	return ToolResult{Content: fmt.Sprintf("wrote %d bytes to %s", len(p.Content), resolvedPath)}, nil
}

// ─── EditFileTool ────────────────────────────────────────────────────────────

// EditFileTool performs string replacement in a file.
type EditFileTool struct {
	validator *PathValidator
}

// NewEditFileTool creates an EditFileTool with the given path validator.
func NewEditFileTool(validator *PathValidator) *EditFileTool {
	slog.Debug("edit_file: tool created")
	return &EditFileTool{validator: validator}
}

func (t *EditFileTool) Name() string        { return "edit_file" }
func (t *EditFileTool) Description() string { return "Edit a file by replacing a string. Fails if old_string is not found or not unique (unless replace_all is true)." }
func (t *EditFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path to edit"},"old_string":{"type":"string","description":"String to find and replace"},"new_string":{"type":"string","description":"Replacement string"},"replace_all":{"type":"boolean","description":"Replace all occurrences (default: false)"}},"required":["path","old_string","new_string"]}`)
}

func (t *EditFileTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll *bool  `json:"replace_all"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("edit_file: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Path == "" {
		slog.Warn("edit_file: missing path")
		return ToolResult{Content: "missing required parameter: path", IsError: true}, nil
	}

	resolvedPath, err := t.validator.Validate(p.Path)
	if err != nil {
		slog.Error("edit_file: path validation failed", "path", p.Path, "err", err)
		return ToolResult{Content: err.Error(), IsError: true}, nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("edit_file: file not found", "path", resolvedPath)
			return ToolResult{Content: fmt.Sprintf("file not found: %s", resolvedPath), IsError: true}, nil
		}
		slog.Error("edit_file: failed to read", "path", resolvedPath, "err", err)
		return ToolResult{Content: "failed to read file: " + err.Error(), IsError: true}, nil
	}

	content := string(data)
	count := strings.Count(content, p.OldString)

	if count == 0 {
		slog.Error("edit_file: old_string not found", "path", resolvedPath)
		return ToolResult{Content: "old_string not found in file", IsError: true}, nil
	}

	replaceAll := p.ReplaceAll != nil && *p.ReplaceAll

	if count > 1 && !replaceAll {
		slog.Error("edit_file: old_string not unique", "path", resolvedPath, "count", count)
		return ToolResult{Content: fmt.Sprintf("old_string found %d times (not unique). Use replace_all=true to replace all occurrences", count), IsError: true}, nil
	}

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, p.OldString, p.NewString)
	} else {
		newContent = strings.Replace(content, p.OldString, p.NewString, 1)
	}

	if err := os.WriteFile(resolvedPath, []byte(newContent), 0o644); err != nil {
		slog.Error("edit_file: failed to write", "path", resolvedPath, "err", err)
		return ToolResult{Content: "failed to write file: " + err.Error(), IsError: true}, nil
	}

	slog.Info("edit_file: success", "path", resolvedPath, "replacements", count)
	return ToolResult{Content: fmt.Sprintf("replaced %d occurrence(s) in %s", count, resolvedPath)}, nil
}

// ─── Compile-time interface checks ───────────────────────────────────────────

var (
	_ Tool = (*ReadFileTool)(nil)
	_ Tool = (*WriteFileTool)(nil)
	_ Tool = (*EditFileTool)(nil)
)
