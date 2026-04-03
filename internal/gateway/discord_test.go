package gateway

import (
	"context"
	"testing"
)

func TestDiscordChannelName(t *testing.T) {
	dc := NewDiscordChannel(WithDiscordChannelLogger(testLogger()))
	if dc.Name() != "discord" {
		t.Fatalf("expected name 'discord', got %q", dc.Name())
	}
}

func TestDiscordChannelStartStop(t *testing.T) {
	dc := NewDiscordChannel(WithDiscordChannelLogger(testLogger()))

	if err := dc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := dc.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestDiscordChannelSend(t *testing.T) {
	dc := NewDiscordChannel(WithDiscordChannelLogger(testLogger()))

	msg := OutgoingMessage{
		ChannelName: "discord",
		RecipientID: "user-123",
		Text:        "hello from gateway",
		Format:      FormatPlain,
		ReplyTo:     "thread-456",
	}

	if err := dc.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestDiscordChannelImplementsChannel(t *testing.T) {
	var _ Channel = (*DiscordChannel)(nil)
}

func TestDiscordChannelImplementsSendableChannel(t *testing.T) {
	var _ SendableChannel = (*DiscordChannel)(nil)
}

func TestDiscordChannelRegistersWithGateway(t *testing.T) {
	handler := func(_ context.Context, msg IncomingMessage) (OutgoingMessage, error) {
		return OutgoingMessage{Text: "ok"}, nil
	}
	gw := New(handler, WithLogger(testLogger()))
	dc := NewDiscordChannel(WithDiscordChannelLogger(testLogger()))

	if err := gw.Register(dc); err != nil {
		t.Fatalf("register discord channel: %v", err)
	}

	names := gw.Channels()
	if len(names) != 1 || names[0] != "discord" {
		t.Fatalf("expected [discord], got %v", names)
	}

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gw.Stop()

	// Send through gateway.
	msg := OutgoingMessage{
		ChannelName: "discord",
		RecipientID: "user-1",
		Text:        "test message",
	}
	if err := gw.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestDiscordAndWebChannelCoexist(t *testing.T) {
	handler := func(_ context.Context, msg IncomingMessage) (OutgoingMessage, error) {
		return OutgoingMessage{
			ChannelName: msg.ChannelName,
			RecipientID: msg.SenderID,
			Text:        "ack from " + msg.ChannelName,
		}, nil
	}
	gw := New(handler, WithLogger(testLogger()))

	web := NewWebChannel(gw, WithWebChannelLogger(testLogger()))
	discord := NewDiscordChannel(WithDiscordChannelLogger(testLogger()))

	if err := gw.Register(web); err != nil {
		t.Fatalf("register web: %v", err)
	}
	if err := gw.Register(discord); err != nil {
		t.Fatalf("register discord: %v", err)
	}

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gw.Stop()

	// Route through web.
	resp, err := web.ProcessHTTPMessage(context.Background(), "u1", 10, "hello")
	if err != nil {
		t.Fatalf("web process: %v", err)
	}
	if resp.Text != "ack from web" {
		t.Fatalf("unexpected web response: %s", resp.Text)
	}

	// Route through discord via gateway.
	discordMsg := IncomingMessage{
		ChannelName: "discord",
		SenderID:    "u2",
		Text:        "hey",
	}
	resp, err = gw.HandleMessage(context.Background(), discordMsg)
	if err != nil {
		t.Fatalf("discord handle: %v", err)
	}
	if resp.Text != "ack from discord" {
		t.Fatalf("unexpected discord response: %s", resp.Text)
	}
}
