package server_test

import (
	"net/http"
	"strings"
	"testing"

	"tibi/internal/config"
	"tibi/internal/testutil"
)

const extOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func corsReq(t *testing.T, e *testutil.Env, method, path, origin string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.Srv.URL+path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestCORS(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.CORSOrigins = []string{extOrigin} })
	tok := e.Token("t")

	// Preflight for an upload from the extension.
	pre := corsReq(t, e, "OPTIONS", "/api/v1/items", extOrigin, map[string]string{
		"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "authorization,content-type,x-tibi-filename",
	})
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != extOrigin ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "Authorization") || !strings.Contains(pre.Header.Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Fatalf("preflight: %d %v", pre.StatusCode, pre.Header)
	}
	// Actual requests get the origin and the tus headers exposed.
	r := corsReq(t, e, "GET", "/api/v1/whoami", extOrigin, map[string]string{"Authorization": "Bearer " + tok})
	if r.StatusCode != 200 || r.Header.Get("Access-Control-Allow-Origin") != extOrigin || !strings.Contains(r.Header.Get("Access-Control-Expose-Headers"), "Upload-Offset") {
		t.Fatalf("GET: %d %v", r.StatusCode, r.Header)
	}
	// Other origins get nothing (the browser then blocks the response).
	for _, o := range []string{"https://evil.example", "chrome-extension://otherotherotherotherotherotherot"} {
		r := corsReq(t, e, "OPTIONS", "/api/v1/items", o, map[string]string{"Access-Control-Request-Method": "POST"})
		if r.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s allowed", o)
		}
	}
	// Public pages never carry CORS headers.
	if r := corsReq(t, e, "GET", "/robots.txt", extOrigin, nil); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS on a public page")
	}

	// "*" allows any origin.
	any := testutil.New(t, func(c *config.Server) { c.CORSOrigins = []string{"*"} })
	if r := corsReq(t, any, "GET", "/api/v1/openapi.yaml", "https://app.example", nil); r.Header.Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatal("* did not allow")
	}
	// None configured: no CORS at all.
	none := testutil.New(t, nil)
	if r := corsReq(t, none, "OPTIONS", "/api/v1/items", extOrigin, map[string]string{"Access-Control-Request-Method": "POST"}); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS without cors_origins")
	}
}
