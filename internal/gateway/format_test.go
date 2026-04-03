package gateway

import (
	"testing"
)

func TestConvertMarkdown(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		format MessageFormat
		want   string
	}{
		// ── Markdown passthrough ──────────────────────────────────────
		{
			name:   "markdown passthrough",
			input:  "**bold** and *italic*",
			format: FormatMarkdown,
			want:   "**bold** and *italic*",
		},
		{
			name:   "markdown passthrough with code",
			input:  "use `fmt.Println` here",
			format: FormatMarkdown,
			want:   "use `fmt.Println` here",
		},

		// ── Plain text stripping ─────────────────────────────────────
		{
			name:   "strip bold stars",
			input:  "this is **bold** text",
			format: FormatPlain,
			want:   "this is bold text",
		},
		{
			name:   "strip bold underscores",
			input:  "this is __bold__ text",
			format: FormatPlain,
			want:   "this is bold text",
		},
		{
			name:   "strip italic stars",
			input:  "this is *italic* text",
			format: FormatPlain,
			want:   "this is italic text",
		},
		{
			name:   "strip inline code",
			input:  "use `go test` here",
			format: FormatPlain,
			want:   "use go test here",
		},
		{
			name:   "strip fenced code block",
			input:  "```go\nfmt.Println(\"hello\")\n```",
			format: FormatPlain,
			want:   "fmt.Println(\"hello\")",
		},
		{
			name:   "strip headers",
			input:  "# Title\n## Subtitle",
			format: FormatPlain,
			want:   "Title\nSubtitle",
		},
		{
			name:   "strip links",
			input:  "visit [Google](https://google.com) now",
			format: FormatPlain,
			want:   "visit Google now",
		},
		{
			name:   "strip images",
			input:  "![alt text](https://example.com/img.png)",
			format: FormatPlain,
			want:   "alt text",
		},
		{
			name:   "strip blockquotes",
			input:  "> this is a quote",
			format: FormatPlain,
			want:   "this is a quote",
		},
		{
			name:   "strip strikethrough",
			input:  "this is ~~deleted~~ text",
			format: FormatPlain,
			want:   "this is deleted text",
		},
		{
			name:   "strip horizontal rule",
			input:  "above\n---\nbelow",
			format: FormatPlain,
			want:   "above\n\nbelow",
		},
		{
			name:   "strip bold italic combined",
			input:  "***bold italic*** text",
			format: FormatPlain,
			want:   "bold italic text",
		},
		{
			name:   "complex mixed markdown",
			input:  "# Hello\n\nThis is **bold** and *italic* with `code`.\n\n> A quote\n\n[Link](http://example.com)",
			format: FormatPlain,
			want:   "Hello\n\nThis is bold and italic with code.\n\nA quote\n\nLink",
		},
		{
			name:   "empty string",
			input:  "",
			format: FormatPlain,
			want:   "",
		},
		{
			name:   "plain text unchanged",
			input:  "just plain text",
			format: FormatPlain,
			want:   "just plain text",
		},

		// ── HTML fallback ────────────────────────────────────────────
		{
			name:   "html passthrough (not yet implemented)",
			input:  "**bold** text",
			format: FormatHTML,
			want:   "**bold** text",
		},

		// ── Discord format ───────────────────────────────────────────
		{
			name:   "discord headers to bold",
			input:  "# Title\n## Subtitle",
			format: FormatDiscord,
			want:   "**Title**\n**Subtitle**",
		},
		{
			name:   "discord preserves bold",
			input:  "this is **bold** text",
			format: FormatDiscord,
			want:   "this is **bold** text",
		},
		{
			name:   "discord preserves italic",
			input:  "this is *italic* text",
			format: FormatDiscord,
			want:   "this is *italic* text",
		},
		{
			name:   "discord preserves code blocks",
			input:  "```go\nfmt.Println()\n```",
			format: FormatDiscord,
			want:   "```go\nfmt.Println()\n```",
		},
		{
			name:   "discord tables to code blocks",
			input:  "| A | B |\n| - | - |\n| 1 | 2 |",
			format: FormatDiscord,
			want:   "```\n| A | B |\n| - | - |\n| 1 | 2 |\n```",
		},

		// ── Unknown format ───────────────────────────────────────────
		{
			name:   "unknown format passthrough",
			input:  "**text**",
			format: MessageFormat(99),
			want:   "**text**",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertMarkdown(tt.input, tt.format)
			if got != tt.want {
				t.Errorf("ConvertMarkdown(%q, %d)\n  got:  %q\n  want: %q", tt.input, tt.format, got, tt.want)
			}
		})
	}
}

func TestSplitMessageBasic(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		maxLen     int
		wantChunks int
	}{
		{"empty", "", 100, 0},
		{"fits", "hello", 100, 1},
		{"exact", "12345", 5, 1},
		{"split needed", "hello world foo bar", 10, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := SplitMessage(tt.text, tt.maxLen)
			if len(chunks) != tt.wantChunks {
				t.Errorf("SplitMessage(%q, %d) = %d chunks, want %d", tt.text, tt.maxLen, len(chunks), tt.wantChunks)
			}
		})
	}
}

func TestSplitMessageAt2000Chars(t *testing.T) {
	// Build a string just over 2000 chars.
	line := "This is a line of text that is about fifty characters.\n"
	text := ""
	for len(text) < 2100 {
		text += line
	}

	chunks := SplitMessage(text, DiscordMaxMessageLen)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) > DiscordMaxMessageLen+10 {
			t.Errorf("chunk %d exceeds limit: %d", i, len(chunk))
		}
	}
}

func TestConvertTablesToCodeBlocks(t *testing.T) {
	input := "text before\n| A | B |\n| - | - |\n| 1 | 2 |\ntext after"
	got := convertTablesToCodeBlocks(input)

	if got == input {
		t.Fatal("expected table to be wrapped in code fences")
	}
	// Should contain opening and closing ```.
	fenceCount := 0
	for _, line := range splitLines(got) {
		if line == "```" {
			fenceCount++
		}
	}
	if fenceCount != 2 {
		t.Errorf("expected 2 code fences, got %d in:\n%s", fenceCount, got)
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
