package logging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/yavik-kapadia/bastion/internal/db"
)

// captureSink records all flushed batches for assertions.
type captureSink struct {
	mu      sync.Mutex
	batches [][]db.EventLog
	all     []db.EventLog
	// flushDelay simulates a slow downstream.
	flushDelay time.Duration
}

func (c *captureSink) Flush(records []db.EventLog) error {
	if c.flushDelay > 0 {
		time.Sleep(c.flushDelay)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]db.EventLog, len(records))
	copy(cp, records)
	c.batches = append(c.batches, cp)
	c.all = append(c.all, cp...)
	return nil
}

func (c *captureSink) records() []db.EventLog {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]db.EventLog, len(c.all))
	copy(out, c.all)
	return out
}

func (c *captureSink) batchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.batches)
}

type captureBroadcaster struct {
	mu      sync.Mutex
	batches [][]db.EventLog
}

func (c *captureBroadcaster) BroadcastLogs(records []db.EventLog) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]db.EventLog, len(records))
	copy(cp, records)
	c.batches = append(c.batches, cp)
}

func (c *captureBroadcaster) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.batches)
}

func discardInner() slog.Handler {
	return slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
}

// Wait until cond() is true or timeout fires.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestStreamAttrExtracted(t *testing.T) {
	sink := &captureSink{}
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelInfo,
		BatchSize:     1,
		FlushInterval: time.Hour, // rely on batch threshold
	})
	defer h.Close()

	log := slog.New(h)
	log.Info("hello", "stream", "test-1", "extra", "value")

	waitFor(t, time.Second, func() bool { return len(sink.records()) >= 1 })
	rec := sink.records()[0]
	if rec.Stream != "test-1" {
		t.Errorf("stream attr not extracted: got %q", rec.Stream)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(rec.Attrs), &attrs); err != nil {
		t.Fatalf("attrs not JSON: %v", err)
	}
	if attrs["extra"] != "value" {
		t.Errorf("extra attr lost: %+v", attrs)
	}
	if _, ok := attrs["stream"]; ok {
		t.Error("stream key should not be in attrs JSON")
	}
}

func TestLevelFiltering(t *testing.T) {
	sink := &captureSink{}
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelWarn,
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	defer h.Close()

	log := slog.New(h)
	log.Info("ignored")
	log.Warn("captured")
	log.Error("captured-err")

	waitFor(t, time.Second, func() bool { return len(sink.records()) >= 2 })
	recs := sink.records()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if recs[0].Level != "warn" || recs[1].Level != "error" {
		t.Errorf("levels wrong: %+v", recs)
	}
}

func TestBatchSizeFlush(t *testing.T) {
	sink := &captureSink{}
	const batch = 5
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelInfo,
		BatchSize:     batch,
		FlushInterval: time.Hour, // disable time-based flush
	})
	defer h.Close()

	log := slog.New(h)
	for i := 0; i < batch; i++ {
		log.Info("m")
	}
	waitFor(t, time.Second, func() bool { return sink.batchCount() >= 1 })
	if sink.batchCount() < 1 {
		t.Fatal("no batch")
	}
	if len(sink.records()) != batch {
		t.Errorf("expected %d records, got %d", batch, len(sink.records()))
	}
}

func TestFlushIntervalFlush(t *testing.T) {
	sink := &captureSink{}
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelInfo,
		BatchSize:     1000, // huge so we rely on interval
		FlushInterval: 30 * time.Millisecond,
	})
	defer h.Close()

	slog.New(h).Info("tick")
	waitFor(t, time.Second, func() bool { return len(sink.records()) >= 1 })
}

func TestDropOldestUnderPressure(t *testing.T) {
	// Block the sink so the buffer fills.
	sink := &captureSink{flushDelay: 200 * time.Millisecond}
	const bufSize = 8
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelInfo,
		BufferSize:    bufSize,
		BatchSize:     bufSize, // single-batch trigger
		FlushInterval: time.Hour,
	})
	defer h.Close()

	log := slog.New(h)
	// Fire one batch that will start flushing (occupies the drain goroutine).
	for i := 0; i < bufSize; i++ {
		log.Info("a", "i", i)
	}
	// Wait for the drain to dequeue and start its slow flush.
	time.Sleep(20 * time.Millisecond)

	// Now spam beyond capacity while flush is blocked.
	const spam = bufSize * 3
	for i := 0; i < spam; i++ {
		log.Info("b", "i", i)
	}
	if h.Dropped() == 0 {
		t.Error("expected drops under pressure")
	}

	// Let things settle.
	waitFor(t, 2*time.Second, func() bool {
		return h.Flushed() > 0 && len(sink.records()) > 0
	})
	if h.Flushed() == 0 {
		t.Error("expected at least some flushed records")
	}
}

func TestBroadcasterReceivesBatches(t *testing.T) {
	sink := &captureSink{}
	bcast := &captureBroadcaster{}
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		Broadcaster:   bcast,
		MinLevel:      slog.LevelInfo,
		BatchSize:     2,
		FlushInterval: time.Hour,
	})
	defer h.Close()

	log := slog.New(h)
	log.Info("one")
	log.Info("two")
	waitFor(t, time.Second, func() bool { return bcast.count() >= 1 })
	if bcast.count() < 1 {
		t.Fatal("broadcaster never called")
	}
}

func TestEnabledDelegates(t *testing.T) {
	h := NewDBHandler(Options{
		Inner:    discardInner(),
		Sink:     &captureSink{},
		MinLevel: slog.LevelError,
	})
	defer h.Close()
	// Inner is debug-enabled, so Enabled should return true for debug.
	if !h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Enabled should defer to inner")
	}
}

func TestWithAttrsBindsStream(t *testing.T) {
	sink := &captureSink{}
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelInfo,
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	defer h.Close()

	log := slog.New(h).With("stream", "bound")
	log.Info("hello")
	waitFor(t, time.Second, func() bool { return len(sink.records()) >= 1 })
	if sink.records()[0].Stream != "bound" {
		t.Errorf("WithAttrs stream not picked up: %q", sink.records()[0].Stream)
	}
}

func TestCloseFlushesPending(t *testing.T) {
	sink := &captureSink{}
	h := NewDBHandler(Options{
		Inner:         discardInner(),
		Sink:          sink,
		MinLevel:      slog.LevelInfo,
		BatchSize:     1000,
		FlushInterval: time.Hour,
	})

	log := slog.New(h)
	log.Info("a")
	log.Info("b")
	h.Close()

	if len(sink.records()) != 2 {
		t.Errorf("Close did not flush pending: got %d", len(sink.records()))
	}
}
