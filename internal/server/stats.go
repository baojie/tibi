package server

import (
	"bytes"
	"net"
	"net/http"

	"github.com/baojie/tibi/internal/metrics"
	"github.com/baojie/tibi/internal/store"
)

// metricsAllowed: an admin token, or a direct connection from this machine.
// A request that came through a reverse proxy (X-Forwarded-For / Forwarded)
// is never "local", since the proxy itself connects from localhost.
func (s *Server) metricsAllowed(r *http.Request) bool {
	if secret := bearer(r); secret != "" {
		if t, err := s.store.Authenticate(r.Context(), secret); err == nil && t.Has(store.ScopeAdmin) {
			return true
		}
	}
	// Without a token only when explicitly allowed: behind a reverse proxy
	// on the same machine every request comes from loopback, and a plain
	// proxy_pass adds no X-Forwarded-For to tell them apart.
	if !s.cfg.MetricsAllowLocal {
		return false
	}
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-Ip") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.metricsAllowed(r) {
		apiError(w, http.StatusUnauthorized, "unauthorized", "metrics need an admin token (or metrics_allow_local and a direct local connection)")
		return
	}
	st, err := s.store.Stats(r.Context(), 0)
	if err != nil {
		s.internal(w, "metrics", err)
		return
	}
	_, today := s.usage.Today("")
	gauges := []metrics.Gauge{
		{Name: "tibi_storage_bytes", Help: "Bytes of distinct stored blobs.", Value: float64(st.StorageBytes)},
		{Name: "tibi_logical_bytes", Help: "Total size of live items.", Value: float64(st.LogicalBytes)},
		{Name: "tibi_served_bytes_today", Help: "Bytes served since midnight UTC.", Value: float64(today)},
		{Name: "tibi_collections", Help: "Live collections.", Value: float64(st.Collections)},
		{Name: "tibi_tokens", Help: "Active API tokens.", Value: float64(st.Tokens)},
	}
	for _, k := range []string{store.KindImage, store.KindVideo, store.KindAudio, store.KindText, store.KindFile, store.KindEncrypted} {
		gauges = append(gauges, metrics.Gauge{Name: "tibi_items", Help: "Live items by kind.",
			Labels: map[string]string{"kind": k}, Value: float64(st.ItemsByKind[k])})
	}
	var buf bytes.Buffer
	metrics.Write(&buf, gauges)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write(buf.Bytes())
}

// TopItemJSON is a most-served item in /api/v1/stats.
type TopItemJSON struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	ViewCount   int64  `json:"view_count"`
	BytesServed int64  `json:"bytes_served"`
}

// StatsJSON is the body of GET /api/v1/stats.
type StatsJSON struct {
	Items            int            `json:"items"`
	ItemsByKind      map[string]int `json:"items_by_kind"`
	LogicalBytes     int64          `json:"logical_bytes"`
	StorageBytes     int64          `json:"storage_bytes"`
	BytesServedTotal int64          `json:"bytes_served_total"`
	BytesServedToday int64          `json:"bytes_served_today"`
	Collections      int            `json:"collections"`
	Tokens           int            `json:"tokens"`
	TopItems         []TopItemJSON  `json:"top_items"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if err := s.usage.Flush(r.Context()); err != nil { // include the latest traffic
		s.log.Warn("usage flush", "err", err)
	}
	st, err := s.store.Stats(r.Context(), 10)
	if err != nil {
		s.internal(w, "stats", err)
		return
	}
	writeJSON(w, http.StatusOK, s.statsJSON(st))
}

func (s *Server) statsJSON(st *store.Stats) StatsJSON {
	_, today := s.usage.Today("")
	out := StatsJSON{
		Items: st.Items, ItemsByKind: st.ItemsByKind, LogicalBytes: st.LogicalBytes, StorageBytes: st.StorageBytes,
		BytesServedTotal: st.BytesServed, BytesServedToday: today, Collections: st.Collections, Tokens: st.Tokens,
		TopItems: []TopItemJSON{},
	}
	for _, it := range st.Top {
		out.TopItems = append(out.TopItems, TopItemJSON{ID: it.ID, URL: s.cfg.BaseURL + "/" + it.ID, Kind: it.Kind,
			Name: downloadName(it), ViewCount: it.ViewCount, BytesServed: it.BytesServed})
	}
	return out
}
