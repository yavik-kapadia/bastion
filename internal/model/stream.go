package model

import "time"

// Stream represents a configured SRT relay stream.
type Stream struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Passphrase       string    `json:"passphrase,omitempty"` // plaintext; never stored directly
	KeyLength        int       `json:"key_length"`           // 0, 16, 24, or 32
	MaxSubscribers   int       `json:"max_subscribers"`      // 0 = unlimited
	AllowedPublishers []string `json:"allowed_publishers"`   // CIDR list; empty = any
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
