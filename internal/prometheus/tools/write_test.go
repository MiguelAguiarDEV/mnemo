package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileWriteTool_CreateNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	tool := NewFileWriteTool(nil)
	input, _ := json.Marshal(writeInput{FilePath: path, Content: "hello world"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "File written") {
		t.Errorf("expected success message, got %q", result)
	}
	if !strings.Contains(result, "11 bytes") {
		t.Errorf("expected byte count, got %q", result)
	}

	// Verify file contents.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestFileWriteTool_CreateWithDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "deep.txt")

	tool := NewFileWriteTool(nil)
	input, _ := json.Marshal(writeInput{FilePath: path, Content: "deep content"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "File written") {
		t.Errorf("expected success message, got %q", result)
	}

	// Verify file exists.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep content" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestFileWriteTool_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")

	// Create initial file.
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileWriteTool(nil)
	input, _ := json.Marshal(writeInput{FilePath: path, Content: "new content"})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify overwritten content.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Errorf("file content = %q, want %q", string(data), "new content")
	}
}

func TestFileWriteTool_EmptyPath(t *testing.T) {
	tool := NewFileWriteTool(nil)
	input, _ := json.Marshal(writeInput{FilePath: "", Content: "content"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestFileWriteTool_InvalidJSON(t *testing.T) {
	tool := NewFileWriteTool(nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFileWriteTool_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	tool := NewFileWriteTool(nil)
	input, _ := json.Marshal(writeInput{FilePath: path, Content: ""})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "0 bytes") {
		t.Errorf("expected 0 bytes, got %q", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}

func TestFileWriteTool_Name(t *testing.T) {
	tool := NewFileWriteTool(nil)
	if tool.Name() != "write" {
		t.Errorf("expected 'write', got %q", tool.Name())
	}
}

func TestFileWriteTool_Schema(t *testing.T) {
	tool := NewFileWriteTool(nil)
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("invalid schema JSON: %v", err)
	}
}
