package cloudstore

import (
	"testing"
	"time"
)

// ── CostByProject ─────────────────────────────────────────────────────────

func TestCostByProject(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@example.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Insert test tool call records with different projects.
	// proj-b total cost (0.10) > proj-a total cost (0.01+0.02=0.03).
	insertToolCall(t, cs, u.ID, "proj-a", "claude-sonnet-4", 100, 50, 0.01)
	insertToolCall(t, cs, u.ID, "proj-a", "claude-sonnet-4", 200, 100, 0.02)
	insertToolCall(t, cs, u.ID, "proj-b", "gpt-4o-mini", 300, 150, 0.10)

	results, err := cs.CostByProject(u.ID, nil)
	if err != nil {
		t.Fatalf("CostByProject: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(results))
	}

	// Results are ordered by cost_usd DESC.
	if results[0].Project != "proj-b" {
		t.Errorf("first project = %q, want proj-b (highest cost)", results[0].Project)
	}
	if results[0].Calls != 1 {
		t.Errorf("proj-b calls = %d, want 1", results[0].Calls)
	}

	if results[1].Project != "proj-a" {
		t.Errorf("second project = %q, want proj-a", results[1].Project)
	}
	if results[1].Calls != 2 {
		t.Errorf("proj-a calls = %d, want 2", results[1].Calls)
	}
}

func TestCostByProjectWithSinceFilter(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@example.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Insert an old record and a recent one.
	insertToolCallAt(t, cs, u.ID, "proj-a", "claude-sonnet-4", 100, 50, 0.01,
		time.Now().AddDate(0, -2, 0))
	insertToolCallAt(t, cs, u.ID, "proj-b", "gpt-4o-mini", 200, 100, 0.05,
		time.Now())

	since := time.Now().AddDate(0, -1, 0)
	results, err := cs.CostByProject(u.ID, &since)
	if err != nil {
		t.Fatalf("CostByProject with since: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 project after filter, got %d", len(results))
	}
	if results[0].Project != "proj-b" {
		t.Errorf("project = %q, want proj-b", results[0].Project)
	}
}

// ── CostByDay ─────────────────────────────────────────────────────────────

func TestCostByDay(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@example.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)

	insertToolCallAt(t, cs, u.ID, "proj-a", "claude-sonnet-4", 100, 50, 0.01, today)
	insertToolCallAt(t, cs, u.ID, "proj-a", "claude-sonnet-4", 200, 100, 0.02, today)
	insertToolCallAt(t, cs, u.ID, "proj-a", "gpt-4o-mini", 300, 150, 0.03, yesterday)

	results, err := cs.CostByDay(u.ID, nil)
	if err != nil {
		t.Fatalf("CostByDay: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 days, got %d", len(results))
	}

	// Results ordered by date DESC, so today first.
	if results[0].Date != today.Format("2006-01-02") {
		t.Errorf("first date = %q, want %q", results[0].Date, today.Format("2006-01-02"))
	}
	if results[0].Calls != 2 {
		t.Errorf("today calls = %d, want 2", results[0].Calls)
	}
	if results[1].Calls != 1 {
		t.Errorf("yesterday calls = %d, want 1", results[1].Calls)
	}
}

// ── BudgetUsage ───────────────────────────────────────────────────────────

func TestBudgetUsage(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@example.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 15, 12, 0, 0, 0, time.UTC)

	// Claude model call.
	insertToolCallAt(t, cs, u.ID, "proj-a", "claude-sonnet-4-20250514", 1000, 500, 10.0, thisMonth)
	// OpenAI model call.
	insertToolCallAt(t, cs, u.ID, "proj-b", "gpt-4o-mini", 2000, 1000, 5.0, thisMonth)

	report, err := cs.BudgetUsage(u.ID, now, 200.0, 100.0)
	if err != nil {
		t.Fatalf("BudgetUsage: %v", err)
	}

	if report.ClaudeUsed != 10.0 {
		t.Errorf("ClaudeUsed = %v, want 10.0", report.ClaudeUsed)
	}
	if report.OpenAIUsed != 5.0 {
		t.Errorf("OpenAIUsed = %v, want 5.0", report.OpenAIUsed)
	}
	if report.ClaudeBudget != 200.0 {
		t.Errorf("ClaudeBudget = %v, want 200.0", report.ClaudeBudget)
	}
	if report.OpenAIBudget != 100.0 {
		t.Errorf("OpenAIBudget = %v, want 100.0", report.OpenAIBudget)
	}

	// Claude: 10/200 = 5%
	if report.ClaudePct != 5.0 {
		t.Errorf("ClaudePct = %v, want 5.0", report.ClaudePct)
	}
	// OpenAI: 5/100 = 5%
	if report.OpenAIPct != 5.0 {
		t.Errorf("OpenAIPct = %v, want 5.0", report.OpenAIPct)
	}
}

func TestBudgetUsageZeroBudget(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@example.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	now := time.Now().UTC()

	report, err := cs.BudgetUsage(u.ID, now, 0.0, 0.0)
	if err != nil {
		t.Fatalf("BudgetUsage: %v", err)
	}

	// Zero budget should not cause division by zero — pct stays 0.
	if report.ClaudePct != 0 {
		t.Errorf("ClaudePct = %v, want 0 (zero budget)", report.ClaudePct)
	}
	if report.OpenAIPct != 0 {
		t.Errorf("OpenAIPct = %v, want 0 (zero budget)", report.OpenAIPct)
	}
}

func TestBudgetUsageIgnoresOtherMonths(t *testing.T) {
	cs := newTestStore(t)

	u, err := cs.CreateUser("alice", "alice@example.com", "secret123")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Use a fixed reference month and insert data in the previous month.
	refMonth := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	prevMonth := time.Date(2025, 5, 10, 12, 0, 0, 0, time.UTC)

	// Insert a call in May — should NOT appear in June's report.
	insertToolCallAt(t, cs, u.ID, "proj-a", "claude-sonnet-4", 1000, 500, 50.0, prevMonth)

	report, err := cs.BudgetUsage(u.ID, refMonth, 200.0, 200.0)
	if err != nil {
		t.Fatalf("BudgetUsage: %v", err)
	}

	if report.ClaudeUsed != 0 {
		t.Errorf("ClaudeUsed = %v, want 0 (other month's data)", report.ClaudeUsed)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────

func insertToolCall(t *testing.T, cs *CloudStore, userID, project, model string, tokensIn, tokensOut int64, cost float64) {
	t.Helper()
	insertToolCallAt(t, cs, userID, project, model, tokensIn, tokensOut, cost, time.Now().UTC())
}

func insertToolCallAt(t *testing.T, cs *CloudStore, userID, project, model string, tokensIn, tokensOut int64, cost float64, at time.Time) {
	t.Helper()
	_, err := cs.db.Exec(`
		INSERT INTO agent_tool_calls (user_id, session_id, project, agent, tool_name, model, tokens_in, tokens_out, cost_usd, occurred_at)
		VALUES ($1, 'test-session', $2, 'test-agent', 'test-tool', $3, $4, $5, $6, $7)
	`, userID, project, model, tokensIn, tokensOut, cost, at)
	if err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
}
