package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yavik14/bastion/internal/db"
	"github.com/yavik14/bastion/internal/metrics"
	"github.com/yavik14/bastion/internal/model"
	"github.com/yavik14/bastion/internal/relay"
	"github.com/yavik14/bastion/internal/ws"
)

// fakeRelay is a stub RelayReader for tests.
type fakeRelay struct{}

func (f *fakeRelay) ActiveStreams() map[string]relay.StreamStats { return nil }
func (f *fakeRelay) StreamStats(name string) (relay.StreamStats, bool) {
	return relay.StreamStats{}, false
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Create admin user.
	if err := database.Users.Create("admin-id", "admin", "admin-password", model.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	hub := ws.NewHub()
	srv, err := NewServer(database, &fakeRelay{}, metrics.NewProm(), hub, nil, "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{AllowedOrigins: []string{"*"}}))
	r.Post("/api/v1/auth/login", srv.login)
	r.Get("/metrics", promhttp.HandlerFor(srv.prom.Registry, promhttp.HandlerOpts{}).ServeHTTP)
	r.Group(func(r chi.Router) {
		r.Use(srv.requireAuth)
		r.Post("/api/v1/auth/api-keys", srv.createAPIKey)
		r.Get("/api/v1/streams", srv.listStreams)
		r.Post("/api/v1/streams", srv.createStream)
		r.Get("/api/v1/streams/{name}", srv.getStream)
		r.Put("/api/v1/streams/{name}", srv.updateStream)
		r.Delete("/api/v1/streams/{name}", srv.deleteStream)
		r.Get("/api/v1/metrics/global", srv.globalMetrics)
	})
	r.Group(func(r chi.Router) {
		r.Use(srv.requireAdmin)
		r.Get("/api/v1/users", srv.listUsers)
		r.Post("/api/v1/users", srv.createUser)
		r.Delete("/api/v1/users/{id}", srv.deleteUser)
	})

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return srv, ts
}

func loginAs(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status: %d", resp.StatusCode)
	}
	var res struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	return res.Data.Token
}

func authDo(t *testing.T, ts *httptest.Server, method, path, token string, body any) *http.Response {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, ts.URL+path, buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func TestLogin(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")
	if token == "" {
		t.Error("expected non-empty token")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	_, ts := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	resp, _ := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestStreamCRUD(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// Create
	resp := authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{
		"name":        "test-stream",
		"description": "integration",
		"key_length":  32,
		"passphrase":  "supersecret99",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create stream: got %d", resp.StatusCode)
	}

	// List
	resp = authDo(t, ts, "GET", "/api/v1/streams", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list streams: got %d", resp.StatusCode)
	}
	var listRes struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&listRes)
	if len(listRes.Data) != 1 {
		t.Errorf("expected 1 stream, got %d", len(listRes.Data))
	}
	// Passphrase must NOT be returned.
	if listRes.Data[0]["passphrase"] != nil && listRes.Data[0]["passphrase"] != "" {
		t.Error("passphrase should not be returned in list")
	}
	resp.Body.Close()

	// Get
	resp = authDo(t, ts, "GET", "/api/v1/streams/test-stream", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get stream: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Update
	resp = authDo(t, ts, "PUT", "/api/v1/streams/test-stream", token, map[string]any{
		"description": "updated",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update stream: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete
	resp = authDo(t, ts, "DELETE", "/api/v1/streams/test-stream", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete stream: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify gone
	resp = authDo(t, ts, "GET", "/api/v1/streams/test-stream", token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUnauthorized(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/streams", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestCreateStreamValidation(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// No name
	resp := authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", resp.StatusCode)
	}

	// Encryption enabled but passphrase too short
	resp = authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{
		"name":       "enc-stream",
		"key_length": 32,
		"passphrase": "short",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for short passphrase, got %d", resp.StatusCode)
	}

	_ = time.Now() // suppress unused import
}

func TestAdminOnlyEndpoints(t *testing.T) {
	_, ts := newTestServer(t)
	adminToken := loginAs(t, ts, "admin", "admin-password")

	// Create a viewer user
	resp := authDo(t, ts, "POST", "/api/v1/users", adminToken, map[string]any{
		"username": "viewer1",
		"password": "viewpass99",
		"role":     "viewer",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create viewer: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	viewerToken := loginAs(t, ts, "viewer1", "viewpass99")

	// Viewer tries to list users — should be forbidden
	resp = authDo(t, ts, "GET", "/api/v1/users", viewerToken, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for viewer on /users, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Admin can list users
	resp = authDo(t, ts, "GET", "/api/v1/users", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin should see users, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
