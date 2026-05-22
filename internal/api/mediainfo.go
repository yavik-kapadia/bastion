package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MediaInfo describes what the publisher's encoder is actually sending:
// codec, resolution, frame rate, pixel format, color space, bit rate.
// Populated by a one-shot ffprobe against the local SRT relay after a
// publisher attaches. Re-probed when the publisher reconnects (detected
// via Stream.PublisherSessionID changing).
type MediaInfo struct {
	Codec       string `json:"codec"`
	Profile     string `json:"profile,omitempty"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         string `json:"fps"`              // "120/1", "30000/1001" etc. — kept raw so the UI can render both forms
	PixFmt      string `json:"pix_fmt,omitempty"`
	ColorSpace  string `json:"color_space,omitempty"`
	ColorRange  string `json:"color_range,omitempty"`
	BitRateKbps int    `json:"bit_rate_kbps,omitempty"` // 0 = unknown
}

// mediaInfoCache holds the most recent successful probe per stream, keyed
// by stream name. Each entry remembers the publisher session ID it was
// probed under so we can detect publisher reconnects and re-probe.
//
// In-memory only — re-populated on next publisher attach after restart.
type mediaInfoCache struct {
	mu      sync.RWMutex
	entries map[string]mediaInfoEntry
}

type mediaInfoEntry struct {
	sessionID uint64
	info      *MediaInfo
}

func newMediaInfoCache() *mediaInfoCache {
	return &mediaInfoCache{entries: make(map[string]mediaInfoEntry)}
}

func (c *mediaInfoCache) get(name string) *MediaInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[name]; ok {
		return e.info
	}
	return nil
}

// needsProbe reports whether we should run ffprobe for this stream:
//   - sessionID > 0 (publisher actually attached)
//   - and we either have no cached info OR the cached info is from a
//     prior publisher session.
func (c *mediaInfoCache) needsProbe(name string, sessionID uint64) bool {
	if sessionID == 0 {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[name]
	return !ok || e.sessionID != sessionID
}

func (c *mediaInfoCache) put(name string, sessionID uint64, info *MediaInfo) {
	c.mu.Lock()
	c.entries[name] = mediaInfoEntry{sessionID: sessionID, info: info}
	c.mu.Unlock()
}

func (c *mediaInfoCache) delete(name string) {
	c.mu.Lock()
	delete(c.entries, name)
	c.mu.Unlock()
}

// runMediaInfoProbes is a background loop, started from Server.Start, that
// polls the relay every 5s for active publishers and runs ffprobe against
// any stream whose current publisher session we haven't probed yet. Also
// evicts cache entries for streams whose publisher has gone away.
func (s *Server) runMediaInfoProbes(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanForProbeWork(ctx)
		}
	}
}

func (s *Server) scanForProbeWork(ctx context.Context) {
	active := s.relay.ActiveStreams()
	// Evict cache entries for streams that no longer have a publisher.
	s.mediaInfoCache.mu.Lock()
	for name := range s.mediaInfoCache.entries {
		if stats, ok := active[name]; !ok || !stats.HasPublisher {
			delete(s.mediaInfoCache.entries, name)
		}
	}
	s.mediaInfoCache.mu.Unlock()

	for name, stats := range active {
		if !stats.HasPublisher {
			continue
		}
		if !s.mediaInfoCache.needsProbe(name, stats.PublisherSessionID) {
			continue
		}
		go s.probeOne(ctx, name, stats.PublisherSessionID)
	}
}

// probeOne runs ffprobe against the local SRT relay for one stream, parses
// the result, and caches it under the given session ID. Skips silently if
// the session ID has changed by the time we finish.
func (s *Server) probeOne(ctx context.Context, name string, sessionID uint64) {
	info, err := s.probeMediaInfo(ctx, name)
	if err != nil {
		slog.Debug("mediainfo: probe failed", "stream", name, "err", err)
		return
	}
	// Verify the publisher hasn't reconnected to a different session while we
	// were probing — if it has, drop our stale result on the floor.
	if stats, ok := s.relay.StreamStats(name); ok && stats.PublisherSessionID != sessionID {
		return
	}
	s.mediaInfoCache.put(name, sessionID, info)
}

// probeMediaInfo invokes ffprobe with JSON output against the local SRT
// listener and parses the first video stream's descriptors.
func (s *Server) probeMediaInfo(ctx context.Context, name string) (*MediaInfo, error) {
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

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-loglevel", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-select_streams", "v:0", // first video stream
		srtURL,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseFFprobeOutput(out)
}

// ffprobeStream and ffprobeFormat are the subset of ffprobe's JSON we care
// about. Everything else is ignored.
type ffprobeOutput struct {
	Streams []struct {
		CodecName  string `json:"codec_name"`
		Profile    string `json:"profile"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		AvgFrame   string `json:"avg_frame_rate"`   // "120/1"
		RFrame     string `json:"r_frame_rate"`     // base frame rate
		PixFmt     string `json:"pix_fmt"`
		ColorSpace string `json:"color_space"`
		ColorRange string `json:"color_range"`
		BitRate    string `json:"bit_rate"` // bits/sec as string, may be empty
	} `json:"streams"`
	Format struct {
		BitRate string `json:"bit_rate"` // overall container bit_rate; live SRT often empty
	} `json:"format"`
}

func parseFFprobeOutput(jsonBytes []byte) (*MediaInfo, error) {
	var raw ffprobeOutput
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}
	if len(raw.Streams) == 0 {
		return nil, fmt.Errorf("ffprobe found no video stream")
	}
	v := raw.Streams[0]

	fps := v.AvgFrame
	if fps == "" || fps == "0/0" {
		fps = v.RFrame
	}

	mi := &MediaInfo{
		Codec:      v.CodecName,
		Profile:    v.Profile,
		Width:      v.Width,
		Height:     v.Height,
		FPS:        fps,
		PixFmt:     v.PixFmt,
		ColorSpace: v.ColorSpace,
		ColorRange: v.ColorRange,
	}

	// Bit rate: prefer stream-level, fall back to format-level. Convert to kbps.
	for _, b := range []string{v.BitRate, raw.Format.BitRate} {
		if b == "" {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
		if err == nil && n > 0 {
			mi.BitRateKbps = int(n / 1000)
			break
		}
	}

	return mi, nil
}
