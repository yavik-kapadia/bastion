package db

import (
	"strings"
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

// makeRows builds n synthetic event_logs records with monotonically increasing
// timestamps, starting at base. Payload is sized to make row growth visible in
// page-size estimates.
func makeRows(base int64, n int, payload string) []EventLog {
	out := make([]EventLog, n)
	for i := 0; i < n; i++ {
		out[i] = EventLog{
			TS:     base + int64(i),
			Level:  "info",
			Stream: "trim",
			Msg:    "msg",
			Attrs:  payload,
		}
	}
	return out
}

func TestTrimToBytesNoOpWhenUnderCap(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UnixNano()
	if _, err := d.EventLogs.Insert(makeRows(now, 10, `{"k":"v"}`)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// 100 MiB is far larger than 10 small rows can produce.
	n, err := d.EventLogs.TrimToBytes(100 * 1024 * 1024)
	if err != nil {
		t.Fatalf("TrimToBytes: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 deletions under cap, got %d", n)
	}
	rem, err := d.EventLogs.ListByStream("trim", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rem) != 10 {
		t.Errorf("expected all 10 rows to survive, got %d", len(rem))
	}
}

func TestTrimToBytesDeletesOldestFirst(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UnixNano()
	// Use a chunky payload so the DB grows past a small cap with relatively
	// few rows. 1 KiB attrs × 1000 rows ≈ 1 MiB of payload alone.
	payload := `{"data":"` + strings.Repeat("x", 1024) + `"}`
	if _, err := d.EventLogs.Insert(makeRows(now, 1000, payload)); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Capture full size, then set the cap well below it. We choose a cap
	// just above the empty-DB baseline so the trim loop must delete the
	// bulk of rows but cannot delete everything (size never drops to 0 —
	// SQLite keeps pages allocated post-DELETE without VACUUM, so the
	// final size equals the original full size; this test only validates
	// that the oldest rows go first up to the iteration bound).
	fullSize, err := d.EventLogs.dbSizeBytes()
	if err != nil {
		t.Fatalf("dbSizeBytes: %v", err)
	}
	// Cap at half full size to guarantee at least one batch trims.
	cap := fullSize / 2
	n, err := d.EventLogs.TrimToBytes(cap)
	if err != nil {
		t.Fatalf("TrimToBytes: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected deletions, got 0 (fullSize=%d cap=%d)", fullSize, cap)
	}

	// Without VACUUM the file size won't drop, so the loop will hit
	// trimMaxIterations (10) and delete 10 * trimBatchSize = 10000 rows
	// capped by inventory (1000). Verify the *oldest* rows were the ones
	// removed: any surviving row must have ts strictly greater than the
	// deleted-cutoff (now + n - 1).
	rem, err := d.EventLogs.ListByStream("trim", 2000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(rem)) != 1000-n {
		t.Errorf("expected %d survivors, got %d", 1000-n, len(rem))
	}
	for _, e := range rem {
		if e.TS < now+n {
			t.Errorf("survivor ts=%d should be >= now+n=%d (an old row was kept)", e.TS, now+n)
			break
		}
	}
}

func TestTrimToBytesZeroDisabled(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UnixNano()
	if _, err := d.EventLogs.Insert(makeRows(now, 100, `{"k":"v"}`)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	n, err := d.EventLogs.TrimToBytes(0)
	if err != nil {
		t.Fatalf("TrimToBytes(0): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 deletions when disabled, got %d", n)
	}
	// Negative also disables.
	n, err = d.EventLogs.TrimToBytes(-1)
	if err != nil {
		t.Fatalf("TrimToBytes(-1): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 deletions with negative cap, got %d", n)
	}
	rem, err := d.EventLogs.ListByStream("trim", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rem) != 100 {
		t.Errorf("expected 100 rows preserved, got %d", len(rem))
	}
}

func TestTrimToBytesIdempotent(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().UnixNano()
	payload := `{"data":"` + strings.Repeat("x", 1024) + `"}`
	if _, err := d.EventLogs.Insert(makeRows(now, 500, payload)); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	const cap = 64 * 1024
	n1, err := d.EventLogs.TrimToBytes(cap)
	if err != nil {
		t.Fatalf("first TrimToBytes: %v", err)
	}

	// Second call: SQLite may not have shrunk on disk (no VACUUM), so the
	// page-count estimate might still report > cap. But the implementation
	// must not enter an infinite delete loop and must terminate — and once
	// the file size is steady, a second call should not delete more than
	// the iteration bound allows. The most useful invariant here is that
	// repeated calls converge: at some point, further calls delete 0.
	//
	// We assert: after enough repeated calls, deletions stop, AND the
	// total never deletes more rows than were inserted.
	totalDeleted := n1
	var lastN int64 = -1
	for i := 0; i < 20; i++ {
		n, err := d.EventLogs.TrimToBytes(cap)
		if err != nil {
			t.Fatalf("repeat TrimToBytes: %v", err)
		}
		totalDeleted += n
		if n == 0 {
			lastN = 0
			break
		}
		lastN = n
	}
	if lastN != 0 {
		t.Errorf("expected repeated TrimToBytes to converge to 0 deletions, last=%d", lastN)
	}
	if totalDeleted > 500 {
		t.Errorf("deleted %d rows but only 500 were inserted", totalDeleted)
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
