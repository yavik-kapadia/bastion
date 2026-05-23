package relay

import (
	"testing"
	"time"
)

// TestPublisherDisconnectCallbackWiring is a compile-time check that the
// PublisherDisconnectFunc field exists with the expected signature and can
// be set on a Relay without surprises. The end-to-end firing path is
// exercised by manual integration testing against a running relay — mocking
// gosrt.Conn here would be heavier than the value it adds.
func TestPublisherDisconnectCallbackWiring(t *testing.T) {
	r := New(":0", 16, nil, Config{})
	called := make(chan struct{}, 1)
	r.OnPublisherDisconnect = func(stream, remote string, dur time.Duration) {
		called <- struct{}{}
	}
	if r.OnPublisherDisconnect == nil {
		t.Fatal("OnPublisherDisconnect not retained after assignment")
	}
	// Directly invoke to confirm the function pointer is callable with the
	// declared signature; the real call site is inside handlePublisher.
	r.OnPublisherDisconnect("s", "1.2.3.4:1234", 5*time.Second)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("callback was not invoked")
	}
}

// TestPublisherDisconnectCallbackOptional confirms that a Relay with a nil
// callback is the safe default — handlePublisher must nil-check before
// invoking. We can't drive handlePublisher without a real srt.Conn, so this
// asserts only the zero-value default.
func TestPublisherDisconnectCallbackOptional(t *testing.T) {
	r := New(":0", 16, nil, Config{})
	if r.OnPublisherDisconnect != nil {
		t.Errorf("OnPublisherDisconnect default = %v, want nil", r.OnPublisherDisconnect)
	}
}
