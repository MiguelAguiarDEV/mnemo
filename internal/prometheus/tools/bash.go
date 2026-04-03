package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	defaultBashTimeout = 120 * time.Second
	maxBashTimeout     = 120 * time.Second
	maxBashOutput      = 100 * 1024 // 100KB
)

// dangerousPatterns are commands that should never be executed.
var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`rm\s+-rf\s+/\s*$`),
	regexp.MustCompile(`rm\s+-rf\s+/\s+`),
	regexp.MustCompile(`rm\s+-fr\s+/\s*$`),
	regexp.MustCompile(`rm\s+-fr\s+/\s+`),
	regexp.MustCompile(`dd\s+if=`),
	regexp.MustCompile(`mkfs\.`),
	regexp.MustCompile(`:\(\)\s*\{\s*:\|\s*:&\s*\}\s*;`), // fork bomb
	regexp.MustCompile(`>\s*/etc/shadow`),
	regexp.MustCompile(`>\s*/etc/passwd`),
	regexp.MustCompile(`tee\s+/etc/shadow`),
	regexp.MustCompile(`tee\s+/etc/passwd`),
}

// safeEnvVars are environment variables that are safe to pass through.
var safeEnvVars = map[string]bool{
	"PATH": true, "LANG": true, "LANGUAGE": true, "LC_ALL": true,
	"LC_CTYPE": true, "LC_TIME": true, "TERM": true, "COLORTERM": true,
	"NO_COLOR": true, "FORCE_COLOR": true, "TZ": true,
	"GOPATH": true, "GOROOT": true, "GOBIN": true,
	"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "GO111MODULE": true,
	"NODE_ENV": true, "PYTHONUNBUFFERED": true, "PYTHONDONTWRITEBYTECODE": true,
	"RUST_BACKTRACE": true, "RUST_LOG": true,
	"LS_COLORS": true, "LSCOLORS": true,
	"TMPDIR": true, "TEMP": true, "TMP": true,
	"SHELL": true, "SHLVL": true,
	"XDG_RUNTIME_DIR": true, "XDG_DATA_HOME": true,
	"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true,
}

// BashTool executes shell commands in a controlled environment.
type BashTool struct {
	workingDir string
	logger     *slog.Logger
}

// NewBashTool creates a BashTool with the given working directory.
func NewBashTool(workingDir string, logger *slog.Logger) *BashTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &BashTool{
		workingDir: workingDir,
		logger:     logger,
	}
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "Execute a bash command and return its output."
}

func (t *BashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The bash command to execute"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds (max 120)"
			}
		},
		"required": ["command"]
	}`)
}

type bashInput struct {
	Command string `json:"command"`
	Timeout *int   `json:"timeout,omitempty"`
}

func (t *BashTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	// Security check.
	if blocked := checkDangerousCommand(in.Command); blocked != "" {
		t.logger.Warn("bash: blocked dangerous command", "command", in.Command, "reason", blocked)
		return "", fmt.Errorf("command blocked: %s", blocked)
	}

	// Determine timeout.
	timeout := defaultBashTimeout
	if in.Timeout != nil && *in.Timeout > 0 {
		timeout = time.Duration(*in.Timeout) * time.Second
		if timeout > maxBashTimeout {
			timeout = maxBashTimeout
		}
	}

	t.logger.Info("bash: executing", "command", truncate(in.Command, 200), "timeout", timeout)

	// Create command with timeout context.
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", in.Command)
	cmd.Dir = t.workingDir

	// Set process group so we can kill children on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Build sanitized environment.
	cmd.Env = sanitizeEnv()

	// Capture combined output.
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()

	output := outBuf.String()

	// Truncate if too large.
	if len(output) > maxBashOutput {
		output = output[:maxBashOutput] + "\n... (output truncated at 100KB)"
	}

	// Handle timeout.
	if cmdCtx.Err() == context.DeadlineExceeded {
		// Try to kill the process group.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		t.logger.Warn("bash: command timed out", "timeout", timeout)
		if output != "" {
			return output + "\n(command timed out after " + timeout.String() + ")", nil
		}
		return "(command timed out after " + timeout.String() + ")", nil
	}

	if err != nil {
		// Include exit error info but return the output (non-zero exit is not a Go error).
		var exitErr *exec.ExitError
		if ok := isExitError(err, &exitErr); ok {
			t.logger.Info("bash: command exited with non-zero status",
				"exit_code", exitErr.ExitCode(),
				"command", truncate(in.Command, 100),
			)
			if output == "" {
				return fmt.Sprintf("(exit code %d)", exitErr.ExitCode()), nil
			}
			return output, nil
		}
		return output, fmt.Errorf("exec error: %w", err)
	}

	t.logger.Info("bash: command completed", "output_len", len(output))
	return output, nil
}

// checkDangerousCommand returns a reason string if the command should be blocked.
func checkDangerousCommand(cmd string) string {
	for _, pat := range dangerousPatterns {
		if pat.MatchString(cmd) {
			return fmt.Sprintf("matches dangerous pattern: %s", pat.String())
		}
	}
	return ""
}

// sanitizeEnv returns a filtered set of environment variables.
func sanitizeEnv() []string {
	var env []string
	for _, e := range getEnv() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if safeEnvVars[parts[0]] {
			env = append(env, e)
		}
	}
	return env
}

// getEnv returns the current environment. Overridden in tests.
var getEnv = osEnviron

func osEnviron() []string {
	return os.Environ()
}

// isExitError checks if an error is an exec.ExitError.
func isExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
