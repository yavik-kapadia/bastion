package relay

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	srt "github.com/datarhei/gosrt"
)

// subscriber holds a single subscriber connection and its packet channel.
// closeOnce ensures the channel is closed exactly once regardless of which
// path (writePump exit or publisher disconnect) initiates the close.

// Stream manages a single named SRT relay: one publisher, N subscribers.
// Each subscriber gets a buffered ring-buffer channel; slow consumers have
// their oldest packets overwritten rather than being disconnected.
type Stream struct {
	name    string
	bufSize int

	mu          sync.RWMutex
	publisher   srt.Conn
	subscribers map[uint32]*subscriber
	nextSubID   uint32

	cancel context.CancelFunc

	// Stats counters updated atomically.
	bytesRelayed    atomic.Uint64
	packetsDropped  atomic.Uint64
	subscriberCount atomic.Int32

	createdAt time.Time
}

type subscriber struct {
	id        uint32
	conn      srt.Conn
	ch        chan []byte
	closeOnce sync.Once
}

func (sub *subscriber) closeCh() {
	sub.closeOnce.Do(func() { close(sub.ch) })
}

func newStream(name string, bufSize int) *Stream {
	return &Stream{
		name:        name,
		bufSize:     bufSize,
		subscribers: make(map[uint32]*subscriber),
		createdAt:   time.Now(),
	}
}

// SetPublisher attaches the publishing connection and starts the relay loop.
// Returns an error if a publisher is already active.
func (s *Stream) SetPublisher(ctx context.Context, conn srt.Conn) error {
	s.mu.Lock()
	if s.publisher != nil {
		s.mu.Unlock()
		return fmt.Errorf("stream %q already has an active publisher", s.name)
	}
	s.publisher = conn
	ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	go s.relayLoop(ctx)
	return nil
}

// AddSubscriber registers a new subscriber and starts its write pump.
// Returns the subscriber ID.
func (s *Stream) AddSubscriber(ctx context.Context, conn srt.Conn) uint32 {
	id := atomic.AddUint32(&s.nextSubID, 1)
	sub := &subscriber{
		id:   id,
		conn: conn,
		ch:   make(chan []byte, s.bufSize),
	}

	s.mu.Lock()
	s.subscribers[id] = sub
	s.mu.Unlock()
	s.subscriberCount.Add(1)

	go s.writePump(ctx, sub)
	return id
}

// RemoveSubscriber unregisters and closes a subscriber connection.
func (s *Stream) RemoveSubscriber(id uint32) {
	s.mu.Lock()
	sub, ok := s.subscribers[id]
	if ok {
		delete(s.subscribers, id)
	}
	s.mu.Unlock()
	if ok {
		s.subscriberCount.Add(-1)
		sub.closeCh()
		sub.conn.Close()
	}
}

// Close shuts down the stream: closes the publisher connection and drops all subscribers.
func (s *Stream) Close() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	pub := s.publisher
	s.publisher = nil
	subs := s.subscribers
	s.subscribers = make(map[uint32]*subscriber)
	s.mu.Unlock()

	if pub != nil {
		pub.Close()
	}
	for _, sub := range subs {
		sub.closeCh()
		sub.conn.Close()
	}
	s.subscriberCount.Store(0)
}

// SRTStats holds SRT protocol-level statistics collected from live connections.
type SRTStats struct {
	MsRTT            float64 // Smoothed RTT to publisher in milliseconds
	SendLossRate     float64 // Publisher send-path loss rate (0–100)
	RecvBitrateMbps  float64 // Inbound bitrate from publisher in Mbps
	SendBitrateMbps  float64 // Outbound bitrate to all subscribers combined in Mbps
	PktRetrans       uint64  // Total retransmitted packets on the publish path
	PktUndecrypt     uint64  // Total failed decryptions across all connections
}

// StreamStats returns a snapshot of current stream statistics.
type StreamStats struct {
	Name            string
	SubscriberCount int
	BytesRelayed    uint64
	PacketsDropped  uint64
	HasPublisher    bool
	CreatedAt       time.Time
	SRT             SRTStats
}

func (s *Stream) Stats() StreamStats {
	s.mu.RLock()
	hasPub := s.publisher != nil
	s.mu.RUnlock()
	return StreamStats{
		Name:            s.name,
		SubscriberCount: int(s.subscriberCount.Load()),
		BytesRelayed:    s.bytesRelayed.Load(),
		PacketsDropped:  s.packetsDropped.Load(),
		HasPublisher:    hasPub,
		CreatedAt:       s.createdAt,
		SRT:             s.collectSRTStats(),
	}
}

// collectSRTStats reads live SRT protocol statistics from the publisher and
// all subscriber connections. Called under no lock — grabs a snapshot of conn
// pointers under RLock, then calls Stats() outside the lock to avoid holding
// the mutex during potentially slow SRT internal calls.
func (s *Stream) collectSRTStats() SRTStats {
	s.mu.RLock()
	pub := s.publisher
	subs := make([]*subscriber, 0, len(s.subscribers))
	for _, sub := range s.subscribers {
		subs = append(subs, sub)
	}
	s.mu.RUnlock()

	var out SRTStats
	if pub != nil {
		var st srt.Statistics
		pub.Stats(&st)
		out.MsRTT = st.Instantaneous.MsRTT
		out.SendLossRate = st.Instantaneous.PktSendLossRate
		out.RecvBitrateMbps = st.Instantaneous.MbpsRecvRate
		out.PktRetrans = st.Accumulated.PktRetrans
		out.PktUndecrypt = st.Accumulated.PktRecvUndecrypt
	}
	for _, sub := range subs {
		var st srt.Statistics
		sub.conn.Stats(&st)
		out.SendBitrateMbps += st.Instantaneous.MbpsSentRate
		out.PktUndecrypt += st.Accumulated.PktRecvUndecrypt
	}
	return out
}

// relayLoop reads packets from the publisher and fans them out to all subscribers.
// It uses a sync.Pool to reuse packet buffers and a ring-buffer strategy for
// slow consumers: if a subscriber's channel is full, the oldest packet is dropped
// and the new one is enqueued (rather than disconnecting the subscriber).
// When the publisher disconnects, all subscriber connections are closed so that
// subscriber read loops receive EOF rather than blocking indefinitely.
func (s *Stream) relayLoop(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.publisher = nil
		// Snapshot subscribers; leave them in the map so RemoveSubscriber
		// can still clean up (the once.Do prevents double-close).
		subs := make([]*subscriber, 0, len(s.subscribers))
		for _, sub := range s.subscribers {
			subs = append(subs, sub)
		}
		s.mu.Unlock()

		// Close each subscriber's connection and channel. writePump goroutines
		// will unblock (either Write fails or channel read sees ok=false),
		// call RemoveSubscriber, and exit cleanly.
		for _, sub := range subs {
			sub.conn.Close() // unblocks writePump if blocked in Write
			sub.closeCh()    // unblocks writePump if blocked on channel receive
		}
		slog.Info("relay: publisher disconnected", "stream", s.name)
	}()

	pool := &sync.Pool{New: func() any { return make([]byte, 2048) }}

	for {
		buf := pool.Get().([]byte)
		n, err := s.publisher.Read(buf)
		if err != nil {
			pool.Put(buf)
			if ctx.Err() != nil {
				return // shutting down
			}
			slog.Info("relay: publisher read error", "stream", s.name, "err", err)
			return
		}
		if n == 0 {
			pool.Put(buf)
			continue
		}

		// Make a copy to send to subscribers; return the pool buffer.
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		pool.Put(buf)

		s.bytesRelayed.Add(uint64(n))

		s.mu.RLock()
		for _, sub := range s.subscribers {
			select {
			case sub.ch <- pkt:
				// delivered
			default:
				// Ring-buffer: drop oldest, enqueue new.
				select {
				case <-sub.ch:
					s.packetsDropped.Add(1)
				default:
				}
				select {
				case sub.ch <- pkt:
				default:
				}
			}
		}
		s.mu.RUnlock()
	}
}

// writePump drains a subscriber's channel and writes to its SRT connection.
func (s *Stream) writePump(ctx context.Context, sub *subscriber) {
	defer func() {
		s.RemoveSubscriber(sub.id)
		slog.Debug("relay: subscriber disconnected", "stream", s.name, "sub_id", sub.id)
	}()

	for {
		select {
		case pkt, ok := <-sub.ch:
			if !ok {
				return
			}
			if _, err := sub.conn.Write(pkt); err != nil {
				if ctx.Err() == nil {
					slog.Debug("relay: subscriber write error", "stream", s.name, "err", err)
				}
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
