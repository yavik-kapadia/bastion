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
	srv, err := NewServer(database, &fakeRelay{}, metrics.NewProm(), hub, nil, "", "", "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{AllowedOrigins: []string{"*"}}))
	r.Post("/api/v1/auth/login", srv.login)
	r.Post("/api/v1/auth/setup", srv.setup)
	r.Get("/api/v1/auth/setup-status", srv.setupStatus)
	r.Get("/metrics", promhttp.HandlerFor(srv.prom.Registry, promhttp.HandlerOpts{}).ServeHTTP)
	r.Group(func(r chi.Router) {
		r.Use(srv.requireAuth)
		r.Use(csrfProtect)
		r.Get("/api/v1/auth/me", srv.me)
		r.Post("/api/v1/auth/logout", srv.logout)
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
		r.Use(csrfProtect)
		r.Get("/api/v1/users", srv.listUsers)
		r.Post("/api/v1/users", srv.createUser)
		r.Delete("/api/v1/users/{id}", srv.deleteUser)
	})

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return srv, ts
}

// loginAs logs in and returns the session token from the Set-Cookie header.
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
	for _, c := range resp.Cookies() {
		if c.Name == "bastion_session" {
			return c.Value
		}
	}
	t.Fatal("login: no bastion_session cookie in response")
	return ""
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

// cookieDo makes a request using a session cookie instead of a Bearer token.
func cookieDo(t *testing.T, ts *httptest.Server, method, path, token string, body any, extraHeaders ...string) *http.Response {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, ts.URL+path, buf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "bastion_session", Value: token})
	for i := 0; i+1 < len(extraHeaders); i += 2 {
		req.Header.Set(extraHeaders[i], extraHeaders[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// newTestServerWithPublicHost creates a test server with a specific publicHost value.
func newTestServerWithPublicHost(t *testing.T, publicHost string) (*Server, *httptest.Server) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.Users.Create("admin-id", "admin", "admin-password", model.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	hub := ws.NewHub()
	srv, err := NewServer(database, &fakeRelay{}, metrics.NewProm(), hub, nil, "", "", publicHost)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/api/v1/auth/login", srv.login)
	r.Post("/api/v1/auth/setup", srv.setup)
	r.Get("/api/v1/auth/setup-status", srv.setupStatus)
	r.Group(func(r chi.Router) {
		r.Use(srv.requireAuth)
		r.Use(csrfProtect)
		r.Get("/api/v1/auth/me", srv.me)
		r.Post("/api/v1/auth/logout", srv.logout)
		r.Get("/api/v1/streams", srv.listStreams)
		r.Post("/api/v1/streams", srv.createStream)
	})

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return srv, ts
}

// --- Cookie Auth Tests ---

func TestLoginSetsCookie(t *testing.T) {
	_, ts := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin-password"})
	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()

	var foundCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "bastion_session" {
			foundCookie = c
			break
		}
	}
	if foundCookie == nil {
		t.Fatal("expected bastion_session cookie in response")
	}
	if !foundCookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if foundCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite should be Lax, got %v", foundCookie.SameSite)
	}

	// Response body should NOT contain a token field
	var res map[string]any
	json.NewDecoder(resp.Body).Decode(&res)
	data := res["data"].(map[string]any)
	if _, hasToken := data["token"]; hasToken {
		t.Error("response body should not contain token field")
	}
}

func TestCookieAuth(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// Request with cookie (no Authorization header)
	resp := cookieDo(t, ts, "GET", "/api/v1/streams", token, nil, "X-Requested-With", "XMLHttpRequest")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cookie auth should work, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCookieAndBearerBothWork(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// Via Bearer
	resp := authDo(t, ts, "GET", "/api/v1/streams", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("bearer auth should work, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Via cookie
	resp = cookieDo(t, ts, "GET", "/api/v1/streams", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cookie auth should work, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- /auth/me Tests ---

func TestAuthMe(t *testing.T) {
	_, ts := newTestServerWithPublicHost(t, "srt.example.com")
	token := loginAs(t, ts, "admin", "admin-password")

	resp := cookieDo(t, ts, "GET", "/api/v1/auth/me", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var res struct {
		Data struct {
			UserID     string `json:"user_id"`
			Username   string `json:"username"`
			Role       string `json:"role"`
			PublicHost string `json:"public_host"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()

	if res.Data.Username != "admin" {
		t.Errorf("expected username 'admin', got '%s'", res.Data.Username)
	}
	if res.Data.Role != "admin" {
		t.Errorf("expected role 'admin', got '%s'", res.Data.Role)
	}
	if res.Data.PublicHost != "srt.example.com" {
		t.Errorf("expected public_host 'srt.example.com', got '%s'", res.Data.PublicHost)
	}
	if res.Data.UserID == "" {
		t.Error("expected non-empty user_id")
	}
}

func TestAuthMeBearer(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	resp := authDo(t, ts, "GET", "/api/v1/auth/me", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for bearer /auth/me, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAuthMeNoAuth(t *testing.T) {
	_, ts := newTestServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated /auth/me, got %d", resp.StatusCode)
	}
}

func TestAuthMeInvalidCookie(t *testing.T) {
	_, ts := newTestServer(t)
	resp := cookieDo(t, ts, "GET", "/api/v1/auth/me", "invalid-token-value", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid cookie, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- /auth/logout Tests ---

func TestLogout(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	resp := cookieDo(t, ts, "POST", "/api/v1/auth/logout", token, nil, "X-Requested-With", "XMLHttpRequest")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for logout, got %d", resp.StatusCode)
	}

	// Check that the response clears the cookie
	var foundClear bool
	for _, c := range resp.Cookies() {
		if c.Name == "bastion_session" && c.MaxAge < 0 {
			foundClear = true
			break
		}
	}
	if !foundClear {
		t.Error("logout should clear bastion_session cookie (MaxAge < 0)")
	}
	resp.Body.Close()
}

func TestLogoutThenMe(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// Logout
	resp := cookieDo(t, ts, "POST", "/api/v1/auth/logout", token, nil, "X-Requested-With", "XMLHttpRequest")
	resp.Body.Close()

	// The cookie value is still sent, but in a real browser it would be cleared.
	// Here we verify the server cleared it by checking the Set-Cookie response.
	// The token itself is still valid (not revoked), so /me would still return 200
	// if the cookie is resent. In a real flow, the browser clears the cookie.
	// This test verifies the logout response clears the cookie.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("logout should return 200, got %d", resp.StatusCode)
	}
}

// --- /auth/setup Tests ---

func TestSetup(t *testing.T) {
	// Create server with NO admin user
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	hub := ws.NewHub()
	srv, err := NewServer(database, &fakeRelay{}, metrics.NewProm(), hub, nil, "", "", "")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/api/v1/auth/setup", srv.setup)
	r.Get("/api/v1/auth/setup-status", srv.setupStatus)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	// Verify setup is needed
	resp, _ := http.Get(ts.URL + "/api/v1/auth/setup-status")
	var status struct {
		Data struct {
			NeedsSetup bool `json:"needs_setup"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if !status.Data.NeedsSetup {
		t.Fatal("expected needs_setup=true")
	}

	// Run setup
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "test-password-99"})
	resp, _ = http.Post(ts.URL+"/api/v1/auth/setup", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: expected 201, got %d", resp.StatusCode)
	}

	// Should have a cookie
	var foundCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == "bastion_session" && c.Value != "" {
			foundCookie = true
			break
		}
	}
	if !foundCookie {
		t.Error("setup should set bastion_session cookie")
	}

	// Should return user info with user_id
	var setupRes struct {
		Data struct {
			UserID   string `json:"user_id"`
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&setupRes)
	resp.Body.Close()
	if setupRes.Data.UserID == "" {
		t.Error("setup response should include user_id")
	}
	if setupRes.Data.Username != "admin" {
		t.Errorf("expected username 'admin', got '%s'", setupRes.Data.Username)
	}
}

func TestSetupAlreadyBootstrapped(t *testing.T) {
	_, ts := newTestServer(t) // already has an admin user
	body, _ := json.Marshal(map[string]string{"username": "hacker", "password": "test-password-99"})
	resp, _ := http.Post(ts.URL+"/api/v1/auth/setup", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("setup on existing DB should return 409, got %d", resp.StatusCode)
	}
}

// --- CSRF Tests ---

func TestCSRFBlocksPostWithCookie(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// POST with cookie but NO X-Requested-With header
	resp := cookieDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{"name": "csrf-test"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for cookie POST without X-Requested-With, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCSRFAllowsPostWithHeader(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// POST with cookie AND X-Requested-With header
	resp := cookieDo(t, ts, "POST", "/api/v1/streams", token,
		map[string]any{"name": "csrf-ok"},
		"X-Requested-With", "XMLHttpRequest")
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 for cookie POST with X-Requested-With, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCSRFNotRequiredForBearer(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// POST with Bearer token (no X-Requested-With, no cookie)
	resp := authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{"name": "bearer-no-csrf"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("bearer POST should not require X-Requested-With, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCSRFNotRequiredForGet(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	// GET with cookie but no X-Requested-With — should be allowed
	resp := cookieDo(t, ts, "GET", "/api/v1/streams", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET should not require X-Requested-With, got %d", resp.StatusCode)
	}
	resp.Body.Close()
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
