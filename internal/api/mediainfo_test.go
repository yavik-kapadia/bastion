package api

import (
	"sync/atomic"
	"testing"
)

func TestParseFFprobeOutputHappy(t *testing.T) {
	// Real ffprobe output shape for a 1920x1080@120fps H.264 SRT stream
	// (the actual production case that motivated this feature).
	jsonOut := []byte(`{
		"streams": [
			{
				"codec_name": "h264",
				"profile": "High",
				"width": 1920,
				"height": 1080,
				"avg_frame_rate": "120/1",
				"r_frame_rate": "120/1",
				"pix_fmt": "yuv420p",
				"color_space": "bt709",
				"color_range": "tv",
				"bit_rate": "10500000"
			}
		],
		"format": {
			"bit_rate": "10987654"
		}
	}`)

	mi, err := parseFFprobeOutput(jsonOut)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mi.Codec != "h264" {
		t.Errorf("Codec: got %q, want h264", mi.Codec)
	}
	if mi.Width != 1920 || mi.Height != 1080 {
		t.Errorf("dimensions: got %dx%d, want 1920x1080", mi.Width, mi.Height)
	}
	if mi.FPS != "120/1" {
		t.Errorf("FPS: got %q, want 120/1", mi.FPS)
	}
	if mi.PixFmt != "yuv420p" {
		t.Errorf("PixFmt: got %q, want yuv420p", mi.PixFmt)
	}
	if mi.ColorSpace != "bt709" {
		t.Errorf("ColorSpace: got %q, want bt709", mi.ColorSpace)
	}
	if mi.BitRateKbps != 10500 {
		t.Errorf("BitRateKbps: got %d, want 10500 (from stream-level)", mi.BitRateKbps)
	}
}

func TestParseFFprobeOutputFallsBackToRFrameRate(t *testing.T) {
	// avg_frame_rate "0/0" → fall back to r_frame_rate.
	jsonOut := []byte(`{"streams":[{"codec_name":"h264","width":1280,"height":720,"avg_frame_rate":"0/0","r_frame_rate":"30000/1001","pix_fmt":"yuv420p"}],"format":{}}`)
	mi, err := parseFFprobeOutput(jsonOut)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mi.FPS != "30000/1001" {
		t.Errorf("FPS fallback: got %q, want 30000/1001", mi.FPS)
	}
}

func TestParseFFprobeOutputFallsBackToFormatBitRate(t *testing.T) {
	// No stream-level bit_rate → use format-level.
	jsonOut := []byte(`{"streams":[{"codec_name":"h264","width":640,"height":360,"avg_frame_rate":"30/1","bit_rate":""}],"format":{"bit_rate":"5000000"}}`)
	mi, err := parseFFprobeOutput(jsonOut)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mi.BitRateKbps != 5000 {
		t.Errorf("BitRateKbps fallback: got %d, want 5000", mi.BitRateKbps)
	}
}

func TestParseFFprobeOutputNoStreams(t *testing.T) {
	if _, err := parseFFprobeOutput([]byte(`{"streams":[],"format":{}}`)); err == nil {
		t.Fatal("expected error when no video stream present")
	}
}

func TestParseFFprobeOutputInvalidJSON(t *testing.T) {
	if _, err := parseFFprobeOutput([]byte(`{not json`)); err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
}

func TestMediaInfoCacheNeedsProbe(t *testing.T) {
	c := newMediaInfoCache()

	// sessionID 0 = no publisher → never probe.
	if c.needsProbe("s1", 0) {
		t.Error("sessionID 0 should not need probe")
	}

	// Empty cache, real session → probe.
	if !c.needsProbe("s1", 1) {
		t.Error("empty cache with valid session should need probe")
	}

	// After storing, same session should not re-probe.
	c.put("s1", 1, &MediaInfo{Codec: "h264"})
	if c.needsProbe("s1", 1) {
		t.Error("cached entry for same session should not need re-probe")
	}

	// Different session ID (publisher reconnected) → probe.
	if !c.needsProbe("s1", 2) {
		t.Error("session change should trigger re-probe")
	}
}

// sanity: atomic.Uint64 wraparound is irrelevant in practice but we
// confirm that needsProbe handles a non-zero starting cache fine.
func TestMediaInfoCacheRoundTrip(t *testing.T) {
	c := newMediaInfoCache()
	var session atomic.Uint64
	session.Add(7)
	c.put("a", session.Load(), &MediaInfo{Codec: "hevc", Width: 3840, Height: 2160})
	got := c.get("a")
	if got == nil {
		t.Fatal("expected cached MediaInfo")
	}
	if got.Codec != "hevc" || got.Width != 3840 {
		t.Errorf("got %+v", got)
	}
	c.delete("a")
	if c.get("a") != nil {
		t.Error("expected nil after delete")
	}
}
