package athena

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gentleman-Programming/engram/internal/prometheus"
)

// ─── Job ─────────────────────────────────────────────────────────────────────

// JobStatus constants.
const (
	JobStatusPending = "pending"
	JobStatusRunning = "running"
	JobStatusSuccess = "success"
	JobStatusError   = "error"
)

// Job represents a delegated async task.
type Job struct {
	ID          string                   `json:"id"`
	Task        string                   `json:"task"`
	Project     string                   `json:"project"`
	Status      string                   `json:"status"`
	CreatedAt   time.Time                `json:"created_at"`
	StartedAt   *time.Time               `json:"started_at,omitempty"`
	CompletedAt *time.Time               `json:"completed_at,omitempty"`
	Result      *prometheus.DelegateResult `json:"result,omitempty"`
	Error       string                   `json:"error,omitempty"`
}

// ─── JobTracker ──────────────────────────────────────────────────────────────

// JobTracker manages the lifecycle of async delegation jobs.
type JobTracker struct {
	mu         sync.RWMutex
	jobs       map[string]*Job
	seq        atomic.Int64
	logger     *slog.Logger
	onComplete func(job *Job)
	nowFunc    func() time.Time // for testing
}

// NewJobTracker creates a new JobTracker.
// onComplete is called (in a goroutine) when a job finishes -- use it for notifications.
func NewJobTracker(logger *slog.Logger, onComplete func(job *Job)) *JobTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &JobTracker{
		jobs:       make(map[string]*Job),
		logger:     logger,
		onComplete: onComplete,
		nowFunc:    time.Now,
	}
}

// Create registers a new pending job and returns it.
func (jt *JobTracker) Create(task, project string) *Job {
	id := fmt.Sprintf("%d", jt.seq.Add(1))

	job := &Job{
		ID:        id,
		Task:      task,
		Project:   project,
		Status:    JobStatusPending,
		CreatedAt: jt.nowFunc(),
	}

	jt.mu.Lock()
	jt.jobs[id] = job
	jt.mu.Unlock()

	jt.logger.Info("job created", "id", id, "task", task, "project", project)
	return job
}

// Get returns a job by ID.
func (jt *JobTracker) Get(id string) (*Job, bool) {
	jt.mu.RLock()
	defer jt.mu.RUnlock()

	job, ok := jt.jobs[id]
	return job, ok
}

// List returns all jobs, most recent first.
func (jt *JobTracker) List() []*Job {
	jt.mu.RLock()
	defer jt.mu.RUnlock()

	result := make([]*Job, 0, len(jt.jobs))
	for _, job := range jt.jobs {
		result = append(result, job)
	}

	// Sort by created_at descending.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].CreatedAt.After(result[i].CreatedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// RunAsync spawns a goroutine that executes the job using the given worker.
// It updates the job status through its lifecycle and calls onComplete when done.
func (jt *JobTracker) RunAsync(job *Job, worker prometheus.WorkerExecutor, req prometheus.DelegateRequest) {
	// Mark as running.
	jt.mu.Lock()
	now := jt.nowFunc()
	job.Status = JobStatusRunning
	job.StartedAt = &now
	jt.mu.Unlock()

	jt.logger.Info("job started", "id", job.ID)

	go func() {
		result, err := worker.Execute(context.Background(), req)

		jt.mu.Lock()
		completedAt := jt.nowFunc()
		job.CompletedAt = &completedAt

		if err != nil {
			job.Status = JobStatusError
			if result != nil {
				job.Error = result.Error
				job.Result = result
			} else {
				job.Error = err.Error()
			}
			jt.logger.Error("job failed", "id", job.ID, "err", err)
		} else {
			job.Status = JobStatusSuccess
			job.Result = result
			jt.logger.Info("job completed", "id", job.ID,
				"duration", result.Duration,
				"tokens_in", result.TokensUsed.InputTokens,
				"tokens_out", result.TokensUsed.OutputTokens,
			)
		}
		jt.mu.Unlock()

		// Notify via callback (non-blocking).
		if jt.onComplete != nil {
			jt.onComplete(job)
		}
	}()
}

// RunNativeAsync spawns a goroutine that executes the job using a NativeWorker
// (PROMETHEUS v2 with Go-native tools instead of OpenCode).
func (jt *JobTracker) RunNativeAsync(job *Job, worker *prometheus.NativeWorker, req prometheus.DelegateRequest) {
	// Mark as running.
	jt.mu.Lock()
	now := jt.nowFunc()
	job.Status = JobStatusRunning
	job.StartedAt = &now
	jt.mu.Unlock()

	jt.logger.Info("native job started", "id", job.ID)

	go func() {
		result, err := worker.Execute(context.Background(), req)

		jt.mu.Lock()
		completedAt := jt.nowFunc()
		job.CompletedAt = &completedAt

		if err != nil {
			job.Status = JobStatusError
			if result != nil {
				job.Error = result.Error
				job.Result = result
			} else {
				job.Error = err.Error()
			}
			jt.logger.Error("native job failed", "id", job.ID, "err", err)
		} else {
			job.Status = JobStatusSuccess
			job.Result = result
			jt.logger.Info("native job completed", "id", job.ID,
				"duration", result.Duration,
				"tokens_in", result.TokensUsed.InputTokens,
				"tokens_out", result.TokensUsed.OutputTokens,
			)
		}
		jt.mu.Unlock()

		// Notify via callback (non-blocking).
		if jt.onComplete != nil {
			jt.onComplete(job)
		}
	}()
}
