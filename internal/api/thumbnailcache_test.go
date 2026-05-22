package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestThumbnailCacheSingleflight verifies concurrent get() calls for the same
// stream share one fetch invocation — this is what stops dashboard tabs from
// each opening their own SRT subscriber connection.
func TestThumbnailCacheSingleflight(t *testing.T) {
	tc := newThumbnailCache(1 * time.Second)

	var calls atomic.Int32
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})

	fetch := func(ctx context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(fetchStarted)
		}
		<-releaseFetch
		return []byte("frame"), nil
	}

	const concurrent = 8
	var wg sync.WaitGroup
	wg.Add(concurrent)
	results := make([][]byte, concurrent)
	errs := make([]error, concurrent)
	for i := 0; i < concurrent; i++ {
		go func(idx int) {
			defer wg.Done()
			png, err := tc.get(context.Background(), "stream-a", fetch)
			results[idx] = png
			errs[idx] = err
		}(i)
	}

	<-fetchStarted
	close(releaseFetch)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 fetch invocation, got %d", got)
	}
	for i, r := range results {
		if errs[i] != nil {
			t.Errorf("goroutine %d: unexpected err %v", i, errs[i])
		}
		if string(r) != "frame" {
			t.Errorf("goroutine %d: got %q, want %q", i, r, "frame")
		}
	}
}

// TestThumbnailCacheTTL verifies cached entries are returned without invoking
// fetch again, and expired entries cause a fresh fetch.
func TestThumbnailCacheTTL(t *testing.T) {
	tc := newThumbnailCache(50 * time.Millisecond)

	var calls atomic.Int32
	fetch := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte{byte(calls.Load())}, nil
	}

	for i := 0; i < 5; i++ {
		png, err := tc.get(context.Background(), "s", fetch)
		if err != nil {
			t.Fatalf("get %d: %v", i, err)
		}
		if png[0] != 1 {
			t.Fatalf("get %d: served stale fetch %d, expected cached 1", i, png[0])
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("within TTL: expected 1 fetch, got %d", calls.Load())
	}

	time.Sleep(70 * time.Millisecond)

	png, err := tc.get(context.Background(), "s", fetch)
	if err != nil {
		t.Fatalf("post-TTL get: %v", err)
	}
	if png[0] != 2 {
		t.Fatalf("post-TTL: expected fresh fetch (byte 2), got %d", png[0])
	}
	if calls.Load() != 2 {
		t.Fatalf("post-TTL: expected 2 fetches total, got %d", calls.Load())
	}
}

// TestThumbnailCacheErrorNotCached verifies a failed fetch is not cached, so
// the next request retries.
func TestThumbnailCacheErrorNotCached(t *testing.T) {
	tc := newThumbnailCache(1 * time.Second)

	var calls atomic.Int32
	wantErr := errors.New("boom")
	fetch := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return nil, wantErr
		}
		return []byte("ok"), nil
	}

	if _, err := tc.get(context.Background(), "s", fetch); !errors.Is(err, wantErr) {
		t.Fatalf("first call: want %v, got %v", wantErr, err)
	}
	png, err := tc.get(context.Background(), "s", fetch)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if string(png) != "ok" {
		t.Fatalf("second call: got %q, want %q", png, "ok")
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 fetches (error not cached), got %d", calls.Load())
	}
}

// TestThumbnailCacheDifferentStreamsIndependent verifies streams don't block
// each other.
func TestThumbnailCacheDifferentStreamsIndependent(t *testing.T) {
	tc := newThumbnailCache(1 * time.Second)

	releaseA := make(chan struct{})
	startedA := make(chan struct{})
	fetchA := func(ctx context.Context) ([]byte, error) {
		close(startedA)
		<-releaseA
		return []byte("a"), nil
	}
	fetchB := func(ctx context.Context) ([]byte, error) {
		return []byte("b"), nil
	}

	done := make(chan []byte, 1)
	go func() {
		png, _ := tc.get(context.Background(), "a", fetchA)
		done <- png
	}()

	<-startedA
	png, err := tc.get(context.Background(), "b", fetchB)
	if err != nil {
		t.Fatalf("stream b: %v", err)
	}
	if string(png) != "b" {
		t.Fatalf("stream b: got %q, want %q", png, "b")
	}

	close(releaseA)
	if pngA := <-done; string(pngA) != "a" {
		t.Fatalf("stream a: got %q, want %q", pngA, "a")
	}
}
