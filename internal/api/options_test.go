package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yavik-kapadia/bastion/internal/db"
	"github.com/yavik-kapadia/bastion/internal/metrics"
	"github.com/yavik-kapadia/bastion/internal/model"
	"github.com/yavik-kapadia/bastion/internal/relay"
	"github.com/yavik-kapadia/bastion/internal/ws"
)

// fakeRelayWithPub mimics an active publisher so streamThumbnail proceeds past
// its HasPublisher precondition. We only need the gate behavior tested, not
// an actual ffmpeg run.
type fakeRelayWithPub struct{}

func (f *fakeRelayWithPub) ActiveStreams() map[string]relay.StreamStats {
	return map[string]relay.StreamStats{
		"live": {HasPublisher: true, SubscriberCount: 0},
	}
}
func (f *fakeRelayWithPub) StreamStats(name string) (relay.StreamStats, bool) {
	if name == "live" {
		return relay.StreamStats{HasPublisher: true}, true
	}
	return relay.StreamStats{}, false
}
func (f *fakeRelayWithPub) StreamSubscribers(name string) ([]relay.SubscriberStats, bool) {
	if name == "live" {
		return nil, true
	}
	return nil, false
}

// buildOptsServer wires a minimal router with the auth-protected thumbnail
// + login routes, using the given Options.
func buildOptsServer(t *testing.T, opts Options) (*Server, *httptest.Server) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.Users.Create("admin-id", "admin", "admin-password", model.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	hub := ws.NewHub()
	srv, err := NewServer(database, &fakeRelayWithPub{}, metrics.NewProm(), hub, nil, opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/api/v1/auth/login", srv.loginLimiter.middleware(srv.login))
	r.Group(func(r chi.Router) {
		r.Use(srv.requireAuth)
		r.Get("/api/v1/streams/{name}/thumbnail", srv.streamThumbnail)
	})
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return srv, ts
}

// TestThumbnailDisabledReturns404 verifies that when ThumbnailEnabled=false
// the handler short-circuits with 404 and never touches the cache / ffmpeg.
func TestThumbnailDisabledReturns404(t *testing.T) {
	_, ts := buildOptsServer(t, Options{ThumbnailEnabled: false})
	token := loginAs(t, ts, "admin", "admin-password")

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/streams/live/thumbnail", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ThumbnailEnabled=false: want 404, got %d", resp.StatusCode)
	}
}

// TestLoginRateLimitConfigurable proves the LoginRateLimit option is wired
// through — with a limit of 2, the third login attempt within the window
// returns 429 regardless of credential validity.
func TestLoginRateLimitConfigurable(t *testing.T) {
	_, ts := buildOptsServer(t, Options{
		LoginRateLimit:  2,
		LoginRateWindow: 1 * time.Minute,
	})

	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "admin-password",
	})

	// First two attempts: accepted (200).
	for i := 1; i <= 2; i++ {
		resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d: want 200, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Third attempt: blocked by rate limiter.
	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("attempt 3: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("attempt 3: want 429, got %d", resp.StatusCode)
	}
}

// TestSessionTTLApplied verifies the session cookie's Max-Age tracks the
// configured SessionTTL within a 1s tolerance.
func TestSessionTTLApplied(t *testing.T) {
	const ttl = 90 * time.Minute
	_, ts := buildOptsServer(t, Options{SessionTTL: ttl})

	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "admin-password",
	})
	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status: %d", resp.StatusCode)
	}

	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "bastion_session" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("no bastion_session cookie set")
	}
	wantSecs := int(ttl.Seconds())
	if diff := found.MaxAge - wantSecs; diff < -1 || diff > 1 {
		t.Errorf("cookie MaxAge: want %d (±1), got %d", wantSecs, found.MaxAge)
	}
}

// TestForceHTTPSAddsHSTS verifies the Strict-Transport-Security header is
// emitted only when ForceHTTPS is enabled.
func TestForceHTTPSAddsHSTS(t *testing.T) {
	cases := []struct {
		name      string
		force     bool
		wantHSTS  bool
	}{
		{"off", false, false},
		{"on", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.Open(":memory:")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { database.Close() })

			hub := ws.NewHub()
			srv, err := NewServer(database, &fakeRelay{}, metrics.NewProm(), hub, nil, Options{ForceHTTPS: tc.force})
			if err != nil {
				t.Fatalf("new server: %v", err)
			}
			r := chi.NewRouter()
			r.Use(srv.securityHeaders)
			r.Get("/health", healthHandler)
			ts := httptest.NewServer(r)
			t.Cleanup(ts.Close)

			resp, err := http.Get(ts.URL + "/health")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()
			got := resp.Header.Get("Strict-Transport-Security")
			if tc.wantHSTS && got == "" {
				t.Errorf("ForceHTTPS=true: expected HSTS header, got none")
			}
			if !tc.wantHSTS && got != "" {
				t.Errorf("ForceHTTPS=false: expected no HSTS header, got %q", got)
			}
		})
	}
}
