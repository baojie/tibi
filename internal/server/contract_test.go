package server_test

import (
	"fmt"
	"net/http"
	"testing"
	"tibi/internal/config"

	"tibi/internal/testutil"
)

// Every authenticated operation rejects a missing token (401) and a token
// without the needed scope (403). Responses are validated against
// api/openapi.yaml by the test server (see testutil.contractChecked).
func TestAuthErrorsEveryOperation(t *testing.T) {
	e := testutil.New(t, nil)
	owner := e.Token("owner", "upload", "delete")
	readOnly := e.Token("reader", "read_private") // no upload, no delete
	it := up(t, e, owner, "", []byte("x"))
	ops := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/whoami", nil},
		{"GET", "/api/v1/items", nil},
		{"GET", "/api/v1/items/" + it.ID, nil},
		{"PATCH", "/api/v1/items/" + it.ID, map[string]string{"title": "t"}},
		{"DELETE", "/api/v1/items/" + it.ID, nil},
		{"GET", "/api/v1/search?q=x", nil},
		{"POST", "/api/v1/items/" + it.ID + "/fork", "new"},
		{"POST", "/api/v1/items/" + it.ID + "/share-link", nil},
		{"POST", "/api/v1/collections", map[string]string{}},
		{"GET", "/api/v1/collections", nil},
		{"GET", "/api/v1/collections/" + it.ID, nil},
		{"PATCH", "/api/v1/collections/" + it.ID, map[string]string{"title": "t"}},
		{"DELETE", "/api/v1/collections/" + it.ID, nil},
		{"GET", "/api/v1/stats", nil},
		{"GET", "/api/v1/uploads/someupload/item", nil},
	}
	for _, op := range ops {
		if code, b := api(t, e, "", op.method, op.path, op.body); code != 401 {
			t.Errorf("%s %s without token: %d %s", op.method, op.path, code, b)
		}
		if code, b := api(t, e, "tibi_notarealtokennotarealtokennotar", op.method, op.path, op.body); code != 401 {
			t.Errorf("%s %s with bad token: %d %s", op.method, op.path, code, b)
		}
		if op.path == "/api/v1/whoami" {
			continue // any valid token may call it
		}
		if code, b := api(t, e, readOnly, op.method, op.path, op.body); code != 403 {
			t.Errorf("%s %s without scope: %d %s", op.method, op.path, code, b)
		}
	}
	// The served document is the one the tests validate against.
	if resp, b := get(t, e.Srv.URL+"/api/v1/openapi.yaml"); resp.StatusCode != 200 || len(b) < 1000 {
		t.Fatal("openapi.yaml not served")
	}
}

// Every authenticated operation is rate limited per token (PLAN A8.7).
func TestRateLimitEveryOperation(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.RateLimit, c.RateBurst = 1, 1 })
	setup := e.Token("setup", "admin")
	it := up(t, e, setup, "", []byte("x"))
	ops := []struct{ method, path string }{
		{"GET", "/api/v1/whoami"},
		{"POST", "/api/v1/items"},
		{"GET", "/api/v1/items"},
		{"GET", "/api/v1/items/" + it.ID},
		{"PATCH", "/api/v1/items/" + it.ID},
		{"DELETE", "/api/v1/items/" + it.ID},
		{"GET", "/api/v1/search?q=x"},
		{"POST", "/api/v1/items/" + it.ID + "/fork"},
		{"POST", "/api/v1/items/" + it.ID + "/share-link"},
		{"POST", "/api/v1/collections"},
		{"GET", "/api/v1/collections"},
		{"GET", "/api/v1/collections/" + it.ID},
		{"PATCH", "/api/v1/collections/" + it.ID},
		{"DELETE", "/api/v1/collections/" + it.ID},
		{"GET", "/api/v1/stats"},
		{"POST", "/api/v1/uploads/"},
		{"GET", "/api/v1/uploads/someupload/item"},
	}
	for i, op := range ops {
		tok := e.Token(fmt.Sprintf("t%d", i), "admin") // a fresh bucket each
		api(t, e, tok, "GET", "/api/v1/whoami", nil)   // uses the one request
		req, _ := http.NewRequest(op.method, e.Srv.URL+op.path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
			t.Errorf("%s %s over the limit: %d %q", op.method, op.path, resp.StatusCode, resp.Header.Get("Retry-After"))
		}
	}
}
