package gateway

import (
	"log/slog"
	"regexp"
	"strings"
)

// DiscordMaxMessageLen is the maximum character length for a single Discord message.
const DiscordMaxMessageLen = 2000

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
	case FormatDiscord:
		result := convertToDiscord(text)
		slog.Debug("converted to discord format", "result_len", len(result))
		return result
	default:
		slog.Warn("unknown format, returning markdown", "format", format)
		return text
	}
}

// convertToDiscord transforms standard markdown into Discord-compatible markdown.
// Discord supports bold, italic, code, code blocks, and blockquotes but NOT
// headers or tables in messages.
func convertToDiscord(text string) string {
	// Convert headers to bold: ## Title → **Title**
	text = reHeader.ReplaceAllStringFunc(text, func(match string) string {
		return "**"
	})
	// Close bold at end of header line.
	text = reDiscordHeaderLine.ReplaceAllString(text, "**${1}**")

	// Convert markdown tables to code blocks.
	text = convertTablesToCodeBlocks(text)

	return strings.TrimSpace(text)
}

// reDiscordHeaderLine matches a line that starts with ** (from header conversion)
// followed by text until end of line, to wrap it fully in bold.
var reDiscordHeaderLine = regexp.MustCompile(`(?m)^\*\*(.+)$`)

// convertTablesToCodeBlocks wraps markdown table blocks in code fences.
func convertTablesToCodeBlocks(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	inTable := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isTableLine := strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")

		if isTableLine && !inTable {
			inTable = true
			result = append(result, "```")
		} else if !isTableLine && inTable {
			inTable = false
			result = append(result, "```")
		}
		result = append(result, line)
	}
	if inTable {
		result = append(result, "```")
	}

	return strings.Join(result, "\n")
}

// SplitMessage splits text into chunks that fit within maxLen characters.
// It prefers splitting at paragraph boundaries (\n\n), then line boundaries (\n),
// then word boundaries (space). Code blocks are preserved — if a split occurs
// inside a fenced code block, the block is closed and reopened.
func SplitMessage(text string, maxLen int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= maxLen {
		return []string{text}
	}

	if maxLen <= 0 {
		slog.Warn("SplitMessage called with non-positive maxLen", "maxLen", maxLen)
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		cut := chooseSplitPoint(remaining, maxLen)
		chunk := strings.TrimSpace(remaining[:cut])
		remaining = strings.TrimSpace(remaining[cut:])

		// If we split inside a code fence, close it and reopen in the next chunk.
		if strings.Count(chunk, "```")%2 != 0 {
			chunk += "\n```"
			remaining = "```\n" + remaining
		}

		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}

	if len(chunks) > 1 {
		slog.Warn("message split into multiple parts",
			"parts", len(chunks),
			"original_len", len(text),
			"max_len", maxLen,
		)
	}

	return chunks
}

// chooseSplitPoint finds the best position to split text at, preferring
// paragraph breaks, then line breaks, then word breaks.
func chooseSplitPoint(text string, maxLen int) int {
	if len(text) <= maxLen {
		return len(text)
	}
	window := text[:maxLen]
	for _, sep := range []string{"\n\n", "\n", " "} {
		if idx := strings.LastIndex(window, sep); idx > maxLen/2 {
			return idx + len(sep)
		}
	}
	return maxLen
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
