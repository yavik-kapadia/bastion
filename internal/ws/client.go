package ws

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

const (
	writeTimeout = 10 * time.Second
	sendBufSize  = 32
)

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// Handler upgrades the HTTP connection to WebSocket, registers the client with
// the hub, and starts its read and write pumps.
func Handler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // CORS handled by chi middleware
		})
		if err != nil {
			slog.Warn("ws: upgrade failed", "err", err)
			return
		}

		c := &Client{
			hub:  hub,
			conn: conn,
			send: make(chan []byte, sendBufSize),
		}
		hub.Register(c)
		go c.writePump(r.Context())
		c.readPump(r.Context()) // blocks until client disconnects
	}
}

// writePump sends queued messages to the WebSocket connection.
func (c *Client) writePump(ctx context.Context) {
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				// Hub closed the channel — send close frame.
				c.conn.Close(websocket.StatusNormalClosure, "server shutdown")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.conn.Close(websocket.StatusInternalError, "write error")
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// readPump discards incoming messages (dashboard → server messages are not used yet)
// and detects disconnection.
func (c *Client) readPump(ctx context.Context) {
	defer c.hub.Unregister(c)
	for {
		_, _, err := c.conn.Read(ctx)
		if err != nil {
			return // client disconnected or context cancelled
		}
	}
}
