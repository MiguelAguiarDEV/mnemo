package morpheus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Mock Command Runner ──────────────────────────────────────────────────

// mockRunner records calls and returns canned responses.
type mockRunner struct {
	mu       sync.Mutex
	calls    []mockCall
	handlers map[string]func(args []string) ([]byte, error)
}

type mockCall struct {
	Name string
	Args []string
}

func newMockRunner() *mockRunner {
	return &mockRunner{
		handlers: make(map[string]func(args []string) ([]byte, error)),
	}
}

func (m *mockRunner) Run(name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, mockCall{Name: name, Args: args})
	m.mu.Unlock()

	// Build a key from command + first arg for handler lookup.
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}

	m.mu.Lock()
	handler, ok := m.handlers[key]
	m.mu.Unlock()

	if ok {
		return handler(args)
	}
	return []byte(""), nil
}

func (m *mockRunner) onSearch(fn func(args []string) ([]byte, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers["engram search"] = fn
}

func (m *mockRunner) onSave(fn func(args []string) ([]byte, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers["engram save"] = fn
}

func (m *mockRunner) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockRunner) getCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockCall, len(m.calls))
	copy(result, m.calls)
	return result
}

// ─── Test Helpers ─────────────────────────────────────────────────────────

func tempLockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "consolidate.lock")
}

func makeObservations(n int, topicKey string) []Observation {
	obs := make([]Observation, n)
	now := time.Now()
	for i := 0; i < n; i++ {
		obs[i] = Observation{
			ID:        i + 1,
			Title:     fmt.Sprintf("obs-%d", i+1),
			Content:   fmt.Sprintf("content for observation %d", i+1),
			Type:      "discovery",
			TopicKey:  topicKey,
			Project:   "test-project",
			CreatedAt: now.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		}
	}
	return obs
}

func marshalObs(obs []Observation) []byte {
	data, _ := json.Marshal(obs)
	return data
}

// ─── Lock Tests ───────────────────────────────────────────────────────────

func TestLockFile_LastConsolidatedAt_NoFile(t *testing.T) {
	lock := newLockFile(filepath.Join(t.TempDir(), "nonexistent.lock"))

	ts, err := lock.lastConsolidatedAt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.IsZero() {
		t.Fatalf("expected zero time, got %v", ts)
	}
}

func TestLockFile_AcquireAndRelease(t *testing.T) {
	lockPath := tempLockPath(t)
	lock := newLockFile(lockPath)

	// Acquire should succeed.
	_, ok, err := lock.acquire()
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if !ok {
		t.Fatal("expected acquire to succeed")
	}

	// Verify PID written.
	body, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(body)))
	if pid != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), pid)
	}

	// Release should update mtime.
	beforeRelease, _ := lock.lastConsolidatedAt()
	time.Sleep(10 * time.Millisecond) // Ensure mtime differs.
	if err := lock.release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	afterRelease, _ := lock.lastConsolidatedAt()
	if !afterRelease.After(beforeRelease) || afterRelease.Equal(beforeRelease) {
		t.Fatalf("expected mtime to advance after release, before=%v after=%v", beforeRelease, afterRelease)
	}
}

func TestLockFile_AcquireBlocked(t *testing.T) {
	lockPath := tempLockPath(t)
	lock := newLockFile(lockPath)

	// Write another PID (current PID, simulating lock held by us).
	// Since it's our own PID and not stale, re-acquire should succeed
	// (same holder). To test blocking, write a different alive PID.
	// We use PID 1 (init) which should be alive on Linux.
	if err := os.WriteFile(lockPath, []byte("1"), 0o644); err != nil {
		t.Fatalf("write fake lock: %v", err)
	}

	// Set mtime to now (not stale).
	now := time.Now()
	os.Chtimes(lockPath, now, now)

	_, ok, err := lock.acquire()
	if err != nil {
		t.Fatalf("acquire error: %v", err)
	}
	if ok {
		t.Fatal("expected acquire to be blocked by PID 1")
	}
}

func TestLockFile_AcquireStaleLock(t *testing.T) {
	lockPath := tempLockPath(t)
	lock := newLockFile(lockPath)
	lock.staleTTL = time.Millisecond // Make locks stale almost immediately.

	// Write PID 1 with old mtime.
	if err := os.WriteFile(lockPath, []byte("1"), 0o644); err != nil {
		t.Fatalf("write fake lock: %v", err)
	}
	stale := time.Now().Add(-time.Hour)
	os.Chtimes(lockPath, stale, stale)

	_, ok, err := lock.acquire()
	if err != nil {
		t.Fatalf("acquire error: %v", err)
	}
	if !ok {
		t.Fatal("expected acquire to succeed on stale lock")
	}
}

func TestLockFile_Rollback(t *testing.T) {
	lockPath := tempLockPath(t)
	lock := newLockFile(lockPath)

	// Acquire.
	priorMtime, ok, err := lock.acquire()
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}

	// Rollback with zero priorMtime should remove the file.
	if !priorMtime.IsZero() {
		t.Skipf("prior mtime not zero (file existed), skipping remove test")
	}

	if err := lock.rollback(priorMtime); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("expected lock file to be removed after rollback with zero mtime")
	}
}

func TestLockFile_RollbackWithPriorMtime(t *testing.T) {
	lockPath := tempLockPath(t)
	lock := newLockFile(lockPath)

	// Create file with known mtime.
	prior := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	os.WriteFile(lockPath, []byte("0"), 0o644)
	os.Chtimes(lockPath, prior, prior)

	// Acquire (will overwrite body).
	_, ok, err := lock.acquire()
	if err != nil || !ok {
		t.Fatalf("acquire failed: %v", err)
	}

	// Rollback to prior mtime.
	if err := lock.rollback(prior); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	info, _ := os.Stat(lockPath)
	if !info.ModTime().Equal(prior) {
		t.Fatalf("expected mtime=%v, got %v", prior, info.ModTime())
	}
}

// ─── Orient Tests ─────────────────────────────────────────────────────────

func TestOrient_NeedsConsolidation(t *testing.T) {
	runner := newMockRunner()
	obs := makeObservations(25, "test-topic")
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	lockPath := tempLockPath(t)
	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(time.Millisecond),
		WithMinObservations(20),
		WithCommandRunner(runner),
	)

	needed, err := c.NeedsConsolidation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !needed {
		t.Fatal("expected consolidation to be needed")
	}
}

func TestOrient_TooSoon(t *testing.T) {
	runner := newMockRunner()
	lockPath := tempLockPath(t)

	// Create lock file with recent mtime.
	os.WriteFile(lockPath, []byte("0"), 0o644)
	now := time.Now()
	os.Chtimes(lockPath, now, now)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(24*time.Hour),
		WithMinObservations(5),
		WithCommandRunner(runner),
	)

	needed, err := c.NeedsConsolidation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if needed {
		t.Fatal("expected consolidation NOT needed (too soon)")
	}

	// Verify search was never called (cheapest gate first).
	if runner.callCount() != 0 {
		t.Fatalf("expected 0 CLI calls (time gate should bail early), got %d", runner.callCount())
	}
}

func TestOrient_NotEnoughObservations(t *testing.T) {
	runner := newMockRunner()
	obs := makeObservations(3, "test-topic")
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithMinAge(time.Millisecond),
		WithMinObservations(20),
		WithCommandRunner(runner),
	)

	needed, err := c.NeedsConsolidation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if needed {
		t.Fatal("expected consolidation NOT needed (not enough observations)")
	}
}

// ─── Gather Tests ─────────────────────────────────────────────────────────

func TestGather_GroupsByTopicKey(t *testing.T) {
	runner := newMockRunner()
	obs := append(
		makeObservations(3, "topic-a"),
		makeObservations(2, "topic-b")...,
	)
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	clusters, err := c.gather(context.Background())
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}

	// Check cluster sizes.
	clusterMap := make(map[string]int)
	for _, cl := range clusters {
		clusterMap[cl.TopicKey] = len(cl.Observations)
	}
	if clusterMap["topic-a"] != 3 {
		t.Fatalf("expected 3 in topic-a, got %d", clusterMap["topic-a"])
	}
	if clusterMap["topic-b"] != 2 {
		t.Fatalf("expected 2 in topic-b, got %d", clusterMap["topic-b"])
	}
}

func TestGather_NoTopicKeyGroupsByType(t *testing.T) {
	runner := newMockRunner()
	obs := []Observation{
		{ID: 1, Title: "obs-1", Content: "c1", Type: "decision", TopicKey: "", CreatedAt: time.Now().Format(time.RFC3339)},
		{ID: 2, Title: "obs-2", Content: "c2", Type: "decision", TopicKey: "", CreatedAt: time.Now().Format(time.RFC3339)},
		{ID: 3, Title: "obs-3", Content: "c3", Type: "bugfix", TopicKey: "", CreatedAt: time.Now().Format(time.RFC3339)},
	}
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	clusters, err := c.gather(context.Background())
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters (__type__decision, __type__bugfix), got %d", len(clusters))
	}
}

func TestGather_EngineError(t *testing.T) {
	runner := newMockRunner()
	runner.onSearch(func(args []string) ([]byte, error) {
		return []byte("connection refused"), fmt.Errorf("engram failed")
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	_, err := c.gather(context.Background())
	if err == nil {
		t.Fatal("expected error from gather")
	}
	if !strings.Contains(err.Error(), "engram search") {
		t.Fatalf("expected engram search error, got: %v", err)
	}
}

// ─── Consolidate Tests ────────────────────────────────────────────────────

func TestConsolidate_SingleObservation_Kept(t *testing.T) {
	c := New(WithLockPath(tempLockPath(t)))
	clusters := []TopicCluster{
		{TopicKey: "solo", Observations: makeObservations(1, "solo")},
	}

	results, err := c.consolidate(context.Background(), clusters)
	if err != nil {
		t.Fatalf("consolidate failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "kept" {
		t.Fatalf("expected action=kept, got %s", results[0].Action)
	}
}

func TestConsolidate_MultipleObservations_Merged(t *testing.T) {
	c := New(WithLockPath(tempLockPath(t)))

	now := time.Now()
	obs := []Observation{
		{ID: 1, Title: "old-title", Content: "fact A\nfact B", Type: "discovery", TopicKey: "topic-x", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339)},
		{ID: 2, Title: "new-title", Content: "fact B\nfact C", Type: "discovery", TopicKey: "topic-x", CreatedAt: now.Format(time.RFC3339)},
	}
	clusters := []TopicCluster{
		{TopicKey: "topic-x", Observations: obs},
	}

	results, err := c.consolidate(context.Background(), clusters)
	if err != nil {
		t.Fatalf("consolidate failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Action != "merged" {
		t.Fatalf("expected action=merged, got %s", r.Action)
	}
	if r.MergedTitle != "new-title" {
		t.Fatalf("expected newest title 'new-title', got %q", r.MergedTitle)
	}
	if len(r.SourceIDs) != 2 {
		t.Fatalf("expected 2 source IDs, got %d", len(r.SourceIDs))
	}
	// Content should have facts A, B, C (B deduplicated).
	if !strings.Contains(r.MergedContent, "fact A") {
		t.Fatal("merged content missing 'fact A'")
	}
	if !strings.Contains(r.MergedContent, "fact C") {
		t.Fatal("merged content missing 'fact C'")
	}
}

func TestConsolidate_ContradictionResolution(t *testing.T) {
	c := New(WithLockPath(tempLockPath(t)))

	now := time.Now()
	obs := []Observation{
		{ID: 1, Title: "old", Content: "the API uses REST", Type: "discovery", TopicKey: "api-style", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{ID: 2, Title: "updated", Content: "The API uses REST", Type: "discovery", TopicKey: "api-style", CreatedAt: now.Format(time.RFC3339)},
	}
	clusters := []TopicCluster{
		{TopicKey: "api-style", Observations: obs},
	}

	results, err := c.consolidate(context.Background(), clusters)
	if err != nil {
		t.Fatalf("consolidate failed: %v", err)
	}

	r := results[0]
	// "the API uses REST" and "The API uses REST" normalize to the same line.
	// The newer version should be kept.
	lines := splitContentLines(r.MergedContent)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after dedup, got %d: %v", len(lines), lines)
	}
	if lines[0] != "The API uses REST" {
		t.Fatalf("expected newer version 'The API uses REST', got %q", lines[0])
	}
}

func TestConsolidate_EmptyCluster(t *testing.T) {
	c := New(WithLockPath(tempLockPath(t)))
	clusters := []TopicCluster{
		{TopicKey: "empty", Observations: nil},
	}

	results, err := c.consolidate(context.Background(), clusters)
	if err != nil {
		t.Fatalf("consolidate failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty cluster, got %d", len(results))
	}
}

// ─── Prune Tests ──────────────────────────────────────────────────────────

func TestPrune_SavesConsolidated(t *testing.T) {
	runner := newMockRunner()
	var savedTitle, savedContent string
	runner.onSave(func(args []string) ([]byte, error) {
		if len(args) >= 3 {
			savedTitle = args[1]
			savedContent = args[2]
		}
		return []byte("saved OK"), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
		WithProject("test-proj"),
	)

	results := []MergeResult{
		{
			TopicKey:      "topic-x",
			SourceIDs:     []int{1, 2, 3},
			MergedTitle:   "merged observation",
			MergedContent: "combined content here",
			Action:        "merged",
		},
	}

	auditLog, err := c.prune(context.Background(), results)
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
	if len(auditLog) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(auditLog))
	}
	if !strings.Contains(auditLog[0], "merged 3 observations") {
		t.Fatalf("audit log doesn't mention merge: %s", auditLog[0])
	}
	if !strings.HasPrefix(savedTitle, "consolidated:") {
		t.Fatalf("expected title to start with 'consolidated:', got %q", savedTitle)
	}
	if !strings.Contains(savedContent, "combined content here") {
		t.Fatalf("saved content missing merged content: %q", savedContent)
	}

	// Verify --project flag was passed.
	calls := runner.getCalls()
	found := false
	for _, call := range calls {
		for i, arg := range call.Args {
			if arg == "--project" && i+1 < len(call.Args) && call.Args[i+1] == "test-proj" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected --project test-proj in save command")
	}
}

func TestPrune_KeptObservation_NoSave(t *testing.T) {
	runner := newMockRunner()
	runner.onSave(func(args []string) ([]byte, error) {
		t.Fatal("save should not be called for kept observations")
		return nil, nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	results := []MergeResult{
		{TopicKey: "solo", SourceIDs: []int{1}, MergedTitle: "single obs", Action: "kept"},
	}

	auditLog, err := c.prune(context.Background(), results)
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
	if len(auditLog) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(auditLog))
	}
	if !strings.Contains(auditLog[0], "kept") {
		t.Fatalf("audit log should mention 'kept': %s", auditLog[0])
	}
}

func TestPrune_SaveFailure_Logged(t *testing.T) {
	runner := newMockRunner()
	runner.onSave(func(args []string) ([]byte, error) {
		return []byte("error"), fmt.Errorf("save failed")
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	results := []MergeResult{
		{TopicKey: "fail", SourceIDs: []int{1, 2}, MergedTitle: "will fail", MergedContent: "content", Action: "merged"},
	}

	auditLog, err := c.prune(context.Background(), results)
	if err != nil {
		t.Fatalf("prune should not return error on save failure: %v", err)
	}
	if len(auditLog) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(auditLog))
	}
	if !strings.Contains(auditLog[0], "FAILED") {
		t.Fatalf("audit log should mention FAILED: %s", auditLog[0])
	}
}

// ─── Full Run Tests ───────────────────────────────────────────────────────

func TestRun_FullCycle(t *testing.T) {
	runner := newMockRunner()
	obs := makeObservations(25, "full-test")
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})
	var saveCalled atomic.Int32
	runner.onSave(func(args []string) ([]byte, error) {
		saveCalled.Add(1)
		return []byte("saved"), nil
	})

	lockPath := tempLockPath(t)
	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(time.Millisecond),
		WithMinObservations(5),
		WithCommandRunner(runner),
	)

	err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify lock file mtime was updated.
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("lock file should exist after successful run: %v", err)
	}
	if time.Since(info.ModTime()) > time.Second {
		t.Fatal("lock mtime should be recent after successful run")
	}

	// Verify save was called (25 obs, all same topic -> 1 merged save).
	if saveCalled.Load() != 1 {
		t.Fatalf("expected 1 save call, got %d", saveCalled.Load())
	}
}

func TestRun_SkipsWhenNotNeeded(t *testing.T) {
	runner := newMockRunner()
	lockPath := tempLockPath(t)

	// Create recent lock file.
	os.WriteFile(lockPath, []byte("0"), 0o644)
	now := time.Now()
	os.Chtimes(lockPath, now, now)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(24*time.Hour),
		WithCommandRunner(runner),
	)

	err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not error on skip: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("expected no CLI calls when skipping, got %d", runner.callCount())
	}
}

func TestRun_LockBlocked(t *testing.T) {
	runner := newMockRunner()
	obs := makeObservations(25, "blocked")
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	lockPath := tempLockPath(t)
	// Write PID 1 (init) to simulate another holder.
	os.WriteFile(lockPath, []byte("1"), 0o644)
	now := time.Now()
	os.Chtimes(lockPath, now, now)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(time.Millisecond),
		WithMinObservations(5),
		WithCommandRunner(runner),
	)

	err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not error when lock is held: %v", err)
	}
}

func TestRun_RollbackOnGatherError(t *testing.T) {
	runner := newMockRunner()
	searchCallCount := 0
	runner.onSearch(func(args []string) ([]byte, error) {
		searchCallCount++
		if searchCallCount == 1 {
			// First call: orient check returns enough observations.
			return marshalObs(makeObservations(25, "x")), nil
		}
		// Second call: gather fails.
		return nil, fmt.Errorf("engram unavailable")
	})

	lockPath := tempLockPath(t)
	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(time.Millisecond),
		WithMinObservations(5),
		WithCommandRunner(runner),
	)

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from gather failure")
	}

	// Lock file should be rolled back (removed since no prior).
	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Fatal("expected lock file to be removed after rollback")
	}
}

// ─── Background Loop Test ─────────────────────────────────────────────────

func TestRunBackground_CancelStops(t *testing.T) {
	runner := newMockRunner()
	lockPath := tempLockPath(t)

	// Create recent lock to prevent actual consolidation.
	os.WriteFile(lockPath, []byte("0"), 0o644)
	now := time.Now()
	os.Chtimes(lockPath, now, now)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(24*time.Hour),
		WithCheckInterval(10*time.Millisecond),
		WithCommandRunner(runner),
	)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		c.RunBackground(ctx)
		close(done)
	}()

	// Let it tick once.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good, it stopped.
	case <-time.After(2 * time.Second):
		t.Fatal("RunBackground did not stop after context cancel")
	}
}

// ─── Helper Tests ─────────────────────────────────────────────────────────

func TestNormalizeContentLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  Hello World  ", "hello world"},
		{"- bullet point", "bullet point"},
		{"* another bullet", "another bullet"},
		{"> quoted text", "quoted text"},
		{"  multiple   spaces  ", "multiple spaces"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeContentLine(tt.input)
		if got != tt.want {
			t.Errorf("normalizeContentLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitContentLines(t *testing.T) {
	content := "line one\n\nline two\n  \nline three"
	lines := splitContentLines(content)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
}

func TestParseLineJSON(t *testing.T) {
	input := `{"id":1,"title":"obs1","content":"c1","type":"discovery","topic_key":"t1","created_at":"2025-01-01T00:00:00Z"}
{"id":2,"title":"obs2","content":"c2","type":"bugfix","topic_key":"t2","created_at":"2025-01-02T00:00:00Z"}`

	obs := parseLineJSON([]byte(input))
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obs))
	}
	if obs[0].Title != "obs1" {
		t.Fatalf("expected title 'obs1', got %q", obs[0].Title)
	}
}

func TestCountByAction(t *testing.T) {
	results := []MergeResult{
		{Action: "merged"},
		{Action: "kept"},
		{Action: "merged"},
		{Action: "kept"},
		{Action: "kept"},
	}
	if countByAction(results, "merged") != 2 {
		t.Fatal("expected 2 merged")
	}
	if countByAction(results, "kept") != 3 {
		t.Fatal("expected 3 kept")
	}
}

// ─── Option Tests ─────────────────────────────────────────────────────────

func TestNew_Defaults(t *testing.T) {
	c := New()
	if c.engramBin != "engram" {
		t.Fatalf("expected default engramBin='engram', got %q", c.engramBin)
	}
	if c.minAge != DefaultMinAge {
		t.Fatalf("expected default minAge=%v, got %v", DefaultMinAge, c.minAge)
	}
	if c.minObs != DefaultMinObs {
		t.Fatalf("expected default minObs=%d, got %d", DefaultMinObs, c.minObs)
	}
	if c.checkEvery != DefaultCheckEvery {
		t.Fatalf("expected default checkEvery=%v, got %v", DefaultCheckEvery, c.checkEvery)
	}
	if c.lock == nil {
		t.Fatal("expected default lock to be initialized")
	}
}

func TestNew_WithOptions(t *testing.T) {
	lockPath := tempLockPath(t)
	c := New(
		WithEngramBin("/usr/bin/engram"),
		WithLockPath(lockPath),
		WithMinAge(12*time.Hour),
		WithMinObservations(10),
		WithCheckInterval(30*time.Minute),
		WithProject("my-project"),
	)

	if c.engramBin != "/usr/bin/engram" {
		t.Fatalf("expected engramBin='/usr/bin/engram', got %q", c.engramBin)
	}
	if c.minAge != 12*time.Hour {
		t.Fatalf("expected minAge=12h, got %v", c.minAge)
	}
	if c.minObs != 10 {
		t.Fatalf("expected minObs=10, got %d", c.minObs)
	}
	if c.checkEvery != 30*time.Minute {
		t.Fatalf("expected checkEvery=30m, got %v", c.checkEvery)
	}
	if c.project != "my-project" {
		t.Fatalf("expected project='my-project', got %q", c.project)
	}
}

func TestWithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	c := New(WithLogger(logger))
	if c.logger != logger {
		t.Fatal("expected custom logger to be set")
	}
}

// ─── CountNewObservations Tests ───────────────────────────────────────────

func TestCountNewObservations_WithLastRun(t *testing.T) {
	runner := newMockRunner()

	// Create observations, some old and some new.
	now := time.Now()
	lastRun := now.Add(-12 * time.Hour)
	obs := []Observation{
		{ID: 1, Title: "old", Content: "old", Type: "discovery", CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)},
		{ID: 2, Title: "new1", Content: "new", Type: "discovery", CreatedAt: now.Add(-6 * time.Hour).Format(time.RFC3339)},
		{ID: 3, Title: "new2", Content: "new", Type: "discovery", CreatedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
	}
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	lockPath := tempLockPath(t)
	// Create lock with mtime = lastRun
	os.WriteFile(lockPath, []byte("0"), 0o644)
	os.Chtimes(lockPath, lastRun, lastRun)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithCommandRunner(runner),
	)

	count, err := c.countNewObservations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only obs 2 and 3 are newer than lastRun.
	if count != 2 {
		t.Fatalf("expected 2 new observations, got %d", count)
	}
}

func TestCountNewObservations_UnparsableDate(t *testing.T) {
	runner := newMockRunner()
	obs := []Observation{
		{ID: 1, Title: "bad-date", Content: "x", Type: "discovery", CreatedAt: "not-a-date"},
	}
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	lockPath := tempLockPath(t)
	// Set old mtime so time gate passes.
	past := time.Now().Add(-48 * time.Hour)
	os.WriteFile(lockPath, []byte("0"), 0o644)
	os.Chtimes(lockPath, past, past)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithCommandRunner(runner),
	)

	count, err := c.countNewObservations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unparsable dates should be counted (included).
	if count != 1 {
		t.Fatalf("expected 1 (unparsable dates included), got %d", count)
	}
}

func TestCountNewObservations_LineDelimitedJSON(t *testing.T) {
	runner := newMockRunner()
	now := time.Now()
	runner.onSearch(func(args []string) ([]byte, error) {
		// Return line-delimited JSON instead of array.
		line1 := fmt.Sprintf(`{"id":1,"title":"obs1","content":"c1","type":"discovery","topic_key":"t","created_at":"%s"}`, now.Format(time.RFC3339))
		line2 := fmt.Sprintf(`{"id":2,"title":"obs2","content":"c2","type":"discovery","topic_key":"t","created_at":"%s"}`, now.Format(time.RFC3339))
		return []byte(line1 + "\n" + line2), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	count, err := c.countNewObservations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestCountNewObservations_WithProject(t *testing.T) {
	runner := newMockRunner()
	obs := makeObservations(5, "topic")
	runner.onSearch(func(args []string) ([]byte, error) {
		// Verify --project is passed.
		for i, arg := range args {
			if arg == "--project" && i+1 < len(args) && args[i+1] == "my-proj" {
				return marshalObs(obs), nil
			}
		}
		t.Fatal("expected --project my-proj in search args")
		return nil, nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
		WithProject("my-proj"),
	)

	count, err := c.countNewObservations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5, got %d", count)
	}
}

// ─── Run Edge Cases ───────────────────────────────────────────────────────

func TestRun_EmptyGather(t *testing.T) {
	runner := newMockRunner()
	searchCallCount := 0
	runner.onSearch(func(args []string) ([]byte, error) {
		searchCallCount++
		if searchCallCount == 1 {
			// orient: enough observations to trigger.
			return marshalObs(makeObservations(25, "x")), nil
		}
		// gather: returns empty list.
		return []byte("[]"), nil
	})

	lockPath := tempLockPath(t)
	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(time.Millisecond),
		WithMinObservations(5),
		WithCommandRunner(runner),
	)

	err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should succeed with empty gather: %v", err)
	}

	// Lock should exist (released after empty gather).
	if _, statErr := os.Stat(lockPath); os.IsNotExist(statErr) {
		t.Fatal("lock file should exist after empty gather (release sets mtime)")
	}
}

func TestRun_LockBlockedAfterOrient(t *testing.T) {
	runner := newMockRunner()
	obs := makeObservations(25, "blocked")
	runner.onSearch(func(args []string) ([]byte, error) {
		return marshalObs(obs), nil
	})

	lockPath := tempLockPath(t)
	// Write PID 1 with fresh mtime but set the lock's lastConsolidatedAt
	// far in the past so orient passes but lock is held by PID 1.
	os.WriteFile(lockPath, []byte("1"), 0o644)
	now := time.Now()
	os.Chtimes(lockPath, now, now)

	c := New(
		WithEngramBin("engram"),
		WithLockPath(lockPath),
		WithMinAge(time.Millisecond), // orient passes (lock mtime is recent but minAge is tiny)
		WithMinObservations(5),
		WithCommandRunner(runner),
	)
	// Hack: orient will check lock mtime as lastConsolidatedAt, which is
	// now. With minAge=1ms it should still pass after a brief sleep.
	time.Sleep(2 * time.Millisecond)

	err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not error when lock blocked: %v", err)
	}
}

// ─── Prune with __type__ topic key ────────────────────────────────────────

func TestPrune_TypePrefixTopicKey_NoTopicKeyFlag(t *testing.T) {
	runner := newMockRunner()
	runner.onSave(func(args []string) ([]byte, error) {
		// Verify --topic-key is NOT passed for __type__ prefix.
		for _, arg := range args {
			if arg == "--topic-key" {
				t.Fatal("should not pass --topic-key for __type__ prefix topics")
			}
		}
		return []byte("ok"), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithCommandRunner(runner),
	)

	results := []MergeResult{
		{
			TopicKey:      "__type__discovery",
			SourceIDs:     []int{1, 2},
			MergedTitle:   "grouped by type",
			MergedContent: "content",
			Action:        "merged",
		},
	}

	_, err := c.prune(context.Background(), results)
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
}

// ─── Background Loop with Error ───────────────────────────────────────────

func TestRunBackground_HandlesRunError(t *testing.T) {
	runner := newMockRunner()
	callCount := 0
	runner.onSearch(func(args []string) ([]byte, error) {
		callCount++
		if callCount <= 2 {
			// First cycle: orient returns enough obs, gather fails.
			if callCount == 1 {
				return marshalObs(makeObservations(25, "x")), nil
			}
			return nil, fmt.Errorf("gather failed")
		}
		// Subsequent cycles: not enough obs.
		return marshalObs(makeObservations(1, "x")), nil
	})

	c := New(
		WithEngramBin("engram"),
		WithLockPath(tempLockPath(t)),
		WithMinAge(time.Millisecond),
		WithMinObservations(5),
		WithCheckInterval(10*time.Millisecond),
		WithCommandRunner(runner),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.RunBackground(ctx)
		close(done)
	}()

	// Let it run a couple ticks (first will error, second will skip).
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunBackground did not stop after cancel")
	}
}
