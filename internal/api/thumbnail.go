package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"time"

	"github.com/go-chi/chi/v5"
)

// streamThumbnail GET /api/v1/streams/{name}/thumbnail
// Returns a JPEG/PNG frame grabbed from the live SRT stream.
//
// Requests are dedup'd through s.thumbCache: concurrent tabs share one ffmpeg
// run, and a successful frame is cached for ~10s so polling does not open a
// fresh SRT subscriber connection on every refresh.
func (s *Server) streamThumbnail(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	stats, ok := s.relay.StreamStats(name)
	if !ok || !stats.HasPublisher {
		http.NotFound(w, r)
		return
	}

	png, err := s.thumbCache.get(r.Context(), name, func(ctx context.Context) ([]byte, error) {
		return s.grabFrame(ctx, name)
	})
	if err != nil {
		slog.Debug("thumbnail: fetch failed", "stream", name, "err", err)
		http.Error(w, "thumbnail unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png) //nolint:errcheck
}

// grabFrame runs ffmpeg against the local SRT listener to capture a single
// frame as PNG. The SRT subscription created here is short-lived (~1s) but
// does increment subscriberCount during its lifetime — the cache in front of
// this function exists to keep that lifetime rare.
func (s *Server) grabFrame(ctx context.Context, name string) ([]byte, error) {
	_, port, err := net.SplitHostPort(s.srtAddr)
	if err != nil {
		port = "9710"
	}
	srtURL := fmt.Sprintf("srt://127.0.0.1:%s?streamid=#!::m=request,r=%s",
		port, url.QueryEscape(name))

	if stream, err := s.db.Streams.Get(name); err == nil &&
		stream.KeyLength > 0 && stream.Passphrase != "" {
		if plain, err := s.decryptPassphrase(stream.Passphrase); err == nil && plain != "" {
			srtURL += "&passphrase=" + url.QueryEscape(plain)
		}
	}

	// Timeout sized for ~6s GOPs (a common upper bound from prosumer encoders)
	// plus SRT handshake.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-loglevel", "error",
		"-i", srtURL,
		"-vframes", "1",
		"-f", "image2pipe",
		"-vcodec", "png",
		"-",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w (%s)", err, stderr.String())
	}
	return stdout.Bytes(), nil
}
