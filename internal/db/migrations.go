package db

// migrations is the ordered list of SQL DDL statements.
// Each entry is applied exactly once; schema_version tracks the current level.
var migrations = []string{
	// v1: core schema
	`CREATE TABLE IF NOT EXISTS streams (
		id               TEXT PRIMARY KEY,
		name             TEXT UNIQUE NOT NULL,
		description      TEXT    NOT NULL DEFAULT '',
		passphrase_enc   TEXT    NOT NULL DEFAULT '',
		key_length       INTEGER NOT NULL DEFAULT 0,
		max_subscribers  INTEGER NOT NULL DEFAULT 0,
		allowed_publishers TEXT  NOT NULL DEFAULT '[]',
		enabled          INTEGER NOT NULL DEFAULT 1,
		created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE TABLE IF NOT EXISTS users (
		id            TEXT PRIMARY KEY,
		username      TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		role          TEXT NOT NULL DEFAULT 'viewer',
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE TABLE IF NOT EXISTS api_keys (
		id           TEXT PRIMARY KEY,
		user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		key_hash     TEXT UNIQUE NOT NULL,
		key_prefix   TEXT NOT NULL,
		name         TEXT NOT NULL DEFAULT '',
		expires_at   DATETIME,
		last_used_at DATETIME,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE TABLE IF NOT EXISTS connection_log (
		id               TEXT PRIMARY KEY,
		stream_name      TEXT    NOT NULL,
		remote_addr      TEXT    NOT NULL,
		mode             TEXT    NOT NULL,
		connected_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		disconnected_at  DATETIME,
		bytes_sent       INTEGER NOT NULL DEFAULT 0,
		bytes_received   INTEGER NOT NULL DEFAULT 0,
		packets_lost     INTEGER NOT NULL DEFAULT 0
	)`,

	`CREATE INDEX IF NOT EXISTS idx_connection_log_stream ON connection_log(stream_name)`,
	`CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id)`,
}
