package cloudstore

import (
	"fmt"
	"strings"
	"time"
)

// ActivityEntry represents a single entry in the unified activity feed.
type ActivityEntry struct {
	Type       string `json:"type"`       // observation, session
	ID         int64  `json:"id"`
	Project    string `json:"project"`
	Summary    string `json:"summary"`
	OccurredAt string `json:"occurred_at"`
	Data       any    `json:"data,omitempty"`
}

// ActivityFeed returns a unified feed of recent activity across
// observations and sessions. Results are ordered by occurred_at DESC.
func (cs *CloudStore) ActivityFeed(userID, project string, since *time.Time, limit int) ([]ActivityEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	args := []any{userID}
	argN := 2

	var projectFilter string
	if project != "" {
		projectFilter = fmt.Sprintf("$%d", argN)
		args = append(args, project)
		argN++
	}

	var sinceParam string
	if since != nil {
		sinceParam = fmt.Sprintf("$%d", argN)
		args = append(args, *since)
		argN++
	}

	limitParam := fmt.Sprintf("$%d", argN)
	args = append(args, limit)

	// Build per-table WHERE fragments.
	buildWhere := func(userCol, projectCol, timeCol string) string {
		parts := []string{userCol + " = $1"}
		if projectFilter != "" {
			parts = append(parts, projectCol+" = "+projectFilter)
		}
		if sinceParam != "" {
			parts = append(parts, timeCol+" >= "+sinceParam)
		}
		return strings.Join(parts, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT type, id, project, summary, occurred_at FROM (
			SELECT 'observation' AS type,
				id,
				COALESCE(project, '') AS project,
				title AS summary,
				created_at AS occurred_at
			FROM cloud_observations
			WHERE %s AND deleted_at IS NULL

			UNION ALL

			SELECT 'session' AS type,
				0 AS id,
				COALESCE(project, '') AS project,
				project AS summary,
				started_at AS occurred_at
			FROM cloud_sessions
			WHERE %s
		) AS activity
		ORDER BY occurred_at DESC
		LIMIT %s`,
		buildWhere("user_id", "project", "created_at"),
		buildWhere("user_id", "project", "started_at"),
		limitParam,
	)

	rows, err := cs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: activity feed: %w", err)
	}
	defer rows.Close()

	var entries []ActivityEntry
	for rows.Next() {
		var e ActivityEntry
		if err := rows.Scan(&e.Type, &e.ID, &e.Project, &e.Summary, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("cloudstore: scan activity entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}
