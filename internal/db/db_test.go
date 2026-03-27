package db

import (
	"database/sql"
	"testing"
	"time"

	"github.com/yavik-kapadia/bastion/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// --- Stream tests ---

func TestStreamCRUD(t *testing.T) {
	d := openTestDB(t)

	s := &model.Stream{
		ID:             "s1",
		Name:           "test-stream",
		Description:    "integration test",
		Passphrase:     "enc:secret",
		KeyLength:      32,
		MaxSubscribers: 10,
		AllowedPublishers: []string{"192.168.1.0/24"},
		Enabled:        true,
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		UpdatedAt:      time.Now().UTC().Truncate(time.Second),
	}

	// Create
	if err := d.Streams.Create(s); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	got, err := d.Streams.Get("test-stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != s.Name {
		t.Errorf("name: got %q want %q", got.Name, s.Name)
	}
	if got.KeyLength != 32 {
		t.Errorf("key_length: got %d want 32", got.KeyLength)
	}
	if !got.Enabled {
		t.Error("expected enabled=true")
	}
	if len(got.AllowedPublishers) != 1 || got.AllowedPublishers[0] != "192.168.1.0/24" {
		t.Errorf("allowed_publishers: %v", got.AllowedPublishers)
	}

	// List
	list, err := d.Streams.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 stream, got %d", len(list))
	}

	// Update
	got.Description = "updated"
	got.MaxSubscribers = 50
	if err := d.Streams.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, _ := d.Streams.Get("test-stream")
	if updated.Description != "updated" {
		t.Errorf("description not updated: %q", updated.Description)
	}

	// Delete
	if err := d.Streams.Delete("s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = d.Streams.Get("test-stream")
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestStreamDuplicateName(t *testing.T) {
	d := openTestDB(t)
	s := &model.Stream{ID: "a", Name: "dup", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := d.Streams.Create(s); err != nil {
		t.Fatal(err)
	}
	s2 := &model.Stream{ID: "b", Name: "dup", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := d.Streams.Create(s2); err == nil {
		t.Error("expected error on duplicate name, got nil")
	}
}

// --- User / API key tests ---

func TestUserCRUD(t *testing.T) {
	d := openTestDB(t)

	if err := d.Users.Create("u1", "alice", "password123", model.RoleAdmin); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	// Authenticate
	u, err := d.Users.Authenticate("alice", "password123")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if u.Role != model.RoleAdmin {
		t.Errorf("role: got %q want admin", u.Role)
	}

	// Wrong password
	if _, err := d.Users.Authenticate("alice", "wrong"); err == nil {
		t.Error("expected error with wrong password")
	}

	// List
	users, err := d.Users.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Errorf("expected 1 user, got %d", len(users))
	}
}

func TestAPIKey(t *testing.T) {
	d := openTestDB(t)
	if err := d.Users.Create("u1", "bob", "pass12345", model.RoleManager); err != nil {
		t.Fatal(err)
	}

	rawKey, err := d.Users.CreateAPIKey("k1", "u1", "ci-bot")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if len(rawKey) == 0 {
		t.Fatal("empty raw key")
	}

	// Validate correct key
	u, err := d.Users.ValidateAPIKey(rawKey)
	if err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
	if u.Username != "bob" {
		t.Errorf("expected bob, got %q", u.Username)
	}

	// Validate wrong key
	if _, err := d.Users.ValidateAPIKey("wrongkey"); err == nil {
		t.Error("expected error with wrong key")
	}

	// Delete
	if err := d.Users.DeleteAPIKey("k1"); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := d.Users.ValidateAPIKey(rawKey); err == nil {
		t.Error("expected error after key deletion")
	}
}

// --- Migration idempotency ---

func TestMigrationsIdempotent(t *testing.T) {
	d := openTestDB(t)
	// Run migrations a second time via a new Open on the same in-memory DB —
	// we can't reopen :memory:, so test idempotency by calling runMigrations directly.
	if err := runMigrations(d.sql); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}
