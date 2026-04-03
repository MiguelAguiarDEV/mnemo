package remote

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	engramsync "github.com/Gentleman-Programming/engram/internal/sync"
)

func TestNewRemoteTransportRejectsInvalidURL(t *testing.T) {
	_, err := NewRemoteTransport("not-a-url", "tok")
	if err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestNewRemoteTransportAcceptsHTTPAndHTTPS(t *testing.T) {
	tests := []string{
		"http://localhost:8080",
		"https://engram.example.com/base",
	}

	for _, baseURL := range tests {
		t.Run(baseURL, func(t *testing.T) {
			rt, err := NewRemoteTransport(baseURL, "tok")
			if err != nil {
				t.Fatalf("NewRemoteTransport(%q): %v", baseURL, err)
			}
			if rt == nil {
				t.Fatal("expected transport")
			}
		})
	}
}

// ─── ReadManifest Tests ─────────────────────────────────────────────────────

func TestReadManifestSuccess(t *testing.T) {
	manifest := map[string]any{
		"version": 1,
		"chunks": []map[string]any{
			{"id": "aabb1122", "created_by": "alice", "created_at": "2025-01-01T00:00:00Z", "sessions": 1, "memories": 2, "prompts": 3},
			{"id": "ccdd3344", "created_by": "bob", "created_at": "2025-01-02T00:00:00Z", "sessions": 2, "memories": 4, "prompts": 1},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sync/pull" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth header: got %q want %q", got, "Bearer test-token")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(manifest)
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	m, err := rt.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Version != 1 {
		t.Fatalf("version: got %d want 1", m.Version)
	}
	if len(m.Chunks) != 2 {
		t.Fatalf("chunks: got %d want 2", len(m.Chunks))
	}
	if m.Chunks[0].ID != "aabb1122" || m.Chunks[0].CreatedBy != "alice" {
		t.Fatalf("first chunk mismatch: %+v", m.Chunks[0])
	}
	if m.Chunks[1].Memories != 4 {
		t.Fatalf("second chunk memories: got %d want 4", m.Chunks[1].Memories)
	}
}

func TestReadManifestEmptyChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"chunks":[]}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	m, err := rt.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(m.Chunks) != 0 {
		t.Fatalf("expected empty chunks, got %d", len(m.Chunks))
	}
}

func TestReadManifestAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "bad-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should mention 401: %v", err)
	}
}

func TestReadManifestInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ─── WriteChunk Tests ────────────────────────────────────────────────────────

func TestWriteChunkSuccess(t *testing.T) {
	var receivedBody []byte
	var receivedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/sync/push" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"accepted","chunk_id":"aabb1122"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "my-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}

	chunkJSON := `{"sessions":[{"id":"s1","project":"proj","directory":"/tmp","started_at":"2025-01-01 00:00:00"}],"observations":[],"prompts":[]}`
	entry := engramsync.ChunkEntry{ID: "aabb1122", CreatedBy: "alice"}

	err = rt.WriteChunk("aabb1122", []byte(chunkJSON), entry)
	if err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	// Verify auth header.
	if receivedAuth != "Bearer my-token" {
		t.Fatalf("auth: got %q want %q", receivedAuth, "Bearer my-token")
	}

	// Verify the push request body structure.
	var req pushRequest
	if err := json.Unmarshal(receivedBody, &req); err != nil {
		t.Fatalf("unmarshal push body: %v", err)
	}
	if req.ChunkID != "aabb1122" {
		t.Fatalf("chunk_id: got %q", req.ChunkID)
	}
	if req.CreatedBy != "alice" {
		t.Fatalf("created_by: got %q", req.CreatedBy)
	}
	if len(req.Data.Sessions) != 1 {
		t.Fatalf("sessions: got %d want 1", len(req.Data.Sessions))
	}
	if req.Data.Sessions[0].ID != "s1" {
		t.Fatalf("session id: got %q", req.Data.Sessions[0].ID)
	}
}

func TestWriteChunkServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"chunk_id must be 8 hex characters"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	entry := engramsync.ChunkEntry{ID: "bad", CreatedBy: "alice"}
	err = rt.WriteChunk("bad", []byte(`{"sessions":[],"observations":[],"prompts":[]}`), entry)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error should mention 400: %v", err)
	}
}

func TestWriteChunkInvalidData(t *testing.T) {
	rt, err := NewRemoteTransport("http://example.com", "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	entry := engramsync.ChunkEntry{ID: "aabb1122", CreatedBy: "alice"}
	err = rt.WriteChunk("aabb1122", []byte("not json"), entry)
	if err == nil {
		t.Fatal("expected error for invalid chunk JSON")
	}
}

// ─── ReadChunk Tests ─────────────────────────────────────────────────────────

func TestReadChunkSuccess(t *testing.T) {
	pushBody := pushRequest{
		ChunkID:   "aabb1122",
		CreatedBy: "alice",
		Data: pushData{
			Sessions: []pushSession{
				{ID: "s1", Project: "proj", Directory: "/tmp", StartedAt: "2025-01-01 00:00:00"},
			},
			Observations: []pushObservation{
				{SessionID: "s1", Type: "decision", Title: "test obs", Content: "content here", Scope: "project"},
			},
			Prompts: []pushPrompt{
				{SessionID: "s1", Content: "hello prompt", Project: "proj"},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sync/pull/aabb1122" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pushBody)
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	data, err := rt.ReadChunk("aabb1122")
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}

	var chunk chunkData
	if err := json.Unmarshal(data, &chunk); err != nil {
		t.Fatalf("unmarshal chunk: %v", err)
	}
	if len(chunk.Sessions) != 1 || chunk.Sessions[0].ID != "s1" {
		t.Fatalf("sessions: %+v", chunk.Sessions)
	}
	if len(chunk.Observations) != 1 || chunk.Observations[0].Title != "test obs" {
		t.Fatalf("observations: %+v", chunk.Observations)
	}
	if len(chunk.Prompts) != 1 || chunk.Prompts[0].Content != "hello prompt" {
		t.Fatalf("prompts: %+v", chunk.Prompts)
	}
}

func TestReadChunkNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"chunk not found"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	_, err = rt.ReadChunk("missing1")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error should mention 404: %v", err)
	}
}

func TestReadChunkInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	_, err = rt.ReadChunk("aabb1122")
	if err == nil {
		t.Fatal("expected error for invalid response JSON")
	}
}

// ─── WriteManifest Tests ─────────────────────────────────────────────────────

func TestWriteManifestIsNoOp(t *testing.T) {
	rt, err := NewRemoteTransport("http://example.com", "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	if err := rt.WriteManifest(nil); err != nil {
		t.Fatalf("WriteManifest should be no-op: %v", err)
	}
}

// ─── Retry Logic Tests ──────────────────────────────────────────────────────

func TestRetryOn500ThenSuccess(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"internal"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"chunks":[]}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	m, err := rt.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest after retries: %v", err)
	}
	if m.Version != 1 {
		t.Fatalf("version: got %d", m.Version)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestRetryOn429(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"chunks":[]}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	m, err := rt.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Version != 1 {
		t.Fatalf("version: got %d", m.Version)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestNoRetryOn401(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "bad-tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("expected 1 attempt for 401, got %d", got)
	}
}

func TestNoRetryOn403(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("expected 1 attempt for 403, got %d", got)
	}
}

func TestNoRetryOn404(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	_, err = rt.ReadChunk("missing1")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("expected 1 attempt for 404, got %d", got)
	}
}

func TestRetryExhaustsAllAttempts(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"service unavailable"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if !strings.Contains(err.Error(), "retries") {
		t.Fatalf("error should mention retries: %v", err)
	}
	// 1 initial + 3 retries = 4 total attempts.
	if got := atomic.LoadInt32(&attempts); got != 4 {
		t.Fatalf("expected 4 attempts, got %d", got)
	}
}

func TestNetworkErrorReturnsDescriptive(t *testing.T) {
	rt, err := NewRemoteTransport("http://127.0.0.1:1", "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 1 * time.Second}

	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "cloud:") {
		t.Fatalf("error should be descriptive with 'cloud:' prefix: %v", err)
	}
}

func TestRetryPreservesPostBody(t *testing.T) {
	var attempts int32
	var lastBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		body, _ := io.ReadAll(r.Body)
		lastBody = body
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.httpClient = &http.Client{Timeout: 5 * time.Second}

	chunkJSON := `{"sessions":[],"observations":[],"prompts":[]}`
	entry := engramsync.ChunkEntry{ID: "aabb1122", CreatedBy: "alice"}
	err = rt.WriteChunk("aabb1122", []byte(chunkJSON), entry)
	if err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	// Verify the body was resent on retry.
	if len(lastBody) == 0 {
		t.Fatal("expected non-empty body on retry")
	}
	var req pushRequest
	if err := json.Unmarshal(lastBody, &req); err != nil {
		t.Fatalf("unmarshal retry body: %v", err)
	}
	if req.ChunkID != "aabb1122" {
		t.Fatalf("retry body chunk_id: got %q", req.ChunkID)
	}
}

// ─── Data Conversion Tests ──────────────────────────────────────────────────

func TestConvertChunkToPushData(t *testing.T) {
	toolName := "myTool"
	project := "proj"
	topicKey := "key"

	c := &chunkData{
		Sessions: []sessionData{{ID: "s1", Project: "p", Directory: "/d", StartedAt: "2025-01-01"}},
		Observations: []observationData{{
			SessionID: "s1",
			Type:      "decision",
			Title:     "test",
			Content:   "data",
			ToolName:  &toolName,
			Project:   &project,
			Scope:     "project",
			TopicKey:  &topicKey,
		}},
		Prompts: []promptData{{SessionID: "s1", Content: "hello", Project: "p"}},
	}

	pd := convertChunkToPushData(c)
	if len(pd.Sessions) != 1 || pd.Sessions[0].ID != "s1" {
		t.Fatalf("sessions conversion failed: %+v", pd.Sessions)
	}
	if len(pd.Observations) != 1 || pd.Observations[0].ToolName != "myTool" {
		t.Fatalf("observations conversion failed: %+v", pd.Observations)
	}
	if pd.Observations[0].Project != "proj" {
		t.Fatalf("observation project: got %q", pd.Observations[0].Project)
	}
	if pd.Observations[0].TopicKey != "key" {
		t.Fatalf("observation topic_key: got %q", pd.Observations[0].TopicKey)
	}
	if len(pd.Prompts) != 1 || pd.Prompts[0].Content != "hello" {
		t.Fatalf("prompts conversion failed: %+v", pd.Prompts)
	}
}

func TestConvertPushDataToChunk(t *testing.T) {
	pd := &pushData{
		Sessions: []pushSession{{ID: "s1", Project: "p", Directory: "/d", StartedAt: "2025-01-01"}},
		Observations: []pushObservation{{
			SessionID: "s1",
			Type:      "decision",
			Title:     "test",
			Content:   "data",
			ToolName:  "myTool",
			Project:   "proj",
			Scope:     "project",
			TopicKey:  "key",
		}},
		Prompts: []pushPrompt{{SessionID: "s1", Content: "hello", Project: "p"}},
	}

	c := convertPushDataToChunk(pd)
	if len(c.Sessions) != 1 || c.Sessions[0].ID != "s1" {
		t.Fatalf("sessions conversion failed: %+v", c.Sessions)
	}
	if len(c.Observations) != 1 {
		t.Fatalf("observations length: got %d", len(c.Observations))
	}
	if c.Observations[0].ToolName == nil || *c.Observations[0].ToolName != "myTool" {
		t.Fatalf("observation tool_name: %v", c.Observations[0].ToolName)
	}
	if c.Observations[0].Project == nil || *c.Observations[0].Project != "proj" {
		t.Fatalf("observation project: %v", c.Observations[0].Project)
	}
	if c.Observations[0].TopicKey == nil || *c.Observations[0].TopicKey != "key" {
		t.Fatalf("observation topic_key: %v", c.Observations[0].TopicKey)
	}
	if len(c.Prompts) != 1 || c.Prompts[0].Content != "hello" {
		t.Fatalf("prompts conversion failed: %+v", c.Prompts)
	}
}

func TestConvertRoundtrip(t *testing.T) {
	toolName := "tool"
	project := "proj"

	original := &chunkData{
		Sessions: []sessionData{{ID: "s1", Project: "p", Directory: "/d", StartedAt: "2025-01-01"}},
		Observations: []observationData{{
			SessionID: "s1", Type: "bug", Title: "fix", Content: "fixed it",
			ToolName: &toolName, Project: &project, Scope: "project",
		}},
		Prompts: []promptData{{SessionID: "s1", Content: "query", Project: "p"}},
	}

	pd := convertChunkToPushData(original)
	result := convertPushDataToChunk(&pd)

	if len(result.Sessions) != 1 || result.Sessions[0].ID != "s1" {
		t.Fatalf("roundtrip session: %+v", result.Sessions)
	}
	if len(result.Observations) != 1 || result.Observations[0].Title != "fix" {
		t.Fatalf("roundtrip obs: %+v", result.Observations)
	}
	if result.Observations[0].ToolName == nil || *result.Observations[0].ToolName != "tool" {
		t.Fatal("roundtrip tool_name lost")
	}
	if len(result.Prompts) != 1 || result.Prompts[0].Content != "query" {
		t.Fatalf("roundtrip prompt: %+v", result.Prompts)
	}
}

func TestConvertEmptyFields(t *testing.T) {
	c := &chunkData{
		Observations: []observationData{{
			SessionID: "s1", Type: "note", Title: "t", Content: "c", Scope: "global",
		}},
	}
	pd := convertChunkToPushData(c)
	if pd.Observations[0].ToolName != "" {
		t.Fatalf("expected empty tool_name, got %q", pd.Observations[0].ToolName)
	}

	pd2 := &pushData{
		Observations: []pushObservation{{
			SessionID: "s1", Type: "note", Title: "t", Content: "c", Scope: "global",
		}},
	}
	c2 := convertPushDataToChunk(pd2)
	if c2.Observations[0].ToolName != nil {
		t.Fatalf("expected nil tool_name, got %v", c2.Observations[0].ToolName)
	}
}

// ─── Constructor Test ────────────────────────────────────────────────────────

func TestNewRemoteTransport(t *testing.T) {
	rt, err := NewRemoteTransport("https://engram.example.com/", "my-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	if rt.baseURL != "https://engram.example.com" {
		t.Fatalf("baseURL should have trailing slash trimmed: got %q", rt.baseURL)
	}
	if rt.token != "my-token" {
		t.Fatalf("token: got %q", rt.token)
	}
	if rt.httpClient == nil {
		t.Fatal("httpClient should be initialized")
	}
	if rt.httpClient.Timeout != 30*time.Second {
		t.Fatalf("timeout: got %v", rt.httpClient.Timeout)
	}
}

// ─── Mutation Push/Pull Tests ───────────────────────────────────────────────

func TestPushMutationsSuccess(t *testing.T) {
	var receivedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/sync/mutations/push" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth: got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accepted":2,"last_seq":42}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}

	mutations := []MutationEntry{
		{Entity: "session", EntityKey: "s1", Op: "upsert", Payload: json.RawMessage(`{"id":"s1","project":"engram","directory":"/work"}`)},
		{Entity: "observation", EntityKey: "obs-abc", Op: "upsert", Payload: json.RawMessage(`{"sync_id":"obs-abc","session_id":"s1","type":"decision","title":"test","content":"data","scope":"project"}`)},
	}

	result, err := rt.PushMutations(mutations)
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	if result.Accepted != 2 {
		t.Fatalf("accepted: got %d want 2", result.Accepted)
	}
	if result.LastSeq != 42 {
		t.Fatalf("last_seq: got %d want 42", result.LastSeq)
	}

	// Verify the body was sent correctly.
	var req map[string]any
	if err := json.Unmarshal(receivedBody, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	muts, ok := req["mutations"].([]any)
	if !ok || len(muts) != 2 {
		t.Fatalf("expected 2 mutations in body, got %v", req)
	}
}

func TestPushMutationsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid json"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	_, err = rt.PushMutations([]MutationEntry{{Entity: "session", EntityKey: "s1", Op: "upsert", Payload: json.RawMessage(`{}`)}})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error should mention 400: %v", err)
	}
}

func TestPushMutationsRefreshesExpiredToken(t *testing.T) {
	var pushCalls int
	var refreshCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sync/mutations/push":
			pushCalls++
			if pushCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"expired token"}`))
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("refreshed auth = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"accepted":1,"last_seq":5}`))
		case r.Method == http.MethodPost && r.URL.Path == "/auth/refresh":
			refreshCalls++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"refreshed-token","expires_in":3600}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "expired-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	rt.SetTokenRefresher("refresh-token", nil)

	result, err := rt.PushMutations([]MutationEntry{
		{Entity: "session", EntityKey: "s1", Op: "upsert", Payload: json.RawMessage(`{"id":"s1","project":"p","directory":"/d"}`)},
	})
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("accepted: got %d", result.Accepted)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d", refreshCalls)
	}
	if pushCalls != 2 {
		t.Fatalf("push calls = %d", pushCalls)
	}
}

func TestPullMutationsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sync/mutations/pull" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth: got %q", got)
		}
		sinceSeq := r.URL.Query().Get("since_seq")
		if sinceSeq != "10" {
			t.Fatalf("since_seq: got %q want 10", sinceSeq)
		}
		limit := r.URL.Query().Get("limit")
		if limit != "50" {
			t.Fatalf("limit: got %q want 50", limit)
		}

		resp := map[string]any{
			"mutations": []map[string]any{
				{"seq": 11, "entity": "session", "entity_key": "s1", "op": "upsert", "payload": `{"id":"s1"}`, "occurred_at": "2025-01-01T00:00:00Z"},
				{"seq": 12, "entity": "observation", "entity_key": "obs-a", "op": "upsert", "payload": `{"sync_id":"obs-a"}`, "occurred_at": "2025-01-01T00:01:00Z"},
			},
			"has_more": false,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}

	result, err := rt.PullMutations(10, 50)
	if err != nil {
		t.Fatalf("PullMutations: %v", err)
	}
	if len(result.Mutations) != 2 {
		t.Fatalf("mutations: got %d want 2", len(result.Mutations))
	}
	if result.HasMore {
		t.Fatal("expected has_more=false")
	}
	if result.Mutations[0].Seq != 11 || result.Mutations[0].Entity != "session" {
		t.Fatalf("first mutation: %+v", result.Mutations[0])
	}
	if result.Mutations[1].Seq != 12 || result.Mutations[1].Entity != "observation" {
		t.Fatalf("second mutation: %+v", result.Mutations[1])
	}
}

func TestPullMutationsEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"mutations":[],"has_more":false}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}

	result, err := rt.PullMutations(0, 100)
	if err != nil {
		t.Fatalf("PullMutations: %v", err)
	}
	if len(result.Mutations) != 0 {
		t.Fatalf("expected empty mutations, got %d", len(result.Mutations))
	}
	if result.HasMore {
		t.Fatal("expected has_more=false")
	}
}

func TestPullMutationsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "bad-tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}
	_, err = rt.PullMutations(0, 100)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should mention 401: %v", err)
	}
}

func TestPullMutationsHasMorePagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"mutations": []map[string]any{
				{"seq": 1, "entity": "session", "entity_key": "s1", "op": "upsert", "payload": `{}`, "occurred_at": "2025-01-01T00:00:00Z"},
			},
			"has_more": true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	rt, err := NewRemoteTransport(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewRemoteTransport: %v", err)
	}

	result, err := rt.PullMutations(0, 1)
	if err != nil {
		t.Fatalf("PullMutations: %v", err)
	}
	if !result.HasMore {
		t.Fatal("expected has_more=true")
	}
	if len(result.Mutations) != 1 {
		t.Fatalf("mutations: got %d", len(result.Mutations))
	}
}
