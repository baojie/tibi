package server

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Per-token rate limit for the API (PLAN A8.7): a token bucket refilled at
// rate_limit requests per minute, holding up to rate_burst. Public pages are
// not limited here; put per-IP limits in the reverse proxy (docs/DEPLOY.md).
type rateLimiter struct {
	perSec float64
	burst  float64

	mu      sync.Mutex
	buckets map[int64]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute, burst int) *rateLimiter {
	if perMinute <= 0 {
		return nil
	}
	return &rateLimiter{perSec: float64(perMinute) / 60, burst: float64(max(burst, 1)), buckets: map[int64]*bucket{}}
}

// allow takes one request from the token's bucket; when empty it returns
// how long until the next request would be allowed.
func (l *rateLimiter) allow(tokenID int64, now time.Time) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[tokenID]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[tokenID] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSec)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.perSec * float64(time.Second))
}

func rateLimited(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	apiError(w, http.StatusTooManyRequests, "rate_limited", "too many requests for this token; retry after "+strconv.Itoa(int(math.Ceil(wait.Seconds())))+" s")
}
