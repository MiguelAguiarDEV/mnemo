package athena

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── BashTool ────────────────────────────────────────────────────────────────

func TestBashTool(t *testing.T) {
	tool := NewBashTool(BashToolConfig{})

	tests := []struct {
		name        string
		params      map[string]interface{}
		wantErr     bool
		wantContent string
		errContains string
	}{
		{
			name:        "success - echo",
			params:      map[string]interface{}{"command": "echo hello"},
			wantContent: "hello\n",
		},
		{
			name:    "success - captures stderr",
			params:  map[string]interface{}{"command": "echo err >&2"},
			wantErr: false,
		},
		{
			name:        "blocked command - rm -rf /",
			params:      map[string]interface{}{"command": "rm -rf /"},
			wantErr:     true,
			errContains: "command blocked",
		},
		{
			name:        "blocked command - mkfs",
			params:      map[string]interface{}{"command": "mkfs.ext4 /dev/sda"},
			wantErr:     true,
			errContains: "command blocked",
		},
		{
			name:        "exit code non-zero",
			params:      map[string]interface{}{"command": "exit 42"},
			wantErr:     true,
			errContains: "exit code 42",
		},
		{
			name:        "missing command",
			params:      map[string]interface{}{},
			wantErr:     true,
			errContains: "missing required parameter: command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.params)
			result, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("unexpected Execute error: %v", err)
			}

			if tt.wantErr {
				if !result.IsError {
					t.Errorf("expected error result, got success: %s", result.Content)
				}
				if tt.errContains != "" && !strings.Contains(result.Content, tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, result.Content)
				}
			} else {
				if result.IsError {
					t.Errorf("unexpected error: %s", result.Content)
				}
				if tt.wantContent != "" && result.Content != tt.wantContent {
					t.Errorf("content mismatch:\nwant: %q\ngot:  %q", tt.wantContent, result.Content)
				}
			}
		})
	}
}

func TestBashTool_Timeout(t *testing.T) {
	tool := NewBashTool(BashToolConfig{})

	timeout := 1
	params, _ := json.Marshal(map[string]interface{}{
		"command": "sleep 10",
		"timeout": timeout,
	})

	start := time.Now()
	result, err := tool.Execute(context.Background(), params)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected timeout error")
	}
	if !strings.Contains(result.Content, "timed out") {
		t.Errorf("expected 'timed out' in error, got: %s", result.Content)
	}
	// Should finish in ~1s, not 10s.
	if elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %s", elapsed)
	}
}

func TestBashTool_StderrCapture(t *testing.T) {
	tool := NewBashTool(BashToolConfig{})

	params, _ := json.Marshal(map[string]interface{}{
		"command": "echo out && echo err >&2",
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should contain both stdout and stderr.
	if !strings.Contains(result.Content, "out") {
		t.Errorf("expected stdout 'out' in content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "err") {
		t.Errorf("expected stderr 'err' in content: %s", result.Content)
	}
}

func TestBashTool_Interface(t *testing.T) {
	tool := NewBashTool(BashToolConfig{})
	if tool.Name() != "bash" {
		t.Errorf("expected name 'bash', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Errorf("invalid JSON schema: %v", err)
	}
}

// ─── GrepTool ────────────────────────────────────────────────────────────────

func TestGrepTool(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files.
	os.WriteFile(filepath.Join(tmpDir, "file1.go"), []byte("package main\nfunc Hello() {}\nfunc World() {}"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "file2.go"), []byte("package main\nfunc Foo() {}"), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# Hello\nThis is a test"), 0o644)

	tool := NewGrepTool()

	tests := []struct {
		name        string
		params      map[string]interface{}
		wantErr     bool
		wantContain string
		errContains string
	}{
		{
			name:        "pattern match",
			params:      map[string]interface{}{"pattern": "func Hello", "path": tmpDir},
			wantContain: "Hello",
		},
		{
			name:        "no match",
			params:      map[string]interface{}{"pattern": "zzznomatchzzz", "path": tmpDir},
			wantContain: "no matches",
		},
		{
			name:        "file type filter with glob",
			params:      map[string]interface{}{"pattern": "Hello", "path": tmpDir, "glob": "*.go"},
			wantContain: "Hello",
		},
		{
			name:    "max results respected",
			params:  map[string]interface{}{"pattern": "func", "path": tmpDir, "max_results": 1},
			wantErr: false,
		},
		{
			name:        "missing pattern",
			params:      map[string]interface{}{},
			wantErr:     true,
			errContains: "missing required parameter: pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.params)
			result, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("unexpected Execute error: %v", err)
			}

			if tt.wantErr {
				if !result.IsError {
					t.Errorf("expected error result, got success: %s", result.Content)
				}
				if tt.errContains != "" && !strings.Contains(result.Content, tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, result.Content)
				}
			} else {
				if result.IsError {
					t.Errorf("unexpected error: %s", result.Content)
				}
				if tt.wantContain != "" && !strings.Contains(result.Content, tt.wantContain) {
					t.Errorf("expected content containing %q, got %q", tt.wantContain, result.Content)
				}
			}
		})
	}
}

func TestGrepTool_Interface(t *testing.T) {
	tool := NewGrepTool()
	if tool.Name() != "grep" {
		t.Errorf("expected name 'grep', got %q", tool.Name())
	}
}

// ─── GlobTool ────────────────────────────────────────────────────────────────

func TestGlobTool(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files with different timestamps.
	file1 := filepath.Join(tmpDir, "a.go")
	file2 := filepath.Join(tmpDir, "b.go")
	file3 := filepath.Join(tmpDir, "c.txt")

	os.WriteFile(file1, []byte("package a"), 0o644)
	os.WriteFile(file2, []byte("package b"), 0o644)
	os.WriteFile(file3, []byte("text file"), 0o644)

	// Set different modification times.
	os.Chtimes(file1, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))
	os.Chtimes(file2, time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Hour))
	// file3 is newest (just created).

	subDir := filepath.Join(tmpDir, "sub")
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "d.go"), []byte("package d"), 0o644)

	tool := NewGlobTool()

	tests := []struct {
		name        string
		params      map[string]interface{}
		wantErr     bool
		wantContain string
		wantMissing string
		errContains string
	}{
		{
			name:        "match *.go files",
			params:      map[string]interface{}{"pattern": "*.go", "path": tmpDir},
			wantContain: ".go",
		},
		{
			name:        "no match",
			params:      map[string]interface{}{"pattern": "*.rs", "path": tmpDir},
			wantContain: "no matching files",
		},
		{
			name:        "double star pattern",
			params:      map[string]interface{}{"pattern": "**/*.go", "path": tmpDir},
			wantContain: "d.go",
		},
		{
			name:        "missing pattern",
			params:      map[string]interface{}{},
			wantErr:     true,
			errContains: "missing required parameter: pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.params)
			result, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("unexpected Execute error: %v", err)
			}

			if tt.wantErr {
				if !result.IsError {
					t.Errorf("expected error result, got success: %s", result.Content)
				}
				if tt.errContains != "" && !strings.Contains(result.Content, tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, result.Content)
				}
			} else {
				if result.IsError {
					t.Errorf("unexpected error: %s", result.Content)
				}
				if tt.wantContain != "" && !strings.Contains(result.Content, tt.wantContain) {
					t.Errorf("expected content containing %q, got %q", tt.wantContain, result.Content)
				}
			}
		})
	}
}

func TestGlobTool_SortedByMtime(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with explicit ordering.
	oldest := filepath.Join(tmpDir, "oldest.txt")
	middle := filepath.Join(tmpDir, "middle.txt")
	newest := filepath.Join(tmpDir, "newest.txt")

	os.WriteFile(oldest, []byte("old"), 0o644)
	os.WriteFile(middle, []byte("mid"), 0o644)
	os.WriteFile(newest, []byte("new"), 0o644)

	os.Chtimes(oldest, time.Now().Add(-3*time.Hour), time.Now().Add(-3*time.Hour))
	os.Chtimes(middle, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))
	os.Chtimes(newest, time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Hour))

	tool := NewGlobTool()
	params, _ := json.Marshal(map[string]interface{}{"pattern": "*.txt", "path": tmpDir})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(result.Content, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected 3 results, got %d: %s", len(lines), result.Content)
	}

	// Newest should be first.
	if !strings.Contains(lines[0], "newest") {
		t.Errorf("expected newest first, got: %s", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "oldest") {
		t.Errorf("expected oldest last, got: %s", lines[len(lines)-1])
	}
}

func TestGlobTool_Interface(t *testing.T) {
	tool := NewGlobTool()
	if tool.Name() != "glob" {
		t.Errorf("expected name 'glob', got %q", tool.Name())
	}
}

// ─── truncate helper ─────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
