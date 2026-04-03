package cloudstore

import (
	"fmt"
)

// ─── Types ──────────────────────────────────────────────────────────────────

// Conversation represents a chat conversation row.
type Conversation struct {
	ID        int64  `json:"id"`
	UserID    string `json:"user_id"`
	Title     *string `json:"title,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Message represents a single message within a conversation.
type Message struct {
	ID             int64    `json:"id"`
	ConversationID int64    `json:"conversation_id"`
	Role           string   `json:"role"`
	Content        string   `json:"content"`
	Model          *string  `json:"model,omitempty"`
	TokensIn       *int     `json:"tokens_in,omitempty"`
	TokensOut      *int     `json:"tokens_out,omitempty"`
	CostUSD        *string  `json:"cost_usd,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

// ─── Conversation CRUD ──────────────────────────────────────────────────────

// CreateConversation inserts a new conversation for the given user.
func (cs *CloudStore) CreateConversation(userID string, title string) (int64, error) {
	var id int64
	err := cs.db.QueryRow(
		`INSERT INTO conversations (user_id, title)
		 VALUES ($1, $2)
		 RETURNING id`,
		userID, nullableString(title),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("cloudstore: create conversation: %w", err)
	}
	return id, nil
}

// ListConversations returns the most recent conversations for the user,
// ordered by updated_at DESC.
func (cs *CloudStore) ListConversations(userID string, limit int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := cs.db.Query(
		`SELECT id, user_id, title, created_at, updated_at
		 FROM conversations
		 WHERE user_id = $1
		 ORDER BY updated_at DESC
		 LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: list conversations: %w", err)
	}
	defer rows.Close()

	var convos []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("cloudstore: scan conversation: %w", err)
		}
		convos = append(convos, c)
	}
	return convos, nil
}

// GetConversation returns a single conversation by ID, scoped to the user.
func (cs *CloudStore) GetConversation(userID string, id int64) (*Conversation, error) {
	var c Conversation
	err := cs.db.QueryRow(
		`SELECT id, user_id, title, created_at, updated_at
		 FROM conversations
		 WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: get conversation: %w", err)
	}
	return &c, nil
}

// DeleteConversation removes a conversation and its messages (CASCADE).
func (cs *CloudStore) DeleteConversation(userID string, id int64) error {
	result, err := cs.db.Exec(
		`DELETE FROM conversations WHERE id = $1 AND user_id = $2`,
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("cloudstore: delete conversation: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("cloudstore: conversation not found")
	}
	return nil
}

// RenameConversation updates the title of a conversation.
func (cs *CloudStore) RenameConversation(userID string, id int64, title string) error {
	result, err := cs.db.Exec(
		`UPDATE conversations SET title = $1, updated_at = NOW() WHERE id = $2 AND user_id = $3`,
		title, id, userID,
	)
	if err != nil {
		return fmt.Errorf("cloudstore: rename conversation: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("cloudstore: conversation not found")
	}
	return nil
}

// ─── Message CRUD ───────────────────────────────────────────────────────────

// AddMessage inserts a new message into a conversation and updates the
// conversation's updated_at timestamp.
func (cs *CloudStore) AddMessage(conversationID int64, role, content, model string, tokensIn, tokensOut *int, costUSD *float64) (int64, error) {
	var id int64
	err := cs.db.QueryRow(
		`INSERT INTO messages (conversation_id, role, content, model, tokens_in, tokens_out, cost_usd)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		conversationID, role, content, nullableString(model),
		tokensIn, tokensOut, costUSD,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("cloudstore: add message: %w", err)
	}

	// Touch conversation updated_at.
	_, err = cs.db.Exec(
		"UPDATE conversations SET updated_at = NOW() WHERE id = $1",
		conversationID,
	)
	if err != nil {
		return id, fmt.Errorf("cloudstore: update conversation timestamp: %w", err)
	}

	return id, nil
}

// GetMessages returns messages for a conversation, ordered by created_at ASC.
func (cs *CloudStore) GetMessages(conversationID int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := cs.db.Query(
		`SELECT id, conversation_id, role, content, model, tokens_in, tokens_out,
		        cost_usd::text, created_at
		 FROM messages
		 WHERE conversation_id = $1
		 ORDER BY created_at ASC
		 LIMIT $2`,
		conversationID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: get messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content,
			&m.Model, &m.TokensIn, &m.TokensOut, &m.CostUSD, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("cloudstore: scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}
