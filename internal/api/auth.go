package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/yavik-kapadia/bastion/internal/model"
)

type contextKey string

const (
	ctxUser    contextKey = "user"
	ctxViaCookie contextKey = "via_cookie"
	cookieName             = "bastion_session"
)

// userFromCtx retrieves the authenticated user from the request context.
func userFromCtx(ctx context.Context) *model.User {
	u, _ := ctx.Value(ctxUser).(*model.User)
	return u
}

// authViaCookie returns true if the request was authenticated via cookie.
func authViaCookie(ctx context.Context) bool {
	v, _ := ctx.Value(ctxViaCookie).(bool)
	return v
}

// requireAuth is middleware that validates auth via cookie, Bearer header, or query param.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, viaCookie := s.extractToken(r)
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
		ctx = context.WithValue(ctx, ctxViaCookie, viaCookie)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// csrfProtect rejects state-changing requests authenticated via cookie that lack
// the X-Requested-With header. Bearer-token requests are exempt (external API consumers).
func csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if authViaCookie(r.Context()) && r.Header.Get("X-Requested-With") == "" {
			respondError(w, http.StatusForbidden, "CSRF validation failed")
			return
		}
		next.ServeHTTP(w, r)
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

// extractToken returns the API key and whether it came from a cookie.
// Priority: cookie > Authorization header > query param.
func (s *Server) extractToken(r *http.Request) (string, bool) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer "), false
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q, false
	}
	return "", false
}

// setSessionCookie writes an HttpOnly session cookie with the API key.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
}
