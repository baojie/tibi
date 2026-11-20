// Package metrics keeps process counters and renders the Prometheus text
// exposition format (no client library needed for a handful of series).
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Counters of this process.
var (
	UploadBytes    atomic.Int64 // bytes of items stored by uploads
	ResumableBytes atomic.Int64 // bytes received by resumable (tus) PATCH requests
	ServedBytes    atomic.Int64 // raw/download/thumbnail bytes sent
	GCItems        atomic.Int64 // item rows purged
	GCBlobs        atomic.Int64 // blobs deleted
	UsageFlushErr  atomic.Int64

	reqMu    sync.Mutex
	requests = map[[2]string]int64{} // {method, code} → count
)

// Request counts one HTTP response.
func Request(method string, code int) {
	reqMu.Lock()
	requests[[2]string{method, fmt.Sprint(code)}]++
	reqMu.Unlock()
}

// Gauge is a value computed at scrape time.
type Gauge struct {
	Name, Help string
	Labels     map[string]string
	Value      float64
}

// Write renders all counters plus the given gauges.
func Write(w io.Writer, gauges []Gauge) {
	counter := func(name, help string, v int64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
	}
	fmt.Fprintf(w, "# HELP tibi_http_requests_total HTTP responses by method and status code.\n# TYPE tibi_http_requests_total counter\n")
	reqMu.Lock()
	keys := make([][2]string, 0, len(requests))
	for k := range requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		fmt.Fprintf(w, "tibi_http_requests_total{method=%q,code=%q} %d\n", k[0], k[1], requests[k])
	}
	reqMu.Unlock()
	counter("tibi_upload_bytes_total", "Bytes stored by uploads.", UploadBytes.Load())
	counter("tibi_resumable_received_bytes_total", "Bytes received by resumable upload requests.", ResumableBytes.Load())
	counter("tibi_served_bytes_total", "Bytes of item content sent (raw, download, thumbnails).", ServedBytes.Load())
	counter("tibi_gc_items_total", "Item rows purged by garbage collection.", GCItems.Load())
	counter("tibi_gc_blobs_total", "Blobs deleted by garbage collection.", GCBlobs.Load())
	seen := map[string]bool{}
	for _, g := range gauges {
		if !seen[g.Name] {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", g.Name, g.Help, g.Name)
			seen[g.Name] = true
		}
		var lb []string
		for k, v := range g.Labels {
			lb = append(lb, fmt.Sprintf("%s=%q", k, v))
		}
		sort.Strings(lb)
		labels := ""
		if len(lb) > 0 {
			labels = "{" + strings.Join(lb, ",") + "}"
		}
		fmt.Fprintf(w, "%s%s %g\n", g.Name, labels, g.Value)
	}
}
