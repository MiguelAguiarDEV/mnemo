package athena

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathValidator_Validate(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a subdirectory for testing.
	subDir := filepath.Join(tmpDir, "allowed", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test file.
	testFile := filepath.Join(subDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink pointing to allowed file.
	safeLink := filepath.Join(tmpDir, "allowed", "safe-link.txt")
	os.Symlink(testFile, safeLink)

	tests := []struct {
		name        string
		allowedDirs []string
		blockedDirs []string
		path        string
		wantErr     bool
		errContains string
	}{
		{
			name:        "allowed directory",
			allowedDirs: []string{tmpDir},
			path:        testFile,
			wantErr:     false,
		},
		{
			name:        "subdirectory of allowed",
			allowedDirs: []string{tmpDir},
			path:        filepath.Join(subDir, "test.txt"),
			wantErr:     false,
		},
		{
			name:        "blocked system directory",
			allowedDirs: []string{tmpDir, "/etc"},
			blockedDirs: []string{"/etc"},
			path:        "/etc/passwd",
			wantErr:     true,
			errContains: "blocked directory",
		},
		{
			name:        "not in any allowed directory",
			allowedDirs: []string{tmpDir},
			path:        "/var/log/syslog",
			wantErr:     true,
			errContains: "not in any allowed directory",
		},
		{
			name:        "path traversal attempt",
			allowedDirs: []string{subDir},
			path:        filepath.Join(subDir, "..", "..", "outside.txt"),
			wantErr:     true,
			errContains: "not in any allowed directory",
		},
		{
			name:        "relative path resolved",
			allowedDirs: []string{tmpDir},
			path:        testFile,
			wantErr:     false,
		},
		{
			name:        "no allowed dirs means allow all non-blocked",
			allowedDirs: []string{},
			path:        testFile,
			wantErr:     false,
		},
		{
			name:        "blocked dir takes priority over empty allowed",
			allowedDirs: []string{},
			blockedDirs: []string{tmpDir},
			path:        testFile,
			wantErr:     true,
			errContains: "blocked directory",
		},
		{
			name:        "symlink to allowed path",
			allowedDirs: []string{tmpDir},
			path:        safeLink,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &PathValidator{
				AllowedDirs: tt.allowedDirs,
				BlockedDirs: tt.blockedDirs,
			}
			if len(v.BlockedDirs) == 0 && tt.blockedDirs == nil {
				v.BlockedDirs = DefaultBlockedDirs()
			}

			_, err := v.Validate(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errContains)
				} else if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestNewPathValidator(t *testing.T) {
	v := NewPathValidator([]string{"/tmp", "/home"})
	if len(v.AllowedDirs) != 2 {
		t.Errorf("expected 2 allowed dirs, got %d", len(v.AllowedDirs))
	}
	if len(v.BlockedDirs) != len(DefaultBlockedDirs()) {
		t.Errorf("expected %d blocked dirs, got %d", len(DefaultBlockedDirs()), len(v.BlockedDirs))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
