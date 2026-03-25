package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yavik14/bastion/internal/model"
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

// Authenticate returns the User if username+password are valid.
func (r *UserRepo) Authenticate(username, password string) (*model.User, error) {
	u, err := r.GetByUsername(username)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
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

// Delete removes a user by ID.
func (r *UserRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// CreateAPIKey generates a new random API key for userID, stores its SHA-256 hash,
// and returns the raw key (shown once).
func (r *UserRepo) CreateAPIKey(keyID, userID, name string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	rawHex := hex.EncodeToString(raw)

	sum := sha256.Sum256([]byte(rawHex))
	keyHash := hex.EncodeToString(sum[:])
	prefix := rawHex[:8]

	_, err := r.db.Exec(`
		INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		keyID, userID, keyHash, prefix, name, time.Now().UTC(),
	)
	if err != nil {
		return "", fmt.Errorf("insert api key: %w", err)
	}
	return rawHex, nil
}

// ValidateAPIKey looks up a key by its SHA-256 hash, updates last_used_at, and
// returns the associated User.
func (r *UserRepo) ValidateAPIKey(rawKey string) (*model.User, error) {
	sum := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(sum[:])

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
		SELECT id, user_id, key_prefix, name, last_used_at, created_at
		FROM api_keys WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.APIKey
	for rows.Next() {
		k := &model.APIKey{}
		var lastUsed sql.NullTime
		if err := rows.Scan(&k.ID, &k.UserID, &k.KeyHash, &k.Name, &lastUsed, &k.CreatedAt); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			t := lastUsed.Time
			k.LastUsedAt = &t
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
