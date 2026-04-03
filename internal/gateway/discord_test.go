package gateway

import (
	"context"
	"testing"
)

// ─── Unit Tests ───────────────────────────────────────────────────────────

func TestDiscordChannelName(t *testing.T) {
	dc := NewDiscordChannel("token", nil, WithDiscordChannelLogger(testLogger()))
	if dc.Name() != "discord" {
		t.Fatalf("expected name 'discord', got %q", dc.Name())
	}
}

func TestDiscordChannelImplementsChannel(t *testing.T) {
	var _ Channel = (*DiscordChannel)(nil)
}

func TestDiscordChannelImplementsSendableChannel(t *testing.T) {
	var _ SendableChannel = (*DiscordChannel)(nil)
}

func TestDiscordChannelStartRequiresToken(t *testing.T) {
	dc := NewDiscordChannel("", nil, WithDiscordChannelLogger(testLogger()))
	err := dc.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when starting without token")
	}
}

func TestDiscordChannelStartAlreadyStarted(t *testing.T) {
	dc := NewDiscordChannel("fake-token", nil,
		WithDiscordChannelLogger(testLogger()),
	)
	// Mark as started manually to avoid real connection.
	dc.mu.Lock()
	dc.started = true
	dc.mu.Unlock()

	err := dc.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when starting already-started channel")
	}
}

func TestDiscordChannelStopNotStarted(t *testing.T) {
	dc := NewDiscordChannel("token", nil, WithDiscordChannelLogger(testLogger()))
	// Should not error, just warn.
	if err := dc.Stop(); err != nil {
		t.Fatalf("unexpected error stopping non-started channel: %v", err)
	}
}

func TestDiscordChannelSendNoSession(t *testing.T) {
	dc := NewDiscordChannel("token", nil, WithDiscordChannelLogger(testLogger()))
	// No session connected.
	msg := OutgoingMessage{
		ChannelName: "discord",
		RecipientID: "channel-123",
		Text:        "hello",
	}
	err := dc.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error when sending without session")
	}
}

// ─── Allowed Users Tests ──────────────────────────────────────────────────

func TestDiscordIsUserAllowed(t *testing.T) {
	tests := []struct {
		name         string
		allowedUsers []string
		userID       string
		want         bool
	}{
		{
			name:         "allowed user",
			allowedUsers: []string{"user-1", "user-2"},
			userID:       "user-1",
			want:         true,
		},
		{
			name:         "disallowed user",
			allowedUsers: []string{"user-1", "user-2"},
			userID:       "user-3",
			want:         false,
		},
		{
			name:         "empty list allows all",
			allowedUsers: nil,
			userID:       "anyone",
			want:         true,
		},
		{
			name:         "whitespace in user IDs trimmed",
			allowedUsers: []string{" user-1 ", "user-2"},
			userID:       "user-1",
			want:         true,
		},
		{
			name:         "empty strings filtered",
			allowedUsers: []string{"", "user-1"},
			userID:       "",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := NewDiscordChannel("token", tt.allowedUsers,
				WithDiscordChannelLogger(testLogger()),
			)
			got := dc.isUserAllowed(tt.userID)
			if got != tt.want {
				t.Errorf("isUserAllowed(%q) = %v, want %v", tt.userID, got, tt.want)
			}
		})
	}
}

// ─── Gateway Registration Tests ───────────────────────────────────────────

func TestDiscordChannelRegistersWithGateway(t *testing.T) {
	handler := func(_ context.Context, msg IncomingMessage) (OutgoingMessage, error) {
		return OutgoingMessage{Text: "ok"}, nil
	}
	gw := New(handler, WithLogger(testLogger()))
	dc := NewDiscordChannel("token", nil,
		WithDiscordChannelLogger(testLogger()),
	)

	if err := gw.Register(dc); err != nil {
		t.Fatalf("register discord channel: %v", err)
	}

	names := gw.Channels()
	if len(names) != 1 || names[0] != "discord" {
		t.Fatalf("expected [discord], got %v", names)
	}
}

func TestDiscordAndWebChannelCoexist(t *testing.T) {
	handler := func(_ context.Context, msg IncomingMessage) (OutgoingMessage, error) {
		return OutgoingMessage{
			ChannelName: msg.ChannelName,
			RecipientID: msg.SenderID,
			Text:        "ack from " + msg.ChannelName,
		}, nil
	}
	gw := New(handler, WithLogger(testLogger()))

	web := NewWebChannel(gw, WithWebChannelLogger(testLogger()))
	discord := NewDiscordChannel("token", nil,
		WithDiscordChannelLogger(testLogger()),
	)

	if err := gw.Register(web); err != nil {
		t.Fatalf("register web: %v", err)
	}
	if err := gw.Register(discord); err != nil {
		t.Fatalf("register discord: %v", err)
	}

	// Route through web — doesn't need gateway started since
	// ProcessHTTPMessage calls HandleMessage directly.
	resp, err := web.ProcessHTTPMessage(context.Background(), "u1", 10, "hello")
	if err != nil {
		t.Fatalf("web process: %v", err)
	}
	if resp.Text != "ack from web" {
		t.Fatalf("unexpected web response: %s", resp.Text)
	}

	// Route through discord via gateway HandleMessage.
	discordMsg := IncomingMessage{
		ChannelName: "discord",
		SenderID:    "u2",
		Text:        "hey",
	}
	resp, err = gw.HandleMessage(context.Background(), discordMsg)
	if err != nil {
		t.Fatalf("discord handle: %v", err)
	}
	if resp.Text != "ack from discord" {
		t.Fatalf("unexpected discord response: %s", resp.Text)
	}
}

// ─── Message Splitting Tests ──────────────────────────────────────────────

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		maxLen     int
		wantChunks int
		wantNil    bool
	}{
		{
			name:       "empty string",
			text:       "",
			maxLen:     100,
			wantChunks: 0,
			wantNil:    true,
		},
		{
			name:       "short text fits",
			text:       "hello world",
			maxLen:     100,
			wantChunks: 1,
		},
		{
			name:       "text at exact limit",
			text:       "1234567890",
			maxLen:     10,
			wantChunks: 1,
		},
		{
			name:       "text exceeds limit",
			text:       "hello world this is a test",
			maxLen:     15,
			wantChunks: 2,
		},
		{
			name:       "split at paragraph boundary",
			text:       "first paragraph\n\nsecond paragraph",
			maxLen:     25,
			wantChunks: 2,
		},
		{
			name:       "split at line boundary",
			text:       "line one\nline two\nline three",
			maxLen:     20,
			wantChunks: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := SplitMessage(tt.text, tt.maxLen)
			if tt.wantNil && chunks != nil {
				t.Fatalf("expected nil, got %v", chunks)
			}
			if !tt.wantNil && len(chunks) != tt.wantChunks {
				t.Fatalf("expected %d chunks, got %d: %v", tt.wantChunks, len(chunks), chunks)
			}
		})
	}
}

func TestSplitMessageChunksWithinLimit(t *testing.T) {
	text := "alpha beta gamma\n\n" +
		"delta epsilon zeta\n\n" +
		"eta theta iota\n\n" +
		"kappa lambda mu"
	chunks := SplitMessage(text, 30)
	for i, chunk := range chunks {
		// Code fence closers can add chars, but each chunk content should be bounded.
		if len(chunk) > 40 { // allow margin for code fence repair
			t.Fatalf("chunk %d too large: %d chars", i, len(chunk))
		}
	}
}

func TestSplitMessagePreservesCodeBlocks(t *testing.T) {
	text := "```go\n" +
		"func main() {\n" +
		"    fmt.Println(\"hello\")\n" +
		"    fmt.Println(\"world\")\n" +
		"    fmt.Println(\"test\")\n" +
		"}\n" +
		"```"
	chunks := SplitMessage(text, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if count := countOccurrences(chunk, "```"); count%2 != 0 {
			t.Fatalf("chunk %d has unbalanced code fences (count=%d): %q", i, count, chunk)
		}
	}
}

func TestSplitMessageDiscordLimit(t *testing.T) {
	// Generate a long message.
	var sb []string
	for i := 0; i < 200; i++ {
		sb = append(sb, "This is a test line that adds content to exceed Discord limits.")
	}
	text := joinStrings(sb, "\n")

	chunks := SplitMessage(text, DiscordMaxMessageLen)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long message, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) > DiscordMaxMessageLen+10 { // small margin for code fence repair
			t.Fatalf("chunk %d exceeds Discord limit: %d chars", i, len(chunk))
		}
	}
}

// ─── Format Conversion Tests (Discord-specific) ──────────────────────────

func TestConvertToDiscordHeaders(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "h1 to bold",
			input: "# Title",
			want:  "**Title**",
		},
		{
			name:  "h2 to bold",
			input: "## Subtitle",
			want:  "**Subtitle**",
		},
		{
			name:  "h3 to bold",
			input: "### Section",
			want:  "**Section**",
		},
		{
			name:  "multiple headers",
			input: "# First\n## Second\nSome text",
			want:  "**First**\n**Second**\nSome text",
		},
		{
			name:  "no headers unchanged",
			input: "just regular text",
			want:  "just regular text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertToDiscord(tt.input)
			if got != tt.want {
				t.Errorf("convertToDiscord(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConvertToDiscordTables(t *testing.T) {
	input := "Before table\n| Col1 | Col2 |\n| --- | --- |\n| A | B |\nAfter table"
	got := convertToDiscord(input)

	// Table should be wrapped in code fences.
	if !containsSubstring(got, "```") {
		t.Errorf("expected code fences around table, got:\n%s", got)
	}
	// Non-table text should be preserved.
	if !containsSubstring(got, "Before table") {
		t.Errorf("expected 'Before table' to be preserved")
	}
	if !containsSubstring(got, "After table") {
		t.Errorf("expected 'After table' to be preserved")
	}
}

func TestConvertToDiscordPreservesBoldItalic(t *testing.T) {
	input := "This is **bold** and *italic* text"
	got := convertToDiscord(input)
	if got != input {
		t.Errorf("bold/italic should be preserved:\n  got:  %q\n  want: %q", got, input)
	}
}

func TestConvertToDiscordPreservesCodeBlocks(t *testing.T) {
	input := "```go\nfmt.Println(\"hello\")\n```"
	got := convertToDiscord(input)
	if got != input {
		t.Errorf("code blocks should be preserved:\n  got:  %q\n  want: %q", got, input)
	}
}

func TestConvertMarkdownDiscordFormat(t *testing.T) {
	input := "# Title\n\nSome **bold** text"
	got := ConvertMarkdown(input, FormatDiscord)
	if containsSubstring(got, "# ") {
		t.Errorf("Discord format should not contain markdown headers: %q", got)
	}
	if !containsSubstring(got, "**Title**") {
		t.Errorf("Headers should be converted to bold: %q", got)
	}
	if !containsSubstring(got, "**bold**") {
		t.Errorf("Bold should be preserved: %q", got)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────

func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			count++
		}
	}
	return count
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
