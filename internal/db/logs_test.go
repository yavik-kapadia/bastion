package db

import (
	"testing"
	"time"
)

func TestEventLogsInsertList(t *testing.T) {
	d := openTestDB(t)

	now := time.Now().UnixNano()
	recs := []EventLog{
		{TS: now - 3000, Level: "info", Stream: "alpha", Msg: "a1", Attrs: `{"k":"v1"}`},
		{TS: now - 2000, Level: "warn", Stream: "alpha", Msg: "a2", Attrs: `{"k":"v2"}`},
		{TS: now - 1000, Level: "info", Stream: "", Msg: "g1", Attrs: `{}`},
		{TS: now, Level: "error", Stream: "beta", Msg: "b1", Attrs: `{"err":"boom"}`},
	}

	ids, err := d.EventLogs.Insert(recs)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if len(ids) != 4 {
		t.Fatalf("expected 4 ids, got %d", len(ids))
	}
	for i, id := range ids {
		if id == 0 {
			t.Errorf("id[%d] = 0", i)
		}
	}

	// ListByStream returns oldest-first.
	got, err := d.EventLogs.ListByStream("alpha", 10, 0)
	if err != nil {
		t.Fatalf("ListByStream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("alpha expected 2, got %d", len(got))
	}
	if got[0].Msg != "a1" || got[1].Msg != "a2" {
		t.Errorf("order wrong: %+v", got)
	}
	if got[0].Stream != "alpha" {
		t.Errorf("stream not preserved: %q", got[0].Stream)
	}

	// Global list returns everything, oldest-first.
	all, err := d.EventLogs.ListGlobal(10, 0)
	if err != nil {
		t.Fatalf("ListGlobal: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("global expected 4, got %d", len(all))
	}
	if all[0].Msg != "a1" || all[3].Msg != "b1" {
		t.Errorf("global order wrong: %+v", all)
	}
	// Verify global row (stream NULL) round-trips as empty string.
	foundGlobal := false
	for _, e := range all {
		if e.Msg == "g1" {
			foundGlobal = true
			if e.Stream != "" {
				t.Errorf("global row should have empty stream, got %q", e.Stream)
			}
		}
	}
	if !foundGlobal {
		t.Error("missing global row")
	}
}

func TestEventLogsSinceCursor(t *testing.T) {
	d := openTestDB(t)
	base := time.Now().UnixNano()
	_, err := d.EventLogs.Insert([]EventLog{
		{TS: base, Level: "info", Stream: "x", Msg: "first"},
		{TS: base + 100, Level: "info", Stream: "x", Msg: "second"},
		{TS: base + 200, Level: "info", Stream: "x", Msg: "third"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := d.EventLogs.ListByStream("x", 10, base+50)
	if err != nil {
		t.Fatalf("ListByStream: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 after cursor, got %d", len(got))
	}
	if got[0].Msg != "second" || got[1].Msg != "third" {
		t.Errorf("cursor results wrong: %+v", got)
	}
}

func TestEventLogsLimit(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UnixNano()
	batch := make([]EventLog, 50)
	for i := range batch {
		batch[i] = EventLog{
			TS:     now + int64(i),
			Level:  "info",
			Stream: "many",
			Msg:    "msg",
		}
	}
	if _, err := d.EventLogs.Insert(batch); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := d.EventLogs.ListByStream("many", 10, 0)
	if err != nil {
		t.Fatalf("ListByStream: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("expected limit=10, got %d", len(got))
	}
	// Should be the 10 newest, in chronological order.
	if got[0].TS != now+40 || got[9].TS != now+49 {
		t.Errorf("limit didn't pick newest 10 in ASC order: first=%d last=%d", got[0].TS, got[9].TS)
	}
}

func TestEventLogsPurge(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour).UnixNano()
	recent := now.Add(-1 * time.Minute).UnixNano()

	_, err := d.EventLogs.Insert([]EventLog{
		{TS: old, Level: "info", Stream: "s", Msg: "stale"},
		{TS: old + 1, Level: "info", Stream: "s", Msg: "stale2"},
		{TS: recent, Level: "info", Stream: "s", Msg: "fresh"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	n, err := d.EventLogs.PurgeOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 deleted, got %d", n)
	}

	rem, err := d.EventLogs.ListByStream("s", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rem) != 1 || rem[0].Msg != "fresh" {
		t.Errorf("purge removed wrong rows: %+v", rem)
	}
}

func TestEventLogsStreamTsIndexUsed(t *testing.T) {
	d := openTestDB(t)
	// EXPLAIN QUERY PLAN should reference the idx_event_logs_stream_ts index.
	rows, err := d.sql.Query(`EXPLAIN QUERY PLAN
		SELECT id, ts, level, stream, msg, attrs
		FROM event_logs WHERE stream = ? AND ts > ?
		ORDER BY ts DESC LIMIT ?`, "alpha", int64(0), 10)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var sawIndex bool
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if contains(detail, "idx_event_logs_stream_ts") {
			sawIndex = true
		}
	}
	if !sawIndex {
		t.Error("expected plan to use idx_event_logs_stream_ts")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
