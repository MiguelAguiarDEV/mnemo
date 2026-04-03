// Package events provides a Postgres LISTEN/NOTIFY wrapper for real-time
// event distribution in the JARVIS dashboard.
package events

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/lib/pq"
)

// Bus wraps Postgres LISTEN/NOTIFY for real-time event distribution.
// Subscribe(channel) returns a Go channel that receives payloads.
// Publish(channel, payload) sends a NOTIFY via the main connection pool.
type Bus struct {
	dsn      string
	listener *pq.Listener
	db       *sql.DB

	mu   sync.Mutex
	subs map[string][]chan string // channel name -> subscriber chans
	done chan struct{}
}

// New creates a new Bus. It opens a dedicated connection for LISTEN
// (separate from the connection pool) and a pooled *sql.DB for PUBLISH.
func New(dsn string) (*Bus, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("events: open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("events: ping db: %w", err)
	}

	listener := pq.NewListener(dsn, 10*time.Second, time.Minute, nil)

	b := &Bus{
		dsn:      dsn,
		listener: listener,
		db:       db,
		subs:     make(map[string][]chan string),
		done:     make(chan struct{}),
	}

	go b.dispatch()
	return b, nil
}

// Subscribe listens on a Postgres NOTIFY channel and returns a Go channel
// that receives payloads. The returned channel is buffered (capacity 64)
// to avoid blocking the dispatch loop on slow consumers.
func (b *Bus) Subscribe(channel string) (<-chan string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Only LISTEN if this is the first subscriber for this channel.
	if len(b.subs[channel]) == 0 {
		if err := b.listener.Listen(channel); err != nil {
			return nil, fmt.Errorf("events: listen %s: %w", channel, err)
		}
	}

	ch := make(chan string, 64)
	b.subs[channel] = append(b.subs[channel], ch)
	return ch, nil
}

// Publish sends a NOTIFY on the given channel with the given payload.
func (b *Bus) Publish(channel, payload string) error {
	_, err := b.db.Exec("SELECT pg_notify($1, $2)", channel, payload)
	if err != nil {
		return fmt.Errorf("events: publish %s: %w", channel, err)
	}
	return nil
}

// Close shuts down the listener and closes the database connection.
func (b *Bus) Close() {
	close(b.done)

	b.mu.Lock()
	for _, subs := range b.subs {
		for _, ch := range subs {
			close(ch)
		}
	}
	b.subs = nil
	b.mu.Unlock()

	b.listener.Close()
	b.db.Close()
}

// dispatch runs in a goroutine, reading notifications from the pq.Listener
// and fanning them out to all subscribers for that channel.
func (b *Bus) dispatch() {
	for {
		select {
		case <-b.done:
			return
		case n := <-b.listener.Notify:
			if n == nil {
				// Reconnection event — pq sends nil on reconnect.
				continue
			}
			b.mu.Lock()
			subs := b.subs[n.Channel]
			b.mu.Unlock()

			for _, ch := range subs {
				select {
				case ch <- n.Extra:
				default:
					// Drop if subscriber is full — avoid blocking dispatch.
				}
			}
		}
	}
}
