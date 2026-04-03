package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileReadTool_ReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool(nil)
	input, _ := json.Marshal(readInput{FilePath: path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain line numbers.
	if !strings.Contains(result, "1\tline1") {
		t.Errorf("expected line-numbered output, got %q", result)
	}
	if !strings.Contains(result, "2\tline2") {
		t.Errorf("expected line 2, got %q", result)
	}
	if !strings.Contains(result, "3\tline3") {
		t.Errorf("expected line 3, got %q", result)
	}
}

func TestFileReadTool_Offset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool(nil)
	offset := 3
	input, _ := json.Marshal(readInput{FilePath: path, Offset: &offset})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should start from line 3.
	if strings.Contains(result, "1\tline1") {
		t.Errorf("should not contain line 1, got %q", result)
	}
	if !strings.Contains(result, "3\tline3") {
		t.Errorf("expected line 3, got %q", result)
	}
}

func TestFileReadTool_Limit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool(nil)
	limit := 2
	input, _ := json.Marshal(readInput{FilePath: path, Limit: &limit})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only show first 2 lines.
	if !strings.Contains(result, "1\tline1") {
		t.Errorf("expected line 1, got %q", result)
	}
	if !strings.Contains(result, "2\tline2") {
		t.Errorf("expected line 2, got %q", result)
	}
	if strings.Contains(result, "3\tline3") {
		t.Errorf("should not contain line 3, got %q", result)
	}
	// Should have truncation metadata.
	if !strings.Contains(result, "showing lines") {
		t.Errorf("expected partial read metadata, got %q", result)
	}
}

func TestFileReadTool_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i))
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool(nil)
	offset := 3
	limit := 2
	input, _ := json.Marshal(readInput{FilePath: path, Offset: &offset, Limit: &limit})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should show lines 3 and 4.
	if !strings.Contains(result, "3\t") {
		t.Errorf("expected line 3, got %q", result)
	}
	if !strings.Contains(result, "4\t") {
		t.Errorf("expected line 4, got %q", result)
	}
	if strings.Contains(result, "5\t") {
		t.Errorf("should not contain line 5, got %q", result)
	}
}

func TestFileReadTool_FileNotFound(t *testing.T) {
	tool := NewFileReadTool(nil)
	input, _ := json.Marshal(readInput{FilePath: "/nonexistent/file.txt"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileReadTool_BlockedDevice(t *testing.T) {
	tool := NewFileReadTool(nil)

	devices := []string{"/dev/zero", "/dev/random", "/dev/stdin", "/dev/tty"}
	for _, dev := range devices {
		t.Run(dev, func(t *testing.T) {
			input, _ := json.Marshal(readInput{FilePath: dev})
			_, err := tool.Execute(context.Background(), input)
			if err == nil {
				t.Errorf("expected error for blocked device %q", dev)
			}
			if !strings.Contains(err.Error(), "device") {
				t.Errorf("expected device error, got %q", err.Error())
			}
		})
	}
}

func TestFileReadTool_BlockedDevPrefix(t *testing.T) {
	tool := NewFileReadTool(nil)

	// Any /dev/ path should be blocked.
	input, _ := json.Marshal(readInput{FilePath: "/dev/some_device"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for /dev/ path")
	}
}

func TestFileReadTool_EmptyPath(t *testing.T) {
	tool := NewFileReadTool(nil)
	input, _ := json.Marshal(readInput{FilePath: ""})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestFileReadTool_InvalidJSON(t *testing.T) {
	tool := NewFileReadTool(nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFileReadTool_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool(nil)
	input, _ := json.Marshal(readInput{FilePath: path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty file has one empty line.
	if !strings.Contains(result, "1\t") {
		t.Errorf("expected line 1, got %q", result)
	}
}

func TestFileReadTool_OffsetBeyondFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileReadTool(nil)
	offset := 100
	input, _ := json.Marshal(readInput{FilePath: path, Offset: &offset})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "exceeds") {
		t.Errorf("expected exceeds message, got %q", result)
	}
}

func TestFileReadTool_Name(t *testing.T) {
	tool := NewFileReadTool(nil)
	if tool.Name() != "read" {
		t.Errorf("expected 'read', got %q", tool.Name())
	}
}

func TestFileReadTool_Schema(t *testing.T) {
	tool := NewFileReadTool(nil)
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("invalid schema JSON: %v", err)
	}
}
