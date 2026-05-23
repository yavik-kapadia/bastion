// Package notify provides outbound event delivery for Bastion. Currently
// supports a single webhook target that POSTs JSON events; see Event for the
// payload shape.
//
// Design notes:
//   - The Webhook runs a single dispatcher goroutine fed by a bounded channel.
//     Send is non-blocking; if the queue is full, the oldest queued event is
//     dropped to make room (so the most recent activity is preserved). A warn
//     log is emitted on overflow, rate-limited to once per minute to avoid
//     spam during sustained overload.
//   - Empty URL means "disabled" — Send is a no-op and Start exits immediately.
//   - HTTP delivery uses a 10s timeout, one immediate retry on transient
//     errors (network failure or 5xx). 4xx responses are treated as terminal
//     and the event is dropped with a warn log.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Event is an outbound notification payload. The only currently defined Type
// is "publisher_disconnect", fired when a stream's publisher session ends.
type Event struct {
	Type       string    `json:"type"`                 // e.g. "publisher_disconnect"
	Stream     string    `json:"stream"`               // stream name
	Remote     string    `json:"remote,omitempty"`     // last known publisher remote addr
	Duration   string    `json:"duration,omitempty"`   // session length, e.g. "5m23s"
	OccurredAt time.Time `json:"occurred_at"`          // event timestamp (UTC)
}

const (
	queueSize       = 100
	httpTimeout     = 10 * time.Second
	overflowLogRate = time.Minute
)

// Webhook is a buffered, single-dispatcher webhook poster. A zero value or
// one constructed with an empty URL is a no-op.
type Webhook struct {
	URL    string
	client *http.Client
	queue  chan Event

	// overflow log rate-limit state.
	overflowMu      sync.Mutex
	lastOverflowLog time.Time
}

// NewWebhook returns a Webhook posting to url. An empty url disables delivery
// (Send becomes a no-op, Start returns immediately). If url is non-empty but
// not parseable, a warning is logged but the Webhook is still returned in
// disabled state — config errors should not prevent server startup.
func NewWebhook(rawURL string) *Webhook {
	if rawURL == "" {
		return &Webhook{}
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		slog.Warn("notify: invalid webhook URL — webhook disabled", "url", rawURL, "err", err)
		return &Webhook{}
	}
	return &Webhook{
		URL:    rawURL,
		client: &http.Client{Timeout: httpTimeout},
		queue:  make(chan Event, queueSize),
	}
}

// Send enqueues an event for delivery. Non-blocking. If the queue is full,
// the oldest queued event is dropped to make room for the new one and a warn
// log is emitted (rate-limited).
//
// Safe to call before, during, or after Start; events sent before Start runs
// will be processed once the dispatcher comes up (up to queueSize of them).
func (w *Webhook) Send(e Event) {
	if w == nil || w.URL == "" || w.queue == nil {
		return
	}
	select {
	case w.queue <- e:
		return
	default:
	}
	// Queue full — drop oldest, push new.
	select {
	case <-w.queue:
	default:
	}
	select {
	case w.queue <- e:
	default:
		// Lost the race; another sender filled the slot. Drop the new event.
	}
	w.logOverflow()
}

func (w *Webhook) logOverflow() {
	w.overflowMu.Lock()
	defer w.overflowMu.Unlock()
	now := time.Now()
	if now.Sub(w.lastOverflowLog) < overflowLogRate {
		return
	}
	w.lastOverflowLog = now
	slog.Warn("notify: webhook queue full — dropping oldest events", "queue_size", queueSize)
}

// Start runs the dispatcher loop until ctx is cancelled. Safe to call as
// `go wb.Start(ctx)`. For a disabled webhook (empty URL) this returns
// immediately so the caller's `go` statement exits cleanly with no leak.
func (w *Webhook) Start(ctx context.Context) {
	if w == nil || w.URL == "" || w.queue == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-w.queue:
			w.deliver(ctx, e)
		}
	}
}

// deliver POSTs a single event with one retry on transient failure.
func (w *Webhook) deliver(ctx context.Context, e Event) {
	body, err := json.Marshal(e)
	if err != nil {
		slog.Warn("notify: marshal event failed", "err", err, "type", e.Type, "stream", e.Stream)
		return
	}

	for attempt := 1; attempt <= 2; attempt++ {
		transient, err := w.postOnce(ctx, body)
		if err == nil {
			return
		}
		if !transient || attempt == 2 {
			slog.Warn("notify: webhook delivery failed",
				"url", w.URL, "type", e.Type, "stream", e.Stream,
				"attempt", attempt, "err", err)
			return
		}
		// transient on attempt 1 → retry immediately
	}
}

// postOnce sends one HTTP POST. Returns (transient, err). transient=true
// means the caller should retry. err==nil indicates success (2xx).
func (w *Webhook) postOnce(ctx context.Context, body []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		// URL/request construction error — not transient.
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		// Network/timeout error — treat as transient.
		return true, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode >= 500:
		return true, &httpError{status: resp.StatusCode}
	default:
		// 4xx — terminal, do not retry.
		return false, &httpError{status: resp.StatusCode}
	}
}

type httpError struct{ status int }

func (e *httpError) Error() string {
	return http.StatusText(e.status)
}
