package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"tibi/internal/id"
	"tibi/internal/store"
)

// CollectionJSON is the API representation of a collection.
type CollectionJSON struct {
	ID         string     `json:"id"`
	URL        string     `json:"url"`
	Title      string     `json:"title,omitempty"`
	Visibility string     `json:"visibility"`
	ItemCount  int        `json:"item_count"`
	ExpiresAt  *int64     `json:"expires_at"`
	CreatedAt  int64      `json:"created_at"`
	Items      []ItemJSON `json:"items,omitempty"`
}

func (s *Server) collectionJSON(c *store.Collection) CollectionJSON {
	return CollectionJSON{
		ID: c.ID, URL: s.cfg.BaseURL + "/c/" + c.ID, Title: c.Title, Visibility: c.Visibility,
		ItemCount: c.ItemCount, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt,
	}
}

// apiCollection loads the live collection named by the {id} path value.
func (s *Server) apiCollection(w http.ResponseWriter, r *http.Request) (*store.Collection, bool) {
	cid := r.PathValue("id")
	var c *store.Collection
	err := store.ErrNotFound
	if id.Valid(cid) {
		c, err = s.store.GetCollection(r.Context(), cid)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found", "no such collection")
		return nil, false
	}
	if err != nil {
		s.internal(w, "get collection", err)
		return nil, false
	}
	return c, true
}

func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			apiError(w, http.StatusBadRequest, "bad_request", "limit must be 1–100")
			return
		}
		limit = n
	}
	cs, next, err := s.store.ListCollections(r.Context(), limit, r.URL.Query().Get("cursor"))
	if errors.Is(err, store.ErrBadCursor) {
		apiError(w, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	if err != nil {
		s.internal(w, "list collections", err)
		return
	}
	out := make([]CollectionJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.collectionJSON(c))
	}
	var nc *string
	if next != "" {
		nc = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": out, "next_cursor": nc})
}

func (s *Server) writeCollection(w http.ResponseWriter, r *http.Request, c *store.Collection, status int) {
	items, err := s.store.CollectionItems(r.Context(), c.ID)
	if err != nil {
		s.internal(w, "collection items", err)
		return
	}
	j := s.collectionJSON(c)
	j.Items = make([]ItemJSON, 0, len(items))
	for _, it := range items {
		j.Items = append(j.Items, s.itemJSON(it))
	}
	writeJSON(w, status, j)
}

func (s *Server) handleGetCollection(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.apiCollection(w, r); ok {
		s.writeCollection(w, r, c, http.StatusOK)
	}
}

// handleCreateCollection makes a collection, optionally gathering existing
// items (which must not already belong to another collection).
func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      string   `json:"title"`
		Visibility string   `json:"visibility"`
		Expires    string   `json:"expires"`
		Items      []string `json:"items"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		apiError(w, http.StatusBadRequest, "bad_request", "body must be a JSON object with title, visibility, expires, items")
		return
	}
	if body.Visibility != "" && body.Visibility != store.Unlisted && body.Visibility != store.Private {
		apiError(w, http.StatusBadRequest, "bad_request", "visibility must be unlisted or private")
		return
	}
	exp := body.Expires
	if exp == "" {
		exp = s.cfg.DefaultExpires
	}
	at, err := s.expiresAt(exp)
	if err != nil {
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx := r.Context()
	members := make([]*store.Item, 0, len(body.Items))
	for _, iid := range body.Items {
		it, err := s.store.GetItem(ctx, iid)
		if err != nil || !id.Valid(iid) {
			apiError(w, http.StatusBadRequest, "bad_request", "no such item "+iid)
			return
		}
		if it.CollectionID != "" {
			apiError(w, http.StatusBadRequest, "bad_request", "item "+iid+" already belongs to collection "+it.CollectionID)
			return
		}
		members = append(members, it)
	}
	c := &store.Collection{Title: body.Title, Visibility: body.Visibility, TokenID: tokenFrom(ctx).ID, ExpiresAt: at}
	if err := s.store.CreateCollection(ctx, c); err != nil {
		s.internal(w, "create collection", err)
		return
	}
	for i, it := range members {
		if err := s.store.SetCollection(ctx, it.ID, c.ID, i); err != nil {
			s.internal(w, "add to collection", err)
			return
		}
	}
	c, err = s.store.GetCollection(ctx, c.ID)
	if err != nil {
		s.internal(w, "get collection", err)
		return
	}
	s.writeCollection(w, r, c, http.StatusCreated)
}

var collectionPatchable = map[string]bool{"title": true, "visibility": true, "expires": true, "items": true}

func (s *Server) handlePatchCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.apiCollection(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request", "body must be a JSON object")
		return
	}
	var p store.CollectionPatch
	for k, raw := range body {
		if !collectionPatchable[k] {
			apiError(w, http.StatusBadRequest, "bad_request", "field "+strconv.Quote(k)+" cannot be changed")
			return
		}
		if k == "items" {
			if err := json.Unmarshal(raw, &p.Order); err != nil || p.Order == nil {
				apiError(w, http.StatusBadRequest, "bad_request", "items must be an array of item IDs")
				return
			}
			continue
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			apiError(w, http.StatusBadRequest, "bad_request", k+" must be a string")
			return
		}
		switch k {
		case "title":
			p.Title = &v
		case "visibility":
			p.Visibility = &v
		case "expires":
			at, err := s.expiresAt(v)
			if err != nil {
				apiError(w, http.StatusBadRequest, "bad_request", err.Error())
				return
			}
			p.ExpiresAt = &at
		}
	}
	updated, err := s.store.UpdateCollection(r.Context(), c.ID, p)
	switch {
	case errors.Is(err, store.ErrBadOrder):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil && strings.HasPrefix(err.Error(), "visibility"):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found", "no such collection")
	case err != nil:
		s.internal(w, "patch collection", err)
	default:
		s.writeCollection(w, r, updated, http.StatusOK)
	}
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	cid := r.PathValue("id")
	err := store.ErrNotFound
	if id.Valid(cid) {
		err = s.store.DeleteCollection(r.Context(), cid)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found", "no such collection")
		return
	}
	if err != nil {
		s.internal(w, "delete collection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFork creates a new item from the request body with forked_from set.
// Content is immutable, so "editing" a paste means forking it. Title, lang,
// filename and visibility default to the original's.
func (s *Server) handleFork(w http.ResponseWriter, r *http.Request) {
	orig, ok := s.apiItem(w, r)
	if !ok {
		return
	}
	limit := s.cfg.MaxUploadBytes
	if r.ContentLength > limit {
		s.tooLarge(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	get := func(k string) string {
		if v := opt(r, k); v != "" {
			return v
		}
		switch k {
		case "title":
			return orig.Title
		case "filename":
			return orig.Filename
		case "visibility":
			return orig.Visibility
		case "lang":
			return orig.Lang
		}
		return ""
	}
	o, err := s.uploadOptions(r, get)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	if orig.Kind == store.KindText && opt(r, "kind") == "" {
		o.Kind = store.KindText
	}
	o.ForkedFrom = orig.ID
	p, err := s.ingest.Prepare(r.Context(), r.Body, o)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	defer p.Close()
	if err := s.ingest.Commit(r.Context(), p); err != nil {
		s.ingestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.itemJSON(p.Item))
}
