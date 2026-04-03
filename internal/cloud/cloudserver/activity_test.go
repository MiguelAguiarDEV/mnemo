package cloudserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ─── Activity Feed ─────────────────────────────────────────────────────────

func TestActivityFeedReturnsEntries(t *testing.T) {
	srv, _ := testSetup(t)
	h := srv.Handler()
	user := registerUser(t, h, "actuser", "act@test.com", "password123")

	// Create a task to generate activity (task_events: "created")
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, authReq(http.MethodPost, "/api/tasks", `{"title":"Activity task","project":"actproj"}`, user.AccessToken))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create task: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	// Fetch activity feed
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(http.MethodGet, "/api/activity", "", user.AccessToken))

	if rec.Code != http.StatusOK {
		t.Fatalf("activity feed: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec)
	entries, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("activity feed: expected entries array, got %v", body["entries"])
	}
	if len(entries) == 0 {
		t.Fatal("activity feed: expected at least 1 entry after creating a task")
	}

	// Verify at least one entry is a task_update with type "created"
	found := false
	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["type"] == "task_update" && entry["summary"] == "created" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("activity feed: expected a task_update entry with summary=created")
	}
}

func TestActivityFeedFilterByProject(t *testing.T) {
	srv, _ := testSetup(t)
	h := srv.Handler()
	user := registerUser(t, h, "actproj", "actproj@test.com", "password123")

	// Create tasks in different projects
	for _, task := range []string{
		`{"title":"Proj A task","project":"proj-a"}`,
		`{"title":"Proj B task","project":"proj-b"}`,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authReq(http.MethodPost, "/api/tasks", task, user.AccessToken))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create task: expected 201, got %d", rec.Code)
		}
	}

	// Filter activity by project=proj-a
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(http.MethodGet, "/api/activity?project=proj-a", "", user.AccessToken))

	if rec.Code != http.StatusOK {
		t.Fatalf("activity by project: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec)
	entries := body["entries"].([]any)

	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["project"] != "proj-a" {
			t.Fatalf("activity by project: expected all entries project=proj-a, got %v", entry["project"])
		}
	}
}

func TestActivityFeedFilterBySince(t *testing.T) {
	srv, _ := testSetup(t)
	h := srv.Handler()
	user := registerUser(t, h, "actsince", "actsince@test.com", "password123")

	// Record time before creating task
	beforeCreate := time.Now().UTC()

	// Create a task
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, authReq(http.MethodPost, "/api/tasks", `{"title":"Since task"}`, user.AccessToken))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create task: expected 201, got %d", createRec.Code)
	}

	// Use since = 1 hour ago (should include the entry)
	sinceOld := beforeCreate.Add(-1 * time.Hour).Format(time.RFC3339)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(http.MethodGet, fmt.Sprintf("/api/activity?since=%s", sinceOld), "", user.AccessToken))

	if rec.Code != http.StatusOK {
		t.Fatalf("activity since (old): expected 200, got %d", rec.Code)
	}

	body := decodeJSON(t, rec)
	entries := body["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("activity since (old): expected entries with since=1h ago")
	}

	// Use since = 1 hour from now (should exclude everything)
	sinceFuture := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	recFuture := httptest.NewRecorder()
	h.ServeHTTP(recFuture, authReq(http.MethodGet, fmt.Sprintf("/api/activity?since=%s", sinceFuture), "", user.AccessToken))

	if recFuture.Code != http.StatusOK {
		t.Fatalf("activity since (future): expected 200, got %d", recFuture.Code)
	}

	futureBody := decodeJSON(t, recFuture)
	futureEntries := futureBody["entries"].([]any)
	if len(futureEntries) != 0 {
		t.Fatalf("activity since (future): expected 0 entries, got %d", len(futureEntries))
	}
}

func TestActivityFeedRespectsLimit(t *testing.T) {
	srv, _ := testSetup(t)
	h := srv.Handler()
	user := registerUser(t, h, "actlimit", "actlimit@test.com", "password123")

	// Create several tasks to generate multiple activity entries
	for i := 0; i < 5; i++ {
		body := fmt.Sprintf(`{"title":"Limit task %d"}`, i)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authReq(http.MethodPost, "/api/tasks", body, user.AccessToken))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create task %d: expected 201, got %d", i, rec.Code)
		}
	}

	// Request with limit=2
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(http.MethodGet, "/api/activity?limit=2", "", user.AccessToken))

	if rec.Code != http.StatusOK {
		t.Fatalf("activity limit: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec)
	entries := body["entries"].([]any)
	if len(entries) > 2 {
		t.Fatalf("activity limit=2: expected at most 2 entries, got %d", len(entries))
	}
}

func TestActivityFeedEmptyResult(t *testing.T) {
	srv, _ := testSetup(t)
	h := srv.Handler()
	user := registerUser(t, h, "actempty", "actempty@test.com", "password123")

	// Fresh user, no activity — should return empty array, not null
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authReq(http.MethodGet, "/api/activity", "", user.AccessToken))

	if rec.Code != http.StatusOK {
		t.Fatalf("activity empty: expected 200, got %d", rec.Code)
	}

	body := decodeJSON(t, rec)
	entries, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("activity empty: expected entries array, got %v", body["entries"])
	}
	if len(entries) != 0 {
		t.Fatalf("activity empty: expected 0 entries, got %d", len(entries))
	}
}

func TestActivityFeedRequiresAuth(t *testing.T) {
	srv, _ := testSetup(t)
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("activity without auth: expected 401, got %d", rec.Code)
	}
}
