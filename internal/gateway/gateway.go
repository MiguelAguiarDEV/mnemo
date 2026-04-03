package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// MessageHandler processes an incoming message and returns a response.
type MessageHandler func(ctx context.Context, msg IncomingMessage) (OutgoingMessage, error)

// StreamingMessageHandler processes an incoming message with a token callback for streaming.
type StreamingMessageHandler func(ctx context.Context, msg IncomingMessage, onToken func(string)) (OutgoingMessage, error)

// Gateway is the central message router that manages channels and dispatches
// messages to the handler (orchestrator).
type Gateway struct {
	mu               sync.RWMutex
	channels         map[string]Channel
	handler          MessageHandler
	streamingHandler StreamingMessageHandler
	logger           *slog.Logger
	started          bool
}

// New creates a new Gateway with the given message handler.
func New(handler MessageHandler, opts ...GatewayOption) *Gateway {
	g := &Gateway{
		channels: make(map[string]Channel),
		handler:  handler,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(g)
	}
	g.logger.Info("gateway created")
	return g
}

// GatewayOption configures a Gateway.
type GatewayOption func(*Gateway)

// WithLogger sets a custom logger for the Gateway.
func WithLogger(l *slog.Logger) GatewayOption {
	return func(g *Gateway) {
		g.logger = l
	}
}

// WithStreamingHandler sets a streaming message handler for progressive responses.
func WithStreamingHandler(h StreamingMessageHandler) GatewayOption {
	return func(g *Gateway) {
		g.streamingHandler = h
	}
}

// Register adds a channel to the gateway. Returns an error if a channel
// with the same name is already registered or if the gateway is already started.
func (g *Gateway) Register(ch Channel) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := ch.Name()
	if name == "" {
		g.logger.Error("channel registration failed: empty name")
		return fmt.Errorf("gateway: channel name must not be empty")
	}
	if _, exists := g.channels[name]; exists {
		g.logger.Error("channel registration failed: duplicate name", "channel", name)
		return fmt.Errorf("gateway: channel %q already registered", name)
	}
	if g.started {
		g.logger.Error("channel registration failed: gateway already started", "channel", name)
		return fmt.Errorf("gateway: cannot register channel %q after start", name)
	}

	g.channels[name] = ch
	g.logger.Info("channel registered", "channel", name)
	return nil
}

// Start starts all registered channels.
func (g *Gateway) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.started {
		g.logger.Warn("gateway already started")
		return fmt.Errorf("gateway: already started")
	}

	g.logger.Info("starting gateway", "channels", len(g.channels))

	for name, ch := range g.channels {
		g.logger.Debug("starting channel", "channel", name)
		if err := ch.Start(ctx); err != nil {
			g.logger.Error("failed to start channel", "channel", name, "error", err)
			// Stop any channels we already started.
			for stopName, stopCh := range g.channels {
				if stopName == name {
					break
				}
				if stopErr := stopCh.Stop(); stopErr != nil {
					g.logger.Error("failed to stop channel during rollback", "channel", stopName, "error", stopErr)
				}
			}
			return fmt.Errorf("gateway: start channel %q: %w", name, err)
		}
		g.logger.Info("channel started", "channel", name)
	}

	g.started = true
	g.logger.Info("gateway started", "channels", len(g.channels))
	return nil
}

// Stop stops all registered channels.
func (g *Gateway) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.started {
		g.logger.Warn("gateway not started, nothing to stop")
		return nil
	}

	g.logger.Info("stopping gateway", "channels", len(g.channels))

	var firstErr error
	for name, ch := range g.channels {
		g.logger.Debug("stopping channel", "channel", name)
		if err := ch.Stop(); err != nil {
			g.logger.Error("failed to stop channel", "channel", name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("gateway: stop channel %q: %w", name, err)
			}
		} else {
			g.logger.Info("channel stopped", "channel", name)
		}
	}

	g.started = false
	g.logger.Info("gateway stopped")
	return firstErr
}

// HandleMessage processes an incoming message through the handler and returns
// the response. This is the main entry point for channels to submit messages.
func (g *Gateway) HandleMessage(ctx context.Context, msg IncomingMessage) (OutgoingMessage, error) {
	g.logger.Info("routing message",
		"channel", msg.ChannelName,
		"sender", msg.SenderID,
		"text_len", len(msg.Text),
	)

	if g.handler == nil {
		g.logger.Error("no handler configured")
		return OutgoingMessage{}, fmt.Errorf("gateway: no handler configured")
	}

	resp, err := g.handler(ctx, msg)
	if err != nil {
		g.logger.Error("handler failed",
			"channel", msg.ChannelName,
			"sender", msg.SenderID,
			"error", err,
		)
		return OutgoingMessage{}, fmt.Errorf("gateway: handler: %w", err)
	}

	g.logger.Info("message handled",
		"channel", msg.ChannelName,
		"sender", msg.SenderID,
		"response_len", len(resp.Text),
	)
	return resp, nil
}

// HandleMessageStreaming processes a message with a token callback for streaming.
// Falls back to HandleMessage if no streaming handler is configured.
func (g *Gateway) HandleMessageStreaming(ctx context.Context, msg IncomingMessage, onToken func(string)) (OutgoingMessage, error) {
	if g.streamingHandler == nil {
		return g.HandleMessage(ctx, msg)
	}

	g.logger.Info("routing message (streaming)",
		"channel", msg.ChannelName,
		"sender", msg.SenderID,
		"text_len", len(msg.Text),
	)

	resp, err := g.streamingHandler(ctx, msg, onToken)
	if err != nil {
		g.logger.Error("streaming handler failed",
			"channel", msg.ChannelName,
			"sender", msg.SenderID,
			"error", err,
		)
		return OutgoingMessage{}, fmt.Errorf("gateway: streaming handler: %w", err)
	}

	g.logger.Info("message handled (streaming)",
		"channel", msg.ChannelName,
		"sender", msg.SenderID,
		"response_len", len(resp.Text),
	)
	return resp, nil
}

// Send delivers an outgoing message to the appropriate channel. The channel
// must implement SendableChannel; otherwise an error is returned.
func (g *Gateway) Send(ctx context.Context, msg OutgoingMessage) error {
	g.mu.RLock()
	ch, exists := g.channels[msg.ChannelName]
	g.mu.RUnlock()

	if !exists {
		g.logger.Error("send failed: unknown channel", "channel", msg.ChannelName)
		return fmt.Errorf("gateway: unknown channel %q", msg.ChannelName)
	}

	sendable, ok := ch.(SendableChannel)
	if !ok {
		g.logger.Error("send failed: channel does not support Send", "channel", msg.ChannelName)
		return fmt.Errorf("gateway: channel %q does not support Send", msg.ChannelName)
	}

	g.logger.Info("sending message",
		"channel", msg.ChannelName,
		"recipient", msg.RecipientID,
		"text_len", len(msg.Text),
	)

	if err := sendable.Send(ctx, msg); err != nil {
		g.logger.Error("send failed", "channel", msg.ChannelName, "error", err)
		return fmt.Errorf("gateway: send to %q: %w", msg.ChannelName, err)
	}

	g.logger.Info("message sent", "channel", msg.ChannelName, "recipient", msg.RecipientID)
	return nil
}

// Channels returns the names of all registered channels.
func (g *Gateway) Channels() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, 0, len(g.channels))
	for name := range g.channels {
		names = append(names, name)
	}
	g.logger.Debug("listing channels", "count", len(names))
	return names
}
