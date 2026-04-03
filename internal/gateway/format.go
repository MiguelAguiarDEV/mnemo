package gateway

import (
	"log/slog"
	"regexp"
	"strings"
)

// ConvertMarkdown converts markdown text to the specified format.
func ConvertMarkdown(text string, format MessageFormat) string {
	slog.Debug("converting markdown", "format", format, "text_len", len(text))

	switch format {
	case FormatMarkdown:
		slog.Debug("markdown passthrough")
		return text
	case FormatPlain:
		result := stripMarkdown(text)
		slog.Debug("stripped to plain text", "result_len", len(result))
		return result
	case FormatHTML:
		// HTML conversion not yet implemented — pass through for now.
		slog.Warn("HTML conversion not implemented, returning markdown")
		return text
	default:
		slog.Warn("unknown format, returning markdown", "format", format)
		return text
	}
}

// stripMarkdown removes common markdown formatting from text.
func stripMarkdown(text string) string {
	// Order matters — process multi-char patterns before single-char ones.

	// Remove fenced code blocks (``` ... ```) but keep content.
	text = reFencedCode.ReplaceAllString(text, "$1")

	// Remove inline code (` ... `).
	text = reInlineCode.ReplaceAllString(text, "$1")

	// Remove bold+italic (***text*** or ___text___).
	text = reBoldItalic.ReplaceAllString(text, "${1}${2}")

	// Remove bold (**text** or __text__).
	text = reBold.ReplaceAllString(text, "${1}${2}")

	// Remove italic (*text* or _text_) — careful not to match mid-word underscores.
	text = reItalicStar.ReplaceAllString(text, "$1")
	text = reItalicUnderscore.ReplaceAllString(text, "$1")

	// Remove strikethrough (~~text~~).
	text = reStrikethrough.ReplaceAllString(text, "$1")

	// Remove headers (# Header) — regex matches the "# " prefix only.
	text = reHeader.ReplaceAllString(text, "")

	// Remove image syntax ![alt](url) → alt (before links, so ! is consumed).
	text = reImage.ReplaceAllString(text, "$1")

	// Remove link syntax [text](url) → text.
	text = reLink.ReplaceAllString(text, "$1")

	// Remove blockquote markers (> ).
	text = reBlockquote.ReplaceAllString(text, "$1")

	// Remove horizontal rules (---, ***, ___).
	text = reHR.ReplaceAllString(text, "")

	return strings.TrimSpace(text)
}

// Pre-compiled regex patterns for markdown stripping.
var (
	reFencedCode        = regexp.MustCompile("(?s)```[a-zA-Z]*\n?(.*?)```")
	reInlineCode        = regexp.MustCompile("`([^`]+)`")
	reBoldItalic        = regexp.MustCompile(`\*{3}(.+?)\*{3}|_{3}(.+?)_{3}`)
	reBold              = regexp.MustCompile(`\*{2}(.+?)\*{2}|_{2}(.+?)_{2}`)
	reItalicStar        = regexp.MustCompile(`\*(.+?)\*`)
	reItalicUnderscore  = regexp.MustCompile(`(?:^|[ ])_(.+?)_(?:$|[ ])`)
	reStrikethrough     = regexp.MustCompile(`~~(.+?)~~`)
	reHeader            = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reLink              = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reImage             = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	reBlockquote        = regexp.MustCompile(`(?m)^>\s?(.*)`)
	reHR                = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
)
