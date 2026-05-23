package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	srt "github.com/datarhei/gosrt"
)

// AuthFunc is called before accepting a connection. It receives the parsed
// StreamID and the remote address. It may return a passphrase to apply to
// the connection (for per-stream encryption) or an error to reject.
// A nil AuthFunc allows all connections with no encryption.
type AuthFunc func(sid *StreamID, remoteAddr net.Addr) (passphrase string, err error)

// Config holds SRT-level tuning parameters for the relay listener.
type Config struct {
	// Latency is the SRT latency window applied globally.
	// A zero value uses the gosrt default (120ms).
	Latency time.Duration
	// MaxBW is the maximum sending bandwidth in bytes/sec.
	// -1 = unlimited (gosrt default).
	MaxBW int64
	// MaxConcurrentStreams caps the number of distinct streams the relay
	// will track at once. Excess publish/request attempts get rejected.
	// 0 = unlimited.
	MaxConcurrentStreams int
}

// PublisherDisconnectFunc is invoked after a publisher session ends (i.e.
// after WaitForPublisherExit returns). stream is the stream name, remote is
// the publisher's remote address as observed at attach time, and duration is
// the elapsed wall-clock time of the session. Implementations must be safe
// to call from any goroutine and should not block — the relay calls this
// inline on the publisher's lifecycle goroutine.
type PublisherDisconnectFunc func(stream string, remote string, duration time.Duration)

// Relay is the core SRT relay engine: it accepts incoming connections,
// routes publishers and subscribers to named streams, and fans out packets.
type Relay struct {
	addr    string
	bufSize int
	auth    AuthFunc
	cfg     Config

	// OnPublisherDisconnect, if non-nil, is invoked after each publisher
	// session ends. Optional — leave nil to disable. Set before Start.
	OnPublisherDisconnect PublisherDisconnectFunc

	mu      sync.RWMutex
	streams map[string]*Stream
}

// New creates a new Relay that will listen on addr.
// bufSize is the per-subscriber ring-buffer capacity in packets.
func New(addr string, bufSize int, auth AuthFunc, cfg Config) *Relay {
	return &Relay{
		addr:    addr,
		bufSize: bufSize,
		auth:    auth,
		cfg:     cfg,
		streams: make(map[string]*Stream),
	}
}

// Start begins listening for SRT connections. It blocks until ctx is cancelled
// or a fatal listen error occurs.
func (r *Relay) Start(ctx context.Context) error {
	srtCfg := srt.DefaultConfig()
	srtCfg.Logger = srt.NewLogger(nil)
	if r.cfg.Latency > 0 {
		srtCfg.PeerLatency = r.cfg.Latency
		srtCfg.ReceiverLatency = r.cfg.Latency
	}
	if r.cfg.MaxBW != 0 {
		srtCfg.MaxBW = r.cfg.MaxBW
	}

	ln, err := srt.Listen("srt", r.addr, srtCfg)
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

	// Subscribers tagged w=internal in the streamid are our own workers
	// (thumbnail capture, ffprobe media-info). They're invisible to the
	// public subscriber count and logged at debug level only — keeps the
	// operational log clean of the every-10s thumbnail-fetch chatter.
	internal := sid.Mode == ModeRequest && sid.Internal

	if internal {
		slog.Debug("relay: internal worker connection",
			"stream", sid.Name,
			"mode", sid.Mode,
			"remote", req.RemoteAddr(),
		)
	} else {
		slog.Info("relay: new connection",
			"stream", sid.Name,
			"mode", sid.Mode,
			"remote", req.RemoteAddr(),
		)
	}

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
		r.handleSubscriber(ctx, conn, sid.Name, internal)
	}
}

func (r *Relay) handlePublisher(ctx context.Context, conn srt.Conn, name string) {
	r.mu.Lock()
	s, exists := r.streams[name]
	if !exists {
		if r.cfg.MaxConcurrentStreams > 0 && len(r.streams) >= r.cfg.MaxConcurrentStreams {
			r.mu.Unlock()
			slog.Warn("relay: publisher rejected — max_concurrent_streams reached",
				"stream", name, "limit", r.cfg.MaxConcurrentStreams)
			conn.Close()
			return
		}
		s = newStream(name, r.bufSize)
		r.streams[name] = s
	}
	r.mu.Unlock()

	// Capture publisher identity for the disconnect callback before SetPublisher
	// takes ownership of conn. Once relayLoop runs, the connection may be torn
	// down concurrently and RemoteAddr would race.
	var remoteAddr string
	if ra := conn.RemoteAddr(); ra != nil {
		remoteAddr = ra.String()
	}
	startedAt := time.Now()

	if err := s.SetPublisher(ctx, conn); err != nil {
		slog.Warn("relay: publisher rejected", "stream", name, "err", err)
		conn.Close()
		return
	}
	slog.Info("relay: publisher started", "stream", name)

	// Wait for the publisher's read path to complete in relayLoop, then GC
	// the stream entry if no subscribers remain. Without this, r.streams
	// accumulates zombie entries for every transient stream name ever seen.
	s.WaitForPublisherExit()

	// Fire the disconnect callback, if wired. Done before gcStream so the
	// stream entry is still observable to any synchronous handler that wants
	// to consult it (currently none do, but it preserves the option).
	if cb := r.OnPublisherDisconnect; cb != nil {
		cb(name, remoteAddr, time.Since(startedAt))
	}

	r.gcStream(name)
}

// gcStream removes a stream from r.streams iff it has no publisher and no
// subscribers. Called after a publisher disconnects (and periodically by
// other paths if needed). Safe to call concurrently — checks under lock.
func (r *Relay) gcStream(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.streams[name]
	if !ok {
		return
	}
	if !s.idle() {
		return
	}
	delete(r.streams, name)
}

func (r *Relay) handleSubscriber(ctx context.Context, conn srt.Conn, name string, internal bool) {
	r.mu.Lock()
	s, exists := r.streams[name]
	if !exists {
		if r.cfg.MaxConcurrentStreams > 0 && len(r.streams) >= r.cfg.MaxConcurrentStreams {
			r.mu.Unlock()
			slog.Warn("relay: subscriber rejected — max_concurrent_streams reached",
				"stream", name, "limit", r.cfg.MaxConcurrentStreams)
			conn.Close()
			return
		}
		// Create a placeholder stream so the subscriber can wait for a publisher.
		s = newStream(name, r.bufSize)
		r.streams[name] = s
	}
	r.mu.Unlock()

	if internal {
		s.AddInternalSubscriber(ctx, conn)
		slog.Debug("relay: internal worker attached", "stream", name, "sub_id", "internal")
		return
	}
	subID := s.AddSubscriber(ctx, conn)
	slog.Info("relay: subscriber added", "stream", name, "sub_id", subID)
}

