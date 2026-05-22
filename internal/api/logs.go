package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/yavik-kapadia/bastion/internal/db"
)

const (
	logsDefaultLimit = 200
	logsMaxLimit     = 1000
)

// logEntry is the wire shape returned to clients. `Attrs` is decoded so
// callers don't have to double-parse.
type logEntry struct {
	ID     int64          `json:"id"`
	TS     int64          `json:"ts"`
	Level  string         `json:"level"`
	Stream *string        `json:"stream"` // nil for global
	Msg    string         `json:"msg"`
	Attrs  map[string]any `json:"attrs"`
}

func toEntry(e db.EventLog) logEntry {
	attrs := map[string]any{}
	if e.Attrs != "" {
		_ = json.Unmarshal([]byte(e.Attrs), &attrs)
	}
	out := logEntry{
		ID:    e.ID,
		TS:    e.TS,
		Level: e.Level,
		Msg:   e.Msg,
		Attrs: attrs,
	}
	if e.Stream != "" {
		s := e.Stream
		out.Stream = &s
	}
	return out
}

func parseLimit(r *http.Request) int {
	q := r.URL.Query().Get("limit")
	if q == "" {
		return logsDefaultLimit
	}
	n, err := strconv.Atoi(q)
	if err != nil || n <= 0 {
		return logsDefaultLimit
	}
	if n > logsMaxLimit {
		return logsMaxLimit
	}
	return n
}

func parseSince(r *http.Request) int64 {
	q := r.URL.Query().Get("since")
	if q == "" {
		return 0
	}
	n, err := strconv.ParseInt(q, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// streamLogs GET /api/v1/streams/{name}/logs
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		respondError(w, http.StatusBadRequest, "stream name required")
		return
	}
	limit := parseLimit(r)
	since := parseSince(r)
	rows, err := s.db.EventLogs.ListByStream(name, limit, since)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list logs")
		return
	}
	out := make([]logEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEntry(e))
	}
	respond(w, http.StatusOK, out)
}

// globalLogs GET /api/v1/logs
func (s *Server) globalLogs(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r)
	since := parseSince(r)
	rows, err := s.db.EventLogs.ListGlobal(limit, since)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list logs")
		return
	}
	out := make([]logEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEntry(e))
	}
	respond(w, http.StatusOK, out)
}
