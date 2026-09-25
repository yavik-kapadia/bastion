package model

import "time"

// APIKey is a long-lived bearer token used by automation / CI.
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyHash    string     `json:"-"`
	Prefix     string     `json:"prefix"` // first 8 hex chars of the raw key, for identification
	UserID     string     `json:"user_id"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"` // nil = never expires
}
