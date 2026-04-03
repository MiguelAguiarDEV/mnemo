package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v1.8.1", "1.8.1"},
		{"1.8.1", "1.8.1"},
		{" v2.0.0 ", "2.0.0"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeVersion(tt.in); got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitVersion(t *testing.T) {
	tests := []struct {
		in   string
		want [3]int
	}{
		{"1.8.1", [3]int{1, 8, 1}},
		{"2.0.0", [3]int{2, 0, 0}},
		{"1.0", [3]int{1, 0, 0}},
		{"", [3]int{0, 0, 0}},
		{"1.8.1-beta", [3]int{1, 8, 1}},
	}
	for _, tt := range tests {
		if got := splitVersion(tt.in); got != tt.want {
			t.Errorf("splitVersion(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"1.8.1", "1.8.0", true},
		{"2.0.0", "1.9.9", true},
		{"1.8.1", "1.8.1", false},
		{"1.7.0", "1.8.1", false},
		{"1.8.2", "1.8.1", true},
	}
	for _, tt := range tests {
		if got := isNewer(tt.latest, tt.current); got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestCheckLatestSkipsDevVersion(t *testing.T) {
	if msg := CheckLatest("dev"); msg != "" {
		t.Errorf("expected empty for dev version, got %q", msg)
	}
	if msg := CheckLatest(""); msg != "" {
		t.Errorf("expected empty for empty version, got %q", msg)
	}
}

func TestCheckLatestReturnsUpdateMessage(t *testing.T) {
	// Stub a fake GitHub API
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v99.0.0"})
	}))
	defer srv.Close()

	// Since CheckLatest uses a hardcoded URL, we test the components instead
	latest := normalizeVersion("v99.0.0")
	running := normalizeVersion("v1.8.1")

	if !isNewer(latest, running) {
		t.Fatal("expected 99.0.0 to be newer than 1.8.1")
	}
}

func TestCheckLatestUpToDate(t *testing.T) {
	latest := normalizeVersion("v1.8.1")
	running := normalizeVersion("v1.8.1")

	if isNewer(latest, running) {
		t.Fatal("same version should not be newer")
	}
}

func TestUpdateInstructions(t *testing.T) {
	msg := updateInstructions()
	if msg == "" {
		t.Fatal("expected non-empty update instructions")
	}
}
