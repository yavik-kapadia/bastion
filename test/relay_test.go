package test

import (
	"context"
	"testing"
	"time"
)

// TestSRTRelayBasic verifies a single publisher → single subscriber flow.
// This is the simplest possible SRT relay end-to-end test.
func TestSRTRelayBasic(t *testing.T) {
	ts := newTestServer(t)

	const name = "basic-test"

	// Publisher connects.
	pubConn, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	defer pubConn.Close()

	// Brief pause then subscriber connects.
	time.Sleep(50 * time.Millisecond)
	subConn, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
	if err != nil {
		t.Fatalf("subscriber dial: %v", err)
	}
	defer subConn.Close()

	// Send packets from publisher in background.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pkt := make([]byte, 1316)
	for i := range pkt {
		pkt[i] = 0x42
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				pubConn.Write(pkt) //nolint:errcheck
				time.Sleep(15 * time.Millisecond)
			}
		}
	}()

	// Subscriber should receive at least 5 packets within 5 seconds.
	buf := make([]byte, 2048)
	received := 0
	for received < 5 {
		subConn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		n, err := subConn.Read(buf)
		if err != nil {
			t.Fatalf("subscriber read error after %d packets: %v", received, err)
		}
		if n > 0 {
			if buf[0] != 0x42 {
				t.Errorf("unexpected packet content: 0x%02x", buf[0])
			}
			received++
		}
	}
	t.Logf("subscriber received %d packets", received)
}

// TestSRTRelayFanOut verifies two subscribers both receive the same stream.
func TestSRTRelayFanOut(t *testing.T) {
	ts := newTestServer(t)

	const name = "fanout-test"

	// Publisher connects first.
	pubConn, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	defer pubConn.Close()
	time.Sleep(50 * time.Millisecond)

	// Two subscribers connect.
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
	time.Sleep(50 * time.Millisecond)

	// Publisher sends.
	pkt := make([]byte, 1316)
	pkt[0] = 0xCC
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				pubConn.Write(pkt) //nolint:errcheck
				time.Sleep(15 * time.Millisecond)
			}
		}
	}()

	// Verify both subscribers receive data.
	recv := func(conn interface{ Read([]byte) (int, error); SetReadDeadline(time.Time) error }, label string) {
		buf := make([]byte, 2048)
		n := 0
		for n < 5 {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
			count, err := conn.Read(buf)
			if err != nil {
				t.Errorf("%s read error after %d packets: %v", label, n, err)
				return
			}
			if count > 0 {
				n++
			}
		}
		t.Logf("%s: received %d packets", label, n)
	}

	done := make(chan struct{}, 2)
	go func() { recv(sub1, "sub1"); done <- struct{}{} }()
	go func() { recv(sub2, "sub2"); done <- struct{}{} }()
	<-done
	<-done
}
