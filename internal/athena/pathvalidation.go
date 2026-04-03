package athena

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// PathValidator validates file paths against allowed and blocked directory lists.
// It prevents path traversal attacks and access to system directories.
type PathValidator struct {
	AllowedDirs []string // Directories the tools can access (e.g., ~/projects, /tmp)
	BlockedDirs []string // System directories that are always blocked
}

// DefaultBlockedDirs returns the default list of system directories that should never be accessed.
func DefaultBlockedDirs() []string {
	return []string{"/etc", "/usr", "/bin", "/sbin", "/boot", "/proc", "/sys", "/dev", "/lib", "/lib64"}
}

// NewPathValidator creates a PathValidator with the given allowed directories
// and the default blocked directories.
func NewPathValidator(allowedDirs []string) *PathValidator {
	slog.Debug("pathvalidator: created", "allowed_dirs", allowedDirs)
	return &PathValidator{
		AllowedDirs: allowedDirs,
		BlockedDirs: DefaultBlockedDirs(),
	}
}

// Validate checks that the given path is safe to access.
// It resolves the path to absolute, checks for path traversal via symlinks,
// and verifies it is within an allowed directory and not in a blocked directory.
func (v *PathValidator) Validate(path string) (string, error) {
	slog.Debug("pathvalidator: validating", "path", path)

	// Expand ~ to home directory.
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			slog.Error("pathvalidator: failed to get home dir", "err", err)
			return "", fmt.Errorf("failed to resolve home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}

	// Make absolute.
	absPath, err := filepath.Abs(path)
	if err != nil {
		slog.Error("pathvalidator: failed to make absolute", "path", path, "err", err)
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	// Clean the path to remove ../ sequences for the initial check.
	absPath = filepath.Clean(absPath)

	// Try to resolve symlinks. If the file doesn't exist yet (for write operations),
	// resolve the parent directory instead.
	resolvedPath := absPath
	if info, err := os.Lstat(absPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			resolvedPath, err = filepath.EvalSymlinks(absPath)
			if err != nil {
				slog.Error("pathvalidator: failed to resolve symlink", "path", absPath, "err", err)
				return "", fmt.Errorf("failed to resolve symlink: %w", err)
			}
			slog.Debug("pathvalidator: symlink resolved", "from", absPath, "to", resolvedPath)
		}
	} else if os.IsNotExist(err) {
		// File doesn't exist -- resolve parent directory.
		parentDir := filepath.Dir(absPath)
		resolvedParent, evalErr := filepath.EvalSymlinks(parentDir)
		if evalErr == nil {
			resolvedPath = filepath.Join(resolvedParent, filepath.Base(absPath))
		}
		// If parent also doesn't exist, we'll use the cleaned absolute path.
	}

	// Check blocked directories first (highest priority).
	for _, blocked := range v.BlockedDirs {
		cleanBlocked := filepath.Clean(blocked)
		if resolvedPath == cleanBlocked || strings.HasPrefix(resolvedPath, cleanBlocked+"/") {
			slog.Warn("pathvalidator: path in blocked directory", "path", resolvedPath, "blocked_dir", blocked)
			return "", fmt.Errorf("access denied: path %q is in blocked directory %q", resolvedPath, blocked)
		}
	}

	// Check allowed directories.
	if len(v.AllowedDirs) == 0 {
		slog.Debug("pathvalidator: no allowed dirs configured, allowing all non-blocked paths", "path", resolvedPath)
		return resolvedPath, nil
	}

	for _, allowed := range v.AllowedDirs {
		expandedAllowed := allowed
		if strings.HasPrefix(expandedAllowed, "~/") {
			home, err := os.UserHomeDir()
			if err == nil {
				expandedAllowed = filepath.Join(home, expandedAllowed[2:])
			}
		}
		cleanAllowed := filepath.Clean(expandedAllowed)
		if resolvedPath == cleanAllowed || strings.HasPrefix(resolvedPath, cleanAllowed+"/") {
			slog.Debug("pathvalidator: path allowed", "path", resolvedPath, "allowed_dir", cleanAllowed)
			return resolvedPath, nil
		}
	}

	slog.Warn("pathvalidator: path not in any allowed directory", "path", resolvedPath, "allowed_dirs", v.AllowedDirs)
	return "", fmt.Errorf("access denied: path %q is not in any allowed directory", resolvedPath)
}
