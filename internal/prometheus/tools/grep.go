package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	grepDefaultLimit   = 250
	grepMaxLineLength  = 500
)

// vcsDirs are version control directories excluded from grep.
var vcsDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	".bzr": true, ".jj": true, ".sl": true,
}

// GrepTool searches file contents using regex patterns.
type GrepTool struct {
	logger *slog.Logger
}

// NewGrepTool creates a GrepTool.
func NewGrepTool(logger *slog.Logger) *GrepTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &GrepTool{logger: logger}
}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "Search file contents using a regex pattern. Returns matching lines with file paths and line numbers."
}

func (t *GrepTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Regex pattern to search for"
			},
			"path": {
				"type": "string",
				"description": "File or directory to search in (defaults to current directory)"
			},
			"include": {
				"type": "string",
				"description": "Glob pattern to filter files (e.g. '*.go', '*.{ts,tsx}')"
			}
		},
		"required": ["pattern"]
	}`)
}

type grepInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Include string `json:"include,omitempty"`
}

func (t *GrepTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in grepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	searchPath := in.Path
	if searchPath == "" {
		searchPath = "."
	}

	t.logger.Info("grep: searching", "pattern", in.Pattern, "path", searchPath, "include", in.Include)

	// Parse include patterns into a list for matching.
	var includePatterns []string
	if in.Include != "" {
		// Handle patterns like "*.{ts,tsx}" by expanding braces.
		includePatterns = expandBracePattern(in.Include)
	}

	var results []string

	info, err := os.Stat(searchPath)
	if err != nil {
		return "", fmt.Errorf("path not found: %w", err)
	}

	if !info.IsDir() {
		// Search a single file.
		matches, searchErr := searchFile(searchPath, re)
		if searchErr != nil {
			return "", searchErr
		}
		results = matches
	} else {
		// Walk directory.
		_ = filepath.WalkDir(searchPath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}

			// Skip VCS and excluded directories.
			if d.IsDir() {
				if vcsDirs[d.Name()] || excludedDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}

			// Apply include filter.
			if len(includePatterns) > 0 {
				matched := false
				for _, pat := range includePatterns {
					if m, _ := filepath.Match(pat, d.Name()); m {
						matched = true
						break
					}
				}
				if !matched {
					return nil
				}
			}

			// Search file.
			matches, searchErr := searchFile(path, re)
			if searchErr != nil {
				return nil // Skip files that can't be read.
			}
			results = append(results, matches...)

			// Stop early if we have enough results.
			if len(results) > grepDefaultLimit {
				return filepath.SkipAll
			}

			return nil
		})
	}

	// Apply limit.
	truncated := false
	if len(results) > grepDefaultLimit {
		results = results[:grepDefaultLimit]
		truncated = true
	}

	var b strings.Builder
	for _, r := range results {
		b.WriteString(r)
		b.WriteByte('\n')
	}

	if truncated {
		fmt.Fprintf(&b, "\n(results truncated at %d matches)", grepDefaultLimit)
	}

	if len(results) == 0 {
		b.WriteString("(no matches found)")
	}

	t.logger.Info("grep: results", "matches", len(results), "truncated", truncated)
	return b.String(), nil
}

// searchFile searches a single file for regex matches and returns formatted results.
func searchFile(path string, re *regexp.Regexp) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []string
	scanner := bufio.NewScanner(f)

	// Increase scanner buffer for long lines.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if re.MatchString(line) {
			// Cap line length.
			displayLine := line
			if len(displayLine) > grepMaxLineLength {
				displayLine = displayLine[:grepMaxLineLength] + "..."
			}
			results = append(results, fmt.Sprintf("%s:%d:%s", path, lineNum, displayLine))
		}
	}

	return results, scanner.Err()
}

// expandBracePattern expands patterns like "*.{ts,tsx}" into ["*.ts", "*.tsx"].
func expandBracePattern(pattern string) []string {
	start := strings.IndexByte(pattern, '{')
	end := strings.IndexByte(pattern, '}')

	if start == -1 || end == -1 || end < start {
		return []string{pattern}
	}

	prefix := pattern[:start]
	suffix := pattern[end+1:]
	alternatives := strings.Split(pattern[start+1:end], ",")

	var result []string
	for _, alt := range alternatives {
		result = append(result, prefix+strings.TrimSpace(alt)+suffix)
	}
	return result
}
