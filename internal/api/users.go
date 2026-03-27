package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yavik-kapadia/bastion/internal/model"
)

// loginRequest is the body for POST /api/v1/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setup POST /api/v1/auth/setup — creates the first admin user.
// Returns 409 if any users already exist so it cannot be used after initial setup.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Users.Count()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	if n > 0 {
		respondError(w, http.StatusConflict, "already set up — use /api/v1/auth/login")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if len(req.Password) < 8 {
		respondError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	id := newID()
	if err := s.db.Users.Create(id, req.Username, req.Password, "admin"); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create admin")
		return
	}
	rawKey, err := s.db.Users.CreateAPIKey(newID(), id, "setup-session")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create session key")
		return
	}
	setSessionCookie(w, r, rawKey)
	respond(w, http.StatusCreated, map[string]any{
		"user_id":  id,
		"username": req.Username,
		"role":     "admin",
	})
}

// login POST /api/v1/auth/login — returns a newly created API key as a bearer token.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	u, err := s.db.Users.Authenticate(req.Username, req.Password)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	// Issue a new API key scoped to this login session.
	rawKey, err := s.db.Users.CreateAPIKey(newID(), u.ID, "session-"+time.Now().Format("20060102150405"))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "could not create session key")
		return
	}
	setSessionCookie(w, r, rawKey)
	respond(w, http.StatusOK, map[string]any{
		"user_id":  u.ID,
		"username": u.Username,
		"role":     u.Role,
	})
}

// me GET /api/v1/auth/me — returns the current user's info from their session cookie or token.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	respond(w, http.StatusOK, map[string]any{
		"user_id":     u.ID,
		"username":    u.Username,
		"role":        u.Role,
		"public_host": s.publicHost,
	})
}

// logout POST /api/v1/auth/logout — clears the session cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, r)
	respond(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// createAPIKey POST /api/v1/auth/api-keys
func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rawKey, err := s.db.Users.CreateAPIKey(newID(), u.ID, body.Name)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create key")
		return
	}
	respond(w, http.StatusCreated, map[string]string{
		"key": rawKey,
		"note": "Store this key securely — it will not be shown again.",
	})
}

// listUsers GET /api/v1/users (admin only)
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.Users.List()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	for _, u := range users {
		u.PasswordHash = "" // scrub hash
	}
	respond(w, http.StatusOK, users)
}

// setupStatus GET /api/v1/auth/setup-status — tells the frontend whether first-run setup is needed.
func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Users.Count()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	respond(w, http.StatusOK, map[string]bool{"needs_setup": n == 0})
}

// createUser POST /api/v1/users (admin only)
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string     `json:"username"`
		Password string     `json:"password"`
		Role     model.Role `json:"role"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "username and password required")
		return
	}
	if len(req.Password) < 8 {
		respondError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	switch req.Role {
	case model.RoleAdmin, model.RoleManager, model.RoleViewer:
	default:
		req.Role = model.RoleViewer
	}
	id := newID()
	if err := s.db.Users.Create(id, req.Username, req.Password, req.Role); err != nil {
		respondError(w, http.StatusConflict, "username already exists")
		return
	}
	respond(w, http.StatusCreated, map[string]string{"id": id, "username": req.Username, "role": string(req.Role)})
}

// deleteUser DELETE /api/v1/users/{id} (admin only)
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.db.Users.Delete(id); err != nil {
		respondError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	respond(w, http.StatusOK, map[string]string{"deleted": id})
}
