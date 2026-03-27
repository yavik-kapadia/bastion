package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/yavik14/bastion/internal/model"
)

// TestAPIIntegration verifies the REST API works end-to-end against a live server.
func TestAPIIntegration(t *testing.T) {
	ts := newTestServer(t)

	// Login
	token := mustLogin(t, ts.apiAddr, "admin", "test-password-99")
	if token == "" {
		t.Fatal("expected non-empty token after login")
	}

	// Create a stream
	createResp := mustDo(t, "POST", ts.apiAddr+"/api/v1/streams", token, map[string]any{
		"name":        "integration-test",
		"description": "automated",
		"enabled":     true,
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create stream: got %d", createResp.StatusCode)
	}

	// List streams
	listResp := mustDo(t, "GET", ts.apiAddr+"/api/v1/streams", token, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list streams: got %d", listResp.StatusCode)
	}
	var listBody struct {
		Data []map[string]any `json:"data"`
	}
	json.NewDecoder(listResp.Body).Decode(&listBody)
	listResp.Body.Close()
	if len(listBody.Data) != 1 {
		t.Errorf("expected 1 stream, got %d", len(listBody.Data))
	}

	// Get stream
	getResp := mustDo(t, "GET", ts.apiAddr+"/api/v1/streams/integration-test", token, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get stream: got %d", getResp.StatusCode)
	}
	getResp.Body.Close()

	// Update stream
	updateResp := mustDo(t, "PUT", ts.apiAddr+"/api/v1/streams/integration-test", token, map[string]any{
		"description": "updated",
	})
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update stream: got %d", updateResp.StatusCode)
	}
	updateResp.Body.Close()

	// Delete stream
	delResp := mustDo(t, "DELETE", ts.apiAddr+"/api/v1/streams/integration-test", token, nil)
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete stream: got %d", delResp.StatusCode)
	}
	delResp.Body.Close()

	// Verify gone
	gone := mustDo(t, "GET", ts.apiAddr+"/api/v1/streams/integration-test", token, nil)
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", gone.StatusCode)
	}
	gone.Body.Close()
}

// TestEncryptedStreamConfig verifies encrypted streams are created and that
// passphrases are never returned in API responses.
func TestEncryptedStreamConfig(t *testing.T) {
	ts := newTestServer(t)
	token := mustLogin(t, ts.apiAddr, "admin", "test-password-99")

	resp := mustDo(t, "POST", ts.apiAddr+"/api/v1/streams", token, map[string]any{
		"name":       "enc-stream",
		"passphrase": "supersecretpass",
		"key_length": 32,
		"enabled":    true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create encrypted stream: %d", resp.StatusCode)
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	if pp, ok := body.Data["passphrase"]; ok && pp != "" {
		t.Error("passphrase should not be returned in create response")
	}

	// Verify stream is in DB with encryption configured.
	stream, err := ts.db.Streams.Get("enc-stream")
	if err != nil {
		t.Fatalf("get stream from db: %v", err)
	}
	if stream.KeyLength != 32 {
		t.Errorf("key_length: got %d want 32", stream.KeyLength)
	}
	if stream.Passphrase == "" {
		t.Error("passphrase should be stored (encrypted) in DB")
	}
}

// TestUserRoles verifies role-based access control.
func TestUserRoles(t *testing.T) {
	ts := newTestServer(t)
	adminToken := mustLogin(t, ts.apiAddr, "admin", "test-password-99")

	// Admin creates viewer
	resp := mustDo(t, "POST", ts.apiAddr+"/api/v1/users", adminToken, map[string]any{
		"username": "viewer1",
		"password": "viewpass12345",
		"role":     string(model.RoleViewer),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create viewer: %d", resp.StatusCode)
	}
	resp.Body.Close()

	viewerToken := mustLogin(t, ts.apiAddr, "viewer1", "viewpass12345")

	// Viewer can list streams
	r := mustDo(t, "GET", ts.apiAddr+"/api/v1/streams", viewerToken, nil)
	if r.StatusCode != http.StatusOK {
		t.Errorf("viewer should read streams, got %d", r.StatusCode)
	}
	r.Body.Close()

	// Viewer cannot create users
	r = mustDo(t, "POST", ts.apiAddr+"/api/v1/users", viewerToken, map[string]any{
		"username": "hacker", "password": "p", "role": "admin",
	})
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("viewer should not create users, got %d", r.StatusCode)
	}
	r.Body.Close()
}

// TestHealthEndpoint verifies the /health endpoint.
func TestHealthEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.apiAddr + "/health")
	if err != nil {
		t.Fatalf("get /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health: got %d", resp.StatusCode)
	}
}

// --- helpers ---

func mustLogin(t *testing.T, baseURL, user, pass string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "bastion_session" {
			return c.Value
		}
	}
	t.Fatal("login: no bastion_session cookie in response")
	return ""
}

func mustDo(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func init() {
	// Ensure unused imports are satisfied.
	_ = fmt.Sprintf
	_ = time.Second
}
