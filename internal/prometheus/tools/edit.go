package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// FileEditTool performs find-and-replace edits on files.
type FileEditTool struct {
	logger *slog.Logger
}

// NewFileEditTool creates a FileEditTool.
func NewFileEditTool(logger *slog.Logger) *FileEditTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileEditTool{logger: logger}
}

func (t *FileEditTool) Name() string { return "edit" }

func (t *FileEditTool) Description() string {
	return "Edit a file by replacing old_string with new_string. The old_string must be unique in the file."
}

func (t *FileEditTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Absolute path to the file to edit"
			},
			"old_string": {
				"type": "string",
				"description": "The text to find in the file"
			},
			"new_string": {
				"type": "string",
				"description": "The text to replace it with"
			},
			"replace_all": {
				"type": "boolean",
				"description": "Replace all occurrences (default false)"
			}
		},
		"required": ["file_path", "old_string", "new_string"]
	}`)
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *FileEditTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	if in.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	if in.OldString == in.NewString {
		return "", fmt.Errorf("old_string and new_string are identical")
	}

	t.logger.Info("edit: editing file", "path", in.FilePath,
		"old_len", len(in.OldString), "new_len", len(in.NewString))

	// Read current content.
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	// Count occurrences.
	count := strings.Count(content, in.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", in.FilePath)
	}
	if count > 1 && !in.ReplaceAll {
		return "", fmt.Errorf("old_string found %d times in %s (use replace_all: true to replace all, or provide more context to make the match unique)", count, in.FilePath)
	}

	// Perform replacement.
	var newContent string
	if in.ReplaceAll {
		newContent = strings.ReplaceAll(content, in.OldString, in.NewString)
	} else {
		newContent = strings.Replace(content, in.OldString, in.NewString, 1)
	}

	// Write atomically.
	dir := filepath.Dir(in.FilePath)
	tmpPath := filepath.Join(dir, ".edit-tmp-"+filepath.Base(in.FilePath))
	if err := os.WriteFile(tmpPath, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, in.FilePath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename temp file: %w", err)
	}

	replaced := count
	if !in.ReplaceAll {
		replaced = 1
	}

	result := fmt.Sprintf("Edited %s: replaced %d occurrence(s), %d chars -> %d chars",
		in.FilePath, replaced, len(in.OldString), len(in.NewString))
	t.logger.Info("edit: file edited", "path", in.FilePath, "replacements", replaced)
	return result, nil
}
