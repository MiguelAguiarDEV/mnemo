package remote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	engramsync "github.com/Gentleman-Programming/engram/internal/sync"
)

func TestReadManifestRefreshesExpiredToken(t *testing.T) {
	var manifestCalls int
	var refreshCalls int
	var savedToken string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sync/pull":
			manifestCalls++
			if got := r.Header.Get("Authorization"); manifestCalls == 1 && got != "Bearer expired-token" {
				t.Fatalf("first auth header = %q", got)
			}
			if manifestCalls > 1 && r.Header.Get("Authorization") != "Bearer refreshed-token" {
				t.Fatalf("refreshed auth header = %q", r.Header.Get("Authorization"))
			}
			if manifestCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"invalid or expired token"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"version": 1, "chunks": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/auth/refresh":
			refreshCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode refresh body: %v", err)
			}
			if body["refresh_token"] != "refresh-token" {
				t.Fatalf("refresh token = %q", body["refresh_token"])
			}
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
	rt.SetTokenRefresher("refresh-token", func(token string) error {
		savedToken = token
		return nil
	})

	manifest, err := rt.ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if manifest.Version != 1 {
		t.Fatalf("manifest version = %d", manifest.Version)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d", refreshCalls)
	}
	if manifestCalls != 2 {
		t.Fatalf("manifest calls = %d", manifestCalls)
	}
	if savedToken != "refreshed-token" {
		t.Fatalf("saved token = %q", savedToken)
	}
}

func TestWriteChunkRefreshesExpiredToken(t *testing.T) {
	var pushCalls int
	var refreshCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sync/push":
			pushCalls++
			if pushCalls == 1 {
				if got := r.Header.Get("Authorization"); got != "Bearer expired-token" {
					t.Fatalf("first push auth = %q", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"invalid or expired token"}`))
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
				t.Fatalf("refreshed push auth = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"accepted"}`))
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

	err = rt.WriteChunk("abcd1234", []byte(`{"sessions":[],"observations":[],"prompts":[]}`), engramsync.ChunkEntry{ID: "abcd1234", CreatedBy: "alice"})
	if err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d", refreshCalls)
	}
	if pushCalls != 2 {
		t.Fatalf("push calls = %d", pushCalls)
	}
}

func TestReadManifestReturnsRefreshFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sync/pull":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid or expired token"}`))
		case "/auth/refresh":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"refresh expired"}`))
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

	_, err = rt.ReadManifest()
	if err == nil {
		t.Fatal("expected refresh failure")
	}
	if !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("expected refresh error, got %v", err)
	}
}
