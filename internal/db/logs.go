package db

import (
	"database/sql"
	"fmt"
	"time"
)

// EventLog is a single structured log record stored in SQLite for live-tail.
// Records have a 24h TTL enforced by a background purge.
type EventLog struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`     // unix nanoseconds
	Level  string `json:"level"`  // "debug"|"info"|"warn"|"error"
	Stream string `json:"stream"` // "" means global
	Msg    string `json:"msg"`
	Attrs  string `json:"attrs"` // JSON-encoded slog attrs
}

// EventLogsRepo provides batched writes and bounded reads against event_logs.
type EventLogsRepo struct {
	db *sql.DB
}

// Insert writes a batch of records in a single transaction.
// Returns the inserted IDs in the same order as records.
func (r *EventLogsRepo) Insert(records []EventLog) ([]int64, error) {
	if len(records) == 0 {
		return nil, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO event_logs(ts, level, stream, msg, attrs) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback() //nolint:errcheck
		return nil, fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	ids := make([]int64, 0, len(records))
	for i := range records {
		var streamArg any
		if records[i].Stream == "" {
			streamArg = nil
		} else {
			streamArg = records[i].Stream
		}
		attrs := records[i].Attrs
		if attrs == "" {
			attrs = "{}"
		}
		res, err := stmt.Exec(records[i].TS, records[i].Level, streamArg, records[i].Msg, attrs)
		if err != nil {
			tx.Rollback() //nolint:errcheck
			return nil, fmt.Errorf("insert event_log: %w", err)
		}
		id, _ := res.LastInsertId()
		records[i].ID = id
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit event_logs: %w", err)
	}
	return ids, nil
}

// ListByStream returns up to limit entries for a specific stream, optionally
// filtered to entries strictly newer than sinceNanos (0 disables the filter).
// Results are ordered oldest-first so the tail renders chronologically.
func (r *EventLogsRepo) ListByStream(name string, limit int, sinceNanos int64) ([]EventLog, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.Query(
		`SELECT id, ts, level, stream, msg, attrs
		 FROM event_logs
		 WHERE stream = ? AND ts > ?
		 ORDER BY ts DESC
		 LIMIT ?`, name, sinceNanos, limit)
	if err != nil {
		return nil, fmt.Errorf("list event_logs by stream: %w", err)
	}
	defer rows.Close()
	return scanReversed(rows)
}

// ListGlobal returns up to limit entries across the whole table (both stream
// and global rows), strictly newer than sinceNanos (0 disables).
// Results are ordered oldest-first.
func (r *EventLogsRepo) ListGlobal(limit int, sinceNanos int64) ([]EventLog, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.Query(
		`SELECT id, ts, level, stream, msg, attrs
		 FROM event_logs
		 WHERE ts > ?
		 ORDER BY ts DESC
		 LIMIT ?`, sinceNanos, limit)
	if err != nil {
		return nil, fmt.Errorf("list event_logs global: %w", err)
	}
	defer rows.Close()
	return scanReversed(rows)
}

// PurgeOlderThan deletes rows older than maxAge and returns the number removed.
func (r *EventLogsRepo) PurgeOlderThan(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge).UnixNano()
	res, err := r.db.Exec(`DELETE FROM event_logs WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge event_logs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// trimBatchSize is the max rows deleted per iteration of TrimToBytes.
const trimBatchSize = 1000

// trimMaxIterations bounds the trim loop so a runaway writer cannot starve
// the purge goroutine — if we can't get under the cap in this many passes,
// give up until the next tick.
const trimMaxIterations = 10

// TrimToBytes deletes the oldest rows until the database's on-disk size
// is at or below the target. Returns the number of rows deleted.
//
// The target is a soft cap: SQLite VACUUM is not run automatically, so the
// real file size may stay above the cap until the next manual VACUUM (pages
// freed by DELETE stay allocated but unused, and WAL mode reuses them on
// subsequent inserts). The intent is to bound *steady-state* growth — for
// a hard ceiling, run VACUUM out of band.
//
// Size is estimated via PRAGMA page_count * page_size (whole-DB size). This
// over-counts when other tables are large, which is conservative — we may
// trim event_logs slightly sooner than strictly necessary. event_logs is
// expected to dominate in steady state on a live relay.
//
// maxBytes <= 0 disables trimming and returns (0, nil).
func (r *EventLogsRepo) TrimToBytes(maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		return 0, nil
	}

	size, err := r.dbSizeBytes()
	if err != nil {
		return 0, fmt.Errorf("size estimate: %w", err)
	}
	if size <= maxBytes {
		return 0, nil
	}

	var totalDeleted int64
	for i := 0; i < trimMaxIterations; i++ {
		res, err := r.db.Exec(
			`DELETE FROM event_logs
			 WHERE id IN (
			     SELECT id FROM event_logs ORDER BY ts ASC LIMIT ?
			 )`, trimBatchSize)
		if err != nil {
			return totalDeleted, fmt.Errorf("trim event_logs: %w", err)
		}
		n, _ := res.RowsAffected()
		totalDeleted += n
		if n == 0 {
			// Table is empty; nothing more we can do.
			break
		}

		size, err = r.dbSizeBytes()
		if err != nil {
			return totalDeleted, fmt.Errorf("size estimate: %w", err)
		}
		if size <= maxBytes {
			break
		}
	}
	return totalDeleted, nil
}

// dbSizeBytes returns an estimate of the whole-database on-disk size, in
// bytes, computed as page_count * page_size. This includes free (unused)
// pages — i.e. it tracks the file's allocated size, not just live data.
func (r *EventLogsRepo) dbSizeBytes() (int64, error) {
	var pageCount, pageSize int64
	if err := r.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("page_count: %w", err)
	}
	if err := r.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("page_size: %w", err)
	}
	return pageCount * pageSize, nil
}

// scanReversed scans rows in DESC order and returns them reversed (ASC).
func scanReversed(rows *sql.Rows) ([]EventLog, error) {
	var out []EventLog
	for rows.Next() {
		var (
			e      EventLog
			stream sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &stream, &e.Msg, &e.Attrs); err != nil {
			return nil, err
		}
		if stream.Valid {
			e.Stream = stream.String
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse in place so callers get oldest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
