package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ProgressReporter manages progressive message editing on Discord.
// It sends an initial "processing" message, then edits it as tool status
// events and text tokens arrive via OnToken.
type ProgressReporter struct {
	discord   *DiscordChannel
	channelID string
	logger    *slog.Logger

	mu          sync.Mutex
	messageID   string           // ID of the message being edited
	tools       []toolStatus     // ordered list of tools seen
	textBuffer  strings.Builder  // accumulated response text
	lastEdit    time.Time        // rate-limit edits
	editPending bool             // buffered edit waiting
	timer       *time.Timer      // deferred edit timer
	done        bool             // finalized
}

type toolStatus struct {
	Name   string
	Done   bool
	Error  bool
	DurMS  int64
}

const (
	progressMinEditInterval = 2 * time.Second
	progressSplitThreshold  = 1800 // if final text > this, split into multiple messages
)

// NewProgressReporter creates a ProgressReporter for a Discord channel.
func NewProgressReporter(dc *DiscordChannel, channelID string, logger *slog.Logger) *ProgressReporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProgressReporter{
		discord:   dc,
		channelID: channelID,
		logger:    logger,
	}
}

// Start sends the initial "processing" message and saves its ID.
func (p *ProgressReporter) Start(ctx context.Context) error {
	msgID, err := p.discord.SendInitial(ctx, p.channelID, "\U0001f504 Procesando...")
	if err != nil {
		return fmt.Errorf("progress: start: %w", err)
	}

	p.mu.Lock()
	p.messageID = msgID
	p.lastEdit = time.Now()
	p.mu.Unlock()

	p.logger.Info("progress reporter started", "channel_id", p.channelID, "message_id", msgID)
	return nil
}

// OnToken handles a token from the orchestrator. It detects __STATUS__ events
// and regular text tokens, updating the Discord message progressively.
func (p *ProgressReporter) OnToken(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.done || p.messageID == "" {
		return
	}

	if strings.HasPrefix(token, "__STATUS__") {
		p.handleStatus(token[len("__STATUS__"):])
		return
	}

	// Regular text token — buffer it.
	p.textBuffer.WriteString(token)
	p.scheduleEdit()
}

// handleStatus parses a status JSON and updates tool tracking.
// Must be called with p.mu held.
func (p *ProgressReporter) handleStatus(jsonStr string) {
	var evt map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &evt); err != nil {
		p.logger.Warn("progress: invalid status JSON", "raw", jsonStr, "error", err)
		return
	}

	event, _ := evt["event"].(string)
	toolName, _ := evt["tool"].(string)

	switch event {
	case "tool_start":
		p.tools = append(p.tools, toolStatus{Name: toolName})
		p.doEdit() // immediate edit for tool events

	case "tool_done":
		durMS, _ := evt["duration_ms"].(float64)
		isErr, _ := evt["is_error"].(bool)
		for i := len(p.tools) - 1; i >= 0; i-- {
			if p.tools[i].Name == toolName && !p.tools[i].Done {
				p.tools[i].Done = true
				p.tools[i].Error = isErr
				p.tools[i].DurMS = int64(durMS)
				break
			}
		}
		p.doEdit() // immediate edit for tool events

	case "complete":
		// LLM iteration complete status — ignore for display purposes
	}
}

// scheduleEdit schedules a rate-limited edit. Must be called with p.mu held.
func (p *ProgressReporter) scheduleEdit() {
	if p.editPending {
		return
	}

	elapsed := time.Since(p.lastEdit)
	if elapsed >= progressMinEditInterval {
		p.doEdit()
		return
	}

	p.editPending = true
	delay := progressMinEditInterval - elapsed
	p.timer = time.AfterFunc(delay, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.editPending = false
		if !p.done {
			p.doEdit()
		}
	})
}

// doEdit performs the actual Discord message edit. Must be called with p.mu held.
func (p *ProgressReporter) doEdit() {
	if p.discord == nil {
		return
	}

	content := p.buildMessage()
	if content == "" {
		return
	}

	// Truncate to Discord limit for in-progress edits.
	if len(content) > DiscordMaxMessageLen {
		content = content[:DiscordMaxMessageLen-3] + "..."
	}

	go func(text string) {
		if err := p.discord.EditMessage(context.Background(), p.channelID, p.messageID, text); err != nil {
			p.logger.Warn("progress: edit failed", "error", err)
		}
	}(content)

	p.lastEdit = time.Now()
}

// buildMessage constructs the current message content. Must be called with p.mu held.
func (p *ProgressReporter) buildMessage() string {
	var sb strings.Builder

	for _, t := range p.tools {
		if t.Done {
			if t.Error {
				sb.WriteString("\u274c ")
			} else {
				sb.WriteString("\U0001f527 ")
			}
			sb.WriteString(t.Name)
			if t.Error {
				sb.WriteString(" \u2717")
			} else {
				sb.WriteString(" \u2713")
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("\U0001f527 ")
			sb.WriteString(t.Name)
			sb.WriteString("\n")
		}
	}

	text := p.textBuffer.String()
	if text != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
	} else {
		sb.WriteString("\U0001f504 Procesando...")
	}

	return sb.String()
}

// Finalize sends the final message. If the content is too long, it splits
// the tool summary into the original message and sends response text as new message(s).
func (p *ProgressReporter) Finalize(ctx context.Context, text string) error {
	p.mu.Lock()
	// Cancel any pending timer.
	if p.timer != nil {
		p.timer.Stop()
	}
	p.done = true

	// Build tool summary.
	var toolSummary strings.Builder
	for _, t := range p.tools {
		if t.Error {
			toolSummary.WriteString("\u274c ")
		} else {
			toolSummary.WriteString("\U0001f527 ")
		}
		toolSummary.WriteString(t.Name)
		if t.Error {
			toolSummary.WriteString(" \u2717")
		} else {
			toolSummary.WriteString(" \u2713")
		}
		toolSummary.WriteString("\n")
	}

	messageID := p.messageID
	channelID := p.channelID
	p.mu.Unlock()

	if messageID == "" {
		return fmt.Errorf("progress: no message to finalize")
	}

	// Convert markdown for Discord.
	responseText := ConvertMarkdown(text, FormatDiscord)
	summary := toolSummary.String()

	// Decide whether to split or combine.
	combined := ""
	if summary != "" {
		combined = summary + "\n" + responseText
	} else {
		combined = responseText
	}

	if len(combined) <= progressSplitThreshold {
		// Everything fits in one message — edit the original.
		if err := p.discord.EditMessage(ctx, channelID, messageID, combined); err != nil {
			return fmt.Errorf("progress: finalize edit: %w", err)
		}
		return nil
	}

	// Too long — put tool summary in original message, send response as new message(s).
	if summary != "" {
		if err := p.discord.EditMessage(ctx, channelID, messageID, summary); err != nil {
			p.logger.Warn("progress: finalize summary edit failed", "error", err)
		}
	}

	// Send response text as new message(s), split if needed.
	chunks := SplitMessage(responseText, DiscordMaxMessageLen)
	for i, chunk := range chunks {
		if _, err := p.discord.SendInitial(ctx, channelID, chunk); err != nil {
			return fmt.Errorf("progress: finalize send chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}

	return nil
}
