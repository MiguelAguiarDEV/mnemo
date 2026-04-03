package jarvis

import (
	"strings"
	"testing"
)

// ── SelectModel ───────────────────────────────────────────────────────────

func TestSelectModel(t *testing.T) {
	tests := []struct {
		name       string
		complexity string
		budgetPct  float64
		wantModel  string // key in Models map
	}{
		{
			name:       "simple task with low budget usage returns sonnet",
			complexity: ComplexitySimple,
			budgetPct:  0.5,
			wantModel:  "sonnet",
		},
		{
			name:       "complex task with low budget usage returns opus",
			complexity: ComplexityComplex,
			budgetPct:  0.5,
			wantModel:  "opus",
		},
		{
			name:       "complex task at 95% budget auto-downgrades to sonnet",
			complexity: ComplexityComplex,
			budgetPct:  0.95,
			wantModel:  "sonnet",
		},
		{
			name:       "complex task at exactly 90% budget downgrades",
			complexity: ComplexityComplex,
			budgetPct:  0.9,
			wantModel:  "sonnet",
		},
		{
			name:       "complex task just below 90% keeps opus",
			complexity: ComplexityComplex,
			budgetPct:  0.89,
			wantModel:  "opus",
		},
		{
			name:       "simple task at zero budget returns sonnet",
			complexity: ComplexitySimple,
			budgetPct:  0.0,
			wantModel:  "sonnet",
		},
		{
			name:       "simple task at high budget still returns sonnet",
			complexity: ComplexitySimple,
			budgetPct:  0.99,
			wantModel:  "sonnet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectModel(tt.complexity, tt.budgetPct)
			want := Models[tt.wantModel]
			if got.Model != want.Model {
				t.Errorf("SelectModel(%q, %.2f) = %q, want %q",
					tt.complexity, tt.budgetPct, got.Model, want.Model)
			}
		})
	}
}

// ── ClassifyComplexity ────────────────────────────────────────────────────

func TestClassifyComplexity(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "short greeting is simple",
			prompt: "hello",
			want:   ComplexitySimple,
		},
		{
			name:   "status query is simple",
			prompt: "show me the status",
			want:   ComplexitySimple,
		},
		{
			name:   "list request is simple",
			prompt: "list all projects",
			want:   ComplexitySimple,
		},
		{
			name:   "analyze keyword triggers complex",
			prompt: "analyze performance problems",
			want:   ComplexityComplex,
		},
		{
			name:   "design keyword triggers complex",
			prompt: "design a new authentication flow",
			want:   ComplexityComplex,
		},
		{
			name:   "architect keyword triggers complex",
			prompt: "architect a microservices migration",
			want:   ComplexityComplex,
		},
		{
			name:   "long prompt over 500 chars is complex",
			prompt: strings.Repeat("a", 501),
			want:   ComplexityComplex,
		},
		{
			name:   "exactly 500 chars with no keywords is simple",
			prompt: strings.Repeat("a", 500),
			want:   ComplexitySimple,
		},
		{
			name:   "mixed keywords - more complex wins",
			prompt: "analyze and design the review system",
			want:   ComplexityComplex,
		},
		{
			name:   "case insensitive keyword matching",
			prompt: "ANALYZE this DESIGN",
			want:   ComplexityComplex,
		},
		{
			name:   "no keywords defaults to simple",
			prompt: "do something with the code",
			want:   ComplexitySimple,
		},
		{
			name:   "empty prompt is simple",
			prompt: "",
			want:   ComplexitySimple,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyComplexity(tt.prompt)
			if got != tt.want {
				t.Errorf("ClassifyComplexity(%q) = %q, want %q",
					truncate(tt.prompt, 50), got, tt.want)
			}
		})
	}
}

// ── BudgetFromEnv ─────────────────────────────────────────────────────────

func TestBudgetFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		claudeEnv   string
		openaiEnv   string
		wantClaude  float64
		wantOpenAI  float64
	}{
		{
			name:       "defaults when env not set",
			wantClaude: DefaultBudgetClaude,
			wantOpenAI: DefaultBudgetOpenAI,
		},
		{
			name:       "custom values from env",
			claudeEnv:  "100.0",
			openaiEnv:  "50.0",
			wantClaude: 100.0,
			wantOpenAI: 50.0,
		},
		{
			name:       "invalid env falls back to default",
			claudeEnv:  "not-a-number",
			openaiEnv:  "also-bad",
			wantClaude: DefaultBudgetClaude,
			wantOpenAI: DefaultBudgetOpenAI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env vars for this test only.
			if tt.claudeEnv != "" {
				t.Setenv("JARVIS_BUDGET_CLAUDE", tt.claudeEnv)
			}
			if tt.openaiEnv != "" {
				t.Setenv("JARVIS_BUDGET_OPENAI", tt.openaiEnv)
			}

			gotClaude, gotOpenAI := BudgetFromEnv()
			if gotClaude != tt.wantClaude {
				t.Errorf("claude budget = %v, want %v", gotClaude, tt.wantClaude)
			}
			if gotOpenAI != tt.wantOpenAI {
				t.Errorf("openai budget = %v, want %v", gotOpenAI, tt.wantOpenAI)
			}
		})
	}
}

// ── Model Catalog Sanity ──────────────────────────────────────────────────

func TestModelCatalogSanity(t *testing.T) {
	required := []string{"opus", "sonnet", "gpt5", "gpt4o-mini"}
	for _, key := range required {
		t.Run(key, func(t *testing.T) {
			m, ok := Models[key]
			if !ok {
				t.Fatalf("Models[%q] not found", key)
			}
			if m.Model == "" {
				t.Error("Model identifier is empty")
			}
			if m.Provider == "" {
				t.Error("Provider is empty")
			}
			if m.MaxTokens <= 0 {
				t.Errorf("MaxTokens = %d, want > 0", m.MaxTokens)
			}
			if m.CostPer1KIn <= 0 {
				t.Errorf("CostPer1KIn = %v, want > 0", m.CostPer1KIn)
			}
			if m.CostPer1KOut <= 0 {
				t.Errorf("CostPer1KOut = %v, want > 0", m.CostPer1KOut)
			}
		})
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
