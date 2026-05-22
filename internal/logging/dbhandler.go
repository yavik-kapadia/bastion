// Package logging provides a slog handler that tees structured log records
// into a persistent event-log sink in addition to writing them to stdout.
//
// The sink is advisory: under sustained pressure the handler drops the
// oldest pending records rather than blocking the hot path. SQLite writes
// must never stall request handling.
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yavik-kapadia/bastion/internal/db"
)

// Sink receives batches of records assembled by the DBHandler.
// Implementations should be safe for concurrent use; in practice only one
// goroutine (the drain loop) calls Flush.
type Sink interface {
	Flush(records []db.EventLog) error
}

// Broadcaster is the optional WebSocket hook. The handler calls Broadcast
// with each flushed batch (now carrying assigned IDs) so dashboards get
// near-real-time tails. Nil is allowed.
type Broadcaster interface {
	BroadcastLogs(records []db.EventLog)
}

// Options configures a DBHandler.
type Options struct {
	// Inner is the underlying handler that continues to write to stdout
	// (text or JSON). Required.
	Inner slog.Handler
	// Sink receives batched records. Required.
	Sink Sink
	// Broadcaster, if non-nil, is invoked with each flushed batch.
	Broadcaster Broadcaster
	// MinLevel is the lowest level captured into the DB. Records at or
	// above MinLevel are mirrored; records below pass through to Inner only.
	// Defaults to slog.LevelInfo.
	MinLevel slog.Level
	// BufferSize bounds the in-memory channel. Defaults to 2000.
	BufferSize int
	// BatchSize triggers a flush once that many records are queued.
	// Defaults to 250.
	BatchSize int
	// FlushInterval bounds the staleness of buffered records.
	// Defaults to 1 second.
	FlushInterval time.Duration
}

// DBHandler tees slog records to a Sink with batching + drop-oldest behavior.
//
// Zero copies of Records cross the goroutine boundary — we extract the data
// we need into a plain struct first.
type DBHandler struct {
	inner       slog.Handler
	sink        Sink
	broadcaster Broadcaster
	minLevel    slog.Level

	mu       sync.Mutex // guards buf
	buf      []db.EventLog
	bufCap   int
	batchSz  int
	interval time.Duration

	notify chan struct{} // size-1 signal channel
	stop   chan struct{}
	done   chan struct{}

	// counters for diagnostics + tests
	dropped atomic.Uint64
	flushed atomic.Uint64

	// pre-bound attrs (from WithAttrs / WithGroup)
	attrs []slog.Attr
	group string
}

// NewDBHandler constructs a handler. The caller is responsible for calling
// Close on shutdown to drain remaining records.
func NewDBHandler(opts Options) *DBHandler {
	if opts.Inner == nil {
		panic("logging: Inner handler required")
	}
	if opts.Sink == nil {
		panic("logging: Sink required")
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = 2000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 250
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	h := &DBHandler{
		inner:       opts.Inner,
		sink:        opts.Sink,
		broadcaster: opts.Broadcaster,
		minLevel:    opts.MinLevel,
		buf:         make([]db.EventLog, 0, opts.BufferSize),
		bufCap:      opts.BufferSize,
		batchSz:     opts.BatchSize,
		interval:    opts.FlushInterval,
		notify:      make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	go h.runDrain()
	return h
}

// Enabled defers to the inner handler so stdout output retains its level.
func (h *DBHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle forwards to the inner handler and, if level >= MinLevel,
// enqueues a copy of the record for batched DB insertion.
func (h *DBHandler) Handle(ctx context.Context, r slog.Record) error {
	innerErr := h.inner.Handle(ctx, r)
	if r.Level < h.minLevel {
		return innerErr
	}
	rec := h.toEventLog(r)
	h.enqueue(rec)
	return innerErr
}

// WithAttrs returns a child handler with pre-bound attrs. The child shares
// the parent's buffer, drain goroutine, and counters; only the attr list
// and the inner handler differ.
func (h *DBHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &childHandler{
		parent: h.root(),
		inner:  h.inner.WithAttrs(attrs),
		attrs:  merged,
		group:  h.group,
	}
}

// WithGroup pre-applies a group on the inner handler. For the DB side we
// record the group name; nested groups are flattened into a single dotted
// prefix when serializing attrs.
func (h *DBHandler) WithGroup(name string) slog.Handler {
	g := h.group
	if name != "" {
		if g == "" {
			g = name
		} else {
			g = g + "." + name
		}
	}
	return &childHandler{
		parent: h.root(),
		inner:  h.inner.WithGroup(name),
		attrs:  append([]slog.Attr(nil), h.attrs...),
		group:  g,
	}
}

// root returns the underlying handler; a *DBHandler is its own root.
func (h *DBHandler) root() *DBHandler { return h }

// childHandler is a lightweight derivation produced by WithAttrs/WithGroup.
// All buffer + drain state lives on the parent.
type childHandler struct {
	parent *DBHandler
	inner  slog.Handler
	attrs  []slog.Attr
	group  string
}

func (c *childHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return c.inner.Enabled(ctx, level)
}

func (c *childHandler) Handle(ctx context.Context, r slog.Record) error {
	innerErr := c.inner.Handle(ctx, r)
	if r.Level < c.parent.minLevel {
		return innerErr
	}
	rec := c.toEventLog(r)
	c.parent.enqueue(rec)
	return innerErr
}

func (c *childHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(c.attrs)+len(attrs))
	merged = append(merged, c.attrs...)
	merged = append(merged, attrs...)
	return &childHandler{
		parent: c.parent,
		inner:  c.inner.WithAttrs(attrs),
		attrs:  merged,
		group:  c.group,
	}
}

func (c *childHandler) WithGroup(name string) slog.Handler {
	g := c.group
	if name != "" {
		if g == "" {
			g = name
		} else {
			g = g + "." + name
		}
	}
	return &childHandler{
		parent: c.parent,
		inner:  c.inner.WithGroup(name),
		attrs:  append([]slog.Attr(nil), c.attrs...),
		group:  g,
	}
}

// toEventLog mirrors DBHandler.toEventLog but for child contexts.
func (c *childHandler) toEventLog(r slog.Record) db.EventLog {
	attrs := make(map[string]any, r.NumAttrs()+len(c.attrs))
	var stream string
	for _, a := range c.attrs {
		if a.Key == "stream" {
			stream = a.Value.String()
			continue
		}
		attrs[withGroupPrefix(c.group, a.Key)] = attrValue(a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "stream" {
			stream = a.Value.String()
			return true
		}
		attrs[withGroupPrefix(c.group, a.Key)] = attrValue(a.Value)
		return true
	})

	var attrsJSON []byte
	if len(attrs) > 0 {
		var err error
		attrsJSON, err = json.Marshal(attrs)
		if err != nil {
			attrsJSON = []byte("{}")
		}
	} else {
		attrsJSON = []byte("{}")
	}

	return db.EventLog{
		TS:     r.Time.UnixNano(),
		Level:  levelString(r.Level),
		Stream: stream,
		Msg:    r.Message,
		Attrs:  string(attrsJSON),
	}
}

// Close stops the drain goroutine after flushing any remaining records.
// Safe to call multiple times.
func (h *DBHandler) Close() {
	select {
	case <-h.stop:
		return
	default:
	}
	close(h.stop)
	<-h.done
}

// Dropped returns the cumulative number of records that were evicted from
// the buffer because the consumer could not keep up.
func (h *DBHandler) Dropped() uint64 { return h.dropped.Load() }

// Flushed returns the cumulative number of records successfully written.
func (h *DBHandler) Flushed() uint64 { return h.flushed.Load() }

func (h *DBHandler) toEventLog(r slog.Record) db.EventLog {
	attrs := make(map[string]any, r.NumAttrs()+len(h.attrs))
	var stream string

	// Pre-bound attrs first.
	for _, a := range h.attrs {
		if a.Key == "stream" {
			stream = a.Value.String()
			continue
		}
		attrs[withGroupPrefix(h.group, a.Key)] = attrValue(a.Value)
	}

	// Per-record attrs.
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "stream" {
			stream = a.Value.String()
			return true
		}
		attrs[withGroupPrefix(h.group, a.Key)] = attrValue(a.Value)
		return true
	})

	var attrsJSON []byte
	if len(attrs) > 0 {
		var err error
		attrsJSON, err = json.Marshal(attrs)
		if err != nil {
			attrsJSON = []byte("{}")
		}
	} else {
		attrsJSON = []byte("{}")
	}

	return db.EventLog{
		TS:     r.Time.UnixNano(),
		Level:  levelString(r.Level),
		Stream: stream,
		Msg:    r.Message,
		Attrs:  string(attrsJSON),
	}
}

func attrValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano)
	case slog.KindGroup:
		out := map[string]any{}
		for _, a := range v.Group() {
			out[a.Key] = attrValue(a.Value)
		}
		return out
	default:
		return fmt.Sprint(v.Any())
	}
}

func withGroupPrefix(group, key string) string {
	if group == "" {
		return key
	}
	return group + "." + key
}

func levelString(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// enqueue appends to the buffer, evicting the oldest record on overflow.
// Signals the drain loop if the batch threshold is reached.
func (h *DBHandler) enqueue(rec db.EventLog) {
	h.mu.Lock()
	if len(h.buf) >= h.bufCap {
		// Drop oldest. Slice copy is fine — buf is bounded.
		copy(h.buf, h.buf[1:])
		h.buf = h.buf[:len(h.buf)-1]
		dropped := h.dropped.Add(1)
		// Surface buffer pressure to operators, but not on every drop:
		// log on the first drop and then every 1000th to keep stderr quiet.
		if dropped == 1 || dropped%1000 == 0 {
			fmt.Fprintf(os.Stderr,
				"logging: DB sink buffer full, dropped %d records cumulative\n", dropped)
		}
	}
	h.buf = append(h.buf, rec)
	shouldSignal := len(h.buf) >= h.batchSz
	h.mu.Unlock()

	if shouldSignal {
		h.signal()
	}
}

func (h *DBHandler) signal() {
	select {
	case h.notify <- struct{}{}:
	default:
	}
}

// drain pulls the current buffer in one swap and returns it.
func (h *DBHandler) drain() []db.EventLog {
	h.mu.Lock()
	if len(h.buf) == 0 {
		h.mu.Unlock()
		return nil
	}
	out := h.buf
	h.buf = make([]db.EventLog, 0, h.bufCap)
	h.mu.Unlock()
	return out
}

func (h *DBHandler) runDrain() {
	defer close(h.done)
	t := time.NewTicker(h.interval)
	defer t.Stop()

	flush := func() {
		batch := h.drain()
		if len(batch) == 0 {
			return
		}
		if err := h.sink.Flush(batch); err != nil {
			fmt.Fprintf(os.Stderr, "logging: sink flush failed: %v\n", err)
			return
		}
		h.flushed.Add(uint64(len(batch)))
		if h.broadcaster != nil {
			h.broadcaster.BroadcastLogs(batch)
		}
	}

	for {
		select {
		case <-h.stop:
			flush()
			return
		case <-h.notify:
			flush()
		case <-t.C:
			flush()
		}
	}
}

// RepoSink adapts an *db.EventLogsRepo to the Sink interface.
type RepoSink struct{ Repo *db.EventLogsRepo }

// Flush inserts the batch via the repo.
func (s RepoSink) Flush(records []db.EventLog) error {
	_, err := s.Repo.Insert(records)
	return err
}
