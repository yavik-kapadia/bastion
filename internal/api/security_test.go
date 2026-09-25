package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/yavik-kapadia/bastion/internal/model"
)

func decodeData(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

func TestLogoutRevokesSessionKey(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	resp := cookieDo(t, ts, "POST", "/api/v1/auth/logout", token, nil, "X-Requested-With", "XMLHttpRequest")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}

	// The old token must no longer authenticate, via cookie or bearer.
	resp = cookieDo(t, ts, "GET", "/api/v1/auth/me", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("cookie after logout: want 401, got %d", resp.StatusCode)
	}
	resp = authDo(t, ts, "GET", "/api/v1/auth/me", token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bearer after logout: want 401, got %d", resp.StatusCode)
	}
}

func TestLogoutWithBearerKeepsAPIKey(t *testing.T) {
	_, ts := newTestServer(t)
	session := loginAs(t, ts, "admin", "admin-password")

	resp := authDo(t, ts, "POST", "/api/v1/auth/api-keys", session, map[string]string{"name": "ci"})
	var created map[string]string
	decodeData(t, resp, &created)
	key := created["key"]

	resp = authDo(t, ts, "POST", "/api/v1/auth/logout", key, nil)
	resp.Body.Close()

	resp = authDo(t, ts, "GET", "/api/v1/auth/me", key, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("long-lived key should survive bearer logout, got %d", resp.StatusCode)
	}
}

func TestExpiredSessionKeyRejected(t *testing.T) {
	srv, ts := newTestServer(t)
	raw, err := srv.db.Users.CreateSessionKey("sess-old", "admin-id", "session-old", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("CreateSessionKey: %v", err)
	}
	resp := cookieDo(t, ts, "GET", "/api/v1/auth/me", raw, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expired session: want 401, got %d", resp.StatusCode)
	}

	n, err := srv.db.Users.PurgeExpiredAPIKeys()
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("purge removed %d keys, want 1", n)
	}
}

func TestLoginSessionKeyHasExpiry(t *testing.T) {
	srv, ts := newTestServer(t)
	loginAs(t, ts, "admin", "admin-password")
	keys, err := srv.db.Users.ListAPIKeys("admin-id")
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].ExpiresAt == nil {
		t.Fatalf("login should create one expiring key, got %+v", keys)
	}
	if d := time.Until(*keys[0].ExpiresAt); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("session expiry %v not ~ default 24h TTL", d)
	}
}

func TestListAndDeleteOwnAPIKeys(t *testing.T) {
	srv, ts := newTestServer(t)
	if err := srv.db.Users.Create("viewer-id", "viewer", "viewer-password", model.RoleViewer); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, ts, "admin", "admin-password")
	viewer := loginAs(t, ts, "viewer", "viewer-password")

	var viewerKeys []model.APIKey
	decodeData(t, authDo(t, ts, "GET", "/api/v1/auth/api-keys", viewer, nil), &viewerKeys)
	if len(viewerKeys) != 1 {
		t.Fatalf("viewer should see exactly its own session key, got %d", len(viewerKeys))
	}

	// Another user's key can't be revoked through this endpoint.
	resp := authDo(t, ts, "DELETE", "/api/v1/auth/api-keys/"+viewerKeys[0].ID, admin, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleting another user's key: want 404, got %d", resp.StatusCode)
	}

	// Revoking your own key works and the token stops validating.
	resp = authDo(t, ts, "DELETE", "/api/v1/auth/api-keys/"+viewerKeys[0].ID, viewer, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete own key: %d", resp.StatusCode)
	}
	resp = authDo(t, ts, "GET", "/api/v1/auth/me", viewer, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked key: want 401, got %d", resp.StatusCode)
	}
}

func TestUpdateStreamPartialKeepsCapAndLatency(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	resp := authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{
		"name": "capped", "max_subscribers": 5, "latency_ms": 500,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	resp = authDo(t, ts, "PUT", "/api/v1/streams/capped", token, map[string]any{"enabled": false})
	var updated model.Stream
	decodeData(t, resp, &updated)
	if updated.MaxSubscribers != 5 {
		t.Errorf("max_subscribers = %d after partial update, want 5", updated.MaxSubscribers)
	}
	if updated.LatencyMS != 500 {
		t.Errorf("latency_ms = %d after partial update, want 500", updated.LatencyMS)
	}
	if updated.Enabled {
		t.Error("enabled should be false")
	}

	// Explicit 0 still clears the cap.
	resp = authDo(t, ts, "PUT", "/api/v1/streams/capped", token, map[string]any{"max_subscribers": 0})
	decodeData(t, resp, &updated)
	if updated.MaxSubscribers != 0 {
		t.Errorf("explicit max_subscribers=0 should clear cap, got %d", updated.MaxSubscribers)
	}
}

func TestStreamRejectsInvalidAllowedPublishers(t *testing.T) {
	_, ts := newTestServer(t)
	token := loginAs(t, ts, "admin", "admin-password")

	resp := authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{
		"name": "bad-acl", "allowed_publishers": []string{"10.0.0.0/33"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("create with invalid CIDR: want 400, got %d", resp.StatusCode)
	}

	resp = authDo(t, ts, "POST", "/api/v1/streams", token, map[string]any{
		"name": "good-acl", "allowed_publishers": []string{"10.0.0.0/8", "203.0.113.5"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create with valid list: %d", resp.StatusCode)
	}
	resp = authDo(t, ts, "PUT", "/api/v1/streams/good-acl", token, map[string]any{
		"allowed_publishers": []string{"example.com"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("update with hostname: want 400, got %d", resp.StatusCode)
	}
}
