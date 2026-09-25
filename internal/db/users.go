package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yavik-kapadia/bastion/internal/model"
)

// UserRepo provides CRUD operations for user accounts and API keys.
type UserRepo struct {
	db *sql.DB
}

// Create creates a new user, hashing the password with bcrypt.
func (r *UserRepo) Create(id, username, password string, role model.Role) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().UTC()
	_, err = r.db.Exec(`
		INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, username, string(hash), string(role), now, now,
	)
	if err != nil {
		return fmt.Errorf("insert user %q: %w", username, err)
	}
	return nil
}

// dummyHash is a pre-computed bcrypt hash used to ensure constant-time
// response when a username does not exist, preventing user enumeration
// via timing side-channels.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-constant-time-placeholder"), bcrypt.DefaultCost)

// Authenticate returns the User if username+password are valid.
// Always runs bcrypt.CompareHashAndPassword to prevent timing attacks.
func (r *UserRepo) Authenticate(username, password string) (*model.User, error) {
	u, lookupErr := r.GetByUsername(username)
	hash := dummyHash
	if lookupErr == nil {
		hash = []byte(u.PasswordHash)
	}
	compareErr := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if lookupErr != nil || compareErr != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	return u, nil
}

// GetByUsername retrieves a user by username.
func (r *UserRepo) GetByUsername(username string) (*model.User, error) {
	row := r.db.QueryRow(`
		SELECT id, username, password_hash, role, created_at, updated_at
		FROM users WHERE username = ?`, username)
	return scanUser(row)
}

// GetByID retrieves a user by ID.
func (r *UserRepo) GetByID(id string) (*model.User, error) {
	row := r.db.QueryRow(`
		SELECT id, username, password_hash, role, created_at, updated_at
		FROM users WHERE id = ?`, id)
	return scanUser(row)
}

// List returns all users.
func (r *UserRepo) List() ([]*model.User, error) {
	rows, err := r.db.Query(`
		SELECT id, username, password_hash, role, created_at, updated_at
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Count returns the total number of users.
func (r *UserRepo) Count() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// Delete removes a user by ID.
func (r *UserRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// CreateAPIKey generates a new random API key for userID, stores its SHA-256 hash,
// and returns the raw key (shown once). The key never expires.
func (r *UserRepo) CreateAPIKey(keyID, userID, name string) (string, error) {
	return r.insertAPIKey(keyID, userID, name, nil)
}

// CreateSessionKey is CreateAPIKey for login sessions: the key stops
// validating after expiresAt.
func (r *UserRepo) CreateSessionKey(keyID, userID, name string, expiresAt time.Time) (string, error) {
	exp := expiresAt.UTC()
	return r.insertAPIKey(keyID, userID, name, &exp)
}

func (r *UserRepo) insertAPIKey(keyID, userID, name string, expiresAt *time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	rawHex := hex.EncodeToString(raw)
	prefix := rawHex[:8]

	var exp sql.NullTime
	if expiresAt != nil {
		exp = sql.NullTime{Time: *expiresAt, Valid: true}
	}
	_, err := r.db.Exec(`
		INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		keyID, userID, hashKey(rawHex), prefix, name, exp, time.Now().UTC(),
	)
	if err != nil {
		return "", fmt.Errorf("insert api key: %w", err)
	}
	return rawHex, nil
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ValidateAPIKey looks up a key by its SHA-256 hash, updates last_used_at, and
// returns the associated User.
func (r *UserRepo) ValidateAPIKey(rawKey string) (*model.User, error) {
	keyHash := hashKey(rawKey)

	var userID string
	var expires sql.NullTime
	err := r.db.QueryRow(`
		SELECT user_id, expires_at FROM api_keys WHERE key_hash = ?`, keyHash,
	).Scan(&userID, &expires)
	if err != nil {
		return nil, fmt.Errorf("invalid api key")
	}
	if expires.Valid && expires.Time.Before(time.Now()) {
		return nil, fmt.Errorf("api key expired")
	}

	// Update last_used_at (best-effort).
	_, _ = r.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE key_hash = ?`,
		time.Now().UTC(), keyHash)

	return r.GetByID(userID)
}

// ListAPIKeys returns all API keys for a user (without the raw key).
func (r *UserRepo) ListAPIKeys(userID string) ([]*model.APIKey, error) {
	rows, err := r.db.Query(`
		SELECT id, user_id, key_prefix, name, last_used_at, expires_at, created_at
		FROM api_keys WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.APIKey
	for rows.Next() {
		k := &model.APIKey{}
		var lastUsed, expires sql.NullTime
		if err := rows.Scan(&k.ID, &k.UserID, &k.Prefix, &k.Name, &lastUsed, &expires, &k.CreatedAt); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			t := lastUsed.Time
			k.LastUsedAt = &t
		}
		if expires.Valid {
			t := expires.Time
			k.ExpiresAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey removes an API key by ID.
func (r *UserRepo) DeleteAPIKey(id string) error {
	_, err := r.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// DeleteUserAPIKey removes key id only if it belongs to userID.
// Returns sql.ErrNoRows if no such key exists for that user.
func (r *UserRepo) DeleteUserAPIKey(id, userID string) error {
	res, err := r.db.Exec(`DELETE FROM api_keys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RevokeAPIKey deletes the key matching rawKey (used on logout).
func (r *UserRepo) RevokeAPIKey(rawKey string) error {
	_, err := r.db.Exec(`DELETE FROM api_keys WHERE key_hash = ?`, hashKey(rawKey))
	return err
}

// PurgeExpiredAPIKeys deletes keys whose expires_at has passed. The expiry
// comparison happens in Go so it does not depend on how the driver serialises
// DATETIME values.
func (r *UserRepo) PurgeExpiredAPIKeys() (int, error) {
	rows, err := r.db.Query(`SELECT id, expires_at FROM api_keys WHERE expires_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var expired []string
	for rows.Next() {
		var id string
		var exp sql.NullTime
		if err := rows.Scan(&id, &exp); err != nil {
			rows.Close()
			return 0, err
		}
		if exp.Valid && exp.Time.Before(now) {
			expired = append(expired, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range expired {
		if err := r.DeleteAPIKey(id); err != nil {
			return 0, err
		}
	}
	return len(expired), nil
}

func scanUser(s scanner) (*model.User, error) {
	u := &model.User{}
	var created, updated time.Time
	err := s.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &created, &updated)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = created
	u.UpdatedAt = updated
	return u, nil
}
