package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookPostsEvent(t *testing.T) {
	var (
		gotBody []byte
		gotCT   string
		mu      sync.Mutex
		done    = make(chan struct{})
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = b
		gotCT = r.Header.Get("Content-Type")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		close(done)
	}))
	defer srv.Close()

	wb := NewWebhook(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wb.Start(ctx)

	evt := Event{
		Type:       "publisher_disconnect",
		Stream:     "test-stream",
		Remote:     "1.2.3.4:9710",
		Duration:   "5m23s",
		OccurredAt: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
	wb.Send(evt)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook never received POST")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	var got Event
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("unmarshal body: %v (body=%s)", err, gotBody)
	}
	if got.Type != evt.Type || got.Stream != evt.Stream || got.Remote != evt.Remote || got.Duration != evt.Duration {
		t.Errorf("event mismatch: got=%+v want=%+v", got, evt)
	}
	if !got.OccurredAt.Equal(evt.OccurredAt) {
		t.Errorf("OccurredAt = %v, want %v", got.OccurredAt, evt.OccurredAt)
	}
}

func TestWebhookEmptyURLNoOp(t *testing.T) {
	wb := NewWebhook("")
	// Send must not block or panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			wb.Send(Event{Type: "publisher_disconnect", Stream: "x"})
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Send blocked on empty-URL webhook")
	}

	// Start must return immediately for an empty URL.
	startDone := make(chan struct{})
	go func() {
		wb.Start(context.Background())
		close(startDone)
	}()
	select {
	case <-startDone:
	case <-time.After(time.Second):
		t.Fatal("Start did not return for empty-URL webhook")
	}
}

func TestWebhookInvalidURLDisabled(t *testing.T) {
	wb := NewWebhook("not a real url")
	if wb.URL != "" {
		t.Errorf("invalid URL should produce disabled webhook, got URL=%q", wb.URL)
	}
	// Should behave like empty URL.
	wb.Send(Event{Type: "publisher_disconnect", Stream: "x"})
	wb.Start(context.Background()) // returns immediately
}

func TestWebhookDropsOldestOnQueueFull(t *testing.T) {
	// Build a Webhook without starting the dispatcher, so the queue fills up
	// deterministically.
	wb := NewWebhook("http://127.0.0.1:1") // not started → events queue up

	// Fill the queue to capacity.
	for i := 0; i < queueSize; i++ {
		wb.Send(Event{Type: "publisher_disconnect", Stream: "fill", Remote: itoa(i)})
	}
	// Overflow with a marker event. The oldest (Remote="0") should be dropped.
	marker := Event{Type: "publisher_disconnect", Stream: "marker", Remote: "MARKER"}
	wb.Send(marker)

	// Drain the queue and inspect contents.
	drained := drain(wb.queue)
	if len(drained) != queueSize {
		t.Fatalf("queue len = %d, want %d", len(drained), queueSize)
	}

	// The marker must be present (newest preserved).
	foundMarker := false
	for _, e := range drained {
		if e.Remote == "MARKER" {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Errorf("marker event was dropped — newest should be preserved")
	}

	// The very first event ("0") must NOT be present (oldest dropped).
	for _, e := range drained {
		if e.Remote == "0" {
			t.Errorf("oldest event (Remote=0) still in queue — drop-oldest policy violated")
		}
	}
}

func TestWebhookRetryOnTransientError(t *testing.T) {
	var hits atomic.Int32
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		select {
		case <-done:
		default:
			close(done)
		}
	}))
	defer srv.Close()

	wb := NewWebhook(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wb.Start(ctx)

	wb.Send(Event{Type: "publisher_disconnect", Stream: "retry"})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("retry never succeeded; hits=%d", hits.Load())
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("expected 2 attempts (1 fail + 1 success), got %d", got)
	}
}

func TestWebhook4xxNoRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	wb := NewWebhook(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wb.Start(ctx)

	wb.Send(Event{Type: "publisher_disconnect", Stream: "bad"})

	// Wait for delivery to settle.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give a moment to confirm no retry occurs.
	time.Sleep(200 * time.Millisecond)
	if got := hits.Load(); got != 1 {
		t.Errorf("4xx should not retry: hits=%d, want 1", got)
	}
}

func TestStartExitsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wb := NewWebhook(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		wb.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not exit on context cancel")
	}
}

// --- helpers ---

func drain(ch chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func itoa(i int) string {
	// Minimal, allocation-light int→string for test markers.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
