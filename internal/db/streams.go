package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yavik-kapadia/bastion/internal/model"
)

// StreamRepo provides CRUD operations for stream configurations.
type StreamRepo struct {
	db *sql.DB
}

// Create persists a new stream record. The ID must be set by the caller (use a UUID).
// passphrase in s should already be encrypted before calling Create.
func (r *StreamRepo) Create(s *model.Stream) error {
	apJSON, err := json.Marshal(s.AllowedPublishers)
	if err != nil {
		return fmt.Errorf("marshal allowed_publishers: %w", err)
	}
	_, err = r.db.Exec(`
		INSERT INTO streams
			(id, name, description, passphrase_enc, key_length, max_subscribers, allowed_publishers, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.Description, s.Passphrase, s.KeyLength,
		s.MaxSubscribers, string(apJSON), boolInt(s.Enabled),
		s.CreatedAt.UTC(), s.UpdatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert stream %q: %w", s.Name, err)
	}
	return nil
}

// Get retrieves a stream by name. Returns sql.ErrNoRows if not found.
func (r *StreamRepo) Get(name string) (*model.Stream, error) {
	row := r.db.QueryRow(`
		SELECT id, name, description, passphrase_enc, key_length, max_subscribers,
		       allowed_publishers, enabled, created_at, updated_at
		FROM streams WHERE name = ?`, name)
	return scanStream(row)
}

// GetByID retrieves a stream by ID.
func (r *StreamRepo) GetByID(id string) (*model.Stream, error) {
	row := r.db.QueryRow(`
		SELECT id, name, description, passphrase_enc, key_length, max_subscribers,
		       allowed_publishers, enabled, created_at, updated_at
		FROM streams WHERE id = ?`, id)
	return scanStream(row)
}

// List returns all streams ordered by name.
func (r *StreamRepo) List() ([]*model.Stream, error) {
	rows, err := r.db.Query(`
		SELECT id, name, description, passphrase_enc, key_length, max_subscribers,
		       allowed_publishers, enabled, created_at, updated_at
		FROM streams ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list streams: %w", err)
	}
	defer rows.Close()

	var out []*model.Stream
	for rows.Next() {
		s, err := scanStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Update replaces mutable fields of an existing stream (looked up by ID).
func (r *StreamRepo) Update(s *model.Stream) error {
	apJSON, err := json.Marshal(s.AllowedPublishers)
	if err != nil {
		return fmt.Errorf("marshal allowed_publishers: %w", err)
	}
	res, err := r.db.Exec(`
		UPDATE streams SET
			name = ?, description = ?, passphrase_enc = ?, key_length = ?,
			max_subscribers = ?, allowed_publishers = ?, enabled = ?,
			updated_at = ?
		WHERE id = ?`,
		s.Name, s.Description, s.Passphrase, s.KeyLength,
		s.MaxSubscribers, string(apJSON), boolInt(s.Enabled),
		time.Now().UTC(), s.ID,
	)
	if err != nil {
		return fmt.Errorf("update stream %q: %w", s.ID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("stream %q not found", s.ID)
	}
	return nil
}

// Delete removes a stream by ID.
func (r *StreamRepo) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM streams WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete stream %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("stream %q not found", id)
	}
	return nil
}

// scanner abstracts *sql.Row and *sql.Rows for scanStream.
type scanner interface {
	Scan(dest ...any) error
}

func scanStream(s scanner) (*model.Stream, error) {
	var (
		stream   model.Stream
		apJSON   string
		enabled  int
		created  time.Time
		updated  time.Time
	)
	err := s.Scan(
		&stream.ID, &stream.Name, &stream.Description,
		&stream.Passphrase, &stream.KeyLength, &stream.MaxSubscribers,
		&apJSON, &enabled, &created, &updated,
	)
	if err != nil {
		return nil, err
	}
	stream.Enabled = enabled != 0
	stream.CreatedAt = created
	stream.UpdatedAt = updated
	if err := json.Unmarshal([]byte(apJSON), &stream.AllowedPublishers); err != nil {
		stream.AllowedPublishers = nil
	}
	return &stream, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
