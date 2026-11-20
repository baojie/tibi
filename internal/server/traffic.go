package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tibi/internal/store"
)

// countingWriter counts body bytes actually written.
type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.n += int64(n)
	return n, err
}

// countHit reports whether a raw request counts as a view: the first range
// of a file does, the further range requests of a seeking player do not.
func countHit(r *http.Request) bool {
	rg := r.Header.Get("Range")
	return rg == "" || strings.HasPrefix(rg, "bytes=0-")
}

// overCap answers 429 when the item or the instance used up today's
// bandwidth allowance (PLAN A8.10). The view page stays available and
// explains why.
func (s *Server) overCap(w http.ResponseWriter, it *store.Item) bool {
	itemOver, totalOver := s.usage.Over(it.ID, s.cfg.MaxItemBytesDay, s.cfg.MaxTotalBytesDay)
	if !itemOver && !totalOver {
		return false
	}
	s.capReply(w, itemOver)
	return true
}

// reserveCap is overCap for a response about to send n bytes: it counts
// them right away (see usage.Tracker.Reserve). Call release when done.
func (s *Server) reserveCap(w http.ResponseWriter, it *store.Item, n int64) (release func(), ok bool) {
	itemOver, totalOver, release := s.usage.Reserve(it.ID, n, s.cfg.MaxItemBytesDay, s.cfg.MaxTotalBytesDay)
	if itemOver || totalOver {
		s.capReply(w, itemOver)
		return nil, false
	}
	return release, true
}

func (s *Server) capReply(w http.ResponseWriter, itemOver bool) {
	now := s.store.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	w.Header().Set("Retry-After", strconv.Itoa(int(midnight.Sub(now).Seconds())+1))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	if itemOver {
		w.Write([]byte("This item has used up its bandwidth allowance for today. Try again after midnight UTC.\n"))
	} else {
		w.Write([]byte("This server has used up its bandwidth allowance for today. Try again after midnight UTC.\n"))
	}
}

// hotlinkBlocked enforces allowed_referers on inline media (PLAN A8.10):
// a request whose Referer names another site is refused. Requests without
// a Referer (direct visits, chat apps, most apps) and from tibi's own pages
// are always allowed.
func (s *Server) hotlinkBlocked(w http.ResponseWriter, r *http.Request, it *store.Item) bool {
	if len(s.cfg.AllowedReferers) == 0 {
		return false
	}
	switch it.Kind {
	case store.KindImage, store.KindVideo, store.KindAudio:
	default:
		return false
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	host := ""
	if err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if host != "" && (host == hostOf(s.cfg.BaseURL) || host == hostOf(s.cfg.RawBase())) {
		return false
	}
	for _, a := range s.cfg.AllowedReferers {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == host || (strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:])) {
			return false
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte("Embedding this file on other sites is not allowed.\n"))
	return true
}

func hostOf(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// FlushUsage writes pending traffic counts to the database.
func (s *Server) FlushUsage(ctx context.Context) error { return s.usage.Flush(ctx) }

// UsageLoop flushes traffic counts periodically until ctx ends.
func (s *Server) UsageLoop(ctx context.Context) {
	s.usage.Loop(ctx, 10*time.Second, func(err error) { s.log.Error("usage flush", "err", err) })
}
