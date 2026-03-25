// Package api provides the HTTP REST API, WebSocket endpoint, and static dashboard.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yavik14/bastion/internal/crypto"
	"github.com/yavik14/bastion/internal/db"
	"github.com/yavik14/bastion/internal/metrics"
	"github.com/yavik14/bastion/internal/relay"
)

// RelayReader is the relay.Relay subset needed by the API.
type RelayReader interface {
	ActiveStreams() map[string]relay.StreamStats
	StreamStats(name string) (relay.StreamStats, bool)
}

// Server is the HTTP API server.
type Server struct {
	db         *db.DB
	relay      RelayReader
	prom       *metrics.Prom
	encKey     []byte // AES-256 key for passphrase encryption; nil = disabled
	httpServer *http.Server
}

// NewServer constructs an API Server.
func NewServer(database *db.DB, r RelayReader, p *metrics.Prom, encKeyHex string) (*Server, error) {
	s := &Server{db: database, relay: r, prom: p}
	if encKeyHex != "" {
		key, err := hex.DecodeString(encKeyHex)
		if err != nil || (len(key) != 16 && len(key) != 32) {
			return nil, fmt.Errorf("encryption_key must be 32 or 64 hex chars (16 or 32 bytes)")
		}
		s.encKey = key
	}
	return s, nil
}

// Start binds the HTTP server and serves requests until ctx is cancelled.
func (s *Server) Start(ctx context.Context, addr string, corsOrigin string) error {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	// Public endpoints.
	r.Post("/api/v1/auth/login", s.login)
	r.Get("/metrics", promhttp.HandlerFor(s.prom.Registry, promhttp.HandlerOpts{}).ServeHTTP)

	// Authenticated endpoints.
	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)

		r.Post("/api/v1/auth/api-keys", s.createAPIKey)

		r.Get("/api/v1/streams", s.listStreams)
		r.Post("/api/v1/streams", s.createStream)
		r.Get("/api/v1/streams/{name}", s.getStream)
		r.Put("/api/v1/streams/{name}", s.updateStream)
		r.Delete("/api/v1/streams/{name}", s.deleteStream)

		r.Get("/api/v1/metrics/global", s.globalMetrics)
	})

	// Admin-only endpoints.
	r.Group(func(r chi.Router) {
		r.Use(s.requireAdmin)

		r.Get("/api/v1/users", s.listUsers)
		r.Post("/api/v1/users", s.createUser)
		r.Delete("/api/v1/users/{id}", s.deleteUser)
	})

	s.httpServer = &http.Server{Addr: addr, Handler: r}

	go func() {
		<-ctx.Done()
		s.httpServer.Shutdown(context.Background()) //nolint:errcheck
	}()

	slog.Info("api: listening", "addr", addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("api server: %w", err)
	}
	return nil
}

// globalMetrics GET /api/v1/metrics/global
func (s *Server) globalMetrics(w http.ResponseWriter, r *http.Request) {
	active := s.relay.ActiveStreams()
	totalSubs := 0
	totalPubs := 0
	for _, st := range active {
		if st.HasPublisher {
			totalPubs++
		}
		totalSubs += st.SubscriberCount
	}
	respond(w, http.StatusOK, map[string]any{
		"active_streams":     len(active),
		"active_publishers":  totalPubs,
		"active_subscribers": totalSubs,
	})
}

// encryptPassphrase encrypts a passphrase for storage, if a key is configured.
func (s *Server) encryptPassphrase(plain string) (string, error) {
	return crypto.Encrypt(s.encKey, plain)
}

// decryptPassphrase decrypts a stored passphrase.
func (s *Server) decryptPassphrase(enc string) (string, error) {
	return crypto.Decrypt(s.encKey, enc)
}

// newID generates a random 16-byte hex string for use as an opaque ID.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// healthHandler is a simple liveness probe.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}
