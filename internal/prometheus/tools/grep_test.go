package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepTool_BasicSearch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "func.*main", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "func main()") {
		t.Errorf("expected match, got %q", result)
	}
	if !strings.Contains(result, ":3:") {
		t.Errorf("expected line number 3, got %q", result)
	}
}

func TestGrepTool_IncludeFilter(t *testing.T) {
	dir := t.TempDir()

	// Create .go and .txt files.
	goFile := filepath.Join(dir, "code.go")
	txtFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(goFile, []byte("pattern_match here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(txtFile, []byte("pattern_match here too"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{
		Pattern: "pattern_match",
		Path:    dir,
		Include: "*.go",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "code.go") {
		t.Errorf("expected code.go in results, got %q", result)
	}
	if strings.Contains(result, "notes.txt") {
		t.Errorf("should not contain notes.txt, got %q", result)
	}
}

func TestGrepTool_BraceExpansion(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"a.ts":  "match_here",
		"b.tsx": "match_here",
		"c.js":  "match_here",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{
		Pattern: "match_here",
		Path:    dir,
		Include: "*.{ts,tsx}",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "a.ts") {
		t.Errorf("expected a.ts in results, got %q", result)
	}
	if !strings.Contains(result, "b.tsx") {
		t.Errorf("expected b.tsx in results, got %q", result)
	}
	if strings.Contains(result, "c.js") {
		t.Errorf("should not contain c.js, got %q", result)
	}
}

func TestGrepTool_NoMatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("nothing here"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "xyz123", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches', got %q", result)
	}
}

func TestGrepTool_LineCap(t *testing.T) {
	dir := t.TempDir()

	// Create a file with a very long line.
	longLine := "MATCH" + strings.Repeat("x", 1000)
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(longLine), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "MATCH", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Line should be capped at grepMaxLineLength.
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		if strings.Contains(line, "MATCH") {
			// The format is path:line:content, so the content part should be capped.
			parts := strings.SplitN(line, ":", 3)
			if len(parts) == 3 && len(parts[2]) > grepMaxLineLength+10 {
				t.Errorf("line too long: %d chars", len(parts[2]))
			}
		}
	}
}

func TestGrepTool_InvalidRegex(t *testing.T) {
	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "[invalid", Path: "."})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestGrepTool_EmptyPattern(t *testing.T) {
	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: ""})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}

func TestGrepTool_PathNotFound(t *testing.T) {
	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "test", Path: "/nonexistent/path"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestGrepTool_InvalidJSON(t *testing.T) {
	tool := NewGrepTool(nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGrepTool_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.txt")
	content := "line one\nline two target\nline three"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "target", Path: path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "target") {
		t.Errorf("expected 'target' in results, got %q", result)
	}
	if !strings.Contains(result, ":2:") {
		t.Errorf("expected line 2, got %q", result)
	}
}

func TestGrepTool_VCSDirsExcluded(t *testing.T) {
	dir := t.TempDir()

	// Create .git directory with a file.
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("grep_target"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create normal file.
	if err := os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("grep_target"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(nil)
	input, _ := json.Marshal(grepInput{Pattern: "grep_target", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "normal.txt") {
		t.Errorf("expected normal.txt in results, got %q", result)
	}
	if strings.Contains(result, ".git") {
		t.Errorf("should not contain .git files, got %q", result)
	}
}

func TestGrepTool_Name(t *testing.T) {
	tool := NewGrepTool(nil)
	if tool.Name() != "grep" {
		t.Errorf("expected 'grep', got %q", tool.Name())
	}
}

func TestGrepTool_Schema(t *testing.T) {
	tool := NewGrepTool(nil)
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("invalid schema JSON: %v", err)
	}
}

func TestExpandBracePattern(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{"*.go", []string{"*.go"}},
		{"*.{ts,tsx}", []string{"*.ts", "*.tsx"}},
		{"*.{a,b,c}", []string{"*.a", "*.b", "*.c"}},
		{"src/*.{js,jsx}", []string{"src/*.js", "src/*.jsx"}},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := expandBracePattern(tt.pattern)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d patterns, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, g, tt.want[i])
				}
			}
		})
	}
}
