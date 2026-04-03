package athena

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchURLTool(t *testing.T) {
	// Set up test server.
	handler := http.NewServeMux()
	handler.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	handler.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]string{
			"method": r.Method,
			"body":   string(body),
		}
		json.NewEncoder(w).Encode(resp)
	})
	handler.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		// Write 2MB of data.
		data := strings.Repeat("x", 2<<20)
		w.Write([]byte(data))
	})
	handler.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		// This handler does nothing -- the context deadline will cancel it.
		<-r.Context().Done()
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	tool := NewFetchURLTool(FetchURLConfig{AllowPrivateIPs: true}) // Allow private IPs for test server

	tests := []struct {
		name        string
		params      map[string]interface{}
		wantErr     bool
		wantContain string
		errContains string
	}{
		{
			name:        "GET success",
			params:      map[string]interface{}{"url": server.URL + "/ok"},
			wantContain: "success",
		},
		{
			name:        "POST with body",
			params:      map[string]interface{}{"url": server.URL + "/echo", "method": "POST", "body": "test data"},
			wantContain: "test data",
		},
		{
			name:        "GET with headers",
			params:      map[string]interface{}{"url": server.URL + "/ok", "headers": map[string]string{"X-Custom": "value"}},
			wantContain: "success",
		},
		{
			name:        "missing url",
			params:      map[string]interface{}{},
			wantErr:     true,
			errContains: "missing required parameter: url",
		},
		{
			name:        "invalid url",
			params:      map[string]interface{}{"url": "://invalid"},
			wantErr:     true,
			errContains: "invalid URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.params)
			result, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("unexpected Execute error: %v", err)
			}

			if tt.wantErr {
				if !result.IsError {
					t.Errorf("expected error result, got success: %s", result.Content)
				}
				if tt.errContains != "" && !strings.Contains(result.Content, tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, result.Content)
				}
			} else {
				if result.IsError {
					t.Errorf("unexpected error: %s", result.Content)
				}
				if tt.wantContain != "" && !strings.Contains(result.Content, tt.wantContain) {
					t.Errorf("expected content containing %q, got %q", tt.wantContain, result.Content)
				}
			}
		})
	}
}

func TestFetchURLTool_PrivateIPBlocked(t *testing.T) {
	tool := NewFetchURLTool(FetchURLConfig{AllowPrivateIPs: false})

	tests := []struct {
		name string
		url  string
	}{
		{"localhost", "http://localhost:8080/test"},
		{"127.0.0.1", "http://127.0.0.1:8080/test"},
		{"0.0.0.0", "http://0.0.0.0:8080/test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(map[string]interface{}{"url": tt.url})
			result, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Error("expected error for private IP")
			}
			if !strings.Contains(result.Content, "access denied") {
				t.Errorf("expected 'access denied', got: %s", result.Content)
			}
		})
	}
}

func TestFetchURLTool_MaxBodySize(t *testing.T) {
	// Create server that returns > 1MB.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := strings.Repeat("x", 2<<20) // 2MB
		w.Write([]byte(data))
	}))
	defer server.Close()

	tool := NewFetchURLTool(FetchURLConfig{AllowPrivateIPs: true})
	params, _ := json.Marshal(map[string]interface{}{"url": server.URL})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Should contain truncated flag.
	if !strings.Contains(result.Content, `"truncated":true`) {
		t.Errorf("expected truncated:true in response, got: %s", result.Content[:200])
	}
}

func TestFetchURLTool_Interface(t *testing.T) {
	tool := NewFetchURLTool(FetchURLConfig{})
	if tool.Name() != "fetch_url" {
		t.Errorf("expected name 'fetch_url', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Errorf("invalid JSON schema: %v", err)
	}
}
