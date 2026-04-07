package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ConversationStore is the minimal interface DiscordChannel needs to
// create conversations for incoming Discord DMs. The cloudstore.CloudStore
// satisfies this via its CreateConversation method.
type ConversationStore interface {
	CreateConversation(userID string, title string) (int64, error)
}

// DiscordChannel implements the Channel and SendableChannel interfaces using
// discordgo. It handles DMs and mentions, converting Discord messages to
// IncomingMessage and routing responses back through the Gateway.
type DiscordChannel struct {
	session      *discordgo.Session
	botToken     string
	allowedUsers map[string]bool // user IDs permitted to interact
	gateway      *Gateway
	logger       *slog.Logger

	mu      sync.RWMutex
	started bool

	// Conversation management (mirrors HERMES DMRouter behavior).
	convStore     ConversationStore
	defaultUserID string // JARVIS owner UUID used as SenderID in cloud_users
	convMu        sync.Mutex
	convMap       map[string]int64 // discord channelID → conversation ID
}

// NewDiscordChannel creates a new DiscordChannel.
// The channel is not connected until Start is called.
func NewDiscordChannel(botToken string, allowedUsers []string, opts ...DiscordChannelOption) *DiscordChannel {
	allowed := make(map[string]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		uid = strings.TrimSpace(uid)
		if uid != "" {
			allowed[uid] = true
		}
	}

	dc := &DiscordChannel{
		botToken:     botToken,
		allowedUsers: allowed,
		logger:       slog.Default(),
		convMap:      make(map[string]int64),
	}
	for _, opt := range opts {
		opt(dc)
	}
	dc.logger.Info("discord channel created",
		"allowed_users", len(allowed),
	)
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

// WithDiscordChannelGateway sets the Gateway for routing incoming messages.
func WithDiscordChannelGateway(gw *Gateway) DiscordChannelOption {
	return func(dc *DiscordChannel) {
		dc.gateway = gw
	}
}

// WithDiscordConversationStore injects the conversation store used to
// create new conversations when a Discord DM channel is seen for the first time.
func WithDiscordConversationStore(cs ConversationStore) DiscordChannelOption {
	return func(dc *DiscordChannel) {
		dc.convStore = cs
	}
}

// WithDiscordDefaultUserID sets the JARVIS owner UUID used as SenderID
// for conversations originating from Discord. Required for cloud_users FK.
func WithDiscordDefaultUserID(userID string) DiscordChannelOption {
	return func(dc *DiscordChannel) {
		dc.defaultUserID = userID
	}
}

// withDiscordSession injects a pre-built discordgo.Session (for testing).
func withDiscordSession(s *discordgo.Session) DiscordChannelOption {
	return func(dc *DiscordChannel) {
		dc.session = s
	}
}

// Name returns the channel identifier.
func (dc *DiscordChannel) Name() string {
	return "discord"
}

// Start connects to Discord and begins listening for messages.
func (dc *DiscordChannel) Start(ctx context.Context) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if dc.started {
		dc.logger.Warn("discord channel already started")
		return fmt.Errorf("discord: already started")
	}

	if dc.botToken == "" {
		dc.logger.Error("discord channel start failed: empty bot token")
		return fmt.Errorf("discord: bot token is required")
	}

	// Create session if not injected (testing).
	if dc.session == nil {
		dg, err := discordgo.New("Bot " + dc.botToken)
		if err != nil {
			dc.logger.Error("discord session creation failed", "error", err)
			return fmt.Errorf("discord: create session: %w", err)
		}
		dg.Identify.Intents = discordgo.IntentsGuilds |
			discordgo.IntentsGuildMessages |
			discordgo.IntentsMessageContent |
			discordgo.IntentsDirectMessages
		dc.session = dg
	}

	// Register message handler.
	dc.session.AddHandler(dc.onMessageCreate)

	// Register ready handler.
	dc.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		dc.logger.Info("discord connection established",
			"bot_user", r.User.Username,
			"bot_id", r.User.ID,
		)
	})

	if err := dc.session.Open(); err != nil {
		dc.logger.Error("discord connection failed", "error", err)
		return fmt.Errorf("discord: open connection: %w", err)
	}

	dc.started = true
	dc.logger.Info("discord channel started")
	return nil
}

// Stop disconnects from Discord.
func (dc *DiscordChannel) Stop() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if !dc.started {
		dc.logger.Warn("discord channel not started, nothing to stop")
		return nil
	}

	if dc.session != nil {
		if err := dc.session.Close(); err != nil {
			dc.logger.Error("discord disconnect failed", "error", err)
			return fmt.Errorf("discord: close connection: %w", err)
		}
	}

	dc.started = false
	dc.logger.Info("discord channel stopped")
	return nil
}

// Send delivers a message to a Discord channel or DM.
// Long messages are automatically split to respect Discord's 2000 char limit.
func (dc *DiscordChannel) Send(ctx context.Context, msg OutgoingMessage) error {
	dc.mu.RLock()
	sess := dc.session
	dc.mu.RUnlock()

	if sess == nil {
		dc.logger.Error("discord send failed: no session")
		return fmt.Errorf("discord: not connected")
	}

	// Convert markdown to Discord format.
	text := ConvertMarkdown(msg.Text, FormatDiscord)

	// Split into chunks if needed.
	chunks := SplitMessage(text, DiscordMaxMessageLen)
	if len(chunks) == 0 {
		dc.logger.Warn("discord send: empty message after conversion",
			"recipient", msg.RecipientID,
		)
		return nil
	}

	dc.logger.Info("discord sending response",
		"recipient", msg.RecipientID,
		"chunks", len(chunks),
		"total_len", len(text),
	)

	for i, chunk := range chunks {
		if _, err := sess.ChannelMessageSend(msg.RecipientID, chunk); err != nil {
			dc.logger.Error("discord send failed",
				"recipient", msg.RecipientID,
				"chunk", i+1,
				"total_chunks", len(chunks),
				"error", err,
			)
			return fmt.Errorf("discord: send chunk %d/%d: %w", i+1, len(chunks), err)
		}
		dc.logger.Debug("discord chunk sent",
			"recipient", msg.RecipientID,
			"chunk", i+1,
			"chunk_len", len(chunk),
		)
	}

	dc.logger.Info("discord response sent",
		"recipient", msg.RecipientID,
		"reply_to", msg.ReplyTo,
	)
	return nil
}

// SendInitial sends a message and returns its ID for later editing.
func (dc *DiscordChannel) SendInitial(ctx context.Context, channelID, text string) (string, error) {
	dc.mu.RLock()
	sess := dc.session
	dc.mu.RUnlock()

	if sess == nil {
		return "", fmt.Errorf("discord: not connected")
	}

	msg, err := sess.ChannelMessageSend(channelID, text)
	if err != nil {
		dc.logger.Error("discord send initial failed", "channel_id", channelID, "error", err)
		return "", fmt.Errorf("discord: send initial: %w", err)
	}
	dc.logger.Debug("discord initial message sent", "channel_id", channelID, "message_id", msg.ID)
	return msg.ID, nil
}

// DeleteMessage deletes an existing Discord message by ID.
func (dc *DiscordChannel) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	dc.mu.RLock()
	sess := dc.session
	dc.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("discord: not connected")
	}

	if err := sess.ChannelMessageDelete(channelID, messageID); err != nil {
		dc.logger.Error("discord delete failed", "channel_id", channelID, "message_id", messageID, "error", err)
		return fmt.Errorf("discord: delete message: %w", err)
	}
	dc.logger.Debug("discord message deleted", "channel_id", channelID, "message_id", messageID)
	return nil
}

// EditMessage edits an existing Discord message by ID.
func (dc *DiscordChannel) EditMessage(ctx context.Context, channelID, messageID, text string) error {
	dc.mu.RLock()
	sess := dc.session
	dc.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("discord: not connected")
	}

	_, err := sess.ChannelMessageEdit(channelID, messageID, text)
	if err != nil {
		dc.logger.Error("discord edit failed", "channel_id", channelID, "message_id", messageID, "error", err)
		return fmt.Errorf("discord: edit message: %w", err)
	}
	dc.logger.Debug("discord message edited", "channel_id", channelID, "message_id", messageID)
	return nil
}

// onMessageCreate handles incoming Discord messages (DMs and mentions).
func (dc *DiscordChannel) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Guard: nil author or bot messages.
	if m.Author == nil || m.Author.Bot {
		return
	}

	// Skip own messages.
	if s.State != nil && s.State.User != nil && m.Author.ID == s.State.User.ID {
		return
	}

	// Skip empty messages.
	if strings.TrimSpace(m.Content) == "" {
		return
	}

	// Only handle DMs (GuildID empty) or mentions.
	isDM := m.GuildID == ""
	isMention := dc.isBotMentioned(s, m)
	if !isDM && !isMention {
		return
	}

	// Auth check: only allowed users.
	if !dc.isUserAllowed(m.Author.ID) {
		dc.logger.Warn("discord message from unauthorized user",
			"user_id", m.Author.ID,
			"channel_id", m.ChannelID,
		)
		_, _ = s.ChannelMessageSend(m.ChannelID, "Not authorized.")
		return
	}

	dc.logger.Info("discord message received",
		"user_id", m.Author.ID,
		"channel_id", m.ChannelID,
		"is_dm", isDM,
		"text_len", len(m.Content),
	)
	dc.logger.Debug("discord message content",
		"text", m.Content,
	)

	// Route through Gateway if available.
	if dc.gateway == nil {
		dc.logger.Error("discord message received but no gateway configured")
		_, _ = s.ChannelMessageSend(m.ChannelID, "Bot not fully initialized. Try again later.")
		return
	}

	// Strip bot mention from text if it was a mention.
	text := m.Content
	if isMention && s.State != nil && s.State.User != nil {
		text = strings.TrimSpace(strings.ReplaceAll(text, "<@"+s.State.User.ID+">", ""))
		text = strings.TrimSpace(strings.ReplaceAll(text, "<@!"+s.State.User.ID+">", ""))
	}

	// Build metadata.
	metadata := map[string]string{
		"channel_id": m.ChannelID,
		"guild_id":   m.GuildID,
	}

	// Show typing indicator.
	_ = s.ChannelTyping(m.ChannelID)

	// Process asynchronously to not block the event loop.
	go dc.processMessage(s, m, text, metadata)
}

// processMessage routes a message through the Gateway and sends the response.
// If the Gateway has a streaming handler, it uses progressive message editing.
func (dc *DiscordChannel) processMessage(s *discordgo.Session, m *discordgo.MessageCreate, text string, metadata map[string]string) {
	ctx := context.Background()

	// Resolve (or create) the conversation for this Discord channel.
	convID, err := dc.getOrCreateConversation(ctx, m.ChannelID)
	if err != nil {
		dc.logger.Error("discord get/create conversation failed",
			"user_id", m.Author.ID,
			"channel_id", m.ChannelID,
			"error", err,
		)
		_, _ = s.ChannelMessageSend(m.ChannelID, "Something went wrong. Try again later.")
		return
	}

	// Augment metadata: conversation_id (for orchestrator), discord_user_id
	// (preserve the original author), and conversation_id always present.
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["conversation_id"] = strconv.FormatInt(convID, 10)
	metadata["discord_user_id"] = m.Author.ID

	// SenderID must be the JARVIS owner UUID (cloud_users FK). Fall back
	// to the Discord user ID if no default is configured (test mode).
	senderID := dc.defaultUserID
	if senderID == "" {
		senderID = m.Author.ID
	}

	incoming := IncomingMessage{
		ChannelName: "discord",
		SenderID:    senderID,
		Text:        text,
		ReplyTo:     m.ChannelID,
		Metadata:    metadata,
	}

	// Try progressive mode if streaming handler is available.
	if dc.gateway.streamingHandler != nil {
		dc.processMessageProgressive(ctx, incoming, m)
		return
	}

	// Fallback: non-streaming mode.
	resp, err := dc.gateway.HandleMessage(ctx, incoming)
	if err != nil {
		dc.logger.Error("discord message handling failed",
			"user_id", m.Author.ID,
			"channel_id", m.ChannelID,
			"error", err,
		)
		_, _ = s.ChannelMessageSend(m.ChannelID, "Something went wrong. Try again later.")
		return
	}

	outgoing := OutgoingMessage{
		ChannelName: "discord",
		RecipientID: m.ChannelID,
		Text:        resp.Text,
		Format:      FormatDiscord,
		ReplyTo:     m.ChannelID,
	}

	if err := dc.Send(ctx, outgoing); err != nil {
		dc.logger.Error("discord response send failed",
			"user_id", m.Author.ID,
			"channel_id", m.ChannelID,
			"error", err,
		)
	}
}

// processMessageProgressive uses ProgressReporter for live message editing.
func (dc *DiscordChannel) processMessageProgressive(ctx context.Context, incoming IncomingMessage, m *discordgo.MessageCreate) {
	pr := NewProgressReporter(dc, m.ChannelID, dc.logger)

	// Send initial "processing" message.
	if err := pr.Start(ctx); err != nil {
		dc.logger.Error("progress reporter start failed",
			"channel_id", m.ChannelID,
			"error", err,
		)
		_, _ = dc.session.ChannelMessageSend(m.ChannelID, "Something went wrong. Try again later.")
		return
	}

	// Stream tokens through the progress reporter.
	resp, err := dc.gateway.HandleMessageStreaming(ctx, incoming, pr.OnToken)
	if err != nil {
		dc.logger.Error("discord streaming message handling failed",
			"user_id", m.Author.ID,
			"channel_id", m.ChannelID,
			"error", err,
		)
		_ = pr.Finalize(ctx, "Something went wrong. Try again later.")
		return
	}

	// Finalize with the full response text.
	if err := pr.Finalize(ctx, resp.Text); err != nil {
		dc.logger.Error("progress reporter finalize failed",
			"user_id", m.Author.ID,
			"channel_id", m.ChannelID,
			"error", err,
		)
	}
}

// getOrCreateConversation returns the conversation ID for a Discord channel,
// creating a new one in the cloudstore on first use. Mirrors the HERMES
// DMRouter behavior to prevent foreign key violations on message insert.
func (dc *DiscordChannel) getOrCreateConversation(ctx context.Context, channelID string) (int64, error) {
	dc.convMu.Lock()
	defer dc.convMu.Unlock()

	if convID, ok := dc.convMap[channelID]; ok {
		return convID, nil
	}

	if dc.convStore == nil {
		return 0, fmt.Errorf("discord: no conversation store configured")
	}
	if dc.defaultUserID == "" {
		return 0, fmt.Errorf("discord: no default user id configured")
	}

	title := fmt.Sprintf("Discord DM %s", time.Now().Format("2006-01-02 15:04"))
	convID, err := dc.convStore.CreateConversation(dc.defaultUserID, title)
	if err != nil {
		return 0, fmt.Errorf("discord: create conversation: %w", err)
	}

	dc.convMap[channelID] = convID
	dc.logger.Info("discord conversation created",
		"channel_id", channelID,
		"conversation_id", convID,
	)
	return convID, nil
}

// isBotMentioned checks if the bot is mentioned in the message.
func (dc *DiscordChannel) isBotMentioned(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	if s.State == nil || s.State.User == nil {
		return false
	}
	for _, user := range m.Mentions {
		if user.ID == s.State.User.ID {
			return true
		}
	}
	return false
}

// isUserAllowed checks if a user ID is in the allowed list.
// If no allowed users are configured, all users are allowed.
func (dc *DiscordChannel) isUserAllowed(userID string) bool {
	if len(dc.allowedUsers) == 0 {
		return true
	}
	return dc.allowedUsers[userID]
}
