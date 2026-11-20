package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tibi/internal/id"
	"tibi/internal/render"
	"tibi/internal/store"
	"tibi/internal/units"
)

// handleList serves GET /api/v1/items?limit&cursor&kind&source.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			apiError(w, http.StatusBadRequest, "bad_request", "limit must be 1–100")
			return
		}
		limit = n
	}
	items, next, err := s.store.ListItems(r.Context(), store.ListFilter{
		Kind: q.Get("kind"), Source: q.Get("source"), Collection: q.Get("collection"), Limit: limit, Cursor: q.Get("cursor"),
	})
	if errors.Is(err, store.ErrBadCursor) {
		apiError(w, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	if err != nil {
		s.internal(w, "list", err)
		return
	}
	out := make([]ItemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, s.itemJSON(it))
	}
	var nc *string
	if next != "" {
		nc = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": nc})
}

// apiItem loads the live item named by the {id} path value (any visibility).
func (s *Server) apiItem(w http.ResponseWriter, r *http.Request) (*store.Item, bool) {
	itemID := r.PathValue("id")
	if !id.Valid(itemID) {
		apiError(w, http.StatusNotFound, "not_found", "no such item")
		return nil, false
	}
	it, err := s.store.GetItem(r.Context(), itemID)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found", "no such item")
		return nil, false
	}
	if err != nil {
		s.internal(w, "get item", err)
		return nil, false
	}
	return it, true
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	if it, ok := s.apiItem(w, r); ok {
		writeJSON(w, http.StatusOK, s.itemJSON(it))
	}
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")
	err := s.store.DeleteItem(r.Context(), itemID)
	if errors.Is(err, store.ErrNotFound) || !id.Valid(itemID) {
		apiError(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	if err != nil {
		s.internal(w, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// patchableFields may appear in a PATCH body; content never can.
var patchableFields = map[string]bool{"title": true, "lang": true, "visibility": true, "expires": true, "slug": true}

func (s *Server) handlePatchItem(w http.ResponseWriter, r *http.Request) {
	it, ok := s.apiItem(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request", "body must be a JSON object")
		return
	}
	var p store.Patch
	for k, raw := range body {
		if !patchableFields[k] {
			apiError(w, http.StatusBadRequest, "bad_request", "field "+strconv.Quote(k)+" cannot be changed (content is immutable; fork instead)")
			return
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			apiError(w, http.StatusBadRequest, "bad_request", k+" must be a string")
			return
		}
		switch k {
		case "title":
			p.Title = &v
		case "lang":
			if it.Kind != store.KindText {
				apiError(w, http.StatusBadRequest, "bad_request", "lang applies to text items only")
				return
			}
			l, ok := render.ResolveLang(v)
			if !ok {
				apiError(w, http.StatusBadRequest, "bad_request", "unknown lang "+strconv.Quote(v))
				return
			}
			p.Lang = &l
		case "visibility":
			p.Visibility = &v
		case "expires":
			at, err := s.expiresAt(v)
			if err != nil {
				apiError(w, http.StatusBadRequest, "bad_request", err.Error())
				return
			}
			p.ExpiresAt = &at
		case "slug":
			v = strings.ToLower(v)
			p.Slug = &v
		}
	}
	updated, err := s.store.UpdateItem(r.Context(), it.ID, p)
	switch {
	case errors.Is(err, store.ErrSlugInvalid):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", "no such item")
	case err != nil && strings.HasPrefix(err.Error(), "visibility"):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		s.internal(w, "patch", err)
	default:
		writeJSON(w, http.StatusOK, s.itemJSON(updated))
	}
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err)
	apiError(w, http.StatusInternalServerError, "internal", "internal error")
}

// SearchHitJSON is an item plus the matching line of its text.
type SearchHitJSON struct {
	ItemJSON
	Snippet string `json:"snippet,omitempty"`
}

// handleSearch serves GET /api/v1/search?q&kind&limit&cursor.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			apiError(w, http.StatusBadRequest, "bad_request", "limit must be 1–100")
			return
		}
		limit = n
	}
	query := strings.TrimSpace(q.Get("q"))
	if len(query) > 500 {
		apiError(w, http.StatusBadRequest, "bad_request", "query too long")
		return
	}
	hits, next, err := s.store.Search(r.Context(), store.SearchFilter{
		Query: query, Kind: q.Get("kind"), Limit: limit, Cursor: q.Get("cursor"),
	})
	switch {
	case errors.Is(err, store.ErrBadQuery), errors.Is(err, store.ErrBadCursor):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	case err != nil && strings.Contains(err.Error(), "too many"):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	case err != nil:
		s.internal(w, "search", err)
		return
	}
	out := make([]SearchHitJSON, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHitJSON{s.itemJSON(h.Item), h.Snippet})
	}
	var nc *string
	if next != "" {
		nc = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "next_cursor": nc})
}

// maxShareTTL bounds signed share links.
const maxShareTTL = 365 * 24 * time.Hour

// handleShareLink returns a signed, time-limited URL for an item, typically
// a private one: whoever has the link can view it until it expires.
func (s *Server) handleShareLink(w http.ResponseWriter, r *http.Request) {
	it, ok := s.apiItem(w, r)
	if !ok {
		return
	}
	ttl := time.Hour
	if v := r.URL.Query().Get("ttl"); v != "" {
		d, never, err := units.ParseExpiry(v)
		if err != nil || never || d > maxShareTTL {
			apiError(w, http.StatusBadRequest, "bad_request", "ttl must be a duration such as 10m, 1h or 7d, at most 365d")
			return
		}
		ttl = d
	}
	exp := s.store.Now().Add(ttl).Unix()
	if it.ExpiresAt != nil && *it.ExpiresAt < exp {
		exp = *it.ExpiresAt // the link cannot outlive the item
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        s.cfg.BaseURL + "/" + it.ID + "?" + s.signer.Query(it.ID, exp),
		"expires_at": exp,
	})
}
