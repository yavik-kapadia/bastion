package api

import (
	"database/sql"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yavik-kapadia/bastion/internal/model"
)

// validStreamName restricts stream names to safe characters only.
var validStreamName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,63}$`)

// streamRequest is the JSON body for create/update.
type streamRequest struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Passphrase        string   `json:"passphrase"`
	KeyLength         int      `json:"key_length"`
	MaxSubscribers    int      `json:"max_subscribers"`
	AllowedPublishers []string `json:"allowed_publishers"`
	Enabled           *bool    `json:"enabled"`
}

// listStreams GET /api/v1/streams
func (s *Server) listStreams(w http.ResponseWriter, r *http.Request) {
	streams, err := s.db.Streams.List()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list streams")
		return
	}
	// Merge live stats from relay.
	type streamView struct {
		*model.Stream
		HasPublisher    bool `json:"has_publisher"`
		SubscriberCount int  `json:"subscriber_count"`
	}
	views := make([]streamView, 0, len(streams))
	for _, st := range streams {
		v := streamView{Stream: st}
		if stats, ok := s.relay.StreamStats(st.Name); ok {
			v.HasPublisher = stats.HasPublisher
			v.SubscriberCount = stats.SubscriberCount
		}
		// Never expose passphrases over the API.
		v.Passphrase = ""
		views = append(views, v)
	}
	respond(w, http.StatusOK, views)
}

// createStream POST /api/v1/streams
func (s *Server) createStream(w http.ResponseWriter, r *http.Request) {
	var req streamRequest
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validStreamName.MatchString(req.Name) {
		respondError(w, http.StatusBadRequest, "name must be 1–63 characters: letters, digits, hyphens, underscores only")
		return
	}
	if req.KeyLength != 0 && req.KeyLength != 16 && req.KeyLength != 24 && req.KeyLength != 32 {
		respondError(w, http.StatusBadRequest, "key_length must be 0, 16, 24, or 32")
		return
	}
	if req.KeyLength > 0 && len(req.Passphrase) < 10 {
		respondError(w, http.StatusBadRequest, "passphrase must be at least 10 characters when encryption is enabled")
		return
	}

	encPass, err := s.encryptPassphrase(req.Passphrase)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "encryption error")
		return
	}

	now := time.Now().UTC()
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	stream := &model.Stream{
		ID:                newID(),
		Name:              req.Name,
		Description:       req.Description,
		Passphrase:        encPass,
		KeyLength:         req.KeyLength,
		MaxSubscribers:    req.MaxSubscribers,
		AllowedPublishers: req.AllowedPublishers,
		Enabled:           enabled,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.db.Streams.Create(stream); err != nil {
		respondError(w, http.StatusConflict, "stream name already exists or DB error")
		return
	}
	stream.Passphrase = "" // never return passphrase
	respond(w, http.StatusCreated, stream)
}

// getStream GET /api/v1/streams/{name}
func (s *Server) getStream(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	stream, err := s.db.Streams.Get(name)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "stream not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	stream.Passphrase = ""
	// Merge live relay stats so the detail page has the same has_publisher /
	// subscriber_count fields as the list endpoint.
	type streamView struct {
		*model.Stream
		HasPublisher    bool `json:"has_publisher"`
		SubscriberCount int  `json:"subscriber_count"`
	}
	v := streamView{Stream: stream}
	if stats, ok := s.relay.StreamStats(name); ok {
		v.HasPublisher = stats.HasPublisher
		v.SubscriberCount = stats.SubscriberCount
	}
	respond(w, http.StatusOK, v)
}

// updateStream PUT /api/v1/streams/{name}
func (s *Server) updateStream(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	existing, err := s.db.Streams.Get(name)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "stream not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}

	var req streamRequest
	if err := decodeJSON(w, r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.KeyLength != 0 {
		if req.KeyLength != 16 && req.KeyLength != 24 && req.KeyLength != 32 {
			respondError(w, http.StatusBadRequest, "key_length must be 16, 24, or 32")
			return
		}
		existing.KeyLength = req.KeyLength
	}
	if req.Passphrase != "" {
		if len(req.Passphrase) < 10 {
			respondError(w, http.StatusBadRequest, "passphrase must be at least 10 characters")
			return
		}
		enc, err := s.encryptPassphrase(req.Passphrase)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "encryption error")
			return
		}
		existing.Passphrase = enc
	}
	if req.MaxSubscribers >= 0 && req.MaxSubscribers != existing.MaxSubscribers {
		existing.MaxSubscribers = req.MaxSubscribers
	}
	if req.AllowedPublishers != nil {
		existing.AllowedPublishers = req.AllowedPublishers
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := s.db.Streams.Update(existing); err != nil {
		respondError(w, http.StatusInternalServerError, "update failed")
		return
	}
	existing.Passphrase = ""
	respond(w, http.StatusOK, existing)
}

// deleteStream DELETE /api/v1/streams/{name}
func (s *Server) deleteStream(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	stream, err := s.db.Streams.Get(name)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "stream not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := s.db.Streams.Delete(stream.ID); err != nil {
		respondError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	respond(w, http.StatusOK, map[string]string{"deleted": name})
}
