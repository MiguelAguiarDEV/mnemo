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
// Discord supports headers (#, ##, ###), bold, italic, code, code blocks,
// blockquotes, and lists natively. Tables are converted to aligned code blocks.
// Markdown links are flattened since Discord messages do not render them.
func convertToDiscord(text string) string {
	// Convert markdown tables to aligned code blocks (must run before link
	// stripping so cell content is preserved as-is).
	text = convertTablesToCodeBlocks(text)

	// Flatten markdown links: [text](url) → text (url).
	// Skip if inside a fenced code block — handled per-line below.
	text = stripLinksOutsideCode(text)

	return strings.TrimSpace(text)
}

// stripLinksOutsideCode replaces [text](url) with "text (url)" outside fenced
// code blocks. Inline code (backticks) is left alone via the regex itself only
// matching balanced bracket+paren forms.
func stripLinksOutsideCode(text string) string {
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = reLinkCapture.ReplaceAllString(line, "$1 ($2)")
	}
	return strings.Join(lines, "\n")
}

// convertTablesToCodeBlocks finds markdown tables and rewrites them as
// fixed-width aligned plain-text tables wrapped in a code fence. Header
// separator rows (| --- | --- |) are replaced with a single horizontal rule.
func convertTablesToCodeBlocks(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	var tableBlock []string
	inFence := false

	flush := func() {
		if len(tableBlock) == 0 {
			return
		}
		rendered := renderTable(tableBlock)
		result = append(result, "```")
		result = append(result, rendered...)
		result = append(result, "```")
		tableBlock = nil
	}

	for _, line := range lines {
		// Track fenced code blocks so we don't accidentally treat code as table.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flush()
			inFence = !inFence
			result = append(result, line)
			continue
		}
		if inFence {
			result = append(result, line)
			continue
		}

		trimmed := strings.TrimSpace(line)
		isTableLine := strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && len(trimmed) >= 2

		if isTableLine {
			tableBlock = append(tableBlock, trimmed)
			continue
		}
		flush()
		result = append(result, line)
	}
	flush()

	return strings.Join(result, "\n")
}

// renderTable converts raw markdown table rows ("| a | b |") into a
// fixed-width plain-text table with column alignment based on the longest
// cell per column. The separator row ("| --- | --- |") becomes a horizontal
// rule of box-drawing characters.
func renderTable(rows []string) []string {
	if len(rows) == 0 {
		return nil
	}

	// Parse cells from each row.
	parsed := make([][]string, 0, len(rows))
	sepIndex := -1
	for i, row := range rows {
		cells := splitTableRow(row)
		// Strip backticks around cell content for cleaner alignment.
		for j, c := range cells {
			cells[j] = strings.TrimSpace(strings.Trim(c, "`"))
		}
		if sepIndex == -1 && isSeparatorRow(cells) {
			sepIndex = i
		}
		parsed = append(parsed, cells)
	}

	// Compute column widths (skip separator row from width calculation).
	colCount := 0
	for _, p := range parsed {
		if len(p) > colCount {
			colCount = len(p)
		}
	}
	widths := make([]int, colCount)
	for i, p := range parsed {
		if i == sepIndex {
			continue
		}
		for j, cell := range p {
			if l := len([]rune(cell)); l > widths[j] {
				widths[j] = l
			}
		}
	}

	// Render rows.
	out := make([]string, 0, len(parsed))
	for i, p := range parsed {
		if i == sepIndex {
			total := 0
			for j, w := range widths {
				total += w
				if j < len(widths)-1 {
					total += 2 // gap between columns
				}
			}
			out = append(out, strings.Repeat("─", total))
			continue
		}
		var sb strings.Builder
		for j := 0; j < colCount; j++ {
			cell := ""
			if j < len(p) {
				cell = p[j]
			}
			pad := widths[j] - len([]rune(cell))
			if pad < 0 {
				pad = 0
			}
			sb.WriteString(cell)
			if j < colCount-1 {
				sb.WriteString(strings.Repeat(" ", pad))
				sb.WriteString("  ")
			}
		}
		out = append(out, strings.TrimRight(sb.String(), " "))
	}
	return out
}

// splitTableRow splits a markdown table row "| a | b | c |" into ["a","b","c"].
func splitTableRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// isSeparatorRow returns true if every cell looks like ---, :---, ---:, :---:.
func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" {
			return false
		}
		for _, r := range c {
			if r != '-' {
				return false
			}
		}
	}
	return true
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
	reLinkCapture       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reImage             = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	reBlockquote        = regexp.MustCompile(`(?m)^>\s?(.*)`)
	reHR                = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
)
