package test

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestPublisherEOFReconnectCleansSubscribers verifies that when a publisher
// disconnects, all subscriber slots are reclaimed and the next publisher can
// attach cleanly. Reproduces the "1 viewer, 5 slots" symptom by closing the
// publisher with subscribers attached and asserting the count drops.
func TestPublisherEOFReconnectCleansSubscribers(t *testing.T) {
	ts := newTestServer(t)

	const name = "eof-reconnect"

	pub1, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	time.Sleep(80 * time.Millisecond)

	sub1, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
	if err != nil {
		t.Fatalf("sub1 dial: %v", err)
	}
	defer sub1.Close()
	sub2, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
	if err != nil {
		t.Fatalf("sub2 dial: %v", err)
	}
	defer sub2.Close()

	// Wait for both subs to register.
	waitFor(t, 2*time.Second, func() bool {
		stats, ok := ts.relay.StreamStats(name)
		return ok && stats.SubscriberCount == 2
	}, "subscriberCount=2 after dial")

	// Publisher EOFs.
	pub1.Close()

	// Subscribers should be torn down within a couple seconds.
	waitFor(t, 3*time.Second, func() bool {
		stats, ok := ts.relay.StreamStats(name)
		return !ok || stats.SubscriberCount == 0
	}, "subscriberCount=0 after publisher EOF")

	// Second publisher should attach cleanly (state machine returned to pubIdle).
	pub2, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher 2 dial: %v", err)
	}
	defer pub2.Close()

	waitFor(t, 2*time.Second, func() bool {
		stats, ok := ts.relay.StreamStats(name)
		return ok && stats.HasPublisher
	}, "second publisher attached")
}

// TestPublisherEOFRaceWithReconnect spams publisher close/reopen cycles with a
// long-lived subscriber attempting to reconnect after each kill. Asserts no
// panic from the previous send-on-closed-channel race and that subscriberCount
// matches reality (not a stale leak).
func TestPublisherEOFRaceWithReconnect(t *testing.T) {
	ts := newTestServer(t)

	const name = "eof-race"
	const cycles = 8

	for i := 0; i < cycles; i++ {
		pub, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
		if err != nil {
			t.Fatalf("cycle %d publisher dial: %v", i, err)
		}
		time.Sleep(40 * time.Millisecond)

		sub, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
		if err != nil {
			t.Fatalf("cycle %d sub dial: %v", i, err)
		}

		// Push some packets so writePump exercises the Write path.
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			pkt := make([]byte, 1316)
			pkt[0] = byte(i)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					pub.Write(pkt) //nolint:errcheck
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()

		time.Sleep(80 * time.Millisecond)
		cancel()
		wg.Wait()

		pub.Close()
		sub.Close()
	}

	// After all cycles, the stream's subscriberCount must drop to 0 within a
	// bounded wait — we accept up to ~6s to allow gosrt's internal timeouts to
	// flush. A leak would surface as count stuck > 0 here.
	waitFor(t, 6*time.Second, func() bool {
		stats, ok := ts.relay.StreamStats(name)
		return !ok || stats.SubscriberCount == 0
	}, "subscriberCount=0 after race cycles")
}

// TestStreamGCAfterPublisherExits verifies that a stream entry is removed
// from r.streams once the publisher disconnects and there are no subs.
// Without GC, r.streams accumulates entries for every transient stream name.
func TestStreamGCAfterPublisherExits(t *testing.T) {
	ts := newTestServer(t)

	const name = "gc-test"

	pub, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	time.Sleep(80 * time.Millisecond)

	if _, ok := ts.relay.StreamStats(name); !ok {
		t.Fatalf("stream %q not registered after publisher dial", name)
	}

	pub.Close()

	waitFor(t, 5*time.Second, func() bool {
		_, ok := ts.relay.StreamStats(name)
		return !ok
	}, "stream removed from r.streams after publisher exit")
}

// waitFor polls predicate until it returns true or timeout expires.
// Fails the test with msg on timeout.
func waitFor(t *testing.T, timeout time.Duration, predicate func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out: %s", msg)
}
