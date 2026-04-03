package athena

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/prometheus"
)

// ─── Mock Worker Backend ─────────────────────────────────────────────────────

type mockWorkerBackend struct {
	sessionID  string
	sessionErr error
	response   prometheus.Response
	sendErr    error
	delay      time.Duration // simulate work
}

func (m *mockWorkerBackend) CreateSession(ctx context.Context) (string, error) {
	return m.sessionID, m.sessionErr
}

func (m *mockWorkerBackend) Send(ctx context.Context, req prometheus.Request) (prometheus.Response, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	return m.response, m.sendErr
}

// ─── JobTracker.Create Tests ─────────────────────────────────────────────────

func TestJobTrackerCreate(t *testing.T) {
	jt := NewJobTracker(nil, nil)

	job := jt.Create("fix bug", "jarvis")

	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.Task != "fix bug" {
		t.Errorf("task = %q, want %q", job.Task, "fix bug")
	}
	if job.Project != "jarvis" {
		t.Errorf("project = %q, want %q", job.Project, "jarvis")
	}
	if job.Status != JobStatusPending {
		t.Errorf("status = %q, want %q", job.Status, JobStatusPending)
	}
	if job.CreatedAt.IsZero() {
		t.Error("created_at should not be zero")
	}
}

func TestJobTrackerCreate_IncrementingIDs(t *testing.T) {
	jt := NewJobTracker(nil, nil)

	j1 := jt.Create("task1", "p1")
	j2 := jt.Create("task2", "p2")
	j3 := jt.Create("task3", "p3")

	if j1.ID == j2.ID || j2.ID == j3.ID {
		t.Errorf("IDs should be unique: %s, %s, %s", j1.ID, j2.ID, j3.ID)
	}
}

// ─── JobTracker.Get Tests ────────────────────────────────────────────────────

func TestJobTrackerGet_Found(t *testing.T) {
	jt := NewJobTracker(nil, nil)
	created := jt.Create("test", "proj")

	job, ok := jt.Get(created.ID)
	if !ok {
		t.Fatal("expected job to be found")
	}
	if job.Task != "test" {
		t.Errorf("task = %q, want %q", job.Task, "test")
	}
}

func TestJobTrackerGet_NotFound(t *testing.T) {
	jt := NewJobTracker(nil, nil)

	_, ok := jt.Get("nonexistent")
	if ok {
		t.Error("expected job not to be found")
	}
}

// ─── JobTracker.List Tests ───────────────────────────────────────────────────

func TestJobTrackerList_Empty(t *testing.T) {
	jt := NewJobTracker(nil, nil)

	jobs := jt.List()
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestJobTrackerList_Multiple(t *testing.T) {
	jt := NewJobTracker(nil, nil)

	// Use a custom time function for deterministic ordering.
	baseTime := time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)
	callCount := 0
	jt.nowFunc = func() time.Time {
		callCount++
		return baseTime.Add(time.Duration(callCount) * time.Second)
	}

	jt.Create("first", "p1")
	jt.Create("second", "p2")
	jt.Create("third", "p3")

	jobs := jt.List()
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}

	// Most recent first.
	if jobs[0].Task != "third" {
		t.Errorf("first job should be 'third' (most recent), got %q", jobs[0].Task)
	}
	if jobs[2].Task != "first" {
		t.Errorf("last job should be 'first' (oldest), got %q", jobs[2].Task)
	}
}

// ─── JobTracker.RunAsync Tests ───────────────────────────────────────────────

func TestJobTrackerRunAsync_Success(t *testing.T) {
	var completedJob *Job
	var wg sync.WaitGroup
	wg.Add(1)

	onComplete := func(job *Job) {
		completedJob = job
		wg.Done()
	}

	jt := NewJobTracker(nil, onComplete)

	backend := &mockWorkerBackend{
		sessionID: "sess-1",
		response: prometheus.Response{
			Text: "Bug fixed successfully",
			Usage: prometheus.TokenUsage{
				InputTokens:  100,
				OutputTokens: 50,
			},
		},
	}

	worker := prometheus.NewWorker(backend, nil)
	job := jt.Create("fix bug", "jarvis")

	req := prometheus.DelegateRequest{
		Task:    "fix bug",
		Project: "jarvis",
	}

	jt.RunAsync(job, worker, req)

	// Wait for completion with timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job completion")
	}

	if completedJob == nil {
		t.Fatal("onComplete not called")
	}
	if completedJob.Status != JobStatusSuccess {
		t.Errorf("status = %q, want %q", completedJob.Status, JobStatusSuccess)
	}
	if completedJob.Result == nil {
		t.Fatal("result should not be nil")
	}
	if completedJob.Result.Output != "Bug fixed successfully" {
		t.Errorf("output = %q", completedJob.Result.Output)
	}
	if completedJob.StartedAt == nil {
		t.Error("started_at should be set")
	}
	if completedJob.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
	if completedJob.Error != "" {
		t.Errorf("error should be empty, got %q", completedJob.Error)
	}
}

func TestJobTrackerRunAsync_Error(t *testing.T) {
	var completedJob *Job
	var wg sync.WaitGroup
	wg.Add(1)

	onComplete := func(job *Job) {
		completedJob = job
		wg.Done()
	}

	jt := NewJobTracker(nil, onComplete)

	backend := &mockWorkerBackend{
		sessionID: "sess-err",
		sendErr:   fmt.Errorf("model unavailable"),
	}

	worker := prometheus.NewWorker(backend, nil)
	job := jt.Create("do something", "proj")

	req := prometheus.DelegateRequest{Task: "do something"}
	jt.RunAsync(job, worker, req)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	if completedJob.Status != JobStatusError {
		t.Errorf("status = %q, want %q", completedJob.Status, JobStatusError)
	}
	if completedJob.Error == "" {
		t.Error("error should be set")
	}
}

func TestJobTrackerRunAsync_SessionError(t *testing.T) {
	var completedJob *Job
	var wg sync.WaitGroup
	wg.Add(1)

	onComplete := func(job *Job) {
		completedJob = job
		wg.Done()
	}

	jt := NewJobTracker(nil, onComplete)

	backend := &mockWorkerBackend{
		sessionErr: fmt.Errorf("connection refused"),
	}

	worker := prometheus.NewWorker(backend, nil)
	job := jt.Create("task", "proj")

	jt.RunAsync(job, worker, prometheus.DelegateRequest{Task: "task"})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	if completedJob.Status != JobStatusError {
		t.Errorf("status = %q, want %q", completedJob.Status, JobStatusError)
	}
}

func TestJobTrackerRunAsync_StatusTransitions(t *testing.T) {
	// Verify the job goes through pending -> running -> success.
	jt := NewJobTracker(nil, nil)

	backend := &mockWorkerBackend{
		sessionID: "sess-trans",
		response:  prometheus.Response{Text: "done"},
		delay:     50 * time.Millisecond,
	}

	worker := prometheus.NewWorker(backend, nil)
	job := jt.Create("task", "proj")

	if job.Status != JobStatusPending {
		t.Fatalf("initial status = %q, want %q", job.Status, JobStatusPending)
	}

	jt.RunAsync(job, worker, prometheus.DelegateRequest{Task: "task"})

	// Give it a moment to start.
	time.Sleep(10 * time.Millisecond)

	jt.mu.RLock()
	runningStatus := job.Status
	jt.mu.RUnlock()

	if runningStatus != JobStatusRunning {
		t.Errorf("running status = %q, want %q", runningStatus, JobStatusRunning)
	}

	// Wait for completion.
	time.Sleep(200 * time.Millisecond)

	jt.mu.RLock()
	finalStatus := job.Status
	jt.mu.RUnlock()

	if finalStatus != JobStatusSuccess {
		t.Errorf("final status = %q, want %q", finalStatus, JobStatusSuccess)
	}
}

func TestJobTrackerRunAsync_NoCallback(t *testing.T) {
	// Verify it doesn't panic with nil onComplete.
	jt := NewJobTracker(nil, nil)

	backend := &mockWorkerBackend{
		sessionID: "sess-nocb",
		response:  prometheus.Response{Text: "ok"},
	}

	worker := prometheus.NewWorker(backend, nil)
	job := jt.Create("task", "proj")

	jt.RunAsync(job, worker, prometheus.DelegateRequest{Task: "task"})

	// Wait a bit for the goroutine to finish.
	time.Sleep(200 * time.Millisecond)

	jt.mu.RLock()
	status := job.Status
	jt.mu.RUnlock()

	if status != JobStatusSuccess {
		t.Errorf("status = %q, want %q", status, JobStatusSuccess)
	}
}

// ─── Concurrent Safety Test ─────────────────────────────────────────────────

func TestJobTracker_ConcurrentAccess(t *testing.T) {
	jt := NewJobTracker(nil, nil)

	var wg sync.WaitGroup

	// Spawn multiple goroutines creating and listing jobs concurrently.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			jt.Create(fmt.Sprintf("task-%d", n), "proj")
			jt.List()
			jt.Get(fmt.Sprintf("%d", n))
		}(i)
	}

	wg.Wait()

	jobs := jt.List()
	if len(jobs) != 10 {
		t.Errorf("expected 10 jobs, got %d", len(jobs))
	}
}

// ─── Integration Test: Full Lifecycle ────────────────────────────────────────

func TestJobTracker_FullLifecycle(t *testing.T) {
	var notifications []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2) // 2 jobs

	onComplete := func(job *Job) {
		mu.Lock()
		notifications = append(notifications, fmt.Sprintf("%s:%s", job.ID, job.Status))
		mu.Unlock()
		wg.Done()
	}

	jt := NewJobTracker(nil, onComplete)

	// Create a success backend and an error backend.
	successBackend := &mockWorkerBackend{
		sessionID: "sess-ok",
		response:  prometheus.Response{Text: "all good"},
	}
	errorBackend := &mockWorkerBackend{
		sessionID: "sess-fail",
		sendErr:   fmt.Errorf("kaboom"),
	}

	successWorker := prometheus.NewWorker(successBackend, nil)
	errorWorker := prometheus.NewWorker(errorBackend, nil)

	j1 := jt.Create("good task", "proj")
	j2 := jt.Create("bad task", "proj")

	jt.RunAsync(j1, successWorker, prometheus.DelegateRequest{Task: "good task"})
	jt.RunAsync(j2, errorWorker, prometheus.DelegateRequest{Task: "bad task"})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for jobs")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(notifications) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(notifications))
	}

	// Verify both jobs are in the list.
	jobs := jt.List()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	// Verify we can retrieve each by ID.
	got1, ok := jt.Get(j1.ID)
	if !ok {
		t.Fatal("job 1 not found")
	}
	if got1.Status != JobStatusSuccess {
		t.Errorf("job1 status = %q, want %q", got1.Status, JobStatusSuccess)
	}

	got2, ok := jt.Get(j2.ID)
	if !ok {
		t.Fatal("job 2 not found")
	}
	if got2.Status != JobStatusError {
		t.Errorf("job2 status = %q, want %q", got2.Status, JobStatusError)
	}
}
