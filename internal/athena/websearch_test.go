package athena

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const ddgHTMLResults = `<!DOCTYPE html>
<html>
<body>
<div class="serp__results">
  <div class="result results_links results_links_deep web-result">
    <div class="links_main links_deep result__body">
      <h2 class="result__title">
        <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage1&amp;rut=abc">
          First Result Title
        </a>
      </h2>
      <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage1&amp;rut=abc">
        This is the first result snippet with some <b>bold</b> text.
      </a>
    </div>
  </div>
  <div class="result results_links results_links_deep web-result">
    <div class="links_main links_deep result__body">
      <h2 class="result__title">
        <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage2&amp;rut=def">
          Second Result Title
        </a>
      </h2>
      <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage2&amp;rut=def">
        Second snippet here.
      </a>
    </div>
  </div>
  <div class="result results_links results_links_deep web-result">
    <div class="links_main links_deep result__body">
      <h2 class="result__title">
        <a rel="nofollow" class="result__a" href="https://example.com/page3">
          Third Result Title
        </a>
      </h2>
      <a class="result__snippet" href="https://example.com/page3">
        Third snippet &amp; entities &lt;here&gt;.
      </a>
    </div>
  </div>
</div>
</body>
</html>`

const ddgHTMLNoResults = `<!DOCTYPE html>
<html>
<body>
<div class="serp__results">
  <div class="no-results">No results found for your search.</div>
</div>
</body>
</html>`

const ddgHTMLMalformed = `<!DOCTYPE html>
<html>
<body>
<div class="serp__results">
  <div class="result results_links results_links_deep web-result">
    <div class="links_main">
      <h2 class="result__title">
        <a class="result__a" href="https://example.com/ok">Valid Title</a>
      </h2>
      <a class="result__snippet">Valid snippet.</a>
    </div>
  </div>
  <div class="result results_links results_links_deep web-result">
    <div class="links_main">
      <!-- Missing link entirely -->
      <a class="result__snippet">Orphan snippet.</a>
    </div>
  </div>
</div>
<p>unclosed tags everywhere
</body>
</html>`

func TestWebSearchTool_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("expected query parameter 'q'")
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(ddgHTMLResults))
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL

	params, _ := json.Marshal(map[string]interface{}{"query": "test search", "max_results": 2})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var results []WebSearchResult
	if err := json.Unmarshal([]byte(result.Content), &results); err != nil {
		t.Fatalf("failed to parse results: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First result.
	if results[0].Title != "First Result Title" {
		t.Errorf("expected title 'First Result Title', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/page1" {
		t.Errorf("expected URL 'https://example.com/page1', got %q", results[0].URL)
	}
	if !strings.Contains(results[0].Snippet, "first result snippet") {
		t.Errorf("expected snippet containing 'first result snippet', got %q", results[0].Snippet)
	}
	// Bold tags should be stripped.
	if strings.Contains(results[0].Snippet, "<b>") {
		t.Errorf("snippet should not contain HTML tags: %q", results[0].Snippet)
	}

	// Second result.
	if results[1].URL != "https://example.com/page2" {
		t.Errorf("expected URL 'https://example.com/page2', got %q", results[1].URL)
	}
}

func TestWebSearchTool_DefaultMaxResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgHTMLResults))
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL

	// No max_results specified -- should use default of 5.
	params, _ := json.Marshal(map[string]interface{}{"query": "test"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var results []WebSearchResult
	if err := json.Unmarshal([]byte(result.Content), &results); err != nil {
		t.Fatalf("failed to parse results: %v", err)
	}

	// HTML has 3 results, default max is 5, so we get all 3.
	if len(results) != 3 {
		t.Errorf("expected 3 results (all available), got %d", len(results))
	}
}

func TestWebSearchTool_NoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgHTMLNoResults))
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL

	params, _ := json.Marshal(map[string]interface{}{"query": "xyznonexistent123"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var results []WebSearchResult
	if err := json.Unmarshal([]byte(result.Content), &results); err != nil {
		t.Fatalf("failed to parse results: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestWebSearchTool_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until context is cancelled.
		<-r.Context().Done()
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL
	tool.client = &http.Client{Timeout: 100 * time.Millisecond} // fast timeout for test

	params, _ := json.Marshal(map[string]interface{}{"query": "timeout test"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for timeout")
	}
	if !strings.Contains(result.Content, "request failed") {
		t.Errorf("expected 'request failed' in error, got: %s", result.Content)
	}
}

func TestWebSearchTool_MalformedHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgHTMLMalformed))
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL

	params, _ := json.Marshal(map[string]interface{}{"query": "malformed"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var results []WebSearchResult
	if err := json.Unmarshal([]byte(result.Content), &results); err != nil {
		t.Fatalf("failed to parse results: %v", err)
	}

	// Should still extract at least the valid result.
	if len(results) < 1 {
		t.Error("expected at least 1 result from malformed HTML")
	}
	if len(results) > 0 && results[0].Title != "Valid Title" {
		t.Errorf("expected 'Valid Title', got %q", results[0].Title)
	}
}

func TestWebSearchTool_MissingQuery(t *testing.T) {
	tool := NewWebSearchTool()

	params, _ := json.Marshal(map[string]interface{}{})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing query")
	}
	if !strings.Contains(result.Content, "missing required parameter: query") {
		t.Errorf("expected 'missing required parameter: query', got: %s", result.Content)
	}
}

func TestWebSearchTool_InvalidParams(t *testing.T) {
	tool := NewWebSearchTool()

	result, err := tool.Execute(context.Background(), []byte(`{invalid json`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid JSON")
	}
	if !strings.Contains(result.Content, "invalid parameters") {
		t.Errorf("expected 'invalid parameters', got: %s", result.Content)
	}
}

func TestWebSearchTool_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Service Unavailable"))
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL

	params, _ := json.Marshal(map[string]interface{}{"query": "test"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for non-200 status")
	}
	if !strings.Contains(result.Content, "503") {
		t.Errorf("expected status 503 in error, got: %s", result.Content)
	}
}

func TestWebSearchTool_HTMLEntityDecoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgHTMLResults))
	}))
	defer server.Close()

	tool := NewWebSearchTool()
	tool.baseURL = server.URL

	params, _ := json.Marshal(map[string]interface{}{"query": "entities", "max_results": 5})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []WebSearchResult
	json.Unmarshal([]byte(result.Content), &results)

	// Third result has &amp; and &lt; entities.
	if len(results) >= 3 {
		if strings.Contains(results[2].Snippet, "&amp;") {
			t.Errorf("snippet should decode &amp; to &: %q", results[2].Snippet)
		}
		if strings.Contains(results[2].Snippet, "&lt;") {
			t.Errorf("snippet should decode &lt; to <: %q", results[2].Snippet)
		}
		if !strings.Contains(results[2].Snippet, "entities") {
			t.Errorf("expected snippet containing 'entities', got %q", results[2].Snippet)
		}
	}
}

func TestWebSearchTool_Interface(t *testing.T) {
	tool := NewWebSearchTool()
	if tool.Name() != "web_search" {
		t.Errorf("expected name 'web_search', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Errorf("invalid JSON schema: %v", err)
	}
	// Verify required fields.
	required, ok := schema["required"].([]interface{})
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Errorf("expected required: [query], got %v", schema["required"])
	}
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"uddg redirect", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com&rut=abc", "https://example.com"},
		{"protocol relative", "//example.com/page", "https://example.com/page"},
		{"absolute", "https://example.com/page", "https://example.com/page"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveURL(tt.in)
			if got != tt.want {
				t.Errorf("resolveURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain text", "plain text"},
		{"<b>bold</b> text", "bold text"},
		{"a &amp; b &lt;c&gt;", "a & b <c>"},
		{"  spaces  ", "spaces"},
		{"<a href=\"x\">link</a>", "link"},
	}
	for _, tt := range tests {
		got := stripHTML(tt.in)
		if got != tt.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
