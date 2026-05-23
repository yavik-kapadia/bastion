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
	"strconv"

	"github.com/go-chi/chi/v5"
)

// streamThumbnail GET /api/v1/streams/{name}/thumbnail
// Returns a JPEG or WebP frame grabbed from the live SRT stream.
// Format is controlled by [thumbnail].format in bastion.toml.
//
// Requests are dedup'd through s.thumbCache: concurrent tabs share one ffmpeg
// run, and a successful frame is cached for the configured TTL so polling
// does not open a fresh SRT subscriber connection on every refresh.
func (s *Server) streamThumbnail(w http.ResponseWriter, r *http.Request) {
	if !s.thumbnailEnabled {
		// Operator has explicitly disabled thumbnails. Skip the cache lookup
		// entirely so we never spin up ffmpeg / take a subscriber slot.
		http.NotFound(w, r)
		return
	}

	name := chi.URLParam(r, "name")

	stats, ok := s.relay.StreamStats(name)
	if !ok || !stats.HasPublisher {
		http.NotFound(w, r)
		return
	}

	img, err := s.thumbCache.get(r.Context(), name, func(ctx context.Context) ([]byte, error) {
		return s.grabFrame(ctx, name)
	})
	if err != nil {
		slog.Debug("thumbnail: fetch failed", "stream", name, "err", err)
		http.Error(w, "thumbnail unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", s.thumbnailContentType())
	w.Header().Set("Cache-Control", "no-store")
	w.Write(img) //nolint:errcheck
}

func (s *Server) thumbnailContentType() string {
	if s.thumbnailFormat == "webp" {
		return "image/webp"
	}
	return "image/jpeg"
}

// grabFrame runs ffmpeg against the local SRT listener to capture a single
// frame as JPEG. The SRT subscription is short-lived (~1s) but does increment
// subscriberCount during its lifetime — the cache in front of this function
// keeps that lifetime rare.
//
// We switched to JPEG + downscale from full-res PNG after ffmpeg 6.1.1 in
// alpine 3.20 segfaulted encoding a 1920x1080@120fps stream to PNG. The
// downscale + MJPEG path is more memory-friendly, faster, and avoids the
// crashing code path; thumbnails don't need full resolution anyway.
func (s *Server) grabFrame(ctx context.Context, name string) ([]byte, error) {
	_, port, err := net.SplitHostPort(s.srtAddr)
	if err != nil {
		port = "9710"
	}
	// w=internal tells the relay this connection is one of our own workers,
	// so it's excluded from the public subscriberCount and logged at debug.
	srtURL := fmt.Sprintf("srt://127.0.0.1:%s?streamid=#!::m=request,r=%s,w=internal",
		port, url.QueryEscape(name))

	if stream, err := s.db.Streams.Get(name); err == nil &&
		stream.KeyLength > 0 && stream.Passphrase != "" {
		if plain, err := s.decryptPassphrase(stream.Passphrase); err == nil && plain != "" {
			srtURL += "&passphrase=" + url.QueryEscape(plain)
		}
	}

	// Timeout budget covers SRT handshake + the longest realistic GOP (4s at
	// 30fps / 120-frame keyint, 2s at 60fps / 120-frame keyint). Operators
	// can extend this for high-keyint streams via thumbnail.timeout.
	ctx, cancel := context.WithTimeout(ctx, s.thumbnailTimeout)
	defer cancel()

	args := []string{
		"-loglevel", "error",
		"-i", srtURL,
		"-an",                                               // ignore audio — saves work, avoids audio-codec edge cases
		"-map", "0:v:0",                                     // first video stream only
		"-frames:v", "1",                                    // exactly one decoded frame
		"-vf", fmt.Sprintf("scale=%d:-2", s.thumbnailWidth), // -2 keeps aspect ratio, even height
		"-f", "image2pipe",
	}
	switch s.thumbnailFormat {
	case "webp":
		// -method 0 is libwebp's fast lossy preset: roughly 3–5x faster
		// encode vs the default (method 4) with ~5–10% larger file at
		// the same -quality. For frequently-refreshing thumbnails the
		// latency win is worth the bytes.
		args = append(args,
			"-vcodec", "libwebp",
			"-quality", strconv.Itoa(s.thumbnailWebPQuality), // 0-100, higher = better
			"-method", "0",
			"-",
		)
	default: // jpeg
		args = append(args,
			"-vcodec", "mjpeg",
			"-q:v", strconv.Itoa(s.thumbnailJPEGQuality), // 1-31, lower = better
			"-",
		)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w (%s)", err, stderr.String())
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced no output (%s)", stderr.String())
	}
	return stdout.Bytes(), nil
}
