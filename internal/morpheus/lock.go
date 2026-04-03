// Package dream implements background memory consolidation for engram.
//
// It adapts Claude Code's autoDream 4-phase pattern (orient, gather,
// consolidate, prune) to periodically merge and clean up engram
// observations, keeping memory lean and contradiction-free.
package morpheus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lockFile manages a file-based lock whose mtime doubles as the
// "last consolidated at" timestamp (same pattern as Claude Code's
// consolidation lock). The file body contains the holder's PID.
type lockFile struct {
	path     string
	staleTTL time.Duration // lock is stale after this duration (default 1h)
	now      func() time.Time
}

const defaultStaleTTL = time.Hour

func newLockFile(path string) *lockFile {
	return &lockFile{
		path:     path,
		staleTTL: defaultStaleTTL,
		now:      time.Now,
	}
}

// lastConsolidatedAt returns the lock file's mtime, which represents
// when consolidation last completed. Returns zero time if no lock file.
func (l *lockFile) lastConsolidatedAt() (time.Time, error) {
	info, err := os.Stat(l.path)
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("stat lock file: %w", err)
	}
	return info.ModTime(), nil
}

// acquire attempts to take the lock by writing the current PID.
// Returns the prior mtime (for rollback) and whether acquisition succeeded.
// If another process holds the lock and is not stale, returns false.
func (l *lockFile) acquire() (priorMtime time.Time, ok bool, err error) {
	now := l.now()

	// Check existing lock.
	info, err := os.Stat(l.path)
	if err == nil {
		priorMtime = info.ModTime()

		// Read PID from lock body.
		body, readErr := os.ReadFile(l.path)
		if readErr == nil && len(body) > 0 {
			pidStr := strings.TrimSpace(string(body))
			if pid, parseErr := strconv.Atoi(pidStr); parseErr == nil {
				// Check if PID is still alive AND lock is not stale.
				if isProcessAlive(pid) && now.Sub(info.ModTime()) < l.staleTTL {
					return priorMtime, false, nil
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return time.Time{}, false, fmt.Errorf("stat lock: %w", err)
	}

	// Write our PID to claim the lock.
	pid := os.Getpid()
	if err := os.WriteFile(l.path, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return priorMtime, false, fmt.Errorf("write lock: %w", err)
	}

	// Verify we own it (race resolution: last writer wins).
	body, err := os.ReadFile(l.path)
	if err != nil {
		return priorMtime, false, fmt.Errorf("verify lock: %w", err)
	}
	pidStr := strings.TrimSpace(string(body))
	ownerPID, _ := strconv.Atoi(pidStr)
	if ownerPID != pid {
		return priorMtime, false, nil
	}

	return priorMtime, true, nil
}

// release updates the lock file's mtime to now (marking consolidation
// as complete) without removing it. The mtime IS the "last consolidated"
// timestamp.
func (l *lockFile) release() error {
	now := l.now()
	return os.Chtimes(l.path, now, now)
}

// rollback restores the lock mtime to the prior value, undoing the
// acquisition timestamp. Used when consolidation fails.
func (l *lockFile) rollback(priorMtime time.Time) error {
	if priorMtime.IsZero() {
		// No prior lock -- remove entirely.
		return os.Remove(l.path)
	}
	return os.Chtimes(l.path, priorMtime, priorMtime)
}

// isProcessAlive checks if a process with the given PID exists.
// Uses kill(pid, 0) on Unix: returns nil if alive, ESRCH if not,
// EPERM if alive but not ours.
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	// nil means we can signal it (alive, same user).
	// EPERM means alive but owned by another user.
	return err == nil || err == syscall.EPERM
}
