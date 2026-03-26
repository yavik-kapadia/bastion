package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/yavik14/bastion/internal/model"
)

type contextKey string

const ctxUser contextKey = "user"

// userFromCtx retrieves the authenticated user from the request context.
func userFromCtx(ctx context.Context) *model.User {
	u, _ := ctx.Value(ctxUser).(*model.User)
	return u
}

// requireAuth is middleware that validates Bearer API keys.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := bearerToken(r)
		if key == "" {
			respondError(w, http.StatusUnauthorized, "missing authorization")
			return
		}
		u, err := s.db.Users.ValidateAPIKey(key)
		if err != nil {
			respondError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireManager is middleware that requires manager or admin role.
func (s *Server) requireManager(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r.Context())
		if u == nil || (u.Role != model.RoleManager && u.Role != model.RoleAdmin) {
			respondError(w, http.StatusForbidden, "manager role required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// requireAdmin is middleware that requires admin role on top of requireAuth.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r.Context())
		if u == nil || u.Role != model.RoleAdmin {
			respondError(w, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
