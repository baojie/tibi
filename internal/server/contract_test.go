package server_test

import (
	"testing"

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
