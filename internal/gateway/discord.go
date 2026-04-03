package gateway

import (
	"context"
	"log/slog"
)

// DiscordChannel is a stub implementation of the Channel interface for Discord.
// Phase 3 will add the full discordgo-based implementation.
type DiscordChannel struct {
	logger *slog.Logger
}

// NewDiscordChannel creates a new DiscordChannel stub.
func NewDiscordChannel(opts ...DiscordChannelOption) *DiscordChannel {
	dc := &DiscordChannel{
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(dc)
	}
	dc.logger.Info("discord channel created (stub)")
	return dc
}

// DiscordChannelOption configures a DiscordChannel.
type DiscordChannelOption func(*DiscordChannel)

// WithDiscordChannelLogger sets a custom logger for the DiscordChannel.
func WithDiscordChannelLogger(l *slog.Logger) DiscordChannelOption {
	return func(dc *DiscordChannel) {
		dc.logger = l
	}
}

// Name returns the channel identifier.
func (dc *DiscordChannel) Name() string {
	return "discord"
}

// Start is a stub — Phase 3 will connect to Discord API via discordgo.
func (dc *DiscordChannel) Start(_ context.Context) error {
	dc.logger.Info("discord channel started (stub — no-op)")
	// TODO(Phase 3): Connect to Discord via discordgo bot token.
	return nil
}

// Stop is a stub — Phase 3 will disconnect from Discord API.
func (dc *DiscordChannel) Stop() error {
	dc.logger.Info("discord channel stopped (stub — no-op)")
	// TODO(Phase 3): Gracefully disconnect from Discord.
	return nil
}

// Send delivers a message to a Discord channel/user.
// Stub implementation — Phase 3 will use discordgo to send messages.
func (dc *DiscordChannel) Send(_ context.Context, msg OutgoingMessage) error {
	dc.logger.Info("discord send (stub — no-op)",
		"recipient", msg.RecipientID,
		"text_len", len(msg.Text),
		"reply_to", msg.ReplyTo,
	)
	// TODO(Phase 3): Use discordgo session to send message to channel/DM.
	return nil
}
