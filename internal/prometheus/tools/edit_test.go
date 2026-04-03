package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileEditTool_SuccessfulEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	content := "func main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  path,
		OldString: "hello",
		NewString: "world",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Edited") {
		t.Errorf("expected success message, got %q", result)
	}

	// Verify file content changed.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "world") {
		t.Errorf("expected 'world' in file, got %q", string(data))
	}
	if strings.Contains(string(data), "hello") {
		t.Errorf("expected 'hello' to be replaced, got %q", string(data))
	}
}

func TestFileEditTool_OldStringNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("existing content"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  path,
		OldString: "nonexistent",
		NewString: "replacement",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for string not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %q", err.Error())
	}
}

func TestFileEditTool_MultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "foo bar foo baz foo"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(nil)

	// Without replace_all, should fail.
	input, _ := json.Marshal(editInput{
		FilePath:  path,
		OldString: "foo",
		NewString: "qux",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for multiple matches without replace_all")
	}
	if !strings.Contains(err.Error(), "3 times") {
		t.Errorf("expected count in error, got %q", err.Error())
	}
}

func TestFileEditTool_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "foo bar foo baz foo"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:   path,
		OldString:  "foo",
		NewString:  "qux",
		ReplaceAll: true,
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "3 occurrence") {
		t.Errorf("expected 3 occurrences in result, got %q", result)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "foo") {
		t.Errorf("expected all 'foo' replaced, got %q", string(data))
	}
	if string(data) != "qux bar qux baz qux" {
		t.Errorf("expected 'qux bar qux baz qux', got %q", string(data))
	}
}

func TestFileEditTool_FileNotFound(t *testing.T) {
	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  "/nonexistent/file.txt",
		OldString: "old",
		NewString: "new",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileEditTool_EmptyOldString(t *testing.T) {
	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  "/tmp/test.txt",
		OldString: "",
		NewString: "new",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty old_string")
	}
}

func TestFileEditTool_SameStrings(t *testing.T) {
	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  "/tmp/test.txt",
		OldString: "same",
		NewString: "same",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for identical strings")
	}
}

func TestFileEditTool_EmptyPath(t *testing.T) {
	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  "",
		OldString: "old",
		NewString: "new",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestFileEditTool_InvalidJSON(t *testing.T) {
	tool := NewFileEditTool(nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFileEditTool_Name(t *testing.T) {
	tool := NewFileEditTool(nil)
	if tool.Name() != "edit" {
		t.Errorf("expected 'edit', got %q", tool.Name())
	}
}

func TestFileEditTool_Schema(t *testing.T) {
	tool := NewFileEditTool(nil)
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("invalid schema JSON: %v", err)
	}
}

func TestFileEditTool_PreservesOtherContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preserve.txt")
	content := "line1\nREPLACE_ME\nline3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileEditTool(nil)
	input, _ := json.Marshal(editInput{
		FilePath:  path,
		OldString: "REPLACE_ME",
		NewString: "REPLACED",
	})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nREPLACED\nline3\n"
	if string(data) != expected {
		t.Errorf("got %q, want %q", string(data), expected)
	}
}
