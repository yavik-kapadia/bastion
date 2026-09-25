package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseTrustedProxies(t *testing.T) {
	nets, err := parseTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8", "::1", " "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 3 {
		t.Fatalf("want 3 networks, got %d", len(nets))
	}
	if _, err := parseTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error for invalid entry")
	}
}

func TestRealIP(t *testing.T) {
	trusted, _ := parseTrustedProxies([]string{"10.0.0.1", "192.168.0.0/16"})

	cases := []struct {
		name    string
		trusted bool
		remote  string
		xff     string
		xrip    string
		want    string
	}{
		{"no trusted proxies ignores headers", false, "203.0.113.5:4000", "1.2.3.4", "5.6.7.8", "203.0.113.5:4000"},
		{"untrusted peer ignores XFF", true, "203.0.113.5:4000", "1.2.3.4", "", "203.0.113.5:4000"},
		{"untrusted peer ignores X-Real-IP", true, "203.0.113.5:4000", "", "1.2.3.4", "203.0.113.5:4000"},
		{"trusted peer uses XFF", true, "10.0.0.1:4000", "198.51.100.7", "", "198.51.100.7:0"},
		{"right-most untrusted XFF hop wins", true, "10.0.0.1:4000", "6.6.6.6, 198.51.100.7, 192.168.1.1", "", "198.51.100.7:0"},
		{"trusted peer uses X-Real-IP", true, "10.0.0.1:4000", "", "198.51.100.7", "198.51.100.7:0"},
		{"malformed XFF keeps peer", true, "10.0.0.1:4000", "garbage", "", "10.0.0.1:4000"},
		{"trusted peer without headers keeps peer", true, "10.0.0.1:4000", "", "", "10.0.0.1:4000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var list = trusted
			if !tc.trusted {
				list = nil
			}
			var got string
			h := realIP(list)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xrip != "" {
				req.Header.Set("X-Real-IP", tc.xrip)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("RemoteAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

// Spoofed forwarding headers from a direct client must not give it a fresh
// rate-limit bucket.
func TestLoginRateLimitIgnoresSpoofedHeaders(t *testing.T) {
	rl := newLoginRateLimiter(2, time.Minute)
	h := realIP(nil)(rl.middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	codes := make([]int, 0, 3)
	for _, spoof := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.5:4000"
		req.Header.Set("X-Real-IP", spoof)
		req.Header.Set("X-Forwarded-For", spoof)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("third attempt should be rate limited despite spoofed headers, got %v", codes)
	}
}
