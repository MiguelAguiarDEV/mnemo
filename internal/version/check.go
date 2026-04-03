// Package version checks for newer engram releases on GitHub.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner    = "Gentleman-Programming"
	repoName     = "engram"
	checkTimeout = 2 * time.Second
)

// githubRelease is the subset of the GitHub releases API we care about.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// CheckLatest compares the running version against the latest GitHub release.
// Returns a user-facing message if an update is available, or "" if up to date.
// Never returns an error — silently returns "" on any failure (no network, timeout, etc.).
func CheckLatest(current string) string {
	if current == "" || current == "dev" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}

	latest := normalizeVersion(release.TagName)
	running := normalizeVersion(current)

	if latest == "" || latest == running {
		return ""
	}

	if !isNewer(latest, running) {
		return ""
	}

	return fmt.Sprintf(
		"Update available: %s → %s\n%s",
		running, latest, updateInstructions(),
	)
}

// normalizeVersion strips a leading "v" prefix.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// isNewer returns true if latest > current using simple semver comparison.
func isNewer(latest, current string) bool {
	latestParts := splitVersion(latest)
	currentParts := splitVersion(current)

	for i := 0; i < 3; i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}
	return false
}

// splitVersion splits "1.8.1" into [1, 8, 1]. Returns [0,0,0] on parse failure.
func splitVersion(v string) [3]int {
	var parts [3]int
	segments := strings.SplitN(v, ".", 3)
	for i, s := range segments {
		if i >= 3 {
			break
		}
		for _, c := range s {
			if c >= '0' && c <= '9' {
				parts[i] = parts[i]*10 + int(c-'0')
			} else {
				break
			}
		}
	}
	return parts
}

// updateInstructions returns platform-appropriate update commands.
func updateInstructions() string {
	switch runtime.GOOS {
	case "darwin":
		return "  brew update && brew upgrade engram"
	case "linux":
		return "  brew update && brew upgrade engram\n  or: go install github.com/Gentleman-Programming/engram/cmd/engram@latest"
	default:
		return "  go install github.com/Gentleman-Programming/engram/cmd/engram@latest\n  or: https://github.com/Gentleman-Programming/engram/releases/latest"
	}
}
