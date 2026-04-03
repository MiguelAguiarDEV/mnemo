package sentinel

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/sys/unix"
)

// ─── Built-in Check Factories ────────────────────────────────────────────

// NewServerHealthCheck returns a check that GETs the given URL and expects 200.
// Used to verify the JARVIS/mnemo HTTP server is responding.
func NewServerHealthCheck(url string) Check {
	return Check{
		Name:     "server_health",
		Interval: 15 * time.Minute,
		Fn:       serverHealthCheck(url),
	}
}

func serverHealthCheck(url string) CheckFunc {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context) (*CheckResult, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return &CheckResult{
				Status:  StatusError,
				Message: fmt.Sprintf("health endpoint unreachable: %v", err),
				Notify:  true,
			}, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return &CheckResult{
				Status:  StatusWarning,
				Message: fmt.Sprintf("health endpoint returned %d", resp.StatusCode),
				Notify:  true,
			}, nil
		}
		return &CheckResult{
			Status:  StatusOK,
			Message: "server healthy",
		}, nil
	}
}

// NewDiskSpaceCheck returns a check that warns when free disk space on
// the given path falls below thresholdPct (0-100).
func NewDiskSpaceCheck(path string, thresholdPct float64) Check {
	return Check{
		Name:     "disk_space",
		Interval: time.Hour,
		Fn:       diskSpaceCheck(path, thresholdPct),
	}
}

func diskSpaceCheck(path string, thresholdPct float64) CheckFunc {
	return func(ctx context.Context) (*CheckResult, error) {
		var stat unix.Statfs_t
		if err := unix.Statfs(path, &stat); err != nil {
			return nil, fmt.Errorf("statfs %s: %w", path, err)
		}

		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)

		if total == 0 {
			return nil, fmt.Errorf("statfs %s: total blocks is zero", path)
		}

		freePct := float64(free) / float64(total) * 100.0

		if freePct < thresholdPct {
			return &CheckResult{
				Status:  StatusWarning,
				Message: fmt.Sprintf("disk space low: %.1f%% free (threshold: %.0f%%)", freePct, thresholdPct),
				Notify:  true,
			}, nil
		}

		return &CheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("disk space ok: %.1f%% free", freePct),
		}, nil
	}
}

// DBPinger is the minimal interface for checking database connectivity.
// *sql.DB satisfies this.
type DBPinger interface {
	PingContext(ctx context.Context) error
}

// NewPostgresCheck returns a check that pings the database.
func NewPostgresCheck(db DBPinger) Check {
	return Check{
		Name:     "postgres",
		Interval: 15 * time.Minute,
		Fn:       postgresCheck(db),
	}
}

func postgresCheck(db DBPinger) CheckFunc {
	return func(ctx context.Context) (*CheckResult, error) {
		if err := db.PingContext(ctx); err != nil {
			return &CheckResult{
				Status:  StatusError,
				Message: fmt.Sprintf("postgres unreachable: %v", err),
				Notify:  true,
			}, nil
		}
		return &CheckResult{
			Status:  StatusOK,
			Message: "postgres connected",
		}, nil
	}
}

// NeedsConsolidationFunc is the signature for morpheus.Consolidator.NeedsConsolidation.
type NeedsConsolidationFunc func() (bool, error)

// TriggerConsolidationFunc runs a consolidation cycle.
type TriggerConsolidationFunc func(ctx context.Context) error

// NewMemoryConsolidationCheck returns a check that triggers autoDream
// consolidation when needed.
func NewMemoryConsolidationCheck(needsFn NeedsConsolidationFunc, triggerFn TriggerConsolidationFunc) Check {
	return Check{
		Name:     "memory_consolidation",
		Interval: time.Hour,
		Fn:       memoryConsolidationCheck(needsFn, triggerFn),
	}
}

func memoryConsolidationCheck(needsFn NeedsConsolidationFunc, triggerFn TriggerConsolidationFunc) CheckFunc {
	return func(ctx context.Context) (*CheckResult, error) {
		needed, err := needsFn()
		if err != nil {
			return nil, fmt.Errorf("check consolidation need: %w", err)
		}
		if !needed {
			return &CheckResult{
				Status:  StatusOK,
				Message: "memory consolidation not needed",
			}, nil
		}

		// Trigger consolidation in-band (it acquires its own lock).
		if err := triggerFn(ctx); err != nil {
			return &CheckResult{
				Status:  StatusWarning,
				Message: fmt.Sprintf("memory consolidation triggered but failed: %v", err),
				Notify:  true,
			}, nil
		}

		return &CheckResult{
			Status:  StatusOK,
			Message: "memory consolidation completed",
			Notify:  true,
		}, nil
	}
}

// NewOpenCodeCheck returns a check that verifies the OpenCode serve endpoint
// is responding on the given URL.
func NewOpenCodeCheck(url string) Check {
	return Check{
		Name:     "opencode_availability",
		Interval: 15 * time.Minute,
		Fn:       openCodeCheck(url),
	}
}

func openCodeCheck(url string) CheckFunc {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context) (*CheckResult, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return &CheckResult{
				Status:  StatusWarning,
				Message: fmt.Sprintf("OpenCode unreachable: %v", err),
				Notify:  true,
			}, nil
		}
		defer resp.Body.Close()

		// Any response means the port is alive -- we don't require 200.
		return &CheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("OpenCode responding (status %d)", resp.StatusCode),
		}, nil
	}
}

// ─── Default Check Set ───────────────────────────────────────────────────

// DefaultChecks returns the standard set of built-in checks for JARVIS.
// Parameters:
//   - healthURL: the /health endpoint URL (e.g. "http://localhost:8080/health")
//   - openCodeURL: the OpenCode serve base URL (e.g. "http://172.18.0.1:4096")
//   - db: a *sql.DB or any DBPinger for Postgres connectivity
//   - needsFn/triggerFn: dream consolidation callbacks (nil to skip)
func DefaultChecks(
	healthURL string,
	openCodeURL string,
	db DBPinger,
	needsFn NeedsConsolidationFunc,
	triggerFn TriggerConsolidationFunc,
) []Check {
	checks := []Check{
		NewServerHealthCheck(healthURL),
		NewDiskSpaceCheck("/", 10),
	}

	if db != nil {
		checks = append(checks, NewPostgresCheck(db))
	}

	if needsFn != nil && triggerFn != nil {
		checks = append(checks, NewMemoryConsolidationCheck(needsFn, triggerFn))
	}

	if openCodeURL != "" {
		checks = append(checks, NewOpenCodeCheck(openCodeURL))
	}

	return checks
}

// ensure *sql.DB satisfies DBPinger at compile time.
var _ DBPinger = (*sql.DB)(nil)
