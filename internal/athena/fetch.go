package athena

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ─── FetchURLTool ────────────────────────────────────────────────────────────

// FetchURLConfig holds configuration for the FetchURLTool.
type FetchURLConfig struct {
	AllowPrivateIPs bool // If true, allow fetching from private IP ranges
}

// FetchURLTool makes HTTP requests with SSRF protection.
type FetchURLTool struct {
	allowPrivateIPs bool
	client          *http.Client
}

// NewFetchURLTool creates a FetchURLTool with the given config.
func NewFetchURLTool(cfg FetchURLConfig) *FetchURLTool {
	slog.Debug("fetch_url: tool created", "allow_private_ips", cfg.AllowPrivateIPs)
	return &FetchURLTool{
		allowPrivateIPs: cfg.AllowPrivateIPs,
		client:          &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *FetchURLTool) Name() string        { return "fetch_url" }
func (t *FetchURLTool) Description() string { return "Make HTTP requests. Default GET. Timeout 30s. Max response 1MB. Blocks private IPs by default." }
func (t *FetchURLTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"URL to fetch"},"method":{"type":"string","description":"HTTP method (default: GET)"},"headers":{"type":"object","description":"HTTP headers as key-value pairs","additionalProperties":{"type":"string"}},"body":{"type":"string","description":"Request body"}},"required":["url"]}`)
}

func (t *FetchURLTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	var p struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		slog.Error("fetch_url: invalid params", "err", err)
		return ToolResult{Content: "invalid parameters: " + err.Error(), IsError: true}, nil
	}
	if p.URL == "" {
		slog.Warn("fetch_url: missing url")
		return ToolResult{Content: "missing required parameter: url", IsError: true}, nil
	}
	if p.Method == "" {
		p.Method = "GET"
	}
	p.Method = strings.ToUpper(p.Method)

	// Validate URL.
	parsedURL, err := url.Parse(p.URL)
	if err != nil {
		slog.Error("fetch_url: invalid url", "url", p.URL, "err", err)
		return ToolResult{Content: "invalid URL: " + err.Error(), IsError: true}, nil
	}

	// SSRF protection: block private IPs.
	if !t.allowPrivateIPs {
		if err := t.checkPrivateIP(parsedURL.Hostname()); err != nil {
			slog.Warn("fetch_url: private IP blocked", "url", p.URL, "err", err)
			return ToolResult{Content: err.Error(), IsError: true}, nil
		}
	}

	slog.Info("fetch_url: fetching", "url", p.URL, "method", p.Method)

	// Build request.
	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = strings.NewReader(p.Body)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, p.Method, p.URL, bodyReader)
	if err != nil {
		slog.Error("fetch_url: failed to create request", "err", err)
		return ToolResult{Content: "failed to create request: " + err.Error(), IsError: true}, nil
	}

	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		slog.Error("fetch_url: request failed", "url", p.URL, "err", err)
		return ToolResult{Content: "request failed: " + err.Error(), IsError: true}, nil
	}
	defer resp.Body.Close()

	// Read body with 1MB limit.
	const maxBody = 1 << 20 // 1MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		slog.Error("fetch_url: failed to read response", "err", err)
		return ToolResult{Content: "failed to read response: " + err.Error(), IsError: true}, nil
	}

	truncated := false
	if len(body) > maxBody {
		body = body[:maxBody]
		truncated = true
		slog.Warn("fetch_url: response truncated to 1MB", "url", p.URL)
	}

	slog.Info("fetch_url: success", "url", p.URL, "method", p.Method, "status", resp.StatusCode, "body_bytes", len(body))

	result := map[string]interface{}{
		"status":      resp.StatusCode,
		"status_text": resp.Status,
		"body":        string(body),
		"truncated":   truncated,
	}
	data, _ := json.Marshal(result)
	return ToolResult{Content: string(data)}, nil
}

// checkPrivateIP resolves the hostname and checks if it points to a private IP.
func (t *FetchURLTool) checkPrivateIP(hostname string) error {
	// Check common private hostnames.
	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "127.0.0.1" || lower == "::1" || lower == "0.0.0.0" {
		return fmt.Errorf("access denied: private IP address %q", hostname)
	}

	// Resolve the hostname.
	ips, err := net.LookupIP(hostname)
	if err != nil {
		// If we can't resolve, we can't verify -- allow it.
		slog.Debug("fetch_url: DNS resolution failed, allowing", "hostname", hostname, "err", err)
		return nil
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("access denied: %q resolves to private IP %s", hostname, ip.String())
		}
	}

	return nil
}

// ─── Compile-time interface checks ───────────────────────────────────────────

var _ Tool = (*FetchURLTool)(nil)
