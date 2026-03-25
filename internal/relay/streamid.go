package relay

import (
	"fmt"
	"strings"
)

// Mode identifies whether the connecting client is publishing or subscribing.
type Mode int

const (
	ModePublish Mode = iota
	ModeRequest
)

func (m Mode) String() string {
	if m == ModePublish {
		return "publish"
	}
	return "request"
}

// StreamID holds the parsed fields from an SRT stream ID string.
type StreamID struct {
	Mode       Mode
	Name       string // stream resource name
	Passphrase string // optional auth passphrase embedded in stream ID
}

// ParseStreamID parses both the modern SRT Access Control format
// (#!::m=publish,r=name,s=pass) and the legacy slash format (publish/name).
// The passphrase field (s=) is optional in the modern format.
func ParseStreamID(raw string) (*StreamID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("stream ID is empty")
	}

	// Modern SRT Access Control format: #!::key=value,...
	if strings.HasPrefix(raw, "#!::") {
		return parseModern(raw[4:])
	}

	// Legacy slash format: mode/name[/passphrase]
	return parseLegacy(raw)
}

// parseModern handles "#!::m=publish,r=stream-name,s=passphrase".
func parseModern(kv string) (*StreamID, error) {
	sid := &StreamID{}
	for _, pair := range strings.Split(kv, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case "m":
			switch strings.TrimSpace(v) {
			case "publish":
				sid.Mode = ModePublish
			case "request", "play":
				sid.Mode = ModeRequest
			default:
				return nil, fmt.Errorf("unknown mode %q", v)
			}
		case "r":
			sid.Name = strings.TrimSpace(v)
		case "s":
			sid.Passphrase = strings.TrimSpace(v)
		}
	}
	if sid.Name == "" {
		return nil, fmt.Errorf("stream ID missing resource name (r=)")
	}
	return sid, nil
}

// parseLegacy handles "publish/name" and "publish/name/passphrase".
func parseLegacy(raw string) (*StreamID, error) {
	parts := strings.SplitN(raw, "/", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid legacy stream ID %q: expected mode/name", raw)
	}
	sid := &StreamID{}
	switch parts[0] {
	case "publish":
		sid.Mode = ModePublish
	case "request", "play":
		sid.Mode = ModeRequest
	default:
		return nil, fmt.Errorf("unknown mode %q", parts[0])
	}
	sid.Name = parts[1]
	if sid.Name == "" {
		return nil, fmt.Errorf("stream name must not be empty")
	}
	if len(parts) == 3 {
		sid.Passphrase = parts[2]
	}
	return sid, nil
}
