package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yavik-kapadia/bastion/internal/db"
	"github.com/yavik-kapadia/bastion/internal/metrics"
	"github.com/yavik-kapadia/bastion/internal/model"
	"github.com/yavik-kapadia/bastion/internal/ws"
)

// newLogsTestServer builds a minimal server with the logs endpoints wired
// in. We need both a viewer (forbidden) and an admin (allowed) user.
func newLogsTestServer(t *testing.T) (*httptest.Server, *db.DB) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.Users.Create("a", "admin", "admin-password", model.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := database.Users.Create("v", "viewer", "viewer-password", model.RoleViewer); err != nil {
		t.Fatalf("create viewer: %v", err)
	}

	hub := ws.NewHub()
	srv, err := NewServer(database, &fakeRelay{}, metrics.NewProm(), hub, nil, "", "", "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/api/v1/auth/login", srv.login)
	r.Group(func(r chi.Router) {
		r.Use(srv.requireManager)
		r.Use(csrfProtect)
		r.Get("/api/v1/streams/{name}/logs", srv.streamLogs)
		r.Get("/api/v1/logs", srv.globalLogs)
	})
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, database
}

func TestLogsRequireManager(t *testing.T) {
	ts, _ := newLogsTestServer(t)
	token := loginAs(t, ts, "viewer", "viewer-password")

	resp := authDo(t, ts, "GET", "/api/v1/logs", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer should be forbidden, got %d", resp.StatusCode)
	}
}

func TestGlobalLogsHappyPath(t *testing.T) {
	ts, database := newLogsTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	now := time.Now().UnixNano()
	_, err := database.EventLogs.Insert([]db.EventLog{
		{TS: now - 100, Level: "info", Stream: "alpha", Msg: "hello"},
		{TS: now, Level: "warn", Stream: "", Msg: "global"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := authDo(t, ts, "GET", "/api/v1/logs?limit=10", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Level  string  `json:"level"`
			Stream *string `json:"stream"`
			Msg    string  `json:"msg"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(body.Data))
	}
	// Oldest first.
	if body.Data[0].Msg != "hello" || body.Data[1].Msg != "global" {
		t.Errorf("order wrong: %+v", body.Data)
	}
	if body.Data[0].Stream == nil || *body.Data[0].Stream != "alpha" {
		t.Errorf("stream should be alpha")
	}
	if body.Data[1].Stream != nil {
		t.Errorf("global row stream should be nil, got %v", *body.Data[1].Stream)
	}
}

func TestStreamLogsHappyPath(t *testing.T) {
	ts, database := newLogsTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	now := time.Now().UnixNano()
	_, err := database.EventLogs.Insert([]db.EventLog{
		{TS: now - 200, Level: "info", Stream: "alpha", Msg: "a1"},
		{TS: now - 100, Level: "info", Stream: "alpha", Msg: "a2"},
		{TS: now, Level: "info", Stream: "beta", Msg: "b1"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := authDo(t, ts, "GET", "/api/v1/streams/alpha/logs", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Msg    string  `json:"msg"`
			Stream *string `json:"stream"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("expected 2 alpha rows, got %d", len(body.Data))
	}
	for _, row := range body.Data {
		if row.Stream == nil || *row.Stream != "alpha" {
			t.Errorf("non-alpha leaked: %+v", row)
		}
	}
}

func TestStreamLogsSinceCursor(t *testing.T) {
	ts, database := newLogsTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	base := time.Now().UnixNano()
	_, err := database.EventLogs.Insert([]db.EventLog{
		{TS: base, Level: "info", Stream: "s", Msg: "first"},
		{TS: base + 1, Level: "info", Stream: "s", Msg: "second"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	url := "/api/v1/streams/s/logs?since=" + jitoa(base)
	resp := authDo(t, ts, "GET", url, token, nil)
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			Msg string `json:"msg"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Msg != "second" {
		t.Errorf("since cursor wrong: %+v", body.Data)
	}
}

func jitoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
