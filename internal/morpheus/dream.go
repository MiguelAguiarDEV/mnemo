package morpheus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/athena"
)

// ─── Defaults ─────────────────────────────────────────────────────────────

const (
	DefaultMinAge     = 24 * time.Hour
	DefaultMinObs     = 20
	DefaultCheckEvery = time.Hour
)

// ─── Consolidator ─────────────────────────────────────────────────────────

// Consolidator orchestrates the 4-phase memory consolidation cycle:
// orient -> gather -> consolidate -> prune.
//
// It uses the mnemo CLI for observation read/write and a lock file
// whose mtime doubles as the "last consolidated at" timestamp.
type Consolidator struct {
	mnemoBin  string
	runner     athena.CommandRunner
	lock       *lockFile
	logger     *slog.Logger
	minAge     time.Duration
	minObs     int
	checkEvery time.Duration
	project    string // optional project filter
}

// Option configures a Consolidator.
type Option func(*Consolidator)

// WithMnemoBin sets the path to the mnemo CLI binary.
func WithMnemoBin(bin string) Option {
	return func(c *Consolidator) { c.mnemoBin = bin }
}

// WithLockPath sets the lock file path.
func WithLockPath(path string) Option {
	return func(c *Consolidator) { c.lock = newLockFile(path) }
}

// WithLogger sets the slog logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *Consolidator) { c.logger = l }
}

// WithMinAge sets the minimum time since last consolidation.
func WithMinAge(d time.Duration) Option {
	return func(c *Consolidator) { c.minAge = d }
}

// WithMinObservations sets the minimum new observations to trigger.
func WithMinObservations(n int) Option {
	return func(c *Consolidator) { c.minObs = n }
}

// WithCheckInterval sets how often the background loop checks.
func WithCheckInterval(d time.Duration) Option {
	return func(c *Consolidator) { c.checkEvery = d }
}

// WithProject sets the project filter for observations.
func WithProject(p string) Option {
	return func(c *Consolidator) { c.project = p }
}

// WithCommandRunner sets the command runner (for testing).
func WithCommandRunner(r athena.CommandRunner) Option {
	return func(c *Consolidator) { c.runner = r }
}

// New creates a Consolidator with the given options.
func New(opts ...Option) *Consolidator {
	c := &Consolidator{
		mnemoBin:  "mnemo",
		runner:     athena.DefaultCommandRunner(),
		logger:     slog.Default(),
		minAge:     DefaultMinAge,
		minObs:     DefaultMinObs,
		checkEvery: DefaultCheckEvery,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.lock == nil {
		c.lock = newLockFile("/tmp/mnemo-consolidate.lock")
	}
	return c
}

// ─── Public API ───────────────────────────────────────────────────────────

// NeedsConsolidation checks if consolidation should run, without
// acquiring the lock. Useful for cheap pre-checks.
func (c *Consolidator) NeedsConsolidation() (bool, error) {
	return c.orient(context.Background())
}

// Run executes a single consolidation cycle: orient -> gather ->
// consolidate -> prune. Returns nil on success or skip.
func (c *Consolidator) Run(ctx context.Context) error {
	c.logger.Info("dream: starting consolidation cycle")

	// Phase 1: Orient -- check if consolidation is needed.
	needed, err := c.orient(ctx)
	if err != nil {
		return fmt.Errorf("dream orient: %w", err)
	}
	if !needed {
		c.logger.Info("dream: consolidation not needed, skipping")
		return nil
	}

	// Acquire lock.
	priorMtime, acquired, err := c.lock.acquire()
	if err != nil {
		return fmt.Errorf("dream lock acquire: %w", err)
	}
	if !acquired {
		c.logger.Info("dream: another process holds the lock, skipping")
		return nil
	}

	// On failure, rollback lock mtime.
	success := false
	defer func() {
		if !success {
			c.logger.Warn("dream: rolling back lock due to failure")
			if rbErr := c.lock.rollback(priorMtime); rbErr != nil {
				c.logger.Error("dream: lock rollback failed", "err", rbErr)
			}
		}
	}()

	// Phase 2: Gather -- fetch and group observations.
	clusters, err := c.gather(ctx)
	if err != nil {
		return fmt.Errorf("dream gather: %w", err)
	}
	if len(clusters) == 0 {
		c.logger.Info("dream: no observations found, nothing to consolidate")
		success = true
		return c.lock.release()
	}

	// Phase 3: Consolidate -- merge clusters.
	results, err := c.consolidate(ctx, clusters)
	if err != nil {
		return fmt.Errorf("dream consolidate: %w", err)
	}

	// Phase 4: Prune -- write back and generate audit log.
	auditLog, err := c.prune(ctx, results)
	if err != nil {
		return fmt.Errorf("dream prune: %w", err)
	}

	// Log the audit trail.
	if len(auditLog) > 0 {
		c.logger.Info("dream: consolidation complete",
			"actions", len(auditLog),
			"summary", strings.Join(auditLog, "; "),
		)
	}

	// Release lock (updates mtime to now).
	if err := c.lock.release(); err != nil {
		return fmt.Errorf("dream lock release: %w", err)
	}

	success = true
	return nil
}

// ─── Background Loop ──────────────────────────────────────────────────────

// RunBackground starts a loop that checks for consolidation needs every
// checkEvery interval. Blocks until ctx is cancelled. Intended to be
// launched as a goroutine from the cloud server startup.
func (c *Consolidator) RunBackground(ctx context.Context) {
	c.logger.Info("dream: background consolidation loop started",
		"check_every", c.checkEvery,
		"min_age", c.minAge,
		"min_obs", c.minObs,
	)

	tk := time.NewTicker(c.checkEvery)
	defer tk.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("dream: background loop stopped")
			return
		case <-tk.C:
			if err := c.Run(ctx); err != nil {
				c.logger.Error("dream: consolidation run failed", "err", err)
			}
		}
	}
}
