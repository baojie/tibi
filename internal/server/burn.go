package server

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/baojie/tibi/internal/store"
)

// After a burn-after-read item is revealed, its content is fetched once
// more by the reveal page (the image, or the ciphertext for e2e.js). The
// page used to inline it as base64, which made one anonymous POST cost
// ~10× the item's size in server memory. Instead the reveal hands out an
// unguessable URL, /{id}/burned/{grant}, valid for a few minutes and only
// ever sent to the person who revealed it; the bytes stream from the blob,
// which stays until GC purges the deleted row (store.DeletedRetention).

// burnGrantTTL is how long the revealed content can be fetched.
const burnGrantTTL = 10 * time.Minute

type burnGrant struct {
	item *store.Item
	exp  time.Time
}

type burnGrants struct {
	mu sync.Mutex
	m  map[string]burnGrant
}

func (g *burnGrants) issue(it *store.Item, now time.Time) string {
	b := make([]byte, 24)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		g.m = map[string]burnGrant{}
	}
	for k, v := range g.m { // few at a time; sweep on issue
		if now.After(v.exp) {
			delete(g.m, k)
		}
	}
	g.m[tok] = burnGrant{item: it, exp: now.Add(burnGrantTTL)}
	return tok
}

func (g *burnGrants) get(itemID, tok string, now time.Time) *store.Item {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.m[tok]
	if !ok || now.After(v.exp) || v.item.ID != itemID {
		return nil
	}
	return v.item
}

// handleBurned serves /{id}/burned/{grant}: a revealed item's bytes.
func (s *Server) handleBurned(w http.ResponseWriter, r *http.Request, itemID, tok string) {
	it := s.burned.get(itemID, tok, s.store.Now())
	if it == nil {
		notFound(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.serveBlob(w, r, it, false)
}

// sameOrigin rejects cross-site form posts: another site must not be able
// to burn an item by making a visitor's browser submit the reveal form.
// Sec-Fetch-Site decides when present (every current browser sends it).
// Origin is only a fallback: under Referrer-Policy: no-referrer, which
// tibi sets, browsers send "Origin: null" even for same-origin posts.
func (s *Server) sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		return true // no usable signal (old browser, or not a browser)
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	base, _ := url.Parse(s.cfg.BaseURL)
	return strings.EqualFold(u.Host, r.Host) || (base != nil && strings.EqualFold(u.Host, base.Host))
}
