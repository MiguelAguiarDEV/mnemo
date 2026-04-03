package athena

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testValidator(t *testing.T) (*PathValidator, string) {
	t.Helper()
	tmpDir := t.TempDir()
	v := &PathValidator{
		AllowedDirs: []string{tmpDir},
		BlockedDirs: DefaultBlockedDirs(),
	}
	return v, tmpDir
}

// ─── ReadFileTool ────────────────────────────────────────────────────────────

func TestReadFileTool(t *testing.T) {
	v, tmpDir := testValidator(t)
	tool := NewReadFileTool(v)

	// Create test file.
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("line1\nline2\nline3\nline4\nline5"), 0o644)

	tests := []struct {
		name        string
		params      map[string]interface{}
		wantErr     bool
		wantContent string
		errContains string
	}{
		{
			name:        "success - read full file",
			params:      map[string]interface{}{"path": testFile},
			wantContent: "line1\nline2\nline3\nline4\nline5",
		},
		{
			name:        "success - with offset",
			params:      map[string]interface{}{"path": testFile, "offset": 2},
			wantContent: "line3\nline4\nline5",
		},
		{
			name:        "success - with limit",
			params:      map[string]interface{}{"path": testFile, "limit": 2},
			wantContent: "line1\nline2",
		},
		{
			name:        "success - with offset and limit",
			params:      map[string]interface{}{"path": testFile, "offset": 1, "limit": 2},
			wantContent: "line2\nline3",
		},
		{
			name:        "file not found",
			params:      map[string]interface{}{"path": filepath.Join(tmpDir, "nonexistent.txt")},
			wantErr:     true,
			errContains: "file not found",
		},
		{
			name:        "path traversal blocked",
			params:      map[string]interface{}{"path": "/etc/passwd"},
			wantErr:     true,
			errContains: "access denied",
		},
		{
			name:        "blocked directory",
			params:      map[string]interface{}{"path": "/usr/bin/ls"},
			wantErr:     true,
			errContains: "blocked directory",
		},
		{
			name:        "missing path",
			params:      map[string]interface{}{},
			wantErr:     true,
			errContains: "missing required parameter: path",
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

func TestReadFileTool_PermissionDenied(t *testing.T) {
	v, tmpDir := testValidator(t)
	tool := NewReadFileTool(v)

	// Create file with no read permission.
	noReadFile := filepath.Join(tmpDir, "noread.txt")
	os.WriteFile(noReadFile, []byte("secret"), 0o000)
	defer os.Chmod(noReadFile, 0o644) // cleanup

	params, _ := json.Marshal(map[string]interface{}{"path": noReadFile})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for permission denied")
	}
	if !strings.Contains(result.Content, "permission denied") {
		t.Errorf("expected 'permission denied' in error, got: %s", result.Content)
	}
}

// ─── WriteFileTool ───────────────────────────────────────────────────────────

func TestWriteFileTool(t *testing.T) {
	v, tmpDir := testValidator(t)
	tool := NewWriteFileTool(v)

	tests := []struct {
		name        string
		params      map[string]interface{}
		wantErr     bool
		errContains string
		checkFile   string
		checkContent string
	}{
		{
			name:         "create new file",
			params:       map[string]interface{}{"path": filepath.Join(tmpDir, "new.txt"), "content": "hello world"},
			checkFile:    filepath.Join(tmpDir, "new.txt"),
			checkContent: "hello world",
		},
		{
			name:         "overwrite existing file",
			params:       map[string]interface{}{"path": filepath.Join(tmpDir, "new.txt"), "content": "updated"},
			checkFile:    filepath.Join(tmpDir, "new.txt"),
			checkContent: "updated",
		},
		{
			name:         "create with parent directories",
			params:       map[string]interface{}{"path": filepath.Join(tmpDir, "deep", "nested", "dir", "file.txt"), "content": "deep content"},
			checkFile:    filepath.Join(tmpDir, "deep", "nested", "dir", "file.txt"),
			checkContent: "deep content",
		},
		{
			name:        "blocked system path",
			params:      map[string]interface{}{"path": "/etc/hacked.txt", "content": "nope"},
			wantErr:     true,
			errContains: "blocked directory",
		},
		{
			name:        "missing path",
			params:      map[string]interface{}{"content": "hello"},
			wantErr:     true,
			errContains: "missing required parameter: path",
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
				if tt.checkFile != "" {
					data, err := os.ReadFile(tt.checkFile)
					if err != nil {
						t.Fatalf("failed to read written file: %v", err)
					}
					if string(data) != tt.checkContent {
						t.Errorf("file content mismatch:\nwant: %q\ngot:  %q", tt.checkContent, string(data))
					}
				}
			}
		})
	}
}

// ─── EditFileTool ────────────────────────────────────────────────────────────

func TestEditFileTool(t *testing.T) {
	v, tmpDir := testValidator(t)
	tool := NewEditFileTool(v)

	tests := []struct {
		name         string
		fileContent  string
		params       map[string]interface{}
		wantErr      bool
		errContains  string
		wantContent  string
	}{
		{
			name:        "success - single replacement",
			fileContent: "hello world",
			params:      map[string]interface{}{"old_string": "world", "new_string": "Go"},
			wantContent: "hello Go",
		},
		{
			name:        "old_string not found",
			fileContent: "hello world",
			params:      map[string]interface{}{"old_string": "universe", "new_string": "x"},
			wantErr:     true,
			errContains: "old_string not found",
		},
		{
			name:        "not unique - fails without replace_all",
			fileContent: "foo bar foo baz foo",
			params:      map[string]interface{}{"old_string": "foo", "new_string": "qux"},
			wantErr:     true,
			errContains: "not unique",
		},
		{
			name:        "replace_all - replaces all occurrences",
			fileContent: "foo bar foo baz foo",
			params:      map[string]interface{}{"old_string": "foo", "new_string": "qux", "replace_all": true},
			wantContent: "qux bar qux baz qux",
		},
		{
			name:        "file not found",
			fileContent: "",
			params:      map[string]interface{}{"path": filepath.Join(tmpDir, "nonexistent.txt"), "old_string": "x", "new_string": "y"},
			wantErr:     true,
			errContains: "file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file unless a specific path is in params.
			filePath, hasPath := tt.params["path"].(string)
			if !hasPath {
				filePath = filepath.Join(tmpDir, "edit_test_"+tt.name+".txt")
				os.WriteFile(filePath, []byte(tt.fileContent), 0o644)
				tt.params["path"] = filePath
			}

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
				if tt.wantContent != "" {
					data, _ := os.ReadFile(filePath)
					if string(data) != tt.wantContent {
						t.Errorf("file content mismatch:\nwant: %q\ngot:  %q", tt.wantContent, string(data))
					}
				}
			}
		})
	}
}

func TestEditFileTool_EmptyFile(t *testing.T) {
	v, tmpDir := testValidator(t)
	tool := NewEditFileTool(v)

	emptyFile := filepath.Join(tmpDir, "empty.txt")
	os.WriteFile(emptyFile, []byte(""), 0o644)

	params, _ := json.Marshal(map[string]interface{}{
		"path":       emptyFile,
		"old_string": "anything",
		"new_string": "replacement",
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for old_string not found in empty file")
	}
	if !strings.Contains(result.Content, "old_string not found") {
		t.Errorf("expected 'old_string not found', got: %s", result.Content)
	}
}

func TestReadFileTool_Interface(t *testing.T) {
	tool := NewReadFileTool(NewPathValidator(nil))
	if tool.Name() != "read_file" {
		t.Errorf("expected name 'read_file', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Errorf("invalid JSON schema: %v", err)
	}
}

func TestWriteFileTool_Interface(t *testing.T) {
	tool := NewWriteFileTool(NewPathValidator(nil))
	if tool.Name() != "write_file" {
		t.Errorf("expected name 'write_file', got %q", tool.Name())
	}
}

func TestEditFileTool_Interface(t *testing.T) {
	tool := NewEditFileTool(NewPathValidator(nil))
	if tool.Name() != "edit_file" {
		t.Errorf("expected name 'edit_file', got %q", tool.Name())
	}
}
