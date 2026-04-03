package cloudstore

import "fmt"

// ─── Graph Types ───────────────────────────────────────────────────────────

// GraphNode represents a node in the knowledge graph.
type GraphNode struct {
	ID    string            `json:"id"`
	Type  string            `json:"type"` // session, observation, project, topic
	Label string            `json:"label"`
	Data  map[string]string `json:"data,omitempty"`
}

// GraphEdge represents a directed edge between two nodes.
type GraphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"` // belongs_to, has_topic, in_project
}

// GraphResponse holds the full knowledge graph payload.
type GraphResponse struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// BuildGraph constructs a knowledge graph for the given user.
// It derives nodes (session, observation, project, topic) and edges from
// sessions that have observations, capped at maxNodes total.
//
// Strategy: only include sessions that own at least one observation so the
// graph is immediately useful. Sessions are sorted by observation count
// descending so the richest sessions appear first.
func (cs *CloudStore) BuildGraph(userID, project string, maxNodes int) (*GraphResponse, error) {
	if maxNodes <= 0 {
		maxNodes = 100
	}

	// ── Fetch sessions that have observations ─────────────────────────
	sessionQuery := `
		SELECT cs.id, cs.project, cs.started_at, COALESCE(cs.summary, ''),
		       COALESCE(oc.cnt, 0) AS obs_count
		FROM cloud_sessions cs
		INNER JOIN (
			SELECT session_id, count(*) AS cnt
			FROM cloud_observations
			WHERE user_id = $1 AND deleted_at IS NULL
			GROUP BY session_id
		) oc ON oc.session_id = cs.id
		WHERE cs.user_id = $1
	`
	args := []any{userID}
	argIdx := 2

	if project != "" {
		sessionQuery += fmt.Sprintf(" AND cs.project = $%d", argIdx)
		args = append(args, project)
		argIdx++
	}
	sessionQuery += " ORDER BY obs_count DESC, cs.started_at DESC LIMIT $" + fmt.Sprintf("%d", argIdx)
	args = append(args, maxNodes)

	rows, err := cs.db.Query(sessionQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: build graph sessions: %w", err)
	}
	defer rows.Close()

	type sessionRow struct {
		id        string
		project   string
		startedAt string
		summary   string
		obsCount  int
	}
	var sessions []sessionRow
	sessionIDs := make([]string, 0)

	for rows.Next() {
		var s sessionRow
		if err := rows.Scan(&s.id, &s.project, &s.startedAt, &s.summary, &s.obsCount); err != nil {
			return nil, fmt.Errorf("cloudstore: build graph scan session: %w", err)
		}
		sessions = append(sessions, s)
		sessionIDs = append(sessionIDs, s.id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: build graph sessions iter: %w", err)
	}

	if len(sessions) == 0 {
		return &GraphResponse{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, nil
	}

	// ── Fetch observations for those sessions ──────────────────────────
	placeholders := make([]string, len(sessionIDs))
	obsArgs := []any{userID}
	for i, sid := range sessionIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		obsArgs = append(obsArgs, sid)
	}
	inClause := ""
	for i, ph := range placeholders {
		if i > 0 {
			inClause += ", "
		}
		inClause += ph
	}

	obsQuery := fmt.Sprintf(`
		SELECT id, session_id, title, type, project, topic_key, LEFT(content, 200)
		FROM cloud_observations
		WHERE user_id = $1
		  AND session_id IN (%s)
		  AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, inClause)

	obsRows, err := cs.db.Query(obsQuery, obsArgs...)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: build graph observations: %w", err)
	}
	defer obsRows.Close()

	type obsRow struct {
		id             int64
		sessionID      string
		title          string
		obsType        string
		project        *string
		topicKey       *string
		contentPreview string
	}
	var observations []obsRow

	for obsRows.Next() {
		var o obsRow
		if err := obsRows.Scan(&o.id, &o.sessionID, &o.title, &o.obsType, &o.project, &o.topicKey, &o.contentPreview); err != nil {
			return nil, fmt.Errorf("cloudstore: build graph scan observation: %w", err)
		}
		observations = append(observations, o)
	}
	if err := obsRows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: build graph observations iter: %w", err)
	}

	// ── Build graph ────────────────────────────────────────────────────
	nodes := make([]GraphNode, 0, maxNodes)
	edges := make([]GraphEdge, 0)
	seen := make(map[string]bool)

	addNode := func(n GraphNode) bool {
		if seen[n.ID] {
			return true // already present, not a new slot
		}
		if len(nodes) >= maxNodes {
			return false
		}
		seen[n.ID] = true
		nodes = append(nodes, n)
		return true
	}

	// Add project nodes first (few, high value)
	for _, s := range sessions {
		projID := "project:" + s.project
		addNode(GraphNode{ID: projID, Type: "project", Label: s.project})
	}

	// Add session nodes (only those with observations, already filtered)
	for _, s := range sessions {
		sessID := "session:" + s.id
		data := map[string]string{"project": s.project}
		if s.summary != "" {
			data["summary"] = s.summary
		}
		if !addNode(GraphNode{ID: sessID, Type: "session", Label: s.startedAt, Data: data}) {
			break
		}
		edges = append(edges, GraphEdge{
			Source:   sessID,
			Target:   "project:" + s.project,
			Relation: "in_project",
		})
	}

	// Add observation and topic nodes
	for _, o := range observations {
		obsID := fmt.Sprintf("observation:%d", o.id)
		data := map[string]string{"obs_type": o.obsType}
		if o.topicKey != nil && *o.topicKey != "" {
			data["topic_key"] = *o.topicKey
		}
		if o.contentPreview != "" {
			data["content"] = o.contentPreview
		}
		if !addNode(GraphNode{ID: obsID, Type: "observation", Label: o.title, Data: data}) {
			break
		}
		edges = append(edges, GraphEdge{
			Source:   obsID,
			Target:   "session:" + o.sessionID,
			Relation: "belongs_to",
		})

		if o.project != nil && *o.project != "" {
			edges = append(edges, GraphEdge{
				Source:   obsID,
				Target:   "project:" + *o.project,
				Relation: "in_project",
			})
		}

		if o.topicKey != nil && *o.topicKey != "" {
			topicID := "topic:" + *o.topicKey
			addNode(GraphNode{ID: topicID, Type: "topic", Label: *o.topicKey})
			edges = append(edges, GraphEdge{
				Source:   obsID,
				Target:   topicID,
				Relation: "has_topic",
			})
		}
	}

	return &GraphResponse{Nodes: nodes, Edges: edges}, nil
}
