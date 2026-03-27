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
// Grabs a single video frame from the live SRT stream via ffmpeg and returns it as PNG.
// Requires an active publisher; returns 404 if none, 503 if ffmpeg is unavailable.
func (s *Server) streamThumbnail(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Only attempt when a publisher is active.
	stats, ok := s.relay.StreamStats(name)
	if !ok || !stats.HasPublisher {
		http.NotFound(w, r)
		return
	}

	// Build the SRT URL pointing at our own listener.
	_, port, err := net.SplitHostPort(s.srtAddr)
	if err != nil {
		port = "9710"
	}
	srtURL := fmt.Sprintf("srt://127.0.0.1:%s?streamid=#!::m=request,r=%s",
		port, url.QueryEscape(name))

	// Attach passphrase if the stream is encrypted.
	if stream, err := s.db.Streams.Get(name); err == nil &&
		stream.KeyLength > 0 && stream.Passphrase != "" {
		if plain, err := s.decryptPassphrase(stream.Passphrase); err == nil && plain != "" {
			srtURL += "&passphrase=" + url.QueryEscape(plain)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
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
		slog.Debug("thumbnail: ffmpeg failed", "stream", name, "err", err, "stderr", stderr.String())
		http.Error(w, "thumbnail unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(stdout.Bytes()) //nolint:errcheck
}
