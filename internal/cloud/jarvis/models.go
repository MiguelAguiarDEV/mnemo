// Package jarvis implements the JARVIS orchestrator — LLM-powered assistant
// that manages conversations, delegates tasks, and tracks costs.
package jarvis

import (
	"os"
	"strconv"
	"strings"
)

// ─── Model Configuration ──────────────────────────────────────────────────

// ModelConfig describes an LLM model with its pricing and limits.
type ModelConfig struct {
	Provider    string  // "claude" or "openai"
	Model       string  // API model identifier
	MaxTokens   int     // max output tokens
	CostPer1KIn  float64 // USD per 1K input tokens
	CostPer1KOut float64 // USD per 1K output tokens
}

// Predefined model catalog using real Claude models via opencode-claude-auth plugin.
// Costs estimated in USD per 1K tokens (subscription-based, for tracking only).
var Models = map[string]ModelConfig{
	"opus": {
		Provider:     "anthropic",
		Model:        "claude-opus-4-6",
		MaxTokens:    8192,
		CostPer1KIn:  0.005,
		CostPer1KOut: 0.025,
	},
	"sonnet": {
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
		MaxTokens:    8192,
		CostPer1KIn:  0.003,
		CostPer1KOut: 0.015,
	},
	"gpt5": {
		Provider:     "opencode",
		Model:        "gpt-5-nano",
		MaxTokens:    8192,
		CostPer1KIn:  0.001,
		CostPer1KOut: 0.003,
	},
	"gpt4o-mini": {
		Provider:     "opencode",
		Model:        "gpt-5-nano",
		MaxTokens:    4096,
		CostPer1KIn:  0.00015,
		CostPer1KOut: 0.0006,
	},
}

// ─── Complexity Classification ─────────────────────────────────────────────

// Complexity levels returned by ClassifyComplexity.
const (
	ComplexitySimple  = "simple"
	ComplexityComplex = "complex"
)

// complexKeywords trigger "complex" classification when found in the prompt.
var complexKeywords = []string{
	"analyze", "design", "plan", "architect", "refactor",
	"investigate", "debug", "explain why", "compare",
	"review", "optimize", "security", "migration",
}

// simpleKeywords bias toward "simple" classification.
var simpleKeywords = []string{
	"list", "show", "get", "status", "count",
	"what is", "how many", "help", "hello", "hi",
}

// ClassifyComplexity uses simple heuristics (length + keyword matching)
// to decide whether a prompt needs a powerful model.
func ClassifyComplexity(prompt string) string {
	lower := strings.ToLower(prompt)

	// Long prompts are likely complex.
	if len(prompt) > 500 {
		return ComplexityComplex
	}

	complexScore := 0
	simpleScore := 0

	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			complexScore++
		}
	}
	for _, kw := range simpleKeywords {
		if strings.Contains(lower, kw) {
			simpleScore++
		}
	}

	if complexScore > simpleScore {
		return ComplexityComplex
	}
	return ComplexitySimple
}

// ─── Model Selection ───────────────────────────────────────────────────────

// SelectModel picks the best model given task complexity and current budget
// usage percentage (0.0-1.0). At >= 90% budget usage, it auto-downgrades
// to cheaper models regardless of complexity (CT-5).
func SelectModel(complexity string, budgetUsedPct float64) ModelConfig {
	// Auto-downgrade at 90% budget — always use cheap model.
	if budgetUsedPct >= 0.9 {
		return Models["sonnet"]
	}

	if complexity == ComplexityComplex {
		return Models["opus"]
	}
	return Models["sonnet"]
}

// ─── Budget Configuration ──────────────────────────────────────────────────

// DefaultBudgetClaude is the default monthly budget for Claude in USD.
const DefaultBudgetClaude = 200.0

// DefaultBudgetOpenAI is the default monthly budget for OpenAI in USD.
const DefaultBudgetOpenAI = 200.0

// BudgetFromEnv reads budget limits from environment variables.
// Falls back to defaults if not set or invalid.
func BudgetFromEnv() (claudeBudget, openAIBudget float64) {
	claudeBudget = envFloat("JARVIS_BUDGET_CLAUDE", DefaultBudgetClaude)
	openAIBudget = envFloat("JARVIS_BUDGET_OPENAI", DefaultBudgetOpenAI)
	return
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
