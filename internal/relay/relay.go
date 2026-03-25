package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	srt "github.com/datarhei/gosrt"
)

// AuthFunc is called before accepting a connection. It receives the parsed
// StreamID and the remote address. It may return a passphrase to apply to
// the connection (for per-stream encryption) or an error to reject.
// A nil AuthFunc allows all connections with no encryption.
type AuthFunc func(sid *StreamID, remoteAddr net.Addr) (passphrase string, err error)

// Relay is the core SRT relay engine: it accepts incoming connections,
// routes publishers and subscribers to named streams, and fans out packets.
type Relay struct {
	addr    string
	bufSize int
	auth    AuthFunc

	mu      sync.RWMutex
	streams map[string]*Stream
}

// New creates a new Relay that will listen on addr.
// bufSize is the per-subscriber ring-buffer capacity in packets.
func New(addr string, bufSize int, auth AuthFunc) *Relay {
	return &Relay{
		addr:    addr,
		bufSize: bufSize,
		auth:    auth,
		streams: make(map[string]*Stream),
	}
}

// Start begins listening for SRT connections. It blocks until ctx is cancelled
// or a fatal listen error occurs.
func (r *Relay) Start(ctx context.Context) error {
	cfg := srt.DefaultConfig()
	cfg.Logger = srt.NewLogger(nil)

	ln, err := srt.Listen("srt", r.addr, cfg)
	if err != nil {
		return fmt.Errorf("srt listen %s: %w", r.addr, err)
	}
	slog.Info("relay: listening", "addr", r.addr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		req, err := ln.Accept2()
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			slog.Error("relay: accept error", "err", err)
			continue
		}
		if req == nil {
			continue
		}
		go r.handleRequest(ctx, req)
	}
}

// ActiveStreams returns a snapshot of currently registered streams.
func (r *Relay) ActiveStreams() map[string]StreamStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]StreamStats, len(r.streams))
	for name, s := range r.streams {
		out[name] = s.Stats()
	}
	return out
}

// StreamStats returns stats for a single named stream.
func (r *Relay) StreamStats(name string) (StreamStats, bool) {
	r.mu.RLock()
	s, ok := r.streams[name]
	r.mu.RUnlock()
	if !ok {
		return StreamStats{}, false
	}
	return s.Stats(), true
}

// handleRequest processes a single incoming SRT connection request.
func (r *Relay) handleRequest(ctx context.Context, req srt.ConnRequest) {
	sid, err := ParseStreamID(req.StreamId())
	if err != nil {
		slog.Warn("relay: invalid stream ID", "raw", req.StreamId(), "err", err)
		req.Reject(srt.REJ_BADSECRET)
		return
	}

	slog.Info("relay: new connection",
		"stream", sid.Name,
		"mode", sid.Mode,
		"remote", req.RemoteAddr(),
	)

	// Run auth / encryption lookup.
	passphrase := sid.Passphrase
	if r.auth != nil {
		pp, err := r.auth(sid, req.RemoteAddr())
		if err != nil {
			slog.Warn("relay: connection rejected by auth",
				"stream", sid.Name, "remote", req.RemoteAddr(), "err", err)
			req.Reject(srt.REJ_BADSECRET)
			return
		}
		if pp != "" {
			passphrase = pp
		}
	}

	// Apply passphrase before accepting.
	if passphrase != "" {
		if err := req.SetPassphrase(passphrase); err != nil {
			slog.Warn("relay: failed to set passphrase", "stream", sid.Name, "err", err)
			req.Reject(srt.REJ_BADSECRET)
			return
		}
	}

	conn, err := req.Accept()
	if err != nil {
		slog.Warn("relay: accept failed", "stream", sid.Name, "err", err)
		return
	}

	switch sid.Mode {
	case ModePublish:
		r.handlePublisher(ctx, conn, sid.Name)
	case ModeRequest:
		r.handleSubscriber(ctx, conn, sid.Name)
	}
}

func (r *Relay) handlePublisher(ctx context.Context, conn srt.Conn, name string) {
	r.mu.Lock()
	s, exists := r.streams[name]
	if !exists {
		s = newStream(name, r.bufSize)
		r.streams[name] = s
	}
	r.mu.Unlock()

	if err := s.SetPublisher(ctx, conn); err != nil {
		slog.Warn("relay: publisher rejected", "stream", name, "err", err)
		conn.Close()
		return
	}
	slog.Info("relay: publisher started", "stream", name)

	// Block until the publisher disconnects, then clean up empty streams.
	// SetPublisher starts the relay loop in a goroutine; we wait for the
	// connection to close by waiting for the stream to lose its publisher.
	// We detect this via the conn's read path completing in relayLoop.
}

func (r *Relay) handleSubscriber(ctx context.Context, conn srt.Conn, name string) {
	r.mu.Lock()
	s, exists := r.streams[name]
	if !exists {
		// Create a placeholder stream so the subscriber can wait for a publisher.
		s = newStream(name, r.bufSize)
		r.streams[name] = s
	}
	r.mu.Unlock()

	subID := s.AddSubscriber(ctx, conn)
	slog.Info("relay: subscriber added", "stream", name, "sub_id", subID)
}
