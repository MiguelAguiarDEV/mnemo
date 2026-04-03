package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

const (
	defaultReadLimit  = 2000
	maxReadOutputSize = 100 * 1024 // 100KB
)

// blockedDevices are paths that should never be read (would hang or leak data).
var blockedDevices = map[string]bool{
	"/dev/zero": true, "/dev/random": true, "/dev/urandom": true,
	"/dev/full": true, "/dev/stdin": true, "/dev/tty": true,
	"/dev/console": true, "/dev/stdout": true, "/dev/stderr": true,
	"/dev/null": true,
	"/proc/self/fd/0": true, "/proc/self/fd/1": true, "/proc/self/fd/2": true,
}

// FileReadTool reads file contents with optional offset and limit.
type FileReadTool struct {
	logger *slog.Logger
}

// NewFileReadTool creates a FileReadTool.
func NewFileReadTool(logger *slog.Logger) *FileReadTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileReadTool{logger: logger}
}

func (t *FileReadTool) Name() string { return "read" }

func (t *FileReadTool) Description() string {
	return "Read a file from the filesystem. Returns contents with line numbers."
}

func (t *FileReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_path": {
				"type": "string",
				"description": "Absolute path to the file to read"
			},
			"offset": {
				"type": "integer",
				"description": "Line number to start reading from (1-indexed)"
			},
			"limit": {
				"type": "integer",
				"description": "Number of lines to read (default 2000)"
			}
		},
		"required": ["file_path"]
	}`)
}

type readInput struct {
	FilePath string `json:"file_path"`
	Offset   *int   `json:"offset,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

func (t *FileReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	// Block device files.
	if blockedDevices[in.FilePath] || strings.HasPrefix(in.FilePath, "/dev/") {
		t.logger.Warn("read: blocked device access", "path", in.FilePath)
		return "", fmt.Errorf("cannot read device file: %s", in.FilePath)
	}

	t.logger.Info("read: reading file", "path", in.FilePath)

	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// Apply offset (1-indexed).
	offset := 0
	if in.Offset != nil && *in.Offset > 0 {
		offset = *in.Offset - 1 // Convert to 0-indexed.
		if offset >= len(lines) {
			return fmt.Sprintf("(offset %d exceeds file length of %d lines)", *in.Offset, len(lines)), nil
		}
	}

	// Apply limit.
	limit := defaultReadLimit
	if in.Limit != nil && *in.Limit > 0 {
		limit = *in.Limit
	}

	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}

	// Build output with line numbers.
	var b strings.Builder
	for i := offset; i < end; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, lines[i])
	}

	output := b.String()

	// Truncate if too large.
	if len(output) > maxReadOutputSize {
		output = output[:maxReadOutputSize] + "\n... (output truncated at 100KB)"
	}

	// Add metadata if partial read.
	if end < len(lines) {
		output += fmt.Sprintf("\n(showing lines %d-%d of %d total)", offset+1, end, len(lines))
	}

	t.logger.Info("read: file read", "path", in.FilePath, "lines", end-offset, "total", len(lines))
	return output, nil
}
