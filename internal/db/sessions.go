package db

import (
	"database/sql"
	"time"
)

// ConnectionLog is an audit record for a single SRT connection.
type ConnectionLog struct {
	ID             string
	StreamName     string
	RemoteAddr     string
	Mode           string
	ConnectedAt    time.Time
	DisconnectedAt *time.Time
	BytesSent      int64
	BytesReceived  int64
	PacketsLost    int64
}

// SessionRepo logs SRT connection lifecycle events.
type SessionRepo struct {
	db *sql.DB
}

// LogConnect records a new connection.
func (r *SessionRepo) LogConnect(id, streamName, remoteAddr, mode string) error {
	_, err := r.db.Exec(`
		INSERT INTO connection_log (id, stream_name, remote_addr, mode, connected_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, streamName, remoteAddr, mode, time.Now().UTC(),
	)
	return err
}

// LogDisconnect updates the record when a connection closes.
func (r *SessionRepo) LogDisconnect(id string, bytesSent, bytesRecv, pktsLost int64) error {
	_, err := r.db.Exec(`
		UPDATE connection_log
		SET disconnected_at = ?, bytes_sent = ?, bytes_received = ?, packets_lost = ?
		WHERE id = ?`,
		time.Now().UTC(), bytesSent, bytesRecv, pktsLost, id,
	)
	return err
}

// Recent returns the last n connection log entries for a stream (or all streams if streamName is "").
func (r *SessionRepo) Recent(streamName string, n int) ([]*ConnectionLog, error) {
	var rows *sql.Rows
	var err error
	if streamName == "" {
		rows, err = r.db.Query(`
			SELECT id, stream_name, remote_addr, mode, connected_at, disconnected_at,
			       bytes_sent, bytes_received, packets_lost
			FROM connection_log ORDER BY connected_at DESC LIMIT ?`, n)
	} else {
		rows, err = r.db.Query(`
			SELECT id, stream_name, remote_addr, mode, connected_at, disconnected_at,
			       bytes_sent, bytes_received, packets_lost
			FROM connection_log WHERE stream_name = ? ORDER BY connected_at DESC LIMIT ?`,
			streamName, n)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ConnectionLog
	for rows.Next() {
		l := &ConnectionLog{}
		var disc sql.NullTime
		if err := rows.Scan(
			&l.ID, &l.StreamName, &l.RemoteAddr, &l.Mode,
			&l.ConnectedAt, &disc,
			&l.BytesSent, &l.BytesReceived, &l.PacketsLost,
		); err != nil {
			return nil, err
		}
		if disc.Valid {
			t := disc.Time
			l.DisconnectedAt = &t
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
