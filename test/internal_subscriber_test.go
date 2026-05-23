package test

import (
	"testing"
	"time"
)

// TestInternalSubscriberExcludedFromCount verifies that an SRT subscriber
// connecting with w=internal in its streamid does NOT bump the public
// subscriberCount — this is how the thumbnail handler and ffprobe media-info
// loop participate in the fan-out without polluting the viewer-count metric.
func TestInternalSubscriberExcludedFromCount(t *testing.T) {
	ts := newTestServer(t)

	const name = "internal-sub-test"

	pub, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	defer pub.Close()
	time.Sleep(80 * time.Millisecond)

	// External viewer first — should bump the count to 1.
	ext, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
	if err != nil {
		t.Fatalf("external sub dial: %v", err)
	}
	defer ext.Close()

	waitFor(t, 2*time.Second, func() bool {
		stats, ok := ts.relay.StreamStats(name)
		return ok && stats.SubscriberCount == 1
	}, "external subscriber visible as count=1")

	// Internal worker connection — should NOT bump the count.
	internal, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name+",w=internal")
	if err != nil {
		t.Fatalf("internal sub dial: %v", err)
	}
	defer internal.Close()

	// Give the relay a moment to process the new connection.
	time.Sleep(200 * time.Millisecond)

	stats, ok := ts.relay.StreamStats(name)
	if !ok {
		t.Fatal("stream stats missing after internal connect")
	}
	if stats.SubscriberCount != 1 {
		t.Errorf("internal sub leaked into count: got %d, want 1", stats.SubscriberCount)
	}

	// When the publisher exits, the relay tears down all subscribers
	// (including internal ones). After that, count must be 0 and the stream
	// is GC'd. This is the same teardown path the resilience tests exercise.
	pub.Close()
	waitFor(t, 3*time.Second, func() bool {
		s, ok := ts.relay.StreamStats(name)
		return !ok || s.SubscriberCount == 0
	}, "publisher EOF brings external count to 0")
}
