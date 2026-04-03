package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ─── WebSearchTool ──────────────────────────────────────────────────────────

// WebSearchResult represents a single search result.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchTool performs web searches via DuckDuckGo HTML (no API key needed).
type WebSearchTool struct {
	client  *http.Client
	baseURL string // overridable for testing
}

// NewWebSearchTool creates a WebSearchTool with default settings.
func NewWebSearchTool() *WebSearchTool {
	slog.Debug("web_search: tool created")
	return &WebSearchTool{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://html.duckduckgo.com/html/",
	}
}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string { return "Search the web using DuckDuckGo. Returns titles, URLs, and snippets. No API key needed." }
func (t *WebSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"max_results":{"type":"integer","description":"Maximum number of results to return (default: 5)"}},"required":["query"]}`)
}

func (t *WebSearchTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		Query      string `json:"query"`
		MaxResults *int   `json:"max_results"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("web_search: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.Query == "" {
		slog.Warn("web_search: missing query")
		return ToolResult{Content: "missing required parameter: query", IsError: true}, nil
	}

	maxResults := 5
	if p.MaxResults != nil && *p.MaxResults > 0 {
		maxResults = *p.MaxResults
	}

	slog.Info("web_search: searching", "query", p.Query, "max_results", maxResults)

	// Build the request.
	reqURL := fmt.Sprintf("%s?q=%s", t.baseURL, url.QueryEscape(p.Query))

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", reqURL, nil)
	if err != nil {
		slog.Error("web_search: failed to create request", "err", err)
		return ToolResult{Content: "failed to create request: " + err.Error(), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; JARVIS/1.0)")

	resp, err := t.client.Do(req)
	if err != nil {
		slog.Error("web_search: request failed", "query", p.Query, "err", err)
		return ToolResult{Content: "search request failed: " + err.Error(), IsError: true}, nil
	}
	defer resp.Body.Close()

	// Read body with 2MB limit.
	const maxBody = 2 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		slog.Error("web_search: failed to read response", "err", err)
		return ToolResult{Content: "failed to read response: " + err.Error(), IsError: true}, nil
	}

	if resp.StatusCode != http.StatusOK {
		slog.Error("web_search: non-200 status", "status", resp.StatusCode)
		return ToolResult{Content: fmt.Sprintf("search returned status %d", resp.StatusCode), IsError: true}, nil
	}

	results := parseDuckDuckGoHTML(string(body), maxResults)

	slog.Debug("web_search: results count", "count", len(results))
	slog.Info("web_search: done", "query", p.Query, "results", len(results))

	data, _ := json.Marshal(results)
	return ToolResult{Content: string(data)}, nil
}

// parseDuckDuckGoHTML extracts search results from DuckDuckGo HTML response.
// It matches result__a links and result__snippet elements directly, which is
// robust against varying div nesting depths in DuckDuckGo's HTML.
func parseDuckDuckGoHTML(html string, maxResults int) []WebSearchResult {
	var results []WebSearchResult

	linkRe := regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`(?s)<[a-z]+[^>]*class="result__snippet"[^>]*>(.*?)</[a-z]+>`)

	links := linkRe.FindAllStringSubmatch(html, maxResults)
	snippets := snippetRe.FindAllStringSubmatch(html, maxResults)

	for i, link := range links {
		if len(results) >= maxResults {
			break
		}
		if len(link) < 3 {
			continue
		}
		r := WebSearchResult{
			URL:   resolveURL(link[1]),
			Title: stripHTML(link[2]),
		}
		if i < len(snippets) && len(snippets[i]) >= 2 {
			r.Snippet = stripHTML(snippets[i][1])
		}
		if r.URL != "" && r.Title != "" {
			results = append(results, r)
		}
	}

	return results
}

// resolveURL handles DuckDuckGo redirect URLs.
func resolveURL(rawURL string) string {
	// DuckDuckGo wraps URLs like //duckduckgo.com/l/?uddg=<encoded>&rut=...
	if strings.Contains(rawURL, "uddg=") {
		parsed, err := url.Parse(rawURL)
		if err == nil {
			if uddg := parsed.Query().Get("uddg"); uddg != "" {
				return uddg
			}
		}
	}
	// Handle protocol-relative URLs.
	if strings.HasPrefix(rawURL, "//") {
		return "https:" + rawURL
	}
	return rawURL
}

// stripHTML removes HTML tags and decodes common entities.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.TrimSpace(s)
	return s
}

// ─── Compile-time interface check ───────────────────────────────────────────

var _ Tool = (*WebSearchTool)(nil)
