package auth

import (
	"net"
	"testing"
	"time"

	"github.com/yavik-kapadia/bastion/internal/db"
	"github.com/yavik-kapadia/bastion/internal/model"
	"github.com/yavik-kapadia/bastion/internal/relay"
)

func openDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addStream(t *testing.T, d *db.DB, s *model.Stream) {
	t.Helper()
	if err := d.Streams.Create(s); err != nil {
		t.Fatalf("create stream: %v", err)
	}
}

func remoteAddr(addr string) net.Addr {
	a, _ := net.ResolveTCPAddr("tcp", addr)
	return a
}

func TestAllowUnregistered(t *testing.T) {
	d := openDB(t)
	g := NewGuard(d.Streams, nil, nil, true)
	sid := &relay.StreamID{Mode: relay.ModePublish, Name: "unknown"}
	pass, err := g.Authorize(sid, remoteAddr("10.0.0.1:1234"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass != "" {
		t.Errorf("expected empty passphrase, got %q", pass)
	}
}

func TestRejectUnregistered(t *testing.T) {
	d := openDB(t)
	g := NewGuard(d.Streams, nil, nil, false)
	sid := &relay.StreamID{Mode: relay.ModePublish, Name: "ghost"}
	_, err := g.Authorize(sid, remoteAddr("10.0.0.1:1234"))
	if err == nil {
		t.Error("expected error for unregistered stream with allowAnon=false")
	}
}

func TestDisabledStream(t *testing.T) {
	d := openDB(t)
	addStream(t, d, &model.Stream{
		ID: "s1", Name: "off", Enabled: false,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	g := NewGuard(d.Streams, nil, nil, false)
	_, err := g.Authorize(&relay.StreamID{Name: "off"}, remoteAddr("10.0.0.1:1234"))
	if err == nil {
		t.Error("expected error for disabled stream")
	}
}

func TestPublisherAllowlist(t *testing.T) {
	d := openDB(t)
	addStream(t, d, &model.Stream{
		ID: "s1", Name: "restricted", Enabled: true,
		AllowedPublishers: []string{"192.168.1.0/24"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	g := NewGuard(d.Streams, nil, nil, false)

	// Allowed IP
	_, err := g.Authorize(&relay.StreamID{Mode: relay.ModePublish, Name: "restricted"},
		remoteAddr("192.168.1.50:1234"))
	if err != nil {
		t.Errorf("allowed IP rejected: %v", err)
	}

	// Disallowed IP
	_, err = g.Authorize(&relay.StreamID{Mode: relay.ModePublish, Name: "restricted"},
		remoteAddr("10.0.0.1:1234"))
	if err == nil {
		t.Error("expected rejection for disallowed IP")
	}

	// Subscribers bypass the publisher allowlist
	_, err = g.Authorize(&relay.StreamID{Mode: relay.ModeRequest, Name: "restricted"},
		remoteAddr("10.0.0.1:1234"))
	if err != nil {
		t.Errorf("subscriber should not be checked against publisher allowlist: %v", err)
	}
}

func TestEncryptionPassthroughNoKey(t *testing.T) {
	d := openDB(t)
	// Passphrase stored as plaintext (no encryption key configured).
	addStream(t, d, &model.Stream{
		ID: "s1", Name: "enc-stream", Enabled: true,
		Passphrase: "mytestpassphrase",
		KeyLength:  32,
		CreatedAt:  time.Now(), UpdatedAt: time.Now(),
	})
	g := NewGuard(d.Streams, nil, nil, false)
	pass, err := g.Authorize(&relay.StreamID{Name: "enc-stream"}, remoteAddr("1.2.3.4:1234"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass != "mytestpassphrase" {
		t.Errorf("expected passphrase %q, got %q", "mytestpassphrase", pass)
	}
}

func TestMatchCIDR(t *testing.T) {
	tests := []struct {
		ip   string
		list []string
		want bool
	}{
		{"192.168.1.1", []string{"192.168.1.0/24"}, true},
		{"192.168.2.1", []string{"192.168.1.0/24"}, false},
		{"10.0.0.1", []string{"10.0.0.1"}, true},
		{"10.0.0.2", []string{"10.0.0.1"}, false},
		{"::1", []string{"::1/128"}, true},
	}
	for _, tt := range tests {
		got := matchCIDR(tt.ip, tt.list)
		if got != tt.want {
			t.Errorf("matchCIDR(%q, %v) = %v, want %v", tt.ip, tt.list, got, tt.want)
		}
	}
}
