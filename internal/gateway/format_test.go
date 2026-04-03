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
