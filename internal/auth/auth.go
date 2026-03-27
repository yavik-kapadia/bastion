// Package auth wires stream-level access control into the SRT relay.
package auth

import (
	"fmt"
	"net"

	"github.com/yavik14/bastion/internal/crypto"
	"github.com/yavik14/bastion/internal/db"
	"github.com/yavik14/bastion/internal/relay"
)

// StreamStatsGetter is the relay subset used to enforce subscriber caps.
type StreamStatsGetter interface {
	StreamStats(name string) (relay.StreamStats, bool)
}

// Guard implements relay.AuthFunc by looking up stream configurations in the
// database and enforcing encryption, publisher allowlists, and subscriber caps.
type Guard struct {
	streams   *db.StreamRepo
	relay     StreamStatsGetter
	encKey    []byte
	allowAnon bool // allow streams not registered in DB
}

// NewGuard creates a Guard.
// encKey is the AES key used to decrypt stored passphrases (may be nil).
// allowAnon permits connections to streams not registered in the database.
// r is used to check live subscriber counts for MaxSubscribers enforcement.
func NewGuard(streams *db.StreamRepo, r StreamStatsGetter, encKey []byte, allowAnon bool) *Guard {
	return &Guard{streams: streams, relay: r, encKey: encKey, allowAnon: allowAnon}
}

// Authorize is the relay.AuthFunc signature.
// It returns the plaintext passphrase to apply (empty = no encryption),
// or an error to reject the connection.
func (g *Guard) Authorize(sid *relay.StreamID, remoteAddr net.Addr) (string, error) {
	stream, err := g.streams.Get(sid.Name)
	if err != nil {
		// Stream not in DB.
		if g.allowAnon {
			return "", nil // allow, no encryption
		}
		return "", fmt.Errorf("stream %q not registered", sid.Name)
	}

	if !stream.Enabled {
		return "", fmt.Errorf("stream %q is disabled", sid.Name)
	}

	// Enforce subscriber cap (subscribe mode only).
	if sid.Mode == relay.ModeRequest && stream.MaxSubscribers > 0 && g.relay != nil {
		if stats, ok := g.relay.StreamStats(sid.Name); ok {
			if stats.SubscriberCount >= stream.MaxSubscribers {
				return "", fmt.Errorf("stream %q is at subscriber capacity (%d/%d)",
					sid.Name, stats.SubscriberCount, stream.MaxSubscribers)
			}
		}
	}

	// Enforce publisher allowlist (publish mode only).
	if sid.Mode == relay.ModePublish && len(stream.AllowedPublishers) > 0 {
		host, _, err := net.SplitHostPort(remoteAddr.String())
		if err != nil {
			host = remoteAddr.String()
		}
		if !matchCIDR(host, stream.AllowedPublishers) {
			return "", fmt.Errorf("publisher %s not in allowlist for stream %q", host, sid.Name)
		}
	}

	// Decrypt passphrase for encryption-enabled streams.
	if stream.KeyLength > 0 && stream.Passphrase != "" {
		plain, err := crypto.Decrypt(g.encKey, stream.Passphrase)
		if err != nil {
			return "", fmt.Errorf("decrypt passphrase for stream %q: %w", sid.Name, err)
		}
		return plain, nil
	}

	return "", nil
}

// matchCIDR reports whether ip matches any entry in the CIDR/IP list.
func matchCIDR(ip string, list []string) bool {
	parsed := net.ParseIP(ip)
	for _, entry := range list {
		// Try as CIDR first.
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if parsed != nil && cidr.Contains(parsed) {
				return true
			}
			continue
		}
		// Fall back to exact IP match.
		if entry == ip {
			return true
		}
	}
	return false
}
