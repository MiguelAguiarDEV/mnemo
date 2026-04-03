package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── Mock Discord for ProgressReporter tests ────────────────────────────────

type mockDiscordSession struct {
	mu       sync.Mutex
	sent     []mockSentMessage
	edits    []mockEditMessage
	nextID   int
}

type mockSentMessage struct {
	ChannelID string
	Content   string
	ID        string
}

type mockEditMessage struct {
	ChannelID string
	MessageID string
	Content   string
}

func newMockDiscordChannel(t *testing.T) (*DiscordChannel, *mockDiscordSession) {
	t.Helper()
	mock := &mockDiscordSession{}
	dc := NewDiscordChannel("fake-token", nil, WithDiscordChannelLogger(testLogger()))
	// We'll use the mock directly through ProgressReporter's methods
	// by overriding the discord channel methods via a wrapper.
	return dc, mock
}

// mockProgressDiscord wraps DiscordChannel for testing without a real session.
type mockProgressDiscord struct {
	mu    sync.Mutex
	sent  []mockSentMessage
	edits []mockEditMessage
	msgID int
}

func (m *mockProgressDiscord) SendInitial(_ context.Context, channelID, text string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgID++
	id := "msg-" + string(rune('0'+m.msgID))
	m.sent = append(m.sent, mockSentMessage{ChannelID: channelID, Content: text, ID: id})
	return id, nil
}

func (m *mockProgressDiscord) EditMessage(_ context.Context, channelID, messageID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, mockEditMessage{ChannelID: channelID, MessageID: messageID, Content: text})
	return nil
}

func (m *mockProgressDiscord) lastEdit() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.edits) == 0 {
		return ""
	}
	return m.edits[len(m.edits)-1].Content
}

func (m *mockProgressDiscord) editCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.edits)
}

func (m *mockProgressDiscord) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// testableProgressReporter creates a ProgressReporter backed by a mock.
type testableProgressReporter struct {
	*ProgressReporter
	mock *mockProgressDiscord
}

func newTestableProgressReporter(t *testing.T) *testableProgressReporter {
	t.Helper()
	mock := &mockProgressDiscord{}
	// Create a real ProgressReporter but we'll override its methods.
	// Instead, we create a thin wrapper that mimics the same interface.
	pr := &ProgressReporter{
		channelID: "test-channel",
		logger:    testLogger(),
	}
	return &testableProgressReporter{ProgressReporter: pr, mock: mock}
}

// Start sends initial message via mock.
func (t *testableProgressReporter) Start(ctx context.Context) error {
	msgID, err := t.mock.SendInitial(ctx, t.channelID, "\U0001f504 Procesando...")
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.messageID = msgID
	t.lastEdit = time.Now()
	t.mu.Unlock()
	return nil
}

// doEditMock performs edit via mock. Must be called with mu held.
func (t *testableProgressReporter) doEditMock() {
	content := t.buildMessage()
	if content == "" {
		return
	}
	if len(content) > DiscordMaxMessageLen {
		content = content[:DiscordMaxMessageLen-3] + "..."
	}
	_ = t.mock.EditMessage(context.Background(), t.channelID, t.messageID, content)
	t.ProgressReporter.lastEdit = time.Now()
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestProgressReporter_BuildMessage_Initial(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
	}

	pr.mu.Lock()
	msg := pr.buildMessage()
	pr.mu.Unlock()

	if !strings.Contains(msg, "Procesando...") {
		t.Errorf("expected 'Procesando...' in initial message, got %q", msg)
	}
}

func TestProgressReporter_BuildMessage_WithTools(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		tools: []toolStatus{
			{Name: "load_skill", Done: true},
			{Name: "create_task", Done: false},
		},
	}

	pr.mu.Lock()
	msg := pr.buildMessage()
	pr.mu.Unlock()

	if !strings.Contains(msg, "load_skill") {
		t.Errorf("expected 'load_skill' in message, got %q", msg)
	}
	if !strings.Contains(msg, "\u2713") {
		t.Errorf("expected checkmark in message for done tool, got %q", msg)
	}
	if !strings.Contains(msg, "create_task") {
		t.Errorf("expected 'create_task' in message, got %q", msg)
	}
	if !strings.Contains(msg, "Procesando...") {
		t.Errorf("expected 'Procesando...' when no text buffered, got %q", msg)
	}
}

func TestProgressReporter_BuildMessage_WithText(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		tools: []toolStatus{
			{Name: "load_skill", Done: true},
		},
	}
	pr.textBuffer.WriteString("Hello world")

	pr.mu.Lock()
	msg := pr.buildMessage()
	pr.mu.Unlock()

	if !strings.Contains(msg, "load_skill") {
		t.Errorf("expected tool name in message, got %q", msg)
	}
	if !strings.Contains(msg, "Hello world") {
		t.Errorf("expected text in message, got %q", msg)
	}
	if strings.Contains(msg, "Procesando...") {
		t.Errorf("should not contain 'Procesando...' when text is present, got %q", msg)
	}
}

func TestProgressReporter_HandleStatus_ToolStart(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
	}

	statusJSON, _ := json.Marshal(map[string]any{
		"event": "tool_start",
		"tool":  "search_memory",
	})

	pr.mu.Lock()
	pr.handleStatus(string(statusJSON))

	if len(pr.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(pr.tools))
	}
	if pr.tools[0].Name != "search_memory" {
		t.Errorf("expected tool name 'search_memory', got %q", pr.tools[0].Name)
	}
	if pr.tools[0].Done {
		t.Error("tool should not be done yet")
	}
	pr.mu.Unlock()
}

func TestProgressReporter_HandleStatus_ToolDone(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
		tools: []toolStatus{
			{Name: "search_memory", Done: false},
		},
	}

	statusJSON, _ := json.Marshal(map[string]any{
		"event":       "tool_done",
		"tool":        "search_memory",
		"duration_ms": float64(250),
		"is_error":    false,
	})

	pr.mu.Lock()
	pr.handleStatus(string(statusJSON))

	if !pr.tools[0].Done {
		t.Error("tool should be done")
	}
	if pr.tools[0].DurMS != 250 {
		t.Errorf("expected duration 250ms, got %d", pr.tools[0].DurMS)
	}
	pr.mu.Unlock()
}

func TestProgressReporter_HandleStatus_ToolError(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
		tools: []toolStatus{
			{Name: "bad_tool", Done: false},
		},
	}

	statusJSON, _ := json.Marshal(map[string]any{
		"event":       "tool_done",
		"tool":        "bad_tool",
		"duration_ms": float64(100),
		"is_error":    true,
	})

	pr.mu.Lock()
	pr.handleStatus(string(statusJSON))

	if !pr.tools[0].Error {
		t.Error("tool should be marked as error")
	}
	pr.mu.Unlock()
}

func TestProgressReporter_HandleStatus_InvalidJSON(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
	}

	// Should not panic.
	pr.mu.Lock()
	pr.handleStatus("not valid json{{{")
	pr.mu.Unlock()

	if len(pr.tools) != 0 {
		t.Error("should not add any tools for invalid JSON")
	}
}

func TestProgressReporter_OnToken_IgnoresWhenDone(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
		done:      true,
	}

	pr.OnToken("hello")

	if pr.textBuffer.Len() != 0 {
		t.Error("should not buffer text when done")
	}
}

func TestProgressReporter_OnToken_IgnoresWithoutMessageID(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
	}

	pr.OnToken("hello")

	if pr.textBuffer.Len() != 0 {
		t.Error("should not buffer text without message ID")
	}
}

func TestProgressReporter_MessageEvolution(t *testing.T) {
	// Simulate the full message evolution described in the spec.
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
	}

	// Stage 1: Initial — no tools, no text.
	pr.mu.Lock()
	msg1 := pr.buildMessage()
	pr.mu.Unlock()
	if msg1 != "\U0001f504 Procesando..." {
		t.Errorf("stage 1: expected processing indicator, got %q", msg1)
	}

	// Stage 2: Tool started.
	startJSON, _ := json.Marshal(map[string]any{"event": "tool_start", "tool": "load_skill"})
	pr.mu.Lock()
	pr.handleStatus(string(startJSON))
	msg2 := pr.buildMessage()
	pr.mu.Unlock()
	if !strings.Contains(msg2, "load_skill") {
		t.Errorf("stage 2: expected 'load_skill', got %q", msg2)
	}
	if !strings.Contains(msg2, "Procesando...") {
		t.Errorf("stage 2: expected 'Procesando...', got %q", msg2)
	}

	// Stage 3: Tool done, second tool started.
	doneJSON, _ := json.Marshal(map[string]any{"event": "tool_done", "tool": "load_skill", "duration_ms": float64(100)})
	pr.mu.Lock()
	pr.handleStatus(string(doneJSON))
	pr.mu.Unlock()

	start2JSON, _ := json.Marshal(map[string]any{"event": "tool_start", "tool": "create_task"})
	pr.mu.Lock()
	pr.handleStatus(string(start2JSON))
	msg3 := pr.buildMessage()
	pr.mu.Unlock()
	if !strings.Contains(msg3, "load_skill \u2713") {
		t.Errorf("stage 3: expected 'load_skill ✓', got %q", msg3)
	}
	if !strings.Contains(msg3, "create_task") {
		t.Errorf("stage 3: expected 'create_task', got %q", msg3)
	}

	// Stage 4: All tools done, text arrives.
	done2JSON, _ := json.Marshal(map[string]any{"event": "tool_done", "tool": "create_task", "duration_ms": float64(200)})
	pr.mu.Lock()
	pr.handleStatus(string(done2JSON))
	pr.textBuffer.WriteString("Tarea 'revisar logs' creada.")
	msg4 := pr.buildMessage()
	pr.mu.Unlock()
	if !strings.Contains(msg4, "load_skill \u2713") {
		t.Errorf("stage 4: expected 'load_skill ✓', got %q", msg4)
	}
	if !strings.Contains(msg4, "create_task \u2713") {
		t.Errorf("stage 4: expected 'create_task ✓', got %q", msg4)
	}
	if !strings.Contains(msg4, "Tarea 'revisar logs' creada.") {
		t.Errorf("stage 4: expected response text, got %q", msg4)
	}
	if strings.Contains(msg4, "Procesando...") {
		t.Errorf("stage 4: should not contain 'Procesando...' when text is present, got %q", msg4)
	}
}

func TestProgressReporter_BuildMessage_ErrorTool(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		tools: []toolStatus{
			{Name: "broken_tool", Done: true, Error: true},
		},
	}
	pr.textBuffer.WriteString("Something failed")

	pr.mu.Lock()
	msg := pr.buildMessage()
	pr.mu.Unlock()

	if !strings.Contains(msg, "\u274c") {
		t.Errorf("expected error icon for failed tool, got %q", msg)
	}
	if !strings.Contains(msg, "\u2717") {
		t.Errorf("expected cross mark for failed tool, got %q", msg)
	}
}

func TestProgressReporter_OnToken_StatusPrefix(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
	}

	statusJSON, _ := json.Marshal(map[string]any{
		"event": "tool_start",
		"tool":  "my_tool",
	})
	pr.OnToken("__STATUS__" + string(statusJSON))

	pr.mu.Lock()
	defer pr.mu.Unlock()

	if len(pr.tools) != 1 {
		t.Fatalf("expected 1 tool after status token, got %d", len(pr.tools))
	}
	if pr.textBuffer.Len() != 0 {
		t.Error("status token should not be added to text buffer")
	}
}

func TestProgressReporter_OnToken_RegularText(t *testing.T) {
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
		lastEdit:  time.Now(), // recent edit to prevent immediate edit
	}

	pr.OnToken("Hello ")
	pr.OnToken("world")

	pr.mu.Lock()
	defer pr.mu.Unlock()

	if pr.textBuffer.String() != "Hello world" {
		t.Errorf("expected 'Hello world' in buffer, got %q", pr.textBuffer.String())
	}
}

func TestProgressReporter_MultipleSameToolInstances(t *testing.T) {
	// When the same tool is called multiple times, each instance should be tracked.
	pr := &ProgressReporter{
		channelID: "ch1",
		logger:    testLogger(),
		messageID: "msg1",
	}

	start1, _ := json.Marshal(map[string]any{"event": "tool_start", "tool": "search"})
	done1, _ := json.Marshal(map[string]any{"event": "tool_done", "tool": "search", "duration_ms": float64(50)})
	start2, _ := json.Marshal(map[string]any{"event": "tool_start", "tool": "search"})

	pr.mu.Lock()
	pr.handleStatus(string(start1))
	pr.handleStatus(string(done1))
	pr.handleStatus(string(start2))
	pr.mu.Unlock()

	pr.mu.Lock()
	defer pr.mu.Unlock()

	if len(pr.tools) != 2 {
		t.Fatalf("expected 2 tool instances, got %d", len(pr.tools))
	}
	if !pr.tools[0].Done {
		t.Error("first search should be done")
	}
	if pr.tools[1].Done {
		t.Error("second search should not be done yet")
	}
}
