package test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	srt "github.com/datarhei/gosrt"
)

func dialSRT(addr, streamID string) (srt.Conn, error) {
	cfg := srt.DefaultConfig()
	cfg.StreamId = streamID
	return srt.Dial("srt", addr, cfg)
}

// TestMultiStreamRelay verifies 3 simultaneous streams relay independently
// with no cross-stream packet contamination.
func TestMultiStreamRelay(t *testing.T) {
	ts := newTestServer(t)

	const numStreams = 3
	const numSubscribers = 2
	const sendPackets = 30

	// Marker bytes: each stream uses a unique first-byte value.
	markers := make([]byte, numStreams)
	for i := range markers {
		markers[i] = byte(0xA0 + i)
	}

	type subResult struct {
		stream    int
		sub       int
		received  int
		firstByte byte
	}

	// Use a buffered channel large enough for all subscribers; close once all wg goroutines exit.
	results := make(chan subResult, numStreams*numSubscribers)

	var subWg sync.WaitGroup // subscriber goroutines
	var pubWg sync.WaitGroup // publisher goroutines

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < numStreams; i++ {
		si := i
		name := fmt.Sprintf("ms-%d", si)
		marker := markers[si]

		// Subscribers
		for j := 0; j < numSubscribers; j++ {
			sj := j
			subWg.Add(1)
			go func() {
				defer subWg.Done()
				conn, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
				if err != nil {
					t.Logf("sub %d/%d connect: %v", si, sj, err)
					return
				}
				defer conn.Close()

				buf := make([]byte, 1316)
				received := 0
				var first byte
				for received < sendPackets {
					conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
					n, err := conn.Read(buf)
					if err != nil || n == 0 {
						break
					}
					if received == 0 {
						first = buf[0]
					}
					received++
				}
				select {
				case results <- subResult{si, sj, received, first}:
				default:
				}
			}()
		}

		// Give subscribers time to connect before publisher starts.
		time.Sleep(150 * time.Millisecond)

		// Publisher
		pubWg.Add(1)
		go func() {
			defer pubWg.Done()
			conn, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
			if err != nil {
				t.Logf("pub %d connect: %v", si, err)
				return
			}
			defer conn.Close()

			pkt := make([]byte, 1316)
			pkt[0] = marker
			for k := 0; k < sendPackets+5; k++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if _, err := conn.Write(pkt); err != nil {
					return
				}
				time.Sleep(15 * time.Millisecond)
			}
		}()
	}

	// Wait for publishers to finish, then wait for subscribers.
	pubWg.Wait()
	subWg.Wait()
	close(results)

	// Verify results.
	byStream := make(map[int][]subResult)
	for r := range results {
		byStream[r.stream] = append(byStream[r.stream], r)
	}

	for i := 0; i < numStreams; i++ {
		rs := byStream[i]
		if len(rs) == 0 {
			t.Errorf("stream %d: no subscriber results received", i)
			continue
		}
		for _, r := range rs {
			if r.received < 5 {
				t.Errorf("stream %d sub %d: only %d packets (want >= 5)", i, r.sub, r.received)
			}
			if r.firstByte != markers[i] {
				t.Errorf("stream %d sub %d: first byte 0x%02x, want 0x%02x (cross-contamination!)",
					i, r.sub, r.firstByte, markers[i])
			}
		}
	}
}

// TestSubscriberJoinsLateStream verifies a subscriber joining after publisher
// is already running receives subsequent packets.
func TestSubscriberJoinsLateStream(t *testing.T) {
	ts := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const name = "late-join"

	pubConn, err := dialSRT(ts.srtAddr, "#!::m=publish,r="+name)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	defer pubConn.Close()

	pkt := make([]byte, 1316)
	pkt[0] = 0xBB

	// Publisher sends initial packets before subscriber joins.
	for i := 0; i < 5; i++ {
		pubConn.Write(pkt) //nolint:errcheck
		time.Sleep(20 * time.Millisecond)
	}

	// Subscriber joins late.
	subConn, err := dialSRT(ts.srtAddr, "#!::m=request,r="+name)
	if err != nil {
		t.Fatalf("subscriber dial: %v", err)
	}
	defer subConn.Close()

	// Publisher keeps sending.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				pubConn.Write(pkt) //nolint:errcheck
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	// Subscriber should receive packets within 3s.
	buf := make([]byte, 1316)
	received := 0
	for received < 5 {
		subConn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
		n, err := subConn.Read(buf)
		if err != nil || n == 0 {
			break
		}
		received++
	}
	if received < 3 {
		t.Errorf("late subscriber received %d packets, want >= 3", received)
	}
}
