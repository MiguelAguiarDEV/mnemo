package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// FileWriteTool writes content to a file, creating parent directories as needed.
type FileWriteTool struct {
	logger *slog.Logger
}

// NewFileWriteTool creates a FileWriteTool.
func NewFileWriteTool(logger *slog.Logger) *FileWriteTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileWriteTool{logger: logger}
}

func (t *FileWriteTool) Name() string { return "write" }

func (t *FileWriteTool) Description() string {
	return "Write content to a file. Creates parent directories if needed. Overwrites existing files."
}

func (t *FileWriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Absolute path to the file to write"
			},
			"content": {
				"type": "string",
				"description": "The content to write to the file"
			}
		},
		"required": ["file_path", "content"]
	}`)
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *FileWriteTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	t.logger.Info("write: writing file", "path", in.FilePath, "content_len", len(in.Content))

	// Create parent directories.
	dir := filepath.Dir(in.FilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}

	// Write atomically: write to temp file then rename.
	tmpPath := in.FilePath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(in.Content), 0o644); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, in.FilePath); err != nil {
		// Clean up temp file on rename failure.
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename temp file: %w", err)
	}

	result := fmt.Sprintf("File written: %s (%d bytes)", in.FilePath, len(in.Content))
	t.logger.Info("write: file written", "path", in.FilePath, "bytes", len(in.Content))
	return result, nil
}
