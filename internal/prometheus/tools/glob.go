package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const globResultLimit = 100

// excludedDirs are directories skipped during glob traversal.
var excludedDirs = map[string]bool{
	".git": true, "node_modules": true, ".next": true,
	"__pycache__": true, ".cache": true, ".terraform": true,
	"vendor": true, "dist": true, ".venv": true,
}

// GlobTool finds files matching a glob pattern.
type GlobTool struct {
	logger *slog.Logger
}

// NewGlobTool creates a GlobTool.
func NewGlobTool(logger *slog.Logger) *GlobTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobTool{logger: logger}
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern. Supports ** for recursive matching."
}

func (t *GlobTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Glob pattern to match files (e.g. '**/*.go', 'src/**/*.ts')"
			},
			"path": {
				"type": "string",
				"description": "Directory to search in (defaults to current directory)"
			}
		},
		"required": ["pattern"]
	}`)
}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func (t *GlobTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in globInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	searchDir := in.Path
	if searchDir == "" {
		searchDir = "."
	}

	t.logger.Info("glob: searching", "pattern", in.Pattern, "path", searchDir)

	var matches []string

	// Check if the pattern contains "**" for recursive matching.
	if strings.Contains(in.Pattern, "**") {
		matches = globRecursive(searchDir, in.Pattern)
	} else {
		// Simple glob.
		fullPattern := filepath.Join(searchDir, in.Pattern)
		m, err := filepath.Glob(fullPattern)
		if err != nil {
			return "", fmt.Errorf("glob error: %w", err)
		}
		matches = m
	}

	// Sort results.
	sort.Strings(matches)

	// Check for truncation.
	truncated := false
	if len(matches) > globResultLimit {
		matches = matches[:globResultLimit]
		truncated = true
	}

	var b strings.Builder
	for _, m := range matches {
		b.WriteString(m)
		b.WriteByte('\n')
	}

	if truncated {
		fmt.Fprintf(&b, "\n(results truncated at %d files — use a more specific pattern)", globResultLimit)
	}

	if len(matches) == 0 {
		b.WriteString("(no files matched)")
	}

	t.logger.Info("glob: results", "matches", len(matches), "truncated", truncated)
	return b.String(), nil
}

// globRecursive implements ** glob matching via filepath.WalkDir.
func globRecursive(root, pattern string) []string {
	var results []string

	// Split pattern on "**/" to get the suffix pattern.
	// Example: "**/*.go" -> suffix "*.go"
	// Example: "src/**/*.ts" -> prefix "src", suffix "*.ts"
	prefix := ""
	suffix := pattern

	if idx := strings.Index(pattern, "**/"); idx >= 0 {
		prefix = pattern[:idx]
		suffix = pattern[idx+3:]
	} else if pattern == "**" {
		suffix = "*"
	}

	searchRoot := root
	if prefix != "" {
		searchRoot = filepath.Join(root, prefix)
	}

	// Validate search root exists.
	info, err := os.Stat(searchRoot)
	if err != nil || !info.IsDir() {
		return nil
	}

	_ = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors.
		}

		// Skip excluded directories.
		if d.IsDir() && excludedDirs[d.Name()] {
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
		}

		// Check if this file matches the suffix pattern.
		matched, matchErr := filepath.Match(suffix, d.Name())
		if matchErr != nil {
			return nil
		}

		if matched {
			results = append(results, path)
			if len(results) > globResultLimit+1 {
				return filepath.SkipAll
			}
		}

		return nil
	})

	return results
}
