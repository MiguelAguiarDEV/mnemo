package sentinel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ─── Helpers ─────────────────────────────────────────────────────────────

// mockCheck returns a Check with a controllable result.
func mockCheck(name string, result *CheckResult, err error) Check {
	return Check{
		Name: name,
		Fn: func(ctx context.Context) (*CheckResult, error) {
			return result, err
		},
	}
}

// mockCheckWithInterval returns a Check with a specific interval.
func mockCheckWithInterval(name string, interval time.Duration, result *CheckResult) Check {
	return Check{
		Name:     name,
		Interval: interval,
		Fn: func(ctx context.Context) (*CheckResult, error) {
			return result, nil
		},
	}
}

// notificationRecorder records notifications sent by the sentinel.
type notificationRecorder struct {
	mu    sync.Mutex
	calls []notification
}

type notification struct {
	title   string
	message string
}

func (r *notificationRecorder) send(title, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, notification{title: title, message: message})
	return nil
}

func (r *notificationRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *notificationRecorder) last() notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return notification{}
	}
	return r.calls[len(r.calls)-1]
}

// ─── Tests ───────────────────────────────────────────────────────────────

func TestNew_Defaults(t *testing.T) {
	tk := New()

	if tk.interval != DefaultInterval {
		t.Errorf("expected default interval %v, got %v", DefaultInterval, tk.interval)
	}
	if tk.logger == nil {
		t.Error("expected non-nil logger")
	}
	if tk.notifier != nil {
		t.Error("expected nil notifier by default")
	}
	if len(tk.checks) != 0 {
		t.Errorf("expected 0 checks, got %d", len(tk.checks))
	}
}

func TestNew_WithOptions(t *testing.T) {
	rec := &notificationRecorder{}
	checks := []Check{
		mockCheck("test1", &CheckResult{Status: StatusOK, Message: "ok"}, nil),
	}

	tk := New(
		WithInterval(5*time.Minute),
		WithNotifier(rec.send),
		WithChecks(checks),
	)

	if tk.interval != 5*time.Minute {
		t.Errorf("expected interval 5m, got %v", tk.interval)
	}
	if tk.notifier == nil {
		t.Error("expected non-nil notifier")
	}
	if len(tk.checks) != 1 {
		t.Errorf("expected 1 check, got %d", len(tk.checks))
	}
}

func TestRun_OKCheck_NoNotification(t *testing.T) {
	rec := &notificationRecorder{}
	tk := New(
		WithNotifier(rec.send),
		WithChecks([]Check{
			mockCheck("healthy", &CheckResult{
				Status:  StatusOK,
				Message: "all good",
				Notify:  false,
			}, nil),
		}),
	)

	tk.Run(context.Background())

	if rec.count() != 0 {
		t.Errorf("expected 0 notifications for OK check, got %d", rec.count())
	}
}

func TestRun_WarningCheck_SendsNotification(t *testing.T) {
	rec := &notificationRecorder{}
	tk := New(
		WithNotifier(rec.send),
		WithChecks([]Check{
			mockCheck("disk", &CheckResult{
				Status:  StatusWarning,
				Message: "disk space low: 5% free",
				Notify:  true,
			}, nil),
		}),
	)

	tk.Run(context.Background())

	if rec.count() != 1 {
		t.Fatalf("expected 1 notification, got %d", rec.count())
	}
	last := rec.last()
	if last.title != "JARVIS Ticker: disk" {
		t.Errorf("expected title 'JARVIS Ticker: disk', got %q", last.title)
	}
	if last.message != "disk space low: 5% free" {
		t.Errorf("unexpected message: %q", last.message)
	}
}

func TestRun_ErrorCheck_SendsNotification(t *testing.T) {
	rec := &notificationRecorder{}
	tk := New(
		WithNotifier(rec.send),
		WithChecks([]Check{
			mockCheck("db", &CheckResult{
				Status:  StatusError,
				Message: "postgres unreachable",
				Notify:  true,
			}, nil),
		}),
	)

	tk.Run(context.Background())

	if rec.count() != 1 {
		t.Fatalf("expected 1 notification, got %d", rec.count())
	}
}

func TestRun_CheckFuncError_SendsNotification(t *testing.T) {
	rec := &notificationRecorder{}
	tk := New(
		WithNotifier(rec.send),
		WithChecks([]Check{
			mockCheck("broken", nil, errors.New("connection refused")),
		}),
	)

	tk.Run(context.Background())

	if rec.count() != 1 {
		t.Fatalf("expected 1 notification for check error, got %d", rec.count())
	}
	last := rec.last()
	if last.title != "JARVIS Ticker: broken" {
		t.Errorf("unexpected title: %q", last.title)
	}
}

func TestRun_NoNotifier_NoError(t *testing.T) {
	// Ticker with no notifier configured should not panic.
	tk := New(
		WithChecks([]Check{
			mockCheck("warn", &CheckResult{
				Status:  StatusWarning,
				Message: "something wrong",
				Notify:  true,
			}, nil),
		}),
	)

	// Should not panic.
	tk.Run(context.Background())
}

func TestRun_PerCheckInterval_Respected(t *testing.T) {
	callCount := 0
	tk := New(
		WithInterval(time.Millisecond), // base interval very short
		WithChecks([]Check{
			{
				Name:     "slow_check",
				Interval: time.Hour, // this check should only run once
				Fn: func(ctx context.Context) (*CheckResult, error) {
					callCount++
					return &CheckResult{Status: StatusOK, Message: "ok"}, nil
				},
			},
		}),
	)

	// First run: check should execute (never run before).
	tk.Run(context.Background())
	if callCount != 1 {
		t.Fatalf("expected 1 call after first run, got %d", callCount)
	}

	// Second run immediately: check should be skipped (interval=1h not elapsed).
	tk.Run(context.Background())
	if callCount != 1 {
		t.Fatalf("expected 1 call after second run (skipped), got %d", callCount)
	}
}

func TestRun_DefaultInterval_UsedWhenPerCheckIsZero(t *testing.T) {
	callCount := 0
	tk := New(
		WithInterval(time.Hour), // base interval is 1 hour
		WithChecks([]Check{
			{
				Name:     "default_interval_check",
				Interval: 0, // should fall back to ticker's base interval
				Fn: func(ctx context.Context) (*CheckResult, error) {
					callCount++
					return &CheckResult{Status: StatusOK, Message: "ok"}, nil
				},
			},
		}),
	)

	// First run: executes (LastRun is zero).
	tk.Run(context.Background())
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Second run immediately: skipped because base interval (1h) hasn't elapsed.
	tk.Run(context.Background())
	if callCount != 1 {
		t.Fatalf("expected still 1 call (skipped), got %d", callCount)
	}
}

func TestRun_MultipleChecks_AllExecuted(t *testing.T) {
	executed := make(map[string]bool)
	var mu sync.Mutex

	makeCheck := func(name string) Check {
		return Check{
			Name: name,
			Fn: func(ctx context.Context) (*CheckResult, error) {
				mu.Lock()
				executed[name] = true
				mu.Unlock()
				return &CheckResult{Status: StatusOK, Message: "ok"}, nil
			},
		}
	}

	tk := New(WithChecks([]Check{
		makeCheck("check_a"),
		makeCheck("check_b"),
		makeCheck("check_c"),
	}))

	tk.Run(context.Background())

	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"check_a", "check_b", "check_c"} {
		if !executed[name] {
			t.Errorf("check %q was not executed", name)
		}
	}
}

func TestRun_NotifyFalse_NoNotification(t *testing.T) {
	rec := &notificationRecorder{}
	tk := New(
		WithNotifier(rec.send),
		WithChecks([]Check{
			mockCheck("silent_warning", &CheckResult{
				Status:  StatusWarning,
				Message: "warning but no notification",
				Notify:  false,
			}, nil),
		}),
	)

	tk.Run(context.Background())

	if rec.count() != 0 {
		t.Errorf("expected 0 notifications when Notify=false, got %d", rec.count())
	}
}

func TestRunBackground_StopsOnCancel(t *testing.T) {
	tk := New(
		WithInterval(time.Millisecond),
		WithStartupDelay(0),
		WithChecks([]Check{
			mockCheck("fast", &CheckResult{Status: StatusOK, Message: "ok"}, nil),
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		tk.RunBackground(ctx)
		close(done)
	}()

	// Let it run a couple of ticks.
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good, it stopped.
	case <-time.After(time.Second):
		t.Fatal("RunBackground did not stop within 1s after cancel")
	}
}

func TestRunBackground_ExecutesOnStartup(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	tk := New(
		WithInterval(time.Hour), // long interval so only the initial run fires
		WithStartupDelay(0),     // skip 30s delay in tests
		WithChecks([]Check{
			{
				Name: "startup_check",
				Fn: func(ctx context.Context) (*CheckResult, error) {
					mu.Lock()
					callCount++
					mu.Unlock()
					return &CheckResult{Status: StatusOK, Message: "ok"}, nil
				},
			},
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		tk.RunBackground(ctx)
		close(done)
	}()

	// Give it time to run the initial check.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if callCount < 1 {
		t.Errorf("expected at least 1 call on startup, got %d", callCount)
	}
}

func TestRun_ContextCancelled_PassedToCheck(t *testing.T) {
	var receivedCtx context.Context

	tk := New(WithChecks([]Check{
		{
			Name: "ctx_check",
			Fn: func(ctx context.Context) (*CheckResult, error) {
				receivedCtx = ctx
				return &CheckResult{Status: StatusOK, Message: "ok"}, nil
			},
		},
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running

	tk.Run(ctx)

	if receivedCtx == nil {
		t.Fatal("check was not called")
	}
	if receivedCtx.Err() == nil {
		t.Error("expected cancelled context to be passed to check")
	}
}

func TestRun_MixedResults_CorrectNotifications(t *testing.T) {
	rec := &notificationRecorder{}
	tk := New(
		WithNotifier(rec.send),
		WithChecks([]Check{
			mockCheck("ok_check", &CheckResult{Status: StatusOK, Message: "fine", Notify: false}, nil),
			mockCheck("warn_check", &CheckResult{Status: StatusWarning, Message: "low disk", Notify: true}, nil),
			mockCheck("err_check", &CheckResult{Status: StatusError, Message: "db down", Notify: true}, nil),
			mockCheck("ok_notify", &CheckResult{Status: StatusOK, Message: "consolidation done", Notify: true}, nil),
		}),
	)

	tk.Run(context.Background())

	if rec.count() != 3 {
		t.Errorf("expected 3 notifications (warn + error + ok-with-notify), got %d", rec.count())
	}
}

// ─── Check Factory Tests ─────────────────────────────────────────────────

func TestNewServerHealthCheck_Name(t *testing.T) {
	c := NewServerHealthCheck("http://localhost:8080/health")
	if c.Name != "server_health" {
		t.Errorf("expected name 'server_health', got %q", c.Name)
	}
	if c.Interval != 15*time.Minute {
		t.Errorf("expected interval 15m, got %v", c.Interval)
	}
}

func TestNewDiskSpaceCheck_Name(t *testing.T) {
	c := NewDiskSpaceCheck("/", 10)
	if c.Name != "disk_space" {
		t.Errorf("expected name 'disk_space', got %q", c.Name)
	}
	if c.Interval != time.Hour {
		t.Errorf("expected interval 1h, got %v", c.Interval)
	}
}

func TestNewPostgresCheck_Name(t *testing.T) {
	c := NewPostgresCheck(&mockDBPinger{err: nil})
	if c.Name != "postgres" {
		t.Errorf("expected name 'postgres', got %q", c.Name)
	}
}

func TestNewOpenCodeCheck_Name(t *testing.T) {
	c := NewOpenCodeCheck("http://localhost:4096")
	if c.Name != "opencode_availability" {
		t.Errorf("expected name 'opencode_availability', got %q", c.Name)
	}
}

func TestNewMemoryConsolidationCheck_Name(t *testing.T) {
	c := NewMemoryConsolidationCheck(
		func() (bool, error) { return false, nil },
		func(ctx context.Context) error { return nil },
	)
	if c.Name != "memory_consolidation" {
		t.Errorf("expected name 'memory_consolidation', got %q", c.Name)
	}
	if c.Interval != time.Hour {
		t.Errorf("expected interval 1h, got %v", c.Interval)
	}
}

// ─── Postgres Check Tests ────────────────────────────────────────────────

type mockDBPinger struct {
	err error
}

func (m *mockDBPinger) PingContext(ctx context.Context) error {
	return m.err
}

func TestPostgresCheck_OK(t *testing.T) {
	fn := postgresCheck(&mockDBPinger{err: nil})
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusOK {
		t.Errorf("expected OK, got %v", result.Status)
	}
	if result.Notify {
		t.Error("expected Notify=false for OK result")
	}
}

func TestPostgresCheck_Error(t *testing.T) {
	fn := postgresCheck(&mockDBPinger{err: errors.New("connection refused")})
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusError {
		t.Errorf("expected Error, got %v", result.Status)
	}
	if !result.Notify {
		t.Error("expected Notify=true for Error result")
	}
}

// ─── Memory Consolidation Check Tests ────────────────────────────────────

func TestMemoryConsolidationCheck_NotNeeded(t *testing.T) {
	fn := memoryConsolidationCheck(
		func() (bool, error) { return false, nil },
		func(ctx context.Context) error { return nil },
	)
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusOK {
		t.Errorf("expected OK, got %v", result.Status)
	}
}

func TestMemoryConsolidationCheck_Needed_Success(t *testing.T) {
	triggered := false
	fn := memoryConsolidationCheck(
		func() (bool, error) { return true, nil },
		func(ctx context.Context) error {
			triggered = true
			return nil
		},
	)
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !triggered {
		t.Error("expected consolidation to be triggered")
	}
	if result.Status != StatusOK {
		t.Errorf("expected OK after successful consolidation, got %v", result.Status)
	}
	if !result.Notify {
		t.Error("expected Notify=true after consolidation")
	}
}

func TestMemoryConsolidationCheck_Needed_Failure(t *testing.T) {
	fn := memoryConsolidationCheck(
		func() (bool, error) { return true, nil },
		func(ctx context.Context) error { return errors.New("lock held") },
	)
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusWarning {
		t.Errorf("expected Warning, got %v", result.Status)
	}
	if !result.Notify {
		t.Error("expected Notify=true for failed consolidation")
	}
}

func TestMemoryConsolidationCheck_NeedsFuncError(t *testing.T) {
	fn := memoryConsolidationCheck(
		func() (bool, error) { return false, errors.New("mnemo unavailable") },
		func(ctx context.Context) error { return nil },
	)
	_, err := fn(context.Background())
	if err == nil {
		t.Fatal("expected error from needsFn propagation")
	}
}

// ─── Disk Space Check Tests ──────────────────────────────────────────────

func TestDiskSpaceCheck_RealFS(t *testing.T) {
	// This tests against the real filesystem -- "/" should always exist.
	fn := diskSpaceCheck("/", 1) // 1% threshold -- should always pass
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusOK {
		t.Errorf("expected OK (real disk should have >1%% free), got %v: %s", result.Status, result.Message)
	}
}

func TestDiskSpaceCheck_InvalidPath(t *testing.T) {
	fn := diskSpaceCheck("/nonexistent/path/that/should/not/exist", 10)
	_, err := fn(context.Background())
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

// ─── DefaultChecks Tests ─────────────────────────────────────────────────

func TestDefaultChecks_AllPresent(t *testing.T) {
	db := &mockDBPinger{}
	needsFn := func() (bool, error) { return false, nil }
	triggerFn := func(ctx context.Context) error { return nil }

	checks := DefaultChecks("http://localhost:8080/health", "http://localhost:4096", db, needsFn, triggerFn)

	expected := map[string]bool{
		"server_health":        false,
		"disk_space":           false,
		"postgres":             false,
		"memory_consolidation": false,
		"opencode_availability": false,
	}

	for _, c := range checks {
		if _, ok := expected[c.Name]; !ok {
			t.Errorf("unexpected check: %q", c.Name)
		}
		expected[c.Name] = true
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing check: %q", name)
		}
	}
}

func TestDefaultChecks_NilOptionals(t *testing.T) {
	checks := DefaultChecks("http://localhost:8080/health", "", nil, nil, nil)

	// Should only have server_health + disk_space.
	if len(checks) != 2 {
		t.Errorf("expected 2 checks with nil optionals, got %d", len(checks))
		for _, c := range checks {
			t.Logf("  - %s", c.Name)
		}
	}
}

// ─── Notifier Error Handling ─────────────────────────────────────────────

func TestNotify_ErrorFromNotifier_Logged(t *testing.T) {
	failingNotifier := func(title, message string) error {
		return fmt.Errorf("discord unavailable")
	}

	tk := New(
		WithNotifier(failingNotifier),
		WithChecks([]Check{
			mockCheck("test", &CheckResult{
				Status:  StatusError,
				Message: "something broke",
				Notify:  true,
			}, nil),
		}),
	)

	// Should not panic even if notifier returns error.
	tk.Run(context.Background())
}

// ─── WithLogger Test ─────────────────────────────────────────────────────

func TestWithLogger(t *testing.T) {
	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	tk := New(WithLogger(l))
	if tk.logger != l {
		t.Error("expected custom logger to be set")
	}
}

// ─── HTTP Check Tests (httptest) ─────────────────────────────────────────

func TestServerHealthCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fn := serverHealthCheck(srv.URL)
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusOK {
		t.Errorf("expected OK, got %v: %s", result.Status, result.Message)
	}
	if result.Notify {
		t.Error("expected Notify=false for healthy server")
	}
}

func TestServerHealthCheck_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	fn := serverHealthCheck(srv.URL)
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusWarning {
		t.Errorf("expected Warning for non-200, got %v", result.Status)
	}
	if !result.Notify {
		t.Error("expected Notify=true for non-200")
	}
}

func TestServerHealthCheck_Unreachable(t *testing.T) {
	fn := serverHealthCheck("http://127.0.0.1:1") // port 1 -- will refuse connection
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusError {
		t.Errorf("expected Error for unreachable server, got %v", result.Status)
	}
	if !result.Notify {
		t.Error("expected Notify=true for unreachable")
	}
}

func TestOpenCodeCheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fn := openCodeCheck(srv.URL)
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusOK {
		t.Errorf("expected OK, got %v: %s", result.Status, result.Message)
	}
}

func TestOpenCodeCheck_Unreachable(t *testing.T) {
	fn := openCodeCheck("http://127.0.0.1:1")
	result, err := fn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusWarning {
		t.Errorf("expected Warning for unreachable OpenCode, got %v", result.Status)
	}
	if !result.Notify {
		t.Error("expected Notify=true for unreachable")
	}
}

// ─── CheckStatus Constants ───────────────────────────────────────────────

func TestCheckStatus_Values(t *testing.T) {
	if StatusOK != "ok" {
		t.Errorf("StatusOK = %q, want 'ok'", StatusOK)
	}
	if StatusWarning != "warning" {
		t.Errorf("StatusWarning = %q, want 'warning'", StatusWarning)
	}
	if StatusError != "error" {
		t.Errorf("StatusError = %q, want 'error'", StatusError)
	}
}

func TestDefaultInterval_Value(t *testing.T) {
	if DefaultInterval != 15*time.Minute {
		t.Errorf("DefaultInterval = %v, want 15m", DefaultInterval)
	}
}
