package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobTool_BasicPattern(t *testing.T) {
	dir := t.TempDir()

	// Create test files.
	files := []string{"a.go", "b.go", "c.txt", "d.go"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: "*.go", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find 3 .go files.
	for _, f := range []string{"a.go", "b.go", "d.go"} {
		if !strings.Contains(result, f) {
			t.Errorf("expected %q in results, got %q", f, result)
		}
	}
	// Should NOT find .txt.
	if strings.Contains(result, "c.txt") {
		t.Errorf("should not contain c.txt, got %q", result)
	}
}

func TestGlobTool_RecursivePattern(t *testing.T) {
	dir := t.TempDir()

	// Create nested structure.
	dirs := []string{
		filepath.Join(dir, "src"),
		filepath.Join(dir, "src", "pkg"),
		filepath.Join(dir, "lib"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		filepath.Join(dir, "main.go"):            "",
		filepath.Join(dir, "src", "handler.go"):  "",
		filepath.Join(dir, "src", "pkg", "util.go"): "",
		filepath.Join(dir, "lib", "helper.go"):   "",
		filepath.Join(dir, "readme.md"):           "",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: "**/*.go", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find all .go files recursively.
	for _, f := range []string{"main.go", "handler.go", "util.go", "helper.go"} {
		if !strings.Contains(result, f) {
			t.Errorf("expected %q in results, got %q", f, result)
		}
	}
	// Should NOT find .md.
	if strings.Contains(result, "readme.md") {
		t.Errorf("should not contain readme.md, got %q", result)
	}
}

func TestGlobTool_ExcludedDirs(t *testing.T) {
	dir := t.TempDir()

	// Create .git and node_modules dirs with files.
	excluded := []string{".git", "node_modules"}
	for _, d := range excluded {
		p := filepath.Join(dir, d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "internal.go"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a normal file.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: "**/*.go", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find main.go.
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected main.go, got %q", result)
	}
	// Should NOT find files in excluded dirs.
	if strings.Contains(result, ".git") {
		t.Errorf("should not contain .git files, got %q", result)
	}
	if strings.Contains(result, "node_modules") {
		t.Errorf("should not contain node_modules files, got %q", result)
	}
}

func TestGlobTool_Limit(t *testing.T) {
	dir := t.TempDir()

	// Create more than 100 files.
	for i := 0; i < 105; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file_%03d.go", i))
		if err := os.WriteFile(name, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: "*.go", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation message, got %q", result)
	}
}

func TestGlobTool_NoMatches(t *testing.T) {
	dir := t.TempDir()

	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: "*.xyz", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no files matched") {
		t.Errorf("expected 'no files matched', got %q", result)
	}
}

func TestGlobTool_EmptyPattern(t *testing.T) {
	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: ""})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}

func TestGlobTool_InvalidJSON(t *testing.T) {
	tool := NewGlobTool(nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGlobTool_Name(t *testing.T) {
	tool := NewGlobTool(nil)
	if tool.Name() != "glob" {
		t.Errorf("expected 'glob', got %q", tool.Name())
	}
}

func TestGlobTool_Schema(t *testing.T) {
	tool := NewGlobTool(nil)
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("invalid schema JSON: %v", err)
	}
}

func TestGlobTool_NonexistentPath(t *testing.T) {
	tool := NewGlobTool(nil)
	input, _ := json.Marshal(globInput{Pattern: "**/*.go", Path: "/nonexistent/dir"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no files matched") {
		t.Errorf("expected 'no files matched', got %q", result)
	}
}

