package api

import (
	"context"
	"sync"
	"time"
)

// thumbnailCache fronts the per-stream ffmpeg frame grab with two protections
// against excess SRT subscriber load:
//
//   - Singleflight: concurrent requests for the same stream share one ffmpeg run.
//   - Short TTL: a successful PNG is cached so a dashboard with N tabs polling
//     every refreshIntervalMs hits ffmpeg at most once per ttl.
//
// Without this, every dashboard tab opens its own SRT request-mode subscriber
// connection, which inflates the public subscriberCount.
type thumbnailCache struct {
	ttl time.Duration

	mu       sync.Mutex
	entries  map[string]thumbnailEntry
	inflight map[string]*thumbnailInflight
}

type thumbnailEntry struct {
	png       []byte
	expiresAt time.Time
}

type thumbnailInflight struct {
	done chan struct{}
	png  []byte
	err  error
}

func newThumbnailCache(ttl time.Duration) *thumbnailCache {
	return &thumbnailCache{
		ttl:      ttl,
		entries:  make(map[string]thumbnailEntry),
		inflight: make(map[string]*thumbnailInflight),
	}
}

// get returns a cached PNG if fresh, joins any in-flight fetch for the same
// stream, or invokes fetch once and shares the result with all waiters.
func (tc *thumbnailCache) get(ctx context.Context, name string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	tc.mu.Lock()
	if e, ok := tc.entries[name]; ok && time.Now().Before(e.expiresAt) {
		png := e.png
		tc.mu.Unlock()
		return png, nil
	}
	if inf, ok := tc.inflight[name]; ok {
		tc.mu.Unlock()
		select {
		case <-inf.done:
			return inf.png, inf.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	inf := &thumbnailInflight{done: make(chan struct{})}
	tc.inflight[name] = inf
	tc.mu.Unlock()

	png, err := fetch(ctx)

	tc.mu.Lock()
	delete(tc.inflight, name)
	if err == nil {
		tc.entries[name] = thumbnailEntry{png: png, expiresAt: time.Now().Add(tc.ttl)}
	}
	tc.mu.Unlock()

	inf.png = png
	inf.err = err
	close(inf.done)
	return png, err
}
