package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── Arg Parsing ────────────────────────────────────────────────────────────

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "jarvis") {
		t.Fatalf("expected version output, got %q", stdout.String())
	}
}

func TestVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--format", "json", "version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["version"] == "" {
		t.Fatal("expected version field")
	}
}

func TestHelpFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Commands") {
		t.Fatalf("expected help output, got %q", stdout.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected error message, got %q", stderr.String())
	}
}

func TestNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Commands") {
		t.Fatalf("expected usage output, got %q", stdout.String())
	}
}

func TestTasksNoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"tasks"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Fatalf("expected usage message, got %q", stderr.String())
	}
}

func TestTasksCreateNoTitle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"tasks", "create"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// ─── API Client (mock server) ───────────────────────────────────────────────

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *config) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := &config{
		apiURL: srv.URL,
		apiKey: "eng_testkey",
		format: "text",
	}
	return srv, cfg
}

func TestTasksListMock(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		// Verify auth header.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer eng_testkey" {
			t.Errorf("expected Bearer eng_testkey, got %q", auth)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tasks": []map[string]any{
				{"id": 1, "title": "Test task", "status": "open", "priority": "high", "project": "test"},
				{"id": 2, "title": "Done task", "status": "done", "priority": "low"},
			},
			"total": 2,
		})
	})

	// Test text format.
	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Test task") {
		t.Fatalf("expected task title in output, got %q", out)
	}
	if !strings.Contains(out, "2 total") {
		t.Fatalf("expected total count, got %q", out)
	}
}

func TestTasksListJSON(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tasks": []map[string]any{
				{"id": 1, "title": "Test task", "status": "open", "priority": "medium"},
			},
			"total": 1,
		})
	})
	cfg.format = "json"

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}
	tasks := result["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
}

func TestTasksCreateMock(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "New test task" {
			t.Errorf("expected title 'New test task', got %v", body["title"])
		}
		if body["priority"] != "high" {
			t.Errorf("expected priority 'high', got %v", body["priority"])
		}
		if body["source"] != "cli" {
			t.Errorf("expected source 'cli', got %v", body["source"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":       42,
			"title":    "New test task",
			"priority": "high",
			"status":   "open",
		})
	})

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"create", "New", "test", "task", "--priority", "high"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Task created") {
		t.Fatalf("expected success message, got %q", out)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("expected id in output, got %q", out)
	}
}

func TestTasksCompleteMock(t *testing.T) {
	callCount := 0
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++

		switch {
		case r.Method == "GET":
			// Get task to check status.
			json.NewEncoder(w).Encode(map[string]any{
				"task":     map[string]any{"id": 5, "status": "open"},
				"subtasks": []any{},
			})
		case r.Method == "PATCH" && callCount == 2:
			// First PATCH: open -> in_progress.
			json.NewEncoder(w).Encode(map[string]any{"id": 5, "status": "in_progress"})
		case r.Method == "PATCH" && callCount == 3:
			// Second PATCH: in_progress -> done.
			json.NewEncoder(w).Encode(map[string]any{"id": 5, "status": "done"})
		}
	})

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"complete", "5"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "marked as done") {
		t.Fatalf("expected done message, got %q", stdout.String())
	}
	if callCount != 3 {
		t.Fatalf("expected 3 API calls (get + 2 patches), got %d", callCount)
	}
}

func TestTasksDeleteMock(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/7") {
			t.Errorf("expected path ending /7, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	})

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"delete", "7"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "deleted") {
		t.Fatalf("expected deleted message, got %q", stdout.String())
	}
}

func TestTasksListEmptyResult(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"tasks": []any{},
			"total": 0,
		})
	})

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "No tasks found") {
		t.Fatalf("expected empty message, got %q", stdout.String())
	}
}

// ─── Status Command ─────────────────────────────────────────────────────────

func TestStatusMock(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]any{
				"status":  "ok",
				"service": "mnemo-cloud",
				"version": "0.1.0",
			})
		case "/api/tasks":
			json.NewEncoder(w).Encode(map[string]any{
				"tasks": []map[string]any{
					{"id": 1, "status": "open"},
					{"id": 2, "status": "done"},
				},
				"total": 2,
			})
		case "/api/graph":
			json.NewEncoder(w).Encode(map[string]any{
				"sessions": []map[string]any{
					{"id": "s1", "observation_count": 50},
					{"id": "s2", "observation_count": 30},
				},
			})
		case "/api/costs":
			json.NewEncoder(w).Encode(map[string]any{
				"total_cost": 1.23,
				"period":     "month",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	var stdout, stderr bytes.Buffer
	code := cmdStatus(cfg, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "healthy") {
		t.Fatalf("expected healthy in output, got %q", out)
	}
	if !strings.Contains(out, "1 pending, 1 done") {
		t.Fatalf("expected task counts, got %q", out)
	}
	if !strings.Contains(out, "$1.23") {
		t.Fatalf("expected cost amount, got %q", out)
	}
}

func TestStatusJSON(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "0.1.0"})
		case "/api/tasks":
			json.NewEncoder(w).Encode(map[string]any{"tasks": []any{}, "total": 0})
		case "/api/costs":
			json.NewEncoder(w).Encode(map[string]any{"total_cost": 0.0, "period": "month"})
		}
	})
	cfg.format = "json"

	var stdout, stderr bytes.Buffer
	code := cmdStatus(cfg, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["api"] == nil {
		t.Fatal("expected api field in status JSON")
	}
}

// ─── Costs Command ──────────────────────────────────────────────────────────

func TestCostsShowMock(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"total_cost": 5.67,
			"period":     "month",
			"by_model":   []any{},
			"by_day":     []any{},
		})
	})

	var stdout, stderr bytes.Buffer
	code := cmdCosts(cfg, []string{"show"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "$5.67") {
		t.Fatalf("expected cost in output, got %q", stdout.String())
	}
}

// ─── API Error Handling ─────────────────────────────────────────────────────

func TestAPIError401(t *testing.T) {
	_, cfg := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "missing authorization header"})
	})

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"list"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "401") {
		t.Fatalf("expected 401 error, got %q", stderr.String())
	}
}

func TestAPIErrorUnreachable(t *testing.T) {
	cfg := &config{
		apiURL: "http://127.0.0.1:1", // port 1 should not be listening
		apiKey: "eng_test",
		format: "text",
	}

	var stdout, stderr bytes.Buffer
	code := cmdTasks(cfg, []string{"list"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "error") {
		t.Fatalf("expected error message, got %q", stderr.String())
	}
}

// ─── Dream / Ticker (stub) ─────────────────────────────────────────────────

func TestDreamStatus(t *testing.T) {
	cfg := &config{format: "text"}
	var stdout, stderr bytes.Buffer
	code := cmdDream(cfg, []string{"status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "MORPHEUS") {
		t.Fatalf("expected MORPHEUS in output, got %q", stdout.String())
	}
}

func TestTickerStatus(t *testing.T) {
	cfg := &config{format: "text"}
	var stdout, stderr bytes.Buffer
	code := cmdTicker(cfg, []string{"status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "SENTINEL") {
		t.Fatalf("expected SENTINEL in output, got %q", stdout.String())
	}
}

func TestDreamNoSubcommand(t *testing.T) {
	cfg := &config{format: "text"}
	var stdout, stderr bytes.Buffer
	code := cmdDream(cfg, []string{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestTickerNoSubcommand(t *testing.T) {
	cfg := &config{format: "text"}
	var stdout, stderr bytes.Buffer
	code := cmdTicker(cfg, []string{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

// ─── Helper Tests ───────────────────────────────────────────────────────────

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_JARVIS_VAR", "custom")
	if v := envOrDefault("TEST_JARVIS_VAR", "default"); v != "custom" {
		t.Fatalf("expected 'custom', got %q", v)
	}
	if v := envOrDefault("TEST_JARVIS_NONEXIST", "fallback"); v != "fallback" {
		t.Fatalf("expected 'fallback', got %q", v)
	}
}

func TestMapHelpers(t *testing.T) {
	m := map[string]any{
		"str": "hello",
		"num": float64(42),
		"int": 7,
	}

	if v := mapStr(m, "str"); v != "hello" {
		t.Fatalf("expected 'hello', got %q", v)
	}
	if v := mapStr(m, "missing"); v != "" {
		t.Fatalf("expected empty, got %q", v)
	}
	if v := mapFloat(m, "num"); v != 42 {
		t.Fatalf("expected 42, got %f", v)
	}
	if v := mapFloat(m, "int"); v != 7 {
		t.Fatalf("expected 7, got %f", v)
	}
	if v := mapFloat(m, "missing"); v != 0 {
		t.Fatalf("expected 0, got %f", v)
	}
}

func TestStatusColorCode(t *testing.T) {
	if c := statusColorCode("done"); c != colorGreen {
		t.Fatalf("expected green for done")
	}
	if c := statusColorCode("in_progress"); c != colorCyan {
		t.Fatalf("expected cyan for in_progress")
	}
	if c := statusColorCode("blocked"); c != colorRed {
		t.Fatalf("expected red for blocked")
	}
	if c := statusColorCode("open"); c != colorYellow {
		t.Fatalf("expected yellow for open")
	}
}

func TestPriorityColorCode(t *testing.T) {
	if c := priorityColorCode("high"); c != colorRed {
		t.Fatalf("expected red for high")
	}
	if c := priorityColorCode("medium"); c != colorYellow {
		t.Fatalf("expected yellow for medium")
	}
	if c := priorityColorCode("low"); c != colorGreen {
		t.Fatalf("expected green for low")
	}
}
