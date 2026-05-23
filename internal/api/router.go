// Package api provides the HTTP REST API, WebSocket endpoint, and static dashboard.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yavik-kapadia/bastion/internal/crypto"
	"github.com/yavik-kapadia/bastion/internal/db"
	"github.com/yavik-kapadia/bastion/internal/metrics"
	"github.com/yavik-kapadia/bastion/internal/relay"
	"github.com/yavik-kapadia/bastion/internal/ws"
)

// RelayReader is the relay.Relay subset needed by the API.
type RelayReader interface {
	ActiveStreams() map[string]relay.StreamStats
	StreamStats(name string) (relay.StreamStats, bool)
}

// Options bundles the runtime knobs for an api.Server. Zero values map to the
// historical hardcoded defaults so callers can pass an empty Options{}.
type Options struct {
	EncKeyHex  string
	SRTAddr    string
	PublicHost string

	// API behavior
	SessionTTL      time.Duration
	LoginRateLimit  int
	LoginRateWindow time.Duration
	ForceHTTPS      bool

	// Thumbnail pipeline
	ThumbnailEnabled     bool
	ThumbnailCacheTTL    time.Duration
	ThumbnailWidth       int
	ThumbnailJPEGQuality int
	ThumbnailTimeout     time.Duration

	// Per-publisher ffprobe
	MediaInfoEnabled bool

	// Dashboard surface
	BrandName            string
	ThumbnailRefreshRate time.Duration
	ExternalPort         int
	DefaultMaxSubscribers int
}

// Server is the HTTP API server.
type Server struct {
	db             *db.DB
	relay          RelayReader
	prom           *metrics.Prom
	hub            *ws.Hub
	frontend       http.Handler // serves the embedded SPA; nil = no frontend
	encKey         []byte       // AES-256 key for passphrase encryption; nil = disabled
	srtAddr        string       // SRT listener address, used for thumbnail frame grabs
	publicHost     string       // optional host returned to clients for SRT command generation
	thumbCache     *thumbnailCache
	mediaInfoCache *mediaInfoCache
	httpServer     *http.Server

	// Auth/session knobs
	sessionTTL   time.Duration
	loginLimiter *loginRateLimiter
	forceHTTPS   bool

	// Thumbnail knobs (used by streamThumbnail + grabFrame)
	thumbnailEnabled     bool
	thumbnailWidth       int
	thumbnailJPEGQuality int
	thumbnailTimeout     time.Duration

	// MediaInfo knobs
	mediaInfoEnabled bool

	// Dashboard surface
	externalPort          int           // host port surfaced to clients in SRT URLs (0 = use the port from srtAddr)
	defaultMaxSubscribers int           // applied to new streams that don't specify a max
	brandName             string        // dashboard brand
	thumbnailRefreshRate  time.Duration // how often the detail page re-fetches the thumbnail
}

// NewServer constructs an API Server.
// frontendFS is an optional fs.FS containing the built SvelteKit static files.
// Pass nil to disable the dashboard (API-only mode).
//
// Zero-valued fields in opts fall back to the original hardcoded defaults so
// existing tests that pass api.Options{} keep their previous behavior.
func NewServer(database *db.DB, r RelayReader, p *metrics.Prom, hub *ws.Hub, frontendFS fs.FS, opts Options) (*Server, error) {
	// Apply defaults for zero-valued fields.
	if opts.ThumbnailCacheTTL == 0 {
		opts.ThumbnailCacheTTL = 10 * time.Second
	}
	if opts.ThumbnailWidth == 0 {
		opts.ThumbnailWidth = 480
	}
	if opts.ThumbnailJPEGQuality == 0 {
		opts.ThumbnailJPEGQuality = 5
	}
	if opts.ThumbnailTimeout == 0 {
		opts.ThumbnailTimeout = 15 * time.Second
	}
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 24 * time.Hour
	}
	if opts.LoginRateLimit == 0 {
		opts.LoginRateLimit = 10
	}
	if opts.LoginRateWindow == 0 {
		opts.LoginRateWindow = 15 * time.Minute
	}
	if opts.BrandName == "" {
		opts.BrandName = "Bastion"
	}
	if opts.ThumbnailRefreshRate == 0 {
		opts.ThumbnailRefreshRate = 15 * time.Second
	}

	s := &Server{
		db:             database,
		relay:          r,
		prom:           p,
		hub:            hub,
		srtAddr:        opts.SRTAddr,
		publicHost:     opts.PublicHost,
		thumbCache:     newThumbnailCache(opts.ThumbnailCacheTTL),
		mediaInfoCache: newMediaInfoCache(),

		sessionTTL:   opts.SessionTTL,
		loginLimiter: newLoginRateLimiter(opts.LoginRateLimit, opts.LoginRateWindow),
		forceHTTPS:   opts.ForceHTTPS,

		thumbnailEnabled:     opts.ThumbnailEnabled,
		thumbnailWidth:       opts.ThumbnailWidth,
		thumbnailJPEGQuality: opts.ThumbnailJPEGQuality,
		thumbnailTimeout:     opts.ThumbnailTimeout,

		mediaInfoEnabled: opts.MediaInfoEnabled,

		externalPort:          opts.ExternalPort,
		defaultMaxSubscribers: opts.DefaultMaxSubscribers,
		brandName:             opts.BrandName,
		thumbnailRefreshRate:  opts.ThumbnailRefreshRate,
	}
	if frontendFS != nil {
		s.frontend = staticHandler(frontendFS)
	}
	if opts.EncKeyHex != "" {
		key, err := hex.DecodeString(opts.EncKeyHex)
		if err != nil || (len(key) != 16 && len(key) != 32) {
			return nil, fmt.Errorf("encryption_key must be 32 or 64 hex chars (16 or 32 bytes)")
		}
		s.encKey = key
	}
	return s, nil
}

// securityHeaders adds defensive HTTP headers to every response.
// When forceHTTPS is true on the Server, also emits Strict-Transport-Security.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:")
		if s.forceHTTPS {
			// 1 year, include subdomains. Safe to send unconditionally when
			// the operator has explicitly opted into HTTPS-only.
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// Start binds the HTTP server and serves requests until ctx is cancelled.
func (s *Server) Start(ctx context.Context, addr string, corsOrigin string) error {
	if s.mediaInfoEnabled {
		go s.runMediaInfoProbes(ctx)
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(s.securityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Requested-With"},
		AllowCredentials: true,
	}))

	// Public endpoints.
	r.Get("/health", healthHandler)
	r.Post("/api/v1/auth/login", s.loginLimiter.middleware(s.login))
	r.Post("/api/v1/auth/setup", s.setup)
	r.Get("/api/v1/auth/setup-status", s.setupStatus)
	r.Get("/metrics", promhttp.HandlerFor(s.prom.Registry, promhttp.HandlerOpts{}).ServeHTTP)

	// Viewer+ endpoints (any authenticated user).
	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		r.Use(csrfProtect)

		r.Get("/api/v1/auth/me", s.me)
		r.Post("/api/v1/auth/logout", s.logout)
		r.Post("/api/v1/auth/api-keys", s.createAPIKey)

		r.Get("/api/v1/streams", s.listStreams)
		r.Get("/api/v1/streams/{name}", s.getStream)
		r.Get("/api/v1/streams/{name}/thumbnail", s.streamThumbnail)

		r.Get("/api/v1/metrics/global", s.globalMetrics)
		r.Get("/api/v1/ws", ws.Handler(s.hub))
	})

	// Manager+ endpoints (manager or admin).
	r.Group(func(r chi.Router) {
		r.Use(s.requireManager)
		r.Use(csrfProtect)

		r.Post("/api/v1/streams", s.createStream)
		r.Put("/api/v1/streams/{name}", s.updateStream)
		r.Delete("/api/v1/streams/{name}", s.deleteStream)

		r.Get("/api/v1/streams/{name}/logs", s.streamLogs)
		r.Get("/api/v1/logs", s.globalLogs)
	})

	// Admin-only endpoints.
	r.Group(func(r chi.Router) {
		r.Use(s.requireAdmin)
		r.Use(csrfProtect)

		r.Get("/api/v1/users", s.listUsers)
		r.Post("/api/v1/users", s.createUser)
		r.Delete("/api/v1/users/{id}", s.deleteUser)
	})

	// Serve the SvelteKit SPA for all non-API routes.
	if s.frontend != nil {
		r.NotFound(s.frontend.ServeHTTP)
	}

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
