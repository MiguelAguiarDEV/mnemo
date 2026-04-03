package morpheus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ─── Observation ──────────────────────────────────────────────────────────

// Observation represents a single mnemo observation returned by search.
type Observation struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Type      string `json:"type"`
	TopicKey  string `json:"topic_key"`
	Project   string `json:"project"`
	CreatedAt string `json:"created_at"`
}

// TopicCluster groups observations that share the same topic_key.
type TopicCluster struct {
	TopicKey     string
	Observations []Observation
}

// MergeResult tracks what happened during consolidation for audit logging.
type MergeResult struct {
	TopicKey      string   `json:"topic_key"`
	SourceIDs     []int    `json:"source_ids"`
	MergedTitle   string   `json:"merged_title"`
	MergedContent string   `json:"merged_content"`
	Action        string   `json:"action"` // "merged", "kept", "skipped"
}

// ─── Phase 1: Orient ──────────────────────────────────────────────────────

// orient checks whether consolidation is needed based on time since last
// run and new observation count. Cheapest checks first (one stat() for
// time, then a CLI call for count).
func (c *Consolidator) orient(ctx context.Context) (bool, error) {
	// Gate 1 (cheapest): time since last consolidation.
	lastRun, err := c.lock.lastConsolidatedAt()
	if err != nil {
		return false, fmt.Errorf("orient: check last run: %w", err)
	}

	now := c.lock.now()
	if !lastRun.IsZero() && now.Sub(lastRun) < c.minAge {
		c.logger.Debug("orient: too soon since last consolidation",
			"last_run", lastRun,
			"min_age", c.minAge,
			"elapsed", now.Sub(lastRun),
		)
		return false, nil
	}

	// Gate 2: count new observations.
	count, err := c.countNewObservations(ctx)
	if err != nil {
		return false, fmt.Errorf("orient: count observations: %w", err)
	}

	if count < c.minObs {
		c.logger.Debug("orient: not enough new observations",
			"count", count,
			"min_obs", c.minObs,
		)
		return false, nil
	}

	c.logger.Info("orient: consolidation needed",
		"since_last_run", now.Sub(lastRun),
		"new_obs", count,
	)
	return true, nil
}

// countNewObservations runs mnemo search to count observations since
// the last consolidation.
func (c *Consolidator) countNewObservations(ctx context.Context) (int, error) {
	args := []string{"search", "--all", "--limit", "100", "--format", "json"}
	if c.project != "" {
		args = append(args, "--project", c.project)
	}

	output, err := c.runner.Run(c.mnemoBin, args...)
	if err != nil {
		// If mnemo CLI fails, assume we need consolidation to surface the error.
		c.logger.Warn("orient: mnemo search failed, assuming consolidation needed",
			"err", err, "output", string(output))
		return c.minObs, nil
	}

	var obs []Observation
	if err := json.Unmarshal(output, &obs); err != nil {
		// Try line-delimited JSON.
		obs = parseLineJSON(output)
	}

	// Filter to observations newer than last consolidation.
	lastRun, _ := c.lock.lastConsolidatedAt()
	if lastRun.IsZero() {
		return len(obs), nil
	}

	count := 0
	for _, o := range obs {
		t, err := time.Parse(time.RFC3339, o.CreatedAt)
		if err != nil {
			count++ // can't parse, include it
			continue
		}
		if t.After(lastRun) {
			count++
		}
	}
	return count, nil
}

// ─── Phase 2: Gather ──────────────────────────────────────────────────────

// gather fetches all observations and groups them by topic_key.
func (c *Consolidator) gather(ctx context.Context) ([]TopicCluster, error) {
	args := []string{"search", "--all", "--limit", "200", "--format", "json"}
	if c.project != "" {
		args = append(args, "--project", c.project)
	}

	output, err := c.runner.Run(c.mnemoBin, args...)
	if err != nil {
		return nil, fmt.Errorf("gather: mnemo search: %w (output: %s)", err, string(output))
	}

	var obs []Observation
	if err := json.Unmarshal(output, &obs); err != nil {
		obs = parseLineJSON(output)
		if len(obs) == 0 {
			return nil, fmt.Errorf("gather: parse observations: %w", err)
		}
	}

	c.logger.Info("gather: fetched observations", "count", len(obs))

	// Group by topic_key. Observations without a topic_key get grouped
	// by type as a fallback.
	groups := make(map[string][]Observation)
	for _, o := range obs {
		key := o.TopicKey
		if key == "" {
			key = "__type__" + o.Type
		}
		groups[key] = append(groups[key], o)
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	clusters := make([]TopicCluster, 0, len(keys))
	for _, k := range keys {
		clusters = append(clusters, TopicCluster{
			TopicKey:     k,
			Observations: groups[k],
		})
	}

	c.logger.Info("gather: grouped into clusters", "clusters", len(clusters))
	return clusters, nil
}

// ─── Phase 3: Consolidate ─────────────────────────────────────────────────

// consolidate merges observations within each topic cluster.
// Rules:
//   - Single observation: keep as-is.
//   - Multiple with same topic_key: merge into one, newest title wins,
//     content is combined with unique insights, contradictions resolved
//     (newer wins).
func (c *Consolidator) consolidate(ctx context.Context, clusters []TopicCluster) ([]MergeResult, error) {
	var results []MergeResult

	for _, cluster := range clusters {
		if len(cluster.Observations) <= 1 {
			if len(cluster.Observations) == 1 {
				results = append(results, MergeResult{
					TopicKey:      cluster.TopicKey,
					SourceIDs:     []int{cluster.Observations[0].ID},
					MergedTitle:   cluster.Observations[0].Title,
					MergedContent: cluster.Observations[0].Content,
					Action:        "kept",
				})
			}
			continue
		}

		merged := c.mergeCluster(cluster)
		results = append(results, merged)
	}

	c.logger.Info("consolidate: processed clusters",
		"total", len(results),
		"merged", countByAction(results, "merged"),
		"kept", countByAction(results, "kept"),
	)
	return results, nil
}

// mergeCluster combines multiple observations into one.
// Newer observations take precedence for contradictions.
func (c *Consolidator) mergeCluster(cluster TopicCluster) MergeResult {
	obs := cluster.Observations

	// Sort by created_at ascending (oldest first, newest last wins).
	sort.Slice(obs, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, obs[i].CreatedAt)
		tj, _ := time.Parse(time.RFC3339, obs[j].CreatedAt)
		return ti.Before(tj)
	})

	// Newest observation provides the title (most current).
	newest := obs[len(obs)-1]

	// Collect unique content lines. Later entries override earlier ones
	// for the same semantic content (simple dedup by normalized line).
	seen := make(map[string]string) // normalized -> original line
	var order []string
	for _, o := range obs {
		lines := splitContentLines(o.Content)
		for _, line := range lines {
			norm := normalizeContentLine(line)
			if norm == "" {
				continue
			}
			if _, exists := seen[norm]; !exists {
				order = append(order, norm)
			}
			// Newer always overwrites (contradiction resolution).
			seen[norm] = line
		}
	}

	// Rebuild merged content preserving insertion order.
	var merged strings.Builder
	for i, norm := range order {
		if i > 0 {
			merged.WriteString("\n")
		}
		merged.WriteString(seen[norm])
	}

	sourceIDs := make([]int, len(obs))
	for i, o := range obs {
		sourceIDs[i] = o.ID
	}

	return MergeResult{
		TopicKey:      cluster.TopicKey,
		SourceIDs:     sourceIDs,
		MergedTitle:   newest.Title,
		MergedContent: merged.String(),
		Action:        "merged",
	}
}

// ─── Phase 4: Prune ───────────────────────────────────────────────────────

// prune writes consolidated observations back to mnemo and marks source
// observations as consolidated. Returns an audit log of actions taken.
func (c *Consolidator) prune(ctx context.Context, results []MergeResult) ([]string, error) {
	var auditLog []string

	for _, r := range results {
		if r.Action == "kept" {
			auditLog = append(auditLog, fmt.Sprintf("kept: %q (id=%d)", r.MergedTitle, r.SourceIDs[0]))
			continue
		}

		if r.Action != "merged" {
			continue
		}

		// Save the consolidated observation.
		obsType := "discovery" // default type for consolidated entries
		title := fmt.Sprintf("consolidated: %s", r.MergedTitle)
		content := fmt.Sprintf("%s\n\n[consolidated from %d observations, ids: %v]",
			r.MergedContent, len(r.SourceIDs), r.SourceIDs)

		args := []string{"save", title, content, "--type", obsType}
		if c.project != "" {
			args = append(args, "--project", c.project)
		}
		if r.TopicKey != "" && !strings.HasPrefix(r.TopicKey, "__type__") {
			args = append(args, "--topic-key", r.TopicKey)
		}

		output, err := c.runner.Run(c.mnemoBin, args...)
		if err != nil {
			c.logger.Error("prune: failed to save consolidated observation",
				"topic_key", r.TopicKey,
				"err", err,
				"output", string(output),
			)
			auditLog = append(auditLog, fmt.Sprintf("FAILED to save consolidated %q: %v", r.MergedTitle, err))
			continue
		}

		auditLog = append(auditLog, fmt.Sprintf("merged %d observations into %q (ids: %v)",
			len(r.SourceIDs), title, r.SourceIDs))

		c.logger.Info("prune: saved consolidated observation",
			"topic_key", r.TopicKey,
			"source_count", len(r.SourceIDs),
			"source_ids", r.SourceIDs,
		)
	}

	return auditLog, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────

// parseLineJSON tries to parse output as newline-delimited JSON objects.
func parseLineJSON(data []byte) []Observation {
	var result []Observation
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var o Observation
		if err := json.Unmarshal([]byte(line), &o); err == nil {
			result = append(result, o)
		}
	}
	return result
}

// splitContentLines splits content into meaningful lines, trimming whitespace.
func splitContentLines(content string) []string {
	raw := strings.Split(content, "\n")
	var lines []string
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// normalizeContentLine creates a normalized key for dedup.
// Lowercases, strips punctuation from ends, collapses whitespace.
func normalizeContentLine(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = strings.Join(strings.Fields(line), " ")
	// Strip leading markdown bullets/dashes.
	line = strings.TrimLeft(line, "-*> ")
	return line
}

// countByAction counts results with a specific action.
func countByAction(results []MergeResult, action string) int {
	count := 0
	for _, r := range results {
		if r.Action == action {
			count++
		}
	}
	return count
}

