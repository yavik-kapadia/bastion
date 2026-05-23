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

// pubState tracks the lifecycle of a Stream's publisher slot.
//
// Why a state machine: the previous code used a bare `s.publisher != nil`
// check, which let a reconnecting publisher attach mid-teardown (while the
// previous relayLoop's defer was still closing subscriber channels). New
// fan-out then raced against `close(sub.ch)` — a panic on send to a closed
// channel was reachable, and viewers could end up wired to a stale session.
type pubState int

const (
	pubIdle     pubState = iota // no publisher; SetPublisher allowed
	pubActive                   // relayLoop running
	pubDraining                 // relayLoop returned; subscribers being torn down
)

// Stream manages a single named SRT relay: one publisher, N subscribers.
// Each subscriber gets a buffered ring-buffer channel; slow consumers have
// their oldest packets overwritten rather than being disconnected.
type Stream struct {
	name    string
	bufSize int

	mu          sync.RWMutex
	publisher   srt.Conn
	pubState    pubState
	subscribers map[uint32]*subscriber
	nextSubID   uint32

	cancel context.CancelFunc
	// pubDone is closed by relayLoop's defer when teardown is fully complete.
	// Recreated by SetPublisher on each new session so callers can wait on
	// "this publisher has finished" without races. Reads must take s.mu.
	pubDone chan struct{}

	// publisherSessionID increments on every successful SetPublisher. External
	// observers (e.g. the API media-info probe) cache results against this ID
	// so they can detect when the publisher reconnected and refresh.
	publisherSessionID atomic.Uint64

	// Stats counters updated atomically.
	bytesRelayed    atomic.Uint64
	packetsDropped  atomic.Uint64
	subscriberCount atomic.Int32

	createdAt time.Time
}

// subscriber holds a single subscriber connection and its packet channel.
// closeOnce ensures the channel is closed exactly once regardless of which
// path (writePump exit or publisher disconnect) initiates the close.
// done is closed when the writePump goroutine exits, letting relayLoop's
// defer wait on a bounded set of subs without coupling lifetimes globally.
// internal=true subscribers (thumbnail capture, ffprobe media-info) are
// excluded from subscriberCount so the public stat tracks real viewers,
// and are logged at debug level only.
type subscriber struct {
	id        uint32
	conn      srt.Conn
	ch        chan []byte
	done      chan struct{}
	closeOnce sync.Once
	internal  bool
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
// Returns an error if a publisher is already active OR if the previous
// publisher's teardown is still in flight (pubDraining). Callers should treat
// pubDraining as transient — gosrt's Close cancels the publisher's read ctx,
// which unblocks Read in the prior relayLoop, which fires the defer that
// returns us to pubIdle.
func (s *Stream) SetPublisher(ctx context.Context, conn srt.Conn) error {
	s.mu.Lock()
	if s.pubState != pubIdle {
		s.mu.Unlock()
		return fmt.Errorf("stream %q already has an active publisher", s.name)
	}
	s.pubState = pubActive
	s.publisher = conn
	s.pubDone = make(chan struct{})
	s.publisherSessionID.Add(1)
	ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	go s.relayLoop(ctx)
	return nil
}

// WaitForPublisherExit blocks until the most recent publisher session's
// relayLoop has fully torn down (subscribers drained, pubState back to
// pubIdle). Returns immediately if no publisher is active.
func (s *Stream) WaitForPublisherExit() {
	s.mu.RLock()
	done := s.pubDone
	s.mu.RUnlock()
	if done != nil {
		<-done
	}
}

// AddSubscriber registers a new external viewer and starts its write pump.
// Returns the subscriber ID. Bumps subscriberCount.
func (s *Stream) AddSubscriber(ctx context.Context, conn srt.Conn) uint32 {
	return s.addSubscriber(ctx, conn, false)
}

// AddInternalSubscriber registers an internal worker (thumbnail capture,
// ffprobe media-info) as a subscriber. Behavior is identical to
// AddSubscriber except subscriberCount is NOT bumped — these connections
// are invisible to the public viewer count.
func (s *Stream) AddInternalSubscriber(ctx context.Context, conn srt.Conn) uint32 {
	return s.addSubscriber(ctx, conn, true)
}

func (s *Stream) addSubscriber(ctx context.Context, conn srt.Conn, internal bool) uint32 {
	id := atomic.AddUint32(&s.nextSubID, 1)
	sub := &subscriber{
		id:       id,
		conn:     conn,
		ch:       make(chan []byte, s.bufSize),
		done:     make(chan struct{}),
		internal: internal,
	}

	s.mu.Lock()
	s.subscribers[id] = sub
	s.mu.Unlock()
	if !internal {
		s.subscriberCount.Add(1)
	}

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
		if !sub.internal {
			s.subscriberCount.Add(-1)
		}
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
	MsRTT           float64 // Smoothed RTT to publisher in milliseconds
	SendLossRate    float64 // Publisher send-path loss rate (0–100)
	RecvBitrateMbps float64 // Inbound bitrate from publisher in Mbps
	SendBitrateMbps float64 // Outbound bitrate to all subscribers combined in Mbps
	PktRetrans      uint64  // Total retransmitted packets on the publish path
	PktUndecrypt    uint64  // Total failed decryptions across all connections
}

// StreamStats returns a snapshot of current stream statistics.
type StreamStats struct {
	Name               string
	SubscriberCount    int
	BytesRelayed       uint64
	PacketsDropped     uint64
	HasPublisher       bool
	PublisherSessionID uint64 // 0 if no publisher has ever attached
	CreatedAt          time.Time
	SRT                SRTStats
}

func (s *Stream) Stats() StreamStats {
	s.mu.RLock()
	hasPub := s.publisher != nil
	s.mu.RUnlock()
	return StreamStats{
		Name:               s.name,
		SubscriberCount:    int(s.subscriberCount.Load()),
		BytesRelayed:       s.bytesRelayed.Load(),
		PacketsDropped:     s.packetsDropped.Load(),
		HasPublisher:       hasPub,
		PublisherSessionID: s.publisherSessionID.Load(),
		CreatedAt:          s.createdAt,
		SRT:                s.collectSRTStats(),
	}
}

// idle reports true when the stream has no publisher and no subscribers.
// Used by the Relay to garbage-collect empty entries from r.streams.
func (s *Stream) idle() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pubState == pubIdle && len(s.subscribers) == 0
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

// pumpDrainTimeout caps how long relayLoop waits for snapshotted writePumps
// to exit during teardown. gosrt's Close cancels the conn ctx and unblocks
// Write, so in practice all pumps exit within milliseconds. The timeout is a
// safety belt against future gosrt bugs — it ensures pubState always returns
// to pubIdle so the next publisher can attach.
const pumpDrainTimeout = 5 * time.Second

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
		s.pubState = pubDraining
		// Snapshot subscribers; leave them in the map so RemoveSubscriber
		// can still clean up (the once.Do prevents double-close).
		subs := make([]*subscriber, 0, len(s.subscribers))
		for _, sub := range s.subscribers {
			subs = append(subs, sub)
		}
		s.mu.Unlock()

		// Close each subscriber's connection and channel. writePump goroutines
		// will unblock (Write fails when conn ctx is cancelled, or channel read
		// sees ok=false), call RemoveSubscriber, and exit cleanly.
		for _, sub := range subs {
			sub.conn.Close() // unblocks writePump if blocked in Write
			sub.closeCh()    // unblocks writePump if blocked on channel receive
		}

		// Wait for each snapshotted pump to signal exit, bounded by
		// pumpDrainTimeout. We only wait on subs that existed at snapshot time;
		// any new subscriber added during draining (rare — they'd attach to a
		// stream with pubState != pubIdle and no publisher) survives into the
		// next session.
		deadline := time.NewTimer(pumpDrainTimeout)
		defer deadline.Stop()
		for _, sub := range subs {
			select {
			case <-sub.done:
			case <-deadline.C:
				slog.Warn("relay: writePump drain timeout",
					"stream", s.name, "sub_id", sub.id)
				// Don't break — try the remaining sub.done channels in case
				// they're already closed. select will pick them immediately.
			}
		}

		s.mu.Lock()
		s.pubState = pubIdle
		done := s.pubDone
		s.pubDone = nil
		s.mu.Unlock()

		if done != nil {
			close(done)
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
			s.deliver(sub, pkt)
		}
		s.mu.RUnlock()
	}
}

// deliver pushes a packet to a subscriber's ring-buffer channel. If the
// channel is full it drops the oldest packet and enqueues the new one.
// Sends are guarded by recover() because a writePump that's already exited
// may have closed the channel concurrent with this send — panicking on a
// closed channel send would otherwise crash the relay.
func (s *Stream) deliver(sub *subscriber, pkt []byte) {
	defer func() {
		if r := recover(); r != nil {
			// Channel was closed by writePump's exit path (or by
			// closeCh from a teardown). Sub is already gone; skip.
			s.packetsDropped.Add(1)
		}
	}()
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

// writePump drains a subscriber's channel and writes to its SRT connection.
// On exit it closes sub.done so relayLoop's drain wait can complete promptly.
func (s *Stream) writePump(ctx context.Context, sub *subscriber) {
	defer close(sub.done)
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
