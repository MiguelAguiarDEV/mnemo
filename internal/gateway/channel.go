package gateway

import "context"

// Channel represents a message transport (Discord, WhatsApp, Web, etc.).
type Channel interface {
	// Name returns the channel identifier (e.g. "discord", "whatsapp", "web").
	Name() string

	// Start initializes the channel (connect to API, open webhooks, etc.).
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel.
	Stop() error
}

// SendableChannel is a Channel that can deliver outgoing messages directly.
// Not all channels implement this — WebChannel, for example, responds inline
// via HTTP rather than pushing messages.
type SendableChannel interface {
	Channel
	Send(ctx context.Context, msg OutgoingMessage) error
}

// IncomingMessage is a normalized message from any channel.
type IncomingMessage struct {
	ChannelName string            // "discord", "whatsapp", "web", "voice"
	SenderID    string            // channel-specific user identifier
	Text        string            // plain text content
	Attachments []Attachment      // images, files, voice notes
	ReplyTo     string            // thread/reply context (channel-specific)
	Metadata    map[string]string // channel-specific extras (guild_id, phone, etc.)
}

// OutgoingMessage is a normalized response to send to any channel.
type OutgoingMessage struct {
	ChannelName string        // target channel
	RecipientID string        // channel-specific recipient
	Text        string        // response content (markdown)
	Format      MessageFormat // how to render the text
	ReplyTo     string        // thread/reply context
}

// MessageFormat controls how text is rendered per channel.
type MessageFormat int

const (
	FormatMarkdown MessageFormat = iota // full markdown (web, telegram)
	FormatPlain                         // plain text (voice, SMS)
	FormatHTML                          // HTML (email)
)

// Attachment represents a file or media attachment.
type Attachment struct {
	Type     string // "image", "audio", "file"
	URL      string // download URL or local path
	Filename string
	Data     []byte
}
