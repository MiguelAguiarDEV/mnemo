package cloudstore

import (
	"testing"
	"time"
)

// ── CreateConversation ────────────────────────────────────────────────────

func TestCreateConversation(t *testing.T) {
	cs, userID := testUser(t)

	id, err := cs.CreateConversation(userID, "Test Chat")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected ID > 0, got %d", id)
	}

	conv, err := cs.GetConversation(userID, id)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if conv.Title == nil || *conv.Title != "Test Chat" {
		t.Errorf("title = %v, want 'Test Chat'", conv.Title)
	}
	if conv.UserID != userID {
		t.Errorf("user_id = %q, want %q", conv.UserID, userID)
	}
}

func TestCreateConversationEmptyTitle(t *testing.T) {
	cs, userID := testUser(t)

	id, err := cs.CreateConversation(userID, "")
	if err != nil {
		t.Fatalf("CreateConversation with empty title: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected ID > 0, got %d", id)
	}
}

func TestGetConversationNotFound(t *testing.T) {
	cs, userID := testUser(t)

	_, err := cs.GetConversation(userID, 99999)
	if err == nil {
		t.Fatal("expected error for non-existent conversation")
	}
}

// ── ListConversations ─────────────────────────────────────────────────────

func TestListConversations(t *testing.T) {
	cs, userID := testUser(t)

	cs.CreateConversation(userID, "First")
	cs.CreateConversation(userID, "Second")
	cs.CreateConversation(userID, "Third")

	convos, err := cs.ListConversations(userID, 10)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convos) != 3 {
		t.Fatalf("expected 3 conversations, got %d", len(convos))
	}

	// Should be ordered by updated_at DESC (most recent first).
	if convos[0].Title == nil || *convos[0].Title != "Third" {
		t.Errorf("first conversation title = %v, want 'Third'", convos[0].Title)
	}
}

func TestListConversationsDefaultLimit(t *testing.T) {
	cs, userID := testUser(t)

	cs.CreateConversation(userID, "Only one")

	// Passing 0 should use default limit (20), not fail.
	convos, err := cs.ListConversations(userID, 0)
	if err != nil {
		t.Fatalf("ListConversations with 0 limit: %v", err)
	}
	if len(convos) != 1 {
		t.Errorf("expected 1 conversation, got %d", len(convos))
	}
}

func TestListConversationsRespectLimit(t *testing.T) {
	cs, userID := testUser(t)

	for i := 0; i < 5; i++ {
		cs.CreateConversation(userID, "chat")
	}

	convos, err := cs.ListConversations(userID, 2)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convos) != 2 {
		t.Errorf("expected 2 conversations, got %d", len(convos))
	}
}

func TestListConversationsIsolatesUsers(t *testing.T) {
	cs, userID := testUser(t)

	cs.CreateConversation(userID, "user1 chat")

	u2, err := cs.CreateUser("other2", "other2@example.com", "pass")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cs.CreateConversation(u2.ID, "user2 chat")

	convos, err := cs.ListConversations(userID, 10)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convos) != 1 {
		t.Errorf("expected 1 conversation for user1, got %d", len(convos))
	}
}

// ── AddMessage ────────────────────────────────────────────────────────────

func TestAddMessage(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "msg test")

	tokIn := 100
	tokOut := 200
	cost := 0.0015
	msgID, err := cs.AddMessage(convID, "user", "Hello!", "gpt-4", &tokIn, &tokOut, &cost)
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if msgID <= 0 {
		t.Errorf("expected message ID > 0, got %d", msgID)
	}
}

func TestAddMessageUpdatesConversationTimestamp(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "timestamp test")

	conv1, _ := cs.GetConversation(userID, convID)
	updatedBefore := conv1.UpdatedAt

	// Small delay to ensure timestamp difference.
	time.Sleep(50 * time.Millisecond)

	cs.AddMessage(convID, "user", "ping", "", nil, nil, nil)

	conv2, _ := cs.GetConversation(userID, convID)
	if conv2.UpdatedAt <= updatedBefore {
		t.Error("expected updated_at to advance after AddMessage")
	}
}

func TestAddMessageNullOptionalFields(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "null fields")

	msgID, err := cs.AddMessage(convID, "assistant", "response", "", nil, nil, nil)
	if err != nil {
		t.Fatalf("AddMessage with nil optionals: %v", err)
	}

	msgs, err := cs.GetMessages(convID, 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != msgID {
		t.Errorf("msg ID = %d, want %d", msgs[0].ID, msgID)
	}
	if msgs[0].Model != nil {
		t.Errorf("model should be nil for empty string, got %v", msgs[0].Model)
	}
	if msgs[0].TokensIn != nil {
		t.Errorf("tokens_in should be nil, got %v", msgs[0].TokensIn)
	}
}

// ── GetMessages ───────────────────────────────────────────────────────────

func TestGetMessagesOrderASC(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "order test")

	cs.AddMessage(convID, "user", "first", "", nil, nil, nil)
	cs.AddMessage(convID, "assistant", "second", "", nil, nil, nil)
	cs.AddMessage(convID, "user", "third", "", nil, nil, nil)

	msgs, err := cs.GetMessages(convID, 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "first" {
		t.Errorf("msgs[0].Content = %q, want 'first'", msgs[0].Content)
	}
	if msgs[1].Content != "second" {
		t.Errorf("msgs[1].Content = %q, want 'second'", msgs[1].Content)
	}
	if msgs[2].Content != "third" {
		t.Errorf("msgs[2].Content = %q, want 'third'", msgs[2].Content)
	}
}

func TestGetMessagesDefaultLimit(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "default limit")
	cs.AddMessage(convID, "user", "msg", "", nil, nil, nil)

	// 0 limit should use default (100).
	msgs, err := cs.GetMessages(convID, 0)
	if err != nil {
		t.Fatalf("GetMessages with 0 limit: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestGetMessagesRespectsLimit(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "limit test")
	for i := 0; i < 5; i++ {
		cs.AddMessage(convID, "user", "msg", "", nil, nil, nil)
	}

	msgs, err := cs.GetMessages(convID, 3)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages, got %d", len(msgs))
	}
}

func TestGetMessagesEmptyConversation(t *testing.T) {
	cs, userID := testUser(t)

	convID, _ := cs.CreateConversation(userID, "empty")

	msgs, err := cs.GetMessages(convID, 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}
