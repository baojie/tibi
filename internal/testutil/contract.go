package testutil

import (
	"bytes"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"tibi/api"
	"tibi/internal/apispec"
)

var (
	specOnce sync.Once
	spec     *apispec.Spec
	specErr  error

	coverMu sync.Mutex
	covered = map[string]bool{} // "GET /api/v1/items 200"
)

// Spec returns the parsed api/openapi.yaml.
func Spec() (*apispec.Spec, error) {
	specOnce.Do(func() { spec, specErr = apispec.Load(api.OpenAPI) })
	return spec, specErr
}

// contractChecked wraps the server so every API response in every test is
// validated against api/openapi.yaml: an undocumented status, content type
// or field, a missing required field or a wrong type fails the test.
func contractChecked(t testing.TB, h http.Handler) http.Handler {
	s, err := Spec()
	if err != nil {
		t.Fatalf("openapi.yaml: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/healthz" {
			h.ServeHTTP(w, r)
			return
		}
		rec := &recorder{ResponseWriter: w, status: 200}
		h.ServeHTTP(rec, r)
		if _, ok := s.Match(r.Method, r.URL.Path); !ok && rec.status == 404 {
			return // unknown endpoint: the catch-all's 404
		}
		op, err := s.ValidateResponse(r.Method, r.URL.Path, rec.status, rec.Header().Get("Content-Type"), rec.body.Bytes())
		if err != nil {
			t.Errorf("API contract violation: %v\nbody: %.500s", err, rec.body.String())
			return
		}
		coverMu.Lock()
		covered[op.Method+" "+op.Path+" "+strconv.Itoa(rec.status)] = true
		coverMu.Unlock()
	})
}

type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.body.Len() < 8<<20 {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

// UncoveredResponses lists documented operation/status pairs that no
// response in this test binary exercised, ignoring the exempt ones.
func UncoveredResponses(exempt ...string) []string {
	s, err := Spec()
	if err != nil {
		return []string{err.Error()}
	}
	skip := map[string]bool{}
	for _, e := range exempt {
		skip[e] = true
	}
	coverMu.Lock()
	defer coverMu.Unlock()
	var missing []string
	for op, codes := range s.Operations() {
		for _, c := range codes {
			key := op.Method + " " + op.Path + " " + c
			if !covered[key] && !skip[key] {
				missing = append(missing, key)
			}
		}
	}
	sort.Strings(missing)
	return missing
}
