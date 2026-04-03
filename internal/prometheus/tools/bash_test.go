package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashTool_SimpleCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, nil)

	input, _ := json.Marshal(bashInput{Command: "echo hello"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", result)
	}
}

func TestBashTool_WorkingDirectory(t *testing.T) {
	dir := t.TempDir()

	// Create a file in the temp dir.
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("found"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewBashTool(dir, nil)
	input, _ := json.Marshal(bashInput{Command: "cat marker.txt"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "found") {
		t.Errorf("expected 'found', got %q", result)
	}
}

func TestBashTool_Timeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, nil)

	timeout := 1
	input, _ := json.Marshal(bashInput{Command: "sleep 30", Timeout: &timeout})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "timed out") {
		t.Errorf("expected timeout message, got %q", result)
	}
}

func TestBashTool_TimeoutCapped(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, nil)

	// Request 999 seconds; should be capped to maxBashTimeout.
	timeout := 999
	input, _ := json.Marshal(bashInput{Command: "echo capped", Timeout: &timeout})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "capped") {
		t.Errorf("expected 'capped', got %q", result)
	}
}

func TestBashTool_BlockedCommand_RmRf(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	tests := []string{
		"rm -rf /",
		"rm -rf / --no-preserve-root",
		"rm -fr /",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			input, _ := json.Marshal(bashInput{Command: cmd})
			_, err := tool.Execute(context.Background(), input)
			if err == nil {
				t.Errorf("expected error for blocked command %q", cmd)
			}
			if !strings.Contains(err.Error(), "blocked") {
				t.Errorf("expected 'blocked' in error, got %q", err.Error())
			}
		})
	}
}

func TestBashTool_BlockedCommand_ForkBomb(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	input, _ := json.Marshal(bashInput{Command: ":() { :|:& }; :"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for fork bomb")
	}
}

func TestBashTool_BlockedCommand_DD(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	input, _ := json.Marshal(bashInput{Command: "dd if=/dev/zero of=/dev/sda"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for dd command")
	}
}

func TestBashTool_BlockedCommand_EtcShadow(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	tests := []string{
		"echo hacked > /etc/shadow",
		"tee /etc/shadow",
		"echo hacked > /etc/passwd",
	}

	for _, cmd := range tests {
		t.Run(cmd, func(t *testing.T) {
			input, _ := json.Marshal(bashInput{Command: cmd})
			_, err := tool.Execute(context.Background(), input)
			if err == nil {
				t.Errorf("expected error for blocked command %q", cmd)
			}
		})
	}
}

func TestBashTool_NonZeroExit(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	input, _ := json.Marshal(bashInput{Command: "exit 1"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("non-zero exit should not be a Go error, got: %v", err)
	}
	if !strings.Contains(result, "exit code 1") {
		t.Errorf("expected exit code info, got %q", result)
	}
}

func TestBashTool_EmptyCommand(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	input, _ := json.Marshal(bashInput{Command: ""})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestBashTool_InvalidJSON(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestBashTool_SanitizedEnv(t *testing.T) {
	// Override getEnv to return controlled values.
	original := getEnv
	getEnv = func() []string {
		return []string{
			"PATH=/usr/bin",
			"HOME=/root",
			"SECRET_KEY=abc123",
			"LANG=en_US.UTF-8",
			"GOPATH=/go",
		}
	}
	defer func() { getEnv = original }()

	env := sanitizeEnv()

	// Should include PATH, LANG, GOPATH.
	has := make(map[string]bool)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		has[parts[0]] = true
	}

	if !has["PATH"] {
		t.Error("expected PATH in sanitized env")
	}
	if !has["LANG"] {
		t.Error("expected LANG in sanitized env")
	}
	if !has["GOPATH"] {
		t.Error("expected GOPATH in sanitized env")
	}
	if has["HOME"] {
		t.Error("HOME should be filtered out")
	}
	if has["SECRET_KEY"] {
		t.Error("SECRET_KEY should be filtered out")
	}
}

func TestBashTool_Name(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)
	if tool.Name() != "bash" {
		t.Errorf("expected 'bash', got %q", tool.Name())
	}
}

func TestBashTool_Schema(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected type 'object', got %v", schema["type"])
	}
}

func TestCheckDangerousCommand(t *testing.T) {
	tests := []struct {
		cmd     string
		blocked bool
	}{
		{"echo hello", false},
		{"ls -la", false},
		{"rm -rf /", true},
		{"rm -fr /", true},
		{"rm -rf / --no-preserve-root", true},
		{"dd if=/dev/zero of=out", true},
		{"mkfs.ext4 /dev/sda", true},
		{":() { :|:& }; :", true},
		{"echo bad > /etc/shadow", true},
		{"tee /etc/passwd", true},
		{"rm -rf /tmp/safe", false}, // Not root slash
		{"git status", false},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			result := checkDangerousCommand(tt.cmd)
			if tt.blocked && result == "" {
				t.Errorf("expected command %q to be blocked", tt.cmd)
			}
			if !tt.blocked && result != "" {
				t.Errorf("expected command %q to be allowed, got blocked: %s", tt.cmd, result)
			}
		})
	}
}

func TestBashTool_StderrCaptured(t *testing.T) {
	tool := NewBashTool(t.TempDir(), nil)

	input, _ := json.Marshal(bashInput{Command: "echo out; echo err >&2"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "out") {
		t.Errorf("expected stdout, got %q", result)
	}
	if !strings.Contains(result, "err") {
		t.Errorf("expected stderr in combined output, got %q", result)
	}
}
