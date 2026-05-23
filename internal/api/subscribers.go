package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/yavik-kapadia/bastion/internal/relay"
)

// streamSubscribers GET /api/v1/streams/{name}/subscribers
//
// Returns per-subscriber SRT health for the named stream. External
// subscribers only — internal workers (thumbnail capture, ffprobe) are
// excluded. Useful for spotting chatty viewers (high send_loss_rate_pct,
// high pkt_retrans, send_mbps significantly above useful_mbps) before
// they degrade other viewers via shared bandwidth.
func (s *Server) streamSubscribers(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, ok := s.relay.StreamStats(name); !ok {
		respondError(w, http.StatusNotFound, "stream not found")
		return
	}
	subs, _ := s.relay.StreamSubscribers(name)
	if subs == nil {
		// Render an empty array rather than `null` so the frontend can
		// always iterate.
		subs = []relay.SubscriberStats{}
	}
	respond(w, http.StatusOK, subs)
}
