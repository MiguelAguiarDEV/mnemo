package athena

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ─── BashTool ────────────────────────────────────────────────────────────────

// BashToolConfig holds configuration for the BashTool.
type BashToolConfig struct {
	BlockedCommands []string // Commands that are never allowed (e.g., "rm -rf /")
}

// DefaultBlockedCommands returns the default blocklist of dangerous commands.
func DefaultBlockedCommands() []string {
	return []string{
		"rm -rf /",
		"rm -rf /*",
		"dd if=",
		"mkfs",
		":(){ :|:& };:",
		"> /dev/sda",
		"chmod -R 777 /",
		"chown -R",
		"shutdown",
		"reboot",
		"halt",
		"init 0",
		"init 6",
	}
}

// BashTool executes shell commands with timeout and command blocklist.
type BashTool struct {
	blockedCommands []string
}

// NewBashTool creates a BashTool with the given config.
func NewBashTool(cfg BashToolConfig) *BashTool {
	blocked := cfg.BlockedCommands
	if len(blocked) == 0 {
		blocked = DefaultBlockedCommands()
	}
	slog.Debug("bash: tool created", "blocked_commands", len(blocked))
	return &BashTool{blockedCommands: blocked}
}

func (t *BashTool) Name() string        { return "bash" }
func (t *BashTool) Description() string { return "Execute a shell command. Returns stdout+stderr. Default timeout 30s, max 300s." }
func (t *BashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"},"timeout":{"type":"integer","description":"Timeout in seconds (default 30, max 300)"}},"required":["command"]}`)
}

func (t *BashTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Command string `json:"command"`
		Timeout *int   `json:"timeout"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("bash: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Command == "" {
		slog.Warn("bash: missing command")
		return ToolResult{Content: "missing required parameter: command", IsError: true}, nil
	}

	// Check blocklist.
	cmdLower := strings.ToLower(p.Command)
	for _, blocked := range t.blockedCommands {
		if strings.Contains(cmdLower, strings.ToLower(blocked)) {
			slog.Warn("bash: blocked command", "command", truncate(p.Command, 100), "blocked", blocked)
			return ToolResult{Content: fmt.Sprintf("command blocked: contains dangerous pattern %q", blocked), IsError: true}, nil
		}
	}

	// Determine timeout.
	timeout := 30 * time.Second
	if p.Timeout != nil {
		t := *p.Timeout
		if t <= 0 {
			t = 30
		}
		if t > 300 {
			t = 300
		}
		timeout = time.Duration(t) * time.Second
	}

	slog.Info("bash: executing", "command", truncate(p.Command, 100), "timeout", timeout)

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", p.Command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if cmdCtx.Err() == context.DeadlineExceeded {
		slog.Warn("bash: command timed out", "command", truncate(p.Command, 100), "timeout", timeout)
		return ToolResult{Content: fmt.Sprintf("command timed out after %s\n%s", timeout, output), IsError: true}, nil
	}

	if err != nil {
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		slog.Error("bash: command failed", "command", truncate(p.Command, 100), "exit_code", exitCode)
		slog.Debug("bash: full output", "output", output)
		return ToolResult{Content: fmt.Sprintf("exit code %d\n%s", exitCode, output), IsError: true}, nil
	}

	slog.Debug("bash: full output", "output_bytes", len(output))
	slog.Info("bash: success", "command", truncate(p.Command, 100), "output_bytes", len(output))
	return ToolResult{Content: output}, nil
}

// ─── GrepTool ────────────────────────────────────────────────────────────────

// GrepTool searches for patterns in files using regex.
// Uses ripgrep (rg) if available, falls back to Go regex.
type GrepTool struct{}

// NewGrepTool creates a GrepTool.
func NewGrepTool() *GrepTool {
	slog.Debug("grep: tool created")
	return &GrepTool{}
}

func (t *GrepTool) Name() string        { return "grep" }
func (t *GrepTool) Description() string { return "Search for patterns in files using regex. Uses ripgrep if available." }
func (t *GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regex pattern to search for"},"path":{"type":"string","description":"Directory or file to search in (default: current dir)"},"glob":{"type":"string","description":"Glob pattern to filter files (e.g., *.go)"},"type":{"type":"string","description":"File type to search (e.g., go, py, js)"},"max_results":{"type":"integer","description":"Max results to return (default: 50)"}},"required":["pattern"]}`)
}

func (t *GrepTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		Type       string `json:"type"`
		MaxResults *int   `json:"max_results"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("grep: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Pattern == "" {
		slog.Warn("grep: missing pattern")
		return ToolResult{Content: "missing required parameter: pattern", IsError: true}, nil
	}

	maxResults := 50
	if p.MaxResults != nil && *p.MaxResults > 0 {
		maxResults = *p.MaxResults
	}

	searchPath := "."
	if p.Path != "" {
		searchPath = p.Path
	}

	slog.Info("grep: searching", "pattern", p.Pattern, "path", searchPath)

	// Try ripgrep first.
	rgPath, rgErr := exec.LookPath("rg")
	if rgErr == nil {
		return t.execRipgrep(ctx, rgPath, p.Pattern, searchPath, p.Glob, p.Type, maxResults)
	}

	slog.Debug("grep: ripgrep not found, using Go regex fallback")
	return t.goGrep(p.Pattern, searchPath, p.Glob, maxResults)
}

func (t *GrepTool) execRipgrep(ctx context.Context, rgPath, pattern, searchPath, glob, fileType string, maxResults int) (ToolResult, error) {
	args := []string{"-n", "--max-count", fmt.Sprintf("%d", maxResults), "--no-heading"}

	if glob != "" {
		args = append(args, "--glob", glob)
	}
	if fileType != "" {
		args = append(args, "--type", fileType)
	}

	args = append(args, pattern, searchPath)

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, rgPath, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Exit code 1 = no matches.
			slog.Info("grep: no matches", "pattern", pattern)
			return ToolResult{Content: "no matches found"}, nil
		}
		slog.Error("grep: ripgrep failed", "err", err)
		return ToolResult{Content: "search failed: " + err.Error(), IsError: true}, nil
	}

	result := strings.TrimSpace(string(output))
	lines := strings.Split(result, "\n")
	slog.Debug("grep: results", "matches", len(lines))
	slog.Info("grep: found matches", "pattern", pattern, "count", len(lines))

	// Truncate if needed.
	if len(lines) > maxResults {
		lines = lines[:maxResults]
		result = strings.Join(lines, "\n") + fmt.Sprintf("\n... (truncated, showing %d of more results)", maxResults)
	}

	return ToolResult{Content: result}, nil
}

func (t *GrepTool) goGrep(pattern, searchPath, glob string, maxResults int) (ToolResult, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		slog.Error("grep: invalid regex", "pattern", pattern, "err", err)
		return ToolResult{Content: "invalid regex: " + err.Error(), IsError: true}, nil
	}

	var matches []string
	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}
		if glob != "" {
			matched, _ := filepath.Match(glob, filepath.Base(path))
			if !matched {
				return nil
			}
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", path, i+1, line))
				if len(matches) >= maxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	}

	if err := filepath.Walk(searchPath, walkFn); err != nil {
		slog.Error("grep: walk failed", "err", err)
		return ToolResult{Content: "search failed: " + err.Error(), IsError: true}, nil
	}

	if len(matches) == 0 {
		slog.Info("grep: no matches", "pattern", pattern)
		return ToolResult{Content: "no matches found"}, nil
	}

	slog.Info("grep: found matches", "pattern", pattern, "count", len(matches))
	return ToolResult{Content: strings.Join(matches, "\n")}, nil
}

// ─── GlobTool ────────────────────────────────────────────────────────────────

// GlobTool finds files matching a glob pattern, sorted by modification time.
type GlobTool struct{}

// NewGlobTool creates a GlobTool.
func NewGlobTool() *GlobTool {
	slog.Debug("glob: tool created")
	return &GlobTool{}
}

func (t *GlobTool) Name() string        { return "glob" }
func (t *GlobTool) Description() string { return "Find files matching a glob pattern, sorted by modification time (newest first)." }
func (t *GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern (e.g., **/*.go, src/*.ts)"},"path":{"type":"string","description":"Base directory (default: current dir)"}},"required":["pattern"]}`)
}

func (t *GlobTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("glob: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Pattern == "" {
		slog.Warn("glob: missing pattern")
		return ToolResult{Content: "missing required parameter: pattern", IsError: true}, nil
	}

	basePath := "."
	if p.Path != "" {
		basePath = p.Path
	}

	slog.Info("glob: searching", "pattern", p.Pattern, "path", basePath)

	fullPattern := filepath.Join(basePath, p.Pattern)

	// For ** patterns, use Walk-based matching.
	if strings.Contains(p.Pattern, "**") {
		return t.doubleStarGlob(basePath, p.Pattern)
	}

	// Simple glob.
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		slog.Error("glob: pattern failed", "pattern", fullPattern, "err", err)
		return ToolResult{Content: "invalid glob pattern: " + err.Error(), IsError: true}, nil
	}

	return t.formatResults(matches)
}

func (t *GlobTool) doubleStarGlob(basePath, pattern string) (ToolResult, error) {
	// Split the pattern into parts at **.
	// For "**/*.go", we walk the tree and match the suffix.
	suffix := ""
	if idx := strings.LastIndex(pattern, "**"); idx != -1 {
		suffix = pattern[idx+2:]
		if strings.HasPrefix(suffix, "/") {
			suffix = suffix[1:]
		}
	}

	var matches []string
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if suffix == "" {
			matches = append(matches, path)
			return nil
		}
		matched, _ := filepath.Match(suffix, filepath.Base(path))
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		slog.Error("glob: walk failed", "err", err)
		return ToolResult{Content: "glob walk failed: " + err.Error(), IsError: true}, nil
	}

	return t.formatResults(matches)
}

type fileWithTime struct {
	path    string
	modTime time.Time
}

func (t *GlobTool) formatResults(matches []string) (ToolResult, error) {
	if len(matches) == 0 {
		slog.Info("glob: no matches")
		return ToolResult{Content: "no matching files found"}, nil
	}

	// Sort by modification time (newest first).
	files := make([]fileWithTime, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		files = append(files, fileWithTime{path: m, modTime: info.ModTime()})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	var results []string
	for _, f := range files {
		results = append(results, f.path)
	}

	slog.Info("glob: found matches", "count", len(results))
	slog.Debug("glob: matches", "files", results)
	return ToolResult{Content: strings.Join(results, "\n")}, nil
}

// ─── truncate helper ─────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ─── Compile-time interface checks ───────────────────────────────────────────

var (
	_ Tool = (*BashTool)(nil)
	_ Tool = (*GrepTool)(nil)
	_ Tool = (*GlobTool)(nil)
)
