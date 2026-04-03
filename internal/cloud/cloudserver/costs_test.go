package cloudserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiguelAguiarDEV/mnemo/internal/cloud/cloudstore"
)

// ─── Mock Store ────────────────────────────────────────────────────────────

type mockCostStore struct {
	byModel    []cloudstore.ModelCost
	byDay      []cloudstore.DayCost
	bySessions []cloudstore.SessionCost
	budget     *cloudstore.BudgetReport

	byModelErr    error
	byDayErr      error
	bySessionsErr error
	budgetErr     error

	// Captured args for assertions.
	lastSince       *time.Time
	lastBudgetMonth time.Time
}

func (m *mockCostStore) CostByModel(userID string, since *time.Time) ([]cloudstore.ModelCost, error) {
	m.lastSince = since
	return m.byModel, m.byModelErr
}

func (m *mockCostStore) CostByDay(userID string, since *time.Time) ([]cloudstore.DayCost, error) {
	m.lastSince = since
	return m.byDay, m.byDayErr
}

func (m *mockCostStore) CostBySessions(userID string, since *time.Time, limit int) ([]cloudstore.SessionCost, error) {
	m.lastSince = since
	return m.bySessions, m.bySessionsErr
}

func (m *mockCostStore) BudgetUsage(userID string, month time.Time, claudeBudget, openAIBudget float64) (*cloudstore.BudgetReport, error) {
	m.lastBudgetMonth = month
	return m.budget, m.budgetErr
}

// ─── Test Helpers ──────────────────────────────────────────────────────────

// newTestCostServer creates a minimal CloudServer with a mock cost store.
// Requests are injected with a fake userID via context, bypassing auth.
func newTestCostServer(mock *mockCostStore) *CloudServer {
	fixedNow := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	srv := &CloudServer{
		now:         func() time.Time { return fixedNow },
		costQuerier: mock,
	}
	srv.mux = http.NewServeMux()
	// Register cost routes without auth middleware.
	srv.mux.HandleFunc("GET /api/costs", srv.handleCosts)
	srv.mux.HandleFunc("GET /api/costs/sessions", srv.handleCostSessions)
	srv.mux.HandleFunc("GET /api/costs/budget", srv.handleCostBudget)
	return srv
}

// costReq creates a GET request with a fake userID in context.
func costReq(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := context.WithValue(r.Context(), userIDKey, "test-user-id")
	return r.WithContext(ctx)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return m
}

// ─── /api/costs Tests ──────────────────────────────────────────────────────

func TestHandleCosts_DefaultPeriod(t *testing.T) {
	mock := &mockCostStore{
		byModel: []cloudstore.ModelCost{
			{Model: "claude-sonnet-4", CostUSD: 10.50, Calls: 5},
			{Model: "gpt-4o", CostUSD: 3.20, Calls: 2},
		},
		byDay: []cloudstore.DayCost{
			{Date: "2025-06-15", CostUSD: 8.0, Calls: 4},
			{Date: "2025-06-14", CostUSD: 5.7, Calls: 3},
		},
		budget: &cloudstore.BudgetReport{
			ClaudeUsed:   10.50,
			ClaudeBudget: 200,
			OpenAIUsed:   3.20,
			OpenAIBudget: 200,
		},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeBody(t, rec)
	if body["period"] != "month" {
		t.Errorf("period = %v, want month", body["period"])
	}
	totalCost, ok := body["total_cost"].(float64)
	if !ok || totalCost != 13.70 {
		t.Errorf("total_cost = %v, want 13.70", body["total_cost"])
	}
}

func TestHandleCosts_WithSinceParam(t *testing.T) {
	mock := &mockCostStore{
		byModel: []cloudstore.ModelCost{},
		byDay:   []cloudstore.DayCost{},
		budget:  &cloudstore.BudgetReport{},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs?since=2025-06-10T00:00:00Z"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the since time was parsed and passed.
	if mock.lastSince == nil {
		t.Fatal("expected since to be set")
	}
	expected := time.Date(2025, 6, 10, 0, 0, 0, 0, time.UTC)
	if !mock.lastSince.Equal(expected) {
		t.Errorf("since = %v, want %v", mock.lastSince, expected)
	}
}

func TestHandleCosts_WithUntilParam(t *testing.T) {
	mock := &mockCostStore{
		byModel: []cloudstore.ModelCost{},
		byDay: []cloudstore.DayCost{
			{Date: "2025-06-12", CostUSD: 5.0, Calls: 2},
			{Date: "2025-06-15", CostUSD: 3.0, Calls: 1},
		},
		budget: &cloudstore.BudgetReport{},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs?until=2025-06-13T23:59:59Z"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := decodeBody(t, rec)
	byDay, ok := body["by_day"].([]any)
	if !ok {
		t.Fatalf("expected by_day array, got %T", body["by_day"])
	}
	// Only 2025-06-12 should remain (2025-06-15 is after until).
	if len(byDay) != 1 {
		t.Errorf("expected 1 day after until filter, got %d", len(byDay))
	}
}

func TestHandleCosts_EmptyResults(t *testing.T) {
	mock := &mockCostStore{
		byModel: []cloudstore.ModelCost{},
		byDay:   []cloudstore.DayCost{},
		budget:  &cloudstore.BudgetReport{ClaudeBudget: 200, OpenAIBudget: 200},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := decodeBody(t, rec)
	totalCost, _ := body["total_cost"].(float64)
	if totalCost != 0 {
		t.Errorf("total_cost = %v, want 0", totalCost)
	}
}

func TestHandleCosts_StoreError(t *testing.T) {
	mock := &mockCostStore{
		byModelErr: errTest,
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ─── /api/costs/sessions Tests ─────────────────────────────────────────────

func TestHandleCostSessions_Default(t *testing.T) {
	mock := &mockCostStore{
		bySessions: []cloudstore.SessionCost{
			{SessionID: "sess-1", Project: "proj-a", CostUSD: 5.0, Calls: 3},
			{SessionID: "sess-2", Project: "proj-b", CostUSD: 2.0, Calls: 1},
		},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs/sessions"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeBody(t, rec)
	sessions, ok := body["sessions"].([]any)
	if !ok {
		t.Fatalf("expected sessions array, got %T", body["sessions"])
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestHandleCostSessions_EmptyResults(t *testing.T) {
	mock := &mockCostStore{
		bySessions: []cloudstore.SessionCost{},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs/sessions"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := decodeBody(t, rec)
	sessions, ok := body["sessions"].([]any)
	if !ok {
		t.Fatalf("expected sessions array, got %T", body["sessions"])
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestHandleCostSessions_StoreError(t *testing.T) {
	mock := &mockCostStore{
		bySessionsErr: errTest,
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs/sessions"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ─── /api/costs/budget Tests ───────────────────────────────────────────────

func TestHandleCostBudget_OK(t *testing.T) {
	mock := &mockCostStore{
		budget: &cloudstore.BudgetReport{
			ClaudeUsed:   50.0,
			ClaudeBudget: 200.0,
			OpenAIUsed:   30.0,
			OpenAIBudget: 200.0,
		},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs/budget"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeBody(t, rec)

	if body["monthly_budget_usd"] != 400.0 {
		t.Errorf("monthly_budget_usd = %v, want 400", body["monthly_budget_usd"])
	}
	if body["spent_this_month"] != 80.0 {
		t.Errorf("spent_this_month = %v, want 80", body["spent_this_month"])
	}
	if body["remaining"] != 320.0 {
		t.Errorf("remaining = %v, want 320", body["remaining"])
	}
	if body["percent_used"] != 20.0 {
		t.Errorf("percent_used = %v, want 20", body["percent_used"])
	}

	// projected = (80 / 15) * 30 = 160.0  (fixedNow is June 15, 30-day month)
	projected, ok := body["projected_monthly"].(float64)
	if !ok {
		t.Fatalf("projected_monthly missing")
	}
	if projected < 150 || projected > 170 {
		t.Errorf("projected_monthly = %v, expected ~160", projected)
	}
}

func TestHandleCostBudget_EmptySpend(t *testing.T) {
	mock := &mockCostStore{
		budget: &cloudstore.BudgetReport{
			ClaudeUsed:   0,
			ClaudeBudget: 200.0,
			OpenAIUsed:   0,
			OpenAIBudget: 200.0,
		},
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs/budget"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := decodeBody(t, rec)
	if body["spent_this_month"] != 0.0 {
		t.Errorf("spent_this_month = %v, want 0", body["spent_this_month"])
	}
	if body["remaining"] != 400.0 {
		t.Errorf("remaining = %v, want 400", body["remaining"])
	}
	if body["projected_monthly"] != 0.0 {
		t.Errorf("projected_monthly = %v, want 0", body["projected_monthly"])
	}
}

func TestHandleCostBudget_StoreError(t *testing.T) {
	mock := &mockCostStore{
		budgetErr: errTest,
	}

	srv := newTestCostServer(mock)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, costReq("/api/costs/budget"))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

// ─── Date Range Parsing ────────────────────────────────────────────────────

func TestParseDateRange_Defaults(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		period string
		want   time.Time
	}{
		{"day", now.Add(-24 * time.Hour)},
		{"week", now.Add(-7 * 24 * time.Hour)},
		{"month", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/api/costs?period="+tt.period, nil)
		since, until := parseDateRange(r, now, tt.period)
		if !since.Equal(tt.want) {
			t.Errorf("period=%s: since = %v, want %v", tt.period, since, tt.want)
		}
		if !until.IsZero() {
			t.Errorf("period=%s: until should be zero, got %v", tt.period, until)
		}
	}
}

func TestParseDateRange_ExplicitSinceOverridesPeriod(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	r := httptest.NewRequest(http.MethodGet, "/api/costs?period=month&since=2025-01-01T00:00:00Z", nil)

	since, _ := parseDateRange(r, now, "month")
	expected := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if !since.Equal(expected) {
		t.Errorf("since = %v, want %v", since, expected)
	}
}

// ─── Projection Helper ────────────────────────────────────────────────────

func TestProjectMonthlySpend(t *testing.T) {
	// June 15, 12:00 UTC — halfway through a 30-day month.
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	projected := projectMonthlySpend(80.0, now)

	// ~15.5 days elapsed, 30 days in June => 80/15.5*30 ≈ 154.8
	if projected < 140 || projected > 170 {
		t.Errorf("projected = %v, expected ~155", projected)
	}
}

func TestProjectMonthlySpend_DayOne(t *testing.T) {
	// June 1, 00:30 UTC — less than 1 day elapsed, should use 1 as floor.
	now := time.Date(2025, 6, 1, 0, 30, 0, 0, time.UTC)
	projected := projectMonthlySpend(5.0, now)

	// elapsed < 1, clamped to 1 => 5/1*30 = 150
	if projected != 150 {
		t.Errorf("projected = %v, want 150", projected)
	}
}

// ─── Sentinel Error ────────────────────────────────────────────────────────

var errTest = &testError{msg: "mock store error"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
