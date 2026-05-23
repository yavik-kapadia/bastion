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
// Returns a JPEG/PNG frame grabbed from the live SRT stream.
//
// Requests are dedup'd through s.thumbCache: concurrent tabs share one ffmpeg
// run, and a successful frame is cached for ~10s so polling does not open a
// fresh SRT subscriber connection on every refresh.
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

	png, err := s.thumbCache.get(r.Context(), name, func(ctx context.Context) ([]byte, error) {
		return s.grabFrame(ctx, name)
	})
	if err != nil {
		slog.Debug("thumbnail: fetch failed", "stream", name, "err", err)
		http.Error(w, "thumbnail unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png) //nolint:errcheck
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

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-loglevel", "error",
		"-i", srtURL,
		"-an",                                                   // ignore audio — saves work, avoids audio-codec edge cases
		"-map", "0:v:0",                                         // first video stream only, in case of multiple
		"-frames:v", "1",                                        // exactly one decoded frame
		"-vf", fmt.Sprintf("scale=%d:-2", s.thumbnailWidth),     // -2 keeps aspect ratio, even height
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", strconv.Itoa(s.thumbnailJPEGQuality),            // 1-31, lower = better
		"-",
	)

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
