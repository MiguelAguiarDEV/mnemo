package gateway

import (
	"context"
	"fmt"
	"testing"
)

func TestWebChannelName(t *testing.T) {
	gw := New(nil, WithLogger(testLogger()))
	wc := NewWebChannel(gw, WithWebChannelLogger(testLogger()))
	if wc.Name() != "web" {
		t.Fatalf("expected name 'web', got %q", wc.Name())
	}
}

func TestWebChannelStartStop(t *testing.T) {
	gw := New(nil, WithLogger(testLogger()))
	wc := NewWebChannel(gw, WithWebChannelLogger(testLogger()))

	if err := wc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := wc.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestWebChannelProcessHTTPMessage(t *testing.T) {
	handler := func(_ context.Context, msg IncomingMessage) (OutgoingMessage, error) {
		if msg.ChannelName != "web" {
			return OutgoingMessage{}, fmt.Errorf("unexpected channel: %s", msg.ChannelName)
		}
		if msg.SenderID != "user-42" {
			return OutgoingMessage{}, fmt.Errorf("unexpected sender: %s", msg.SenderID)
		}
		convID := msg.Metadata["conversation_id"]
		if convID != "123" {
			return OutgoingMessage{}, fmt.Errorf("unexpected conversation_id: %s", convID)
		}
		return OutgoingMessage{
			ChannelName: "web",
			RecipientID: msg.SenderID,
			Text:        "response to: " + msg.Text,
			Format:      FormatMarkdown,
		}, nil
	}

	gw := New(handler, WithLogger(testLogger()))
	wc := NewWebChannel(gw, WithWebChannelLogger(testLogger()))

	resp, err := wc.ProcessHTTPMessage(context.Background(), "user-42", 123, "hello jarvis")
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if resp.Text != "response to: hello jarvis" {
		t.Fatalf("unexpected response: %s", resp.Text)
	}
	if resp.ChannelName != "web" {
		t.Fatalf("unexpected channel: %s", resp.ChannelName)
	}
	if resp.RecipientID != "user-42" {
		t.Fatalf("unexpected recipient: %s", resp.RecipientID)
	}
}

func TestWebChannelProcessHTTPMessageHandlerError(t *testing.T) {
	handler := func(_ context.Context, _ IncomingMessage) (OutgoingMessage, error) {
		return OutgoingMessage{}, fmt.Errorf("orchestrator unavailable")
	}

	gw := New(handler, WithLogger(testLogger()))
	wc := NewWebChannel(gw, WithWebChannelLogger(testLogger()))

	_, err := wc.ProcessHTTPMessage(context.Background(), "user-1", 1, "hello")
	if err == nil {
		t.Fatal("expected error from handler")
	}
}

func TestWebChannelRegistersWithGateway(t *testing.T) {
	handler := func(_ context.Context, msg IncomingMessage) (OutgoingMessage, error) {
		return OutgoingMessage{Text: "ok"}, nil
	}
	gw := New(handler, WithLogger(testLogger()))
	wc := NewWebChannel(gw, WithWebChannelLogger(testLogger()))

	if err := gw.Register(wc); err != nil {
		t.Fatalf("register webchannel: %v", err)
	}

	names := gw.Channels()
	if len(names) != 1 || names[0] != "web" {
		t.Fatalf("expected [web], got %v", names)
	}

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gw.Stop()

	// Process a message through the WebChannel.
	resp, err := wc.ProcessHTTPMessage(context.Background(), "u1", 10, "test")
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("unexpected response: %s", resp.Text)
	}
}
