// Package ticker implements a proactive health-check ticker for JARVIS.
//
// Inspired by KAIROS's tick mechanism, the ticker periodically runs a set
// of lightweight checks (server health, disk space, DB connectivity, etc.)
// and sends Discord notifications when something needs attention.
//
// The ticker does NOT use the LLM -- all checks are pure Go.
package sentinel

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultInterval is the base tick interval when no per-check interval is set.
const DefaultInterval = 15 * time.Minute

// CheckStatus represents the outcome of a single check.
type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarning CheckStatus = "warning"
	StatusError   CheckStatus = "error"
)

// CheckResult is the output of a single check execution.
type CheckResult struct {
	Status  CheckStatus
	Message string
	Notify  bool // true if this result should trigger a notification
}

// CheckFunc is the signature for individual check functions.
type CheckFunc func(ctx context.Context) (*CheckResult, error)

// Check is a named, independently-scheduled health check.
type Check struct {
	Name     string
	Fn       CheckFunc
	Interval time.Duration // per-check interval; zero means use the ticker's base interval
	LastRun  time.Time
}

// NotifyFunc sends a notification. Matches the pattern used by the
// orchestrator's existing Discord notifier.
type NotifyFunc func(title, message string) error

// Ticker runs periodic health checks and sends notifications on warnings/errors.
type Ticker struct {
	interval     time.Duration
	startupDelay time.Duration
	checks       []Check
	logger       *slog.Logger
	notifier     NotifyFunc

	mu sync.Mutex // protects checks[].LastRun
}

// Option configures a Ticker.
type Option func(*Ticker)

// WithInterval sets the base tick interval.
func WithInterval(d time.Duration) Option {
	return func(t *Ticker) { t.interval = d }
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(t *Ticker) { t.logger = l }
}

// WithNotifier sets the notification callback.
func WithNotifier(fn NotifyFunc) Option {
	return func(t *Ticker) { t.notifier = fn }
}

// WithChecks registers the initial set of checks.
func WithChecks(checks []Check) Option {
	return func(t *Ticker) { t.checks = checks }
}

// WithStartupDelay sets the initial delay before the first check.
// Defaults to 30s. Set to 0 in tests to skip the delay.
func WithStartupDelay(d time.Duration) Option {
	return func(t *Ticker) { t.startupDelay = d }
}

// New creates a Ticker with the given options.
func New(opts ...Option) *Ticker {
	t := &Ticker{
		interval:     DefaultInterval,
		startupDelay: 30 * time.Second,
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Run executes all due checks once. This is the core tick logic,
// separated from the background loop for testability.
func (t *Ticker) Run(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	for i := range t.checks {
		c := &t.checks[i]

		// Determine the effective interval for this check.
		interval := c.Interval
		if interval == 0 {
			interval = t.interval
		}

		// Skip if not enough time has passed since last run.
		if !c.LastRun.IsZero() && now.Sub(c.LastRun) < interval {
			t.logger.Debug("ticker: skipping check (not due)",
				"check", c.Name,
				"next_in", interval-now.Sub(c.LastRun),
			)
			continue
		}

		t.logger.Debug("ticker: running check", "check", c.Name)

		result, err := c.Fn(ctx)
		c.LastRun = now

		if err != nil {
			t.logger.Error("ticker: check failed",
				"check", c.Name,
				"err", err,
			)
			t.notify(c.Name, "Check execution failed: "+err.Error())
			continue
		}

		// Log at the appropriate level based on status.
		switch result.Status {
		case StatusOK:
			t.logger.Info("ticker: check passed",
				"check", c.Name,
				"message", result.Message,
			)
		case StatusWarning:
			t.logger.Warn("ticker: check warning",
				"check", c.Name,
				"message", result.Message,
			)
		case StatusError:
			t.logger.Error("ticker: check error",
				"check", c.Name,
				"message", result.Message,
			)
		}

		// Send notification if the check says so.
		if result.Notify {
			t.notify(c.Name, result.Message)
		}
	}
}

// notify sends a notification, logging errors if the notifier fails or is nil.
func (t *Ticker) notify(checkName, message string) {
	if t.notifier == nil {
		t.logger.Warn("ticker: notification skipped (no notifier configured)",
			"check", checkName,
			"message", message,
		)
		return
	}

	title := "JARVIS Ticker: " + checkName
	if err := t.notifier(title, message); err != nil {
		t.logger.Error("ticker: notification send failed",
			"check", checkName,
			"err", err,
		)
	}
}

// RunBackground starts the ticker loop. Blocks until ctx is cancelled.
// Intended to be launched as a goroutine from server startup.
func (t *Ticker) RunBackground(ctx context.Context) {
	t.logger.Info("ticker: background loop started",
		"interval", t.interval,
		"checks", len(t.checks),
	)

	// Wait for server to be ready before first check.
	if t.startupDelay > 0 {
		t.logger.Info("ticker: waiting for server startup before first check", "delay", t.startupDelay)
		select {
		case <-time.After(t.startupDelay):
		case <-ctx.Done():
			return
		}
	}
	t.Run(ctx)

	tick := time.NewTicker(t.interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			t.logger.Info("ticker: background loop stopped")
			return
		case <-tick.C:
			t.Run(ctx)
		}
	}
}
