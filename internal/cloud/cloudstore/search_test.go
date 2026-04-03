package cloudstore

import (
	"fmt"
	"testing"
)

// ── Observation Search ─────────────────────────────────────────────────────

func TestSearchTitleRanksHigherThanContent(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	// Observation A: "authentication" in title only.
	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Authentication middleware design",
		Content:   "We will add middleware to the server.",
		Project:   "proj",
		Scope:     "project",
	})

	// Observation B: "authentication" in content only.
	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "note",
		Title:     "Server setup notes",
		Content:   "The authentication layer needs JWT tokens and API key support.",
		Project:   "proj",
		Scope:     "project",
	})

	results, err := cs.Search(u.ID, "authentication", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Title match (observation A) must rank higher.
	if results[0].Title != "Authentication middleware design" {
		t.Errorf("expected title-match observation to rank first, got %q (rank %.4f) before %q (rank %.4f)",
			results[0].Title, results[0].Rank, results[1].Title, results[1].Rank)
	}
	if results[0].Rank <= results[1].Rank {
		t.Errorf("title match rank (%.4f) should be > content match rank (%.4f)",
			results[0].Rank, results[1].Rank)
	}
}

func TestSearchMultiTermQuery(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	// Observation with both terms.
	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "JWT authentication design",
		Content:   "Using JWT for authentication in the cloud server.",
		Project:   "proj",
	})

	// Observation with only "JWT".
	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "note",
		Title:     "JWT library evaluation",
		Content:   "Compared golang-jwt and jose libraries.",
		Project:   "proj",
	})

	// Observation with only "authentication".
	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "note",
		Title:     "Authentication patterns",
		Content:   "Reviewed different authentication approaches.",
		Project:   "proj",
	})

	// plainto_tsquery ANDs all terms, so only the obs with both terms should match.
	results, err := cs.Search(u.ID, "JWT authentication", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// At minimum, the observation containing both terms must appear.
	found := false
	for _, r := range results {
		if r.Title == "JWT authentication design" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected observation with both terms to appear in results")
	}
}

func TestSearchPrefixMatching(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Authentication middleware design",
		Content:   "JWT-backed middleware for cloud routes.",
		Project:   "proj",
	})

	results, err := cs.Search(u.ID, "auth*", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected prefix query to match authentication")
	}
	if results[0].Title != "Authentication middleware design" {
		t.Fatalf("unexpected top result: %q", results[0].Title)
	}
}

func TestSearchSpecialCharsDontCauseErrors(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "note",
		Title:     "Some observation",
		Content:   "Just some content here.",
		Project:   "proj",
	})

	// These should NOT produce SQL errors.
	queries := []string{
		"what's the auth?",
		"hello & world",
		"test | or",
		"func()",
		"a:b:c",
		"DROP TABLE cloud_observations;--",
		"' OR 1=1 --",
		"!@#$%^&*()",
		"",
		"   ",
		"<script>alert('xss')</script>",
	}

	for _, q := range queries {
		results, err := cs.Search(u.ID, q, CloudSearchOptions{})
		if err != nil {
			t.Errorf("Search(%q) returned error: %v", q, err)
		}
		// Results may be empty but must not be nil.
		if results == nil {
			t.Errorf("Search(%q) returned nil slice, want non-nil", q)
		}
	}
}

func TestSearchFiltersByUserID(t *testing.T) {
	cs := newTestStore(t)

	alice, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := cs.CreateUser("bob", "bob@test.com", "secret456")
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	cs.CreateSession(alice.ID, "s1", "proj", "/tmp")
	cs.CreateSession(bob.ID, "s2", "proj", "/tmp")

	cs.AddObservation(alice.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "note",
		Title:     "Postgres migration strategy",
		Content:   "Alice's migration notes for Postgres.",
		Project:   "proj",
	})

	cs.AddObservation(bob.ID, AddCloudObservationParams{
		SessionID: "s2",
		Type:      "note",
		Title:     "Postgres connection pooling",
		Content:   "Bob's notes on Postgres pooling configuration.",
		Project:   "proj",
	})

	// Alice searches — should only see her observation.
	aliceResults, err := cs.Search(alice.ID, "Postgres", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search alice: %v", err)
	}
	if len(aliceResults) != 1 {
		t.Fatalf("alice expected 1 result, got %d", len(aliceResults))
	}
	if aliceResults[0].Title != "Postgres migration strategy" {
		t.Errorf("alice result title = %q, want 'Postgres migration strategy'", aliceResults[0].Title)
	}

	// Bob searches — should only see his observation.
	bobResults, err := cs.Search(bob.ID, "Postgres", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search bob: %v", err)
	}
	if len(bobResults) != 1 {
		t.Fatalf("bob expected 1 result, got %d", len(bobResults))
	}
	if bobResults[0].Title != "Postgres connection pooling" {
		t.Errorf("bob result title = %q, want 'Postgres connection pooling'", bobResults[0].Title)
	}
}

func TestSearchEmptyResultsReturnEmptySlice(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	results, err := cs.Search(u.ID, "nonexistentterm", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchWithFilters(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj-a", "/a")
	cs.CreateSession(u.ID, "s2", "proj-b", "/b")

	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "decision",
		Title:     "Database selection",
		Content:   "Chose Postgres for cloud storage.",
		Project:   "proj-a",
		Scope:     "project",
	})

	cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s2",
		Type:      "note",
		Title:     "Database backup strategy",
		Content:   "Postgres backup via pg_dump.",
		Project:   "proj-b",
		Scope:     "global",
	})

	// Filter by project.
	results, err := cs.Search(u.ID, "database", CloudSearchOptions{Project: "proj-a"})
	if err != nil {
		t.Fatalf("Search with project filter: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with project filter, got %d", len(results))
	}
	if *results[0].Project != "proj-a" {
		t.Errorf("project = %q, want 'proj-a'", *results[0].Project)
	}

	// Filter by type.
	results, err = cs.Search(u.ID, "database", CloudSearchOptions{Type: "decision"})
	if err != nil {
		t.Fatalf("Search with type filter: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with type filter, got %d", len(results))
	}
	if results[0].Type != "decision" {
		t.Errorf("type = %q, want 'decision'", results[0].Type)
	}

	// Filter by scope.
	results, err = cs.Search(u.ID, "database", CloudSearchOptions{Scope: "global"})
	if err != nil {
		t.Fatalf("Search with scope filter: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with scope filter, got %d", len(results))
	}
	if results[0].Scope != "global" {
		t.Errorf("scope = %q, want 'global'", results[0].Scope)
	}
}

func TestSearchWithLimit(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	// Insert 5 observations that match "testing".
	for i := 0; i < 5; i++ {
		cs.AddObservation(u.ID, AddCloudObservationParams{
			SessionID: "s1",
			Type:      "note",
			Title:     fmt.Sprintf("Testing approach %d", i),
			Content:   "Notes about testing strategies.",
			Project:   "proj",
		})
	}

	results, err := cs.Search(u.ID, "testing", CloudSearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("Search with limit: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results with limit=3, got %d", len(results))
	}
}

func TestSearchSoftDeletedExcluded(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	obsID, err := cs.AddObservation(u.ID, AddCloudObservationParams{
		SessionID: "s1",
		Type:      "note",
		Title:     "Deletable observation about Kubernetes",
		Content:   "This will be soft deleted.",
		Project:   "proj",
	})
	if err != nil {
		t.Fatalf("AddObservation: %v", err)
	}

	// Before delete — should appear.
	results, err := cs.Search(u.ID, "Kubernetes", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search before delete: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result before delete, got %d", len(results))
	}

	// Soft delete.
	cs.DeleteObservation(u.ID, obsID, false)

	// After delete — should NOT appear.
	results, err = cs.Search(u.ID, "Kubernetes", CloudSearchOptions{})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after soft delete, got %d", len(results))
	}
}

// ── Prompt Search ──────────────────────────────────────────────────────────

func TestSearchPromptsBasic(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj", "/tmp")

	cs.AddPrompt(u.ID, AddCloudPromptParams{
		SessionID: "s1",
		Content:   "How do I refactor the authentication module?",
		Project:   "proj",
	})

	cs.AddPrompt(u.ID, AddCloudPromptParams{
		SessionID: "s1",
		Content:   "What database should I use for caching?",
		Project:   "proj",
	})

	results, err := cs.SearchPrompts(u.ID, "refactor", "", 10)
	if err != nil {
		t.Fatalf("SearchPrompts: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 prompt result, got %d", len(results))
	}
	if results[0].Content != "How do I refactor the authentication module?" {
		t.Errorf("prompt content = %q, want refactor prompt", results[0].Content)
	}
}

func TestSearchPromptsFiltersByUserID(t *testing.T) {
	cs := newTestStore(t)

	alice, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := cs.CreateUser("bob", "bob@test.com", "secret456")
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	cs.CreateSession(alice.ID, "s1", "proj", "/tmp")
	cs.CreateSession(bob.ID, "s2", "proj", "/tmp")

	cs.AddPrompt(alice.ID, AddCloudPromptParams{
		SessionID: "s1",
		Content:   "Alice asks about deployment strategies",
		Project:   "proj",
	})

	cs.AddPrompt(bob.ID, AddCloudPromptParams{
		SessionID: "s2",
		Content:   "Bob asks about deployment automation",
		Project:   "proj",
	})

	// Alice searches — should only see her prompt.
	aliceResults, err := cs.SearchPrompts(alice.ID, "deployment", "", 10)
	if err != nil {
		t.Fatalf("SearchPrompts alice: %v", err)
	}
	if len(aliceResults) != 1 {
		t.Fatalf("alice expected 1 prompt result, got %d", len(aliceResults))
	}
	if aliceResults[0].UserID != alice.ID {
		t.Errorf("expected alice's user ID, got %q", aliceResults[0].UserID)
	}
}

func TestSearchPromptsFiltersByProject(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateSession(u.ID, "s1", "proj-a", "/a")
	cs.CreateSession(u.ID, "s2", "proj-b", "/b")

	cs.AddPrompt(u.ID, AddCloudPromptParams{
		SessionID: "s1",
		Content:   "How to configure monitoring for the server?",
		Project:   "proj-a",
	})
	cs.AddPrompt(u.ID, AddCloudPromptParams{
		SessionID: "s2",
		Content:   "How to set up monitoring alerts?",
		Project:   "proj-b",
	})

	results, err := cs.SearchPrompts(u.ID, "monitoring", "proj-a", 10)
	if err != nil {
		t.Fatalf("SearchPrompts with project: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for proj-a, got %d", len(results))
	}
	if results[0].Project != "proj-a" {
		t.Errorf("project = %q, want 'proj-a'", results[0].Project)
	}
}

func TestSearchPromptsEmptyResults(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	results, err := cs.SearchPrompts(u.ID, "nonexistentterm", "", 10)
	if err != nil {
		t.Fatalf("SearchPrompts: %v", err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchPromptsSpecialChars(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@test.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	queries := []string{
		"what's up?",
		"hello & world",
		"' OR 1=1 --",
		"!@#$%^",
		"",
	}

	for _, q := range queries {
		results, err := cs.SearchPrompts(u.ID, q, "", 10)
		if err != nil {
			t.Errorf("SearchPrompts(%q) returned error: %v", q, err)
		}
		if results == nil {
			t.Errorf("SearchPrompts(%q) returned nil, want non-nil", q)
		}
	}
}

// ── Query Sanitization Unit Tests ──────────────────────────────────────────

func TestSanitizeQuery(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"hello world", "hello world"},
		{"  hello   world  ", "hello world"},
		{"what's the auth?", "what s the auth"},
		{"hello & world", "hello world"},
		{"func(x, y)", "func x y"},
		{"DROP TABLE obs;--", "DROP TABLE obs"},
		{"' OR 1=1 --", "OR 1 1"},
		{"!@#$%^&*()", "*"},
		{"", ""},
		{"   ", ""},
		{"auth*", "auth*"},
		{"hello* world", "hello* world"},
	}

	for _, tt := range tests {
		got := sanitizeQuery(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeQuery(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
