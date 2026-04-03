package notifications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const discordAPIBase = "https://discord.com/api/v10"

// Discord sends notifications as DMs via the Discord Bot API.
type Discord struct {
	botToken string
	userID   string
	client   *http.Client

	// baseURL overrides the Discord API base for testing.
	baseURL string

	mu        sync.Mutex
	channelID string // cached DM channel ID
}

// NewDiscord creates a Discord notifier. botToken is the bot's auth token,
// userID is the Discord snowflake of the user to DM.
func NewDiscord(botToken, userID string) *Discord {
	return &Discord{
		botToken: botToken,
		userID:   userID,
		client:   http.DefaultClient,
		baseURL:  discordAPIBase,
	}
}

// Send delivers a notification as a Discord DM.
func (d *Discord) Send(n Notification) error {
	chID, err := d.dmChannelID()
	if err != nil {
		return fmt.Errorf("discord: get DM channel: %w", err)
	}

	content := formatMessage(n)

	body, _ := json.Marshal(map[string]string{"content": content})
	url := fmt.Sprintf("%s/channels/%s/messages", d.baseURL, chID)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord: send message: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// dmChannelID returns the DM channel for the configured user, creating it
// on first call and caching it for subsequent calls.
func (d *Discord) dmChannelID() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.channelID != "" {
		return d.channelID, nil
	}

	body, _ := json.Marshal(map[string]string{"recipient_id": d.userID})
	url := fmt.Sprintf("%s/users/@me/channels", d.baseURL)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("create DM channel: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create DM channel: status %d: %s", resp.StatusCode, string(respBody))
	}

	var ch struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
		return "", fmt.Errorf("decode DM channel response: %w", err)
	}
	if ch.ID == "" {
		return "", fmt.Errorf("empty channel ID in response")
	}

	d.channelID = ch.ID
	return d.channelID, nil
}

// formatMessage builds a human-readable Discord message with an emoji prefix.
func formatMessage(n Notification) string {
	var emoji string
	switch n.Type {
	case TaskComplete:
		emoji = "\u2705" // ✅
	case InputNeeded:
		emoji = "\u2753" // ❓
	case Alert:
		emoji = "\u26a0\ufe0f" // ⚠️
	case Info:
		emoji = "\u2139\ufe0f" // ℹ️
	default:
		emoji = "\u2139\ufe0f"
	}
	return fmt.Sprintf("%s **%s**\n%s", emoji, n.Title, n.Message)
}
