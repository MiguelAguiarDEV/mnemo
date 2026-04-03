package gateway

import (
	"context"
	"fmt"
	"log/slog"
)

// WebChannel bridges the existing HTTP SSE chat handler with the Gateway.
// It does not listen for messages on its own — instead, the HTTP handler
// calls ProcessHTTPMessage to route a request through the Gateway.
//
// This is a Phase 1 bridge: the existing HTTP handler stays in place,
// but messages can optionally flow through the Gateway for uniform routing.
type WebChannel struct {
	gateway *Gateway
	logger  *slog.Logger
}

// NewWebChannel creates a WebChannel attached to the given Gateway.
func NewWebChannel(gw *Gateway, opts ...WebChannelOption) *WebChannel {
	wc := &WebChannel{
		gateway: gw,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(wc)
	}
	wc.logger.Info("webchannel created")
	return wc
}

// WebChannelOption configures a WebChannel.
type WebChannelOption func(*WebChannel)

// WithWebChannelLogger sets a custom logger for the WebChannel.
func WithWebChannelLogger(l *slog.Logger) WebChannelOption {
	return func(wc *WebChannel) {
		wc.logger = l
	}
}

// Name returns the channel identifier.
func (wc *WebChannel) Name() string {
	wc.logger.Debug("webchannel name requested")
	return "web"
}

// Start is a no-op for WebChannel — the HTTP server is managed externally.
func (wc *WebChannel) Start(_ context.Context) error {
	wc.logger.Info("webchannel started (no-op, HTTP server managed externally)")
	return nil
}

// Stop is a no-op for WebChannel — the HTTP server is managed externally.
func (wc *WebChannel) Stop() error {
	wc.logger.Info("webchannel stopped (no-op)")
	return nil
}

// ProcessHTTPMessage converts an HTTP chat request into an IncomingMessage,
// routes it through the Gateway, and returns the OutgoingMessage.
//
// This is the bridge between the request/response HTTP model and the
// message-based Gateway model.
func (wc *WebChannel) ProcessHTTPMessage(ctx context.Context, userID string, conversationID int64, text string) (OutgoingMessage, error) {
	wc.logger.Info("processing HTTP message",
		"user_id", userID,
		"conversation_id", conversationID,
		"text_len", len(text),
	)

	msg := IncomingMessage{
		ChannelName: "web",
		SenderID:    userID,
		Text:        text,
		Metadata: map[string]string{
			"conversation_id": fmt.Sprintf("%d", conversationID),
		},
	}

	resp, err := wc.gateway.HandleMessage(ctx, msg)
	if err != nil {
		wc.logger.Error("failed to process HTTP message",
			"user_id", userID,
			"conversation_id", conversationID,
			"error", err,
		)
		return OutgoingMessage{}, err
	}

	wc.logger.Info("HTTP message processed",
		"user_id", userID,
		"conversation_id", conversationID,
		"response_len", len(resp.Text),
	)
	return resp, nil
}
