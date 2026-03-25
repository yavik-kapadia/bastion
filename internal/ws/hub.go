// Package ws provides a WebSocket hub for real-time metric broadcasting.
package ws

import (
	"context"
	"log/slog"
	"sync"
)

// Hub manages connected WebSocket clients and fans out broadcast messages to all of them.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}

	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
}

// NewHub creates a Hub ready to Run.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client, 16),
		unregister: make(chan *Client, 16),
		broadcast:  make(chan []byte, 64),
	}
}

// Run processes register/unregister/broadcast events until ctx is cancelled.
// Call this in a dedicated goroutine.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
			slog.Debug("ws: client registered", "total", h.clientCount())

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
			slog.Debug("ws: client unregistered", "total", h.clientCount())

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Client is too slow — drop the message for this client.
					// It will receive the next broadcast tick instead.
				}
			}
			h.mu.RUnlock()

		case <-ctx.Done():
			// Close all clients on shutdown.
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		}
	}
}

// Broadcast enqueues a message to be sent to all connected clients.
// Non-blocking: if the broadcast channel is full, the message is dropped.
func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
		slog.Debug("ws: broadcast channel full, dropping message")
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(c *Client) {
	h.register <- c
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

// ClientCount returns the number of connected WebSocket clients.
func (h *Hub) ClientCount() int {
	return h.clientCount()
}

func (h *Hub) clientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
