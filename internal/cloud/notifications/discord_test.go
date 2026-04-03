package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDiscord_Send_Success(t *testing.T) {
	var createCalls, msgCalls atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if got := r.Header.Get("Authorization"); got != "Bot test-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bot test-token")
		}

		switch r.URL.Path {
		case "/users/@me/channels":
			createCalls.Add(1)
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["recipient_id"] != "123456" {
				t.Errorf("recipient_id = %q, want %q", body["recipient_id"], "123456")
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "dm-channel-99"})
		case "/channels/dm-channel-99/messages":
			msgCalls.Add(1)
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["content"] == "" {
				t.Error("message content is empty")
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	d := NewDiscord("test-token", "123456")
	d.baseURL = ts.URL

	err := d.Send(Notification{
		Type:    TaskComplete,
		Title:   "Build done",
		Message: "All tests passed.",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if got := createCalls.Load(); got != 1 {
		t.Errorf("DM channel create calls = %d, want 1", got)
	}
	if got := msgCalls.Load(); got != 1 {
		t.Errorf("message send calls = %d, want 1", got)
	}
}

func TestDiscord_Send_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me/channels":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "dm-channel-99"})
		case "/channels/dm-channel-99/messages":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"Missing Access"}`))
		}
	}))
	defer ts.Close()

	d := NewDiscord("test-token", "123456")
	d.baseURL = ts.URL

	err := d.Send(Notification{Type: Alert, Title: "Test", Message: "msg"})
	if err == nil {
		t.Fatal("Send() should have returned an error for 403")
	}
	if got := err.Error(); !contains(got, "status 403") {
		t.Errorf("error = %q, want to contain 'status 403'", got)
	}
}

func TestDiscord_Send_ChannelCaching(t *testing.T) {
	var createCalls atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/@me/channels":
			createCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "dm-channel-99"})
		case "/channels/dm-channel-99/messages":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
		}
	}))
	defer ts.Close()

	d := NewDiscord("test-token", "123456")
	d.baseURL = ts.URL

	// Send twice -- channel should only be created once.
	for i := 0; i < 2; i++ {
		if err := d.Send(Notification{Type: Info, Title: "T", Message: "m"}); err != nil {
			t.Fatalf("Send() #%d error = %v", i+1, err)
		}
	}

	if got := createCalls.Load(); got != 1 {
		t.Errorf("DM channel create calls = %d, want 1 (should be cached)", got)
	}
}

func TestDiscord_Send_CreateChannelError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401: Unauthorized"}`))
	}))
	defer ts.Close()

	d := NewDiscord("bad-token", "123456")
	d.baseURL = ts.URL

	err := d.Send(Notification{Type: Info, Title: "T", Message: "m"})
	if err == nil {
		t.Fatal("Send() should have returned an error for channel creation failure")
	}
	if got := err.Error(); !contains(got, "DM channel") {
		t.Errorf("error = %q, want to contain 'DM channel'", got)
	}
}

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		typ  NotificationType
		want string
	}{
		{TaskComplete, "\u2705"},
		{InputNeeded, "\u2753"},
		{Alert, "\u26a0\ufe0f"},
		{Info, "\u2139\ufe0f"},
	}
	for _, tt := range tests {
		msg := formatMessage(Notification{Type: tt.typ, Title: "T", Message: "M"})
		if !contains(msg, tt.want) {
			t.Errorf("formatMessage(%q) = %q, want emoji %q", tt.typ, msg, tt.want)
		}
		if !contains(msg, "**T**") {
			t.Errorf("formatMessage(%q) missing bold title", tt.typ)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
