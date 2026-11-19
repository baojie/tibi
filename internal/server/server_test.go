package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"tibi/internal/blob"

	"tibi/internal/config"
	"tibi/internal/disposition"
	"tibi/internal/testutil"
)

type item struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	RawURL      string `json:"raw_url"`
	DownloadURL string `json:"download_url"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
}

func upload(t *testing.T, e *testutil.Env, token, query string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items"+query, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded") // what curl --data-binary sends
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func mustUpload(t *testing.T, e *testutil.Env, token, query string, body []byte) item {
	t.Helper()
	resp, b := upload(t, e, token, query, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	var it item
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t1")
	data := make([]byte, 300_000)
	rand.Read(data)

	it := mustUpload(t, e, tok, "?filename=x.bin", data)
	if it.Kind != "file" || it.Size != int64(len(data)) || len(it.ID) != 8 {
		t.Fatalf("unexpected item %+v", it)
	}
	if it.URL != e.Cfg.BaseURL+"/"+it.ID || it.RawURL != e.Cfg.BaseURL+"/"+it.ID+".bin" {
		t.Fatalf("urls: %+v", it)
	}
	for _, u := range []string{it.DownloadURL, it.RawURL, e.Srv.URL + "/" + it.ID + "/raw"} {
		resp, b := get(t, u)
		if resp.StatusCode != 200 || !bytes.Equal(b, data) {
			t.Fatalf("%s: status %d, %d bytes, equal=%v", u, resp.StatusCode, len(b), bytes.Equal(b, data))
		}
	}
	resp, _ := get(t, it.DownloadURL)
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "x.bin") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	resp, b := get(t, it.URL)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "x.bin") {
		t.Fatalf("view page: %d", resp.StatusCode)
	}
	// Wrong extension and unknown IDs are 404.
	if resp, _ := get(t, e.Srv.URL+"/"+it.ID+".png"); resp.StatusCode != 404 {
		t.Fatalf("wrong ext: %d", resp.StatusCode)
	}
	if resp, _ := get(t, e.Srv.URL+"/zzzzzzzz/dl"); resp.StatusCode != 404 {
		t.Fatalf("unknown id: %d", resp.StatusCode)
	}
}

func TestSameContentTwiceSharesBlob(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t1")
	a := mustUpload(t, e, tok, "", []byte("hello"))
	b := mustUpload(t, e, tok, "", []byte("hello"))
	if a.ID == b.ID {
		t.Fatal("expected two IDs")
	}
	n := 0
	e.Instance.Blobs.List(context.Background(), "", func(blob.Info) error { n++; return nil })
	if n != 1 {
		t.Fatalf("blobs = %d, want 1", n)
	}
}

func TestAuth(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t1")
	if resp, b := upload(t, e, "", "", []byte("x")); resp.StatusCode != 401 || !strings.Contains(string(b), `"code":"unauthorized"`) {
		t.Fatalf("no token: %d %s", resp.StatusCode, b)
	}
	if resp, _ := upload(t, e, "tibi_bogusbogusbogusbogusbogusbogus12", "", []byte("x")); resp.StatusCode != 401 {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
	mustUpload(t, e, tok, "", []byte("x"))
	if err := e.Instance.Store.RevokeToken(context.Background(), "t1"); err != nil {
		t.Fatal(err)
	}
	if resp, _ := upload(t, e, tok, "", []byte("x")); resp.StatusCode != 401 {
		t.Fatalf("revoked token: %d", resp.StatusCode)
	}
	// Scope enforcement.
	ro := e.Token("ro", "read_private")
	if resp, _ := upload(t, e, ro, "", []byte("x")); resp.StatusCode != 403 {
		t.Fatalf("missing scope: %d", resp.StatusCode)
	}
}

func TestTooLarge(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.MaxUploadBytes = 1000 })
	tok := e.Token("t1")
	mustUpload(t, e, tok, "", make([]byte, 1000))
	if resp, b := upload(t, e, tok, "", make([]byte, 1001)); resp.StatusCode != 413 {
		t.Fatalf("over limit: %d %s", resp.StatusCode, b)
	}
	// Chunked body without Content-Length must also be rejected.
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items", io.MultiReader(bytes.NewReader(make([]byte, 5000))))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.ContentLength = -1
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("chunked over limit: %d", resp.StatusCode)
	}
}

func TestRobotsAndNoindex(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t1")
	it := mustUpload(t, e, tok, "?filename=a.txt", []byte("hi"))

	resp, b := get(t, e.Srv.URL+"/robots.txt")
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Disallow: /") {
		t.Fatalf("robots.txt: %d %q", resp.StatusCode, b)
	}
	routes := []string{
		"/robots.txt", "/healthz", "/", "/static/style.css",
		"/" + it.ID, "/" + it.ID + ".txt", "/" + it.ID + "/raw", "/" + it.ID + "/dl",
		"/doesnotx", "/doesnotx/raw",
	}
	for _, r := range routes {
		resp, _ := get(t, e.Srv.URL+r)
		if got := resp.Header.Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
			t.Errorf("%s: X-Robots-Tag = %q", r, got)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: nosniff missing", r)
		}
	}
	resp, b = get(t, e.Srv.URL+"/"+it.ID)
	if !strings.Contains(string(b), `<meta name="robots" content="noindex`) {
		t.Error("view page lacks robots meta")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "default-src 'self'") {
		t.Errorf("view CSP = %q", csp)
	}
	resp, _ = get(t, e.Srv.URL+"/"+it.ID+"/raw")
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "default-src 'none'; sandbox" {
		t.Errorf("raw CSP = %q", csp)
	}
	if got := resp.Header.Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Errorf("raw CORP = %q", got)
	}
}

func TestActiveContentNeverServedAsHTML(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t1")
	payloads := map[string]string{
		"evil.html":  "<!doctype html><html><script>alert(1)</script></html>",
		"evil.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"evil.xhtml": `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></html>`,
		"evil.js":    "alert(1)",
	}
	for name, body := range payloads {
		it := mustUpload(t, e, tok, "?filename="+name, []byte(body))
		ext := name[strings.LastIndex(name, ".")+1:]
		for _, r := range []string{"/" + it.ID + "." + ext, "/" + it.ID + "/raw", "/" + it.ID + "/dl"} {
			resp, _ := get(t, e.Srv.URL+r)
			ct := strings.ToLower(resp.Header.Get("Content-Type"))
			if strings.Contains(ct, "html") || strings.Contains(ct, "svg") || strings.Contains(ct, "javascript") || strings.Contains(ct, "xml") {
				t.Errorf("%s %s: Content-Type %q", name, r, ct)
			}
			if resp.StatusCode != 200 {
				t.Errorf("%s %s: status %d", name, r, resp.StatusCode)
			}
		}
		// The view page is ours (text/html) but must not contain the payload unescaped.
		_, b := get(t, e.Srv.URL+"/"+it.ID)
		if strings.Contains(string(b), "<script>alert(1)") {
			t.Errorf("%s: payload reflected in view page", name)
		}
	}
	// Hostile filename is escaped on the view page.
	it := mustUpload(t, e, tok, "?filename=%3Cscript%3Ex%3C%2Fscript%3E.txt", []byte("x"))
	_, b := get(t, e.Srv.URL+"/"+it.ID)
	if strings.Contains(string(b), "<script>x</script>") {
		t.Error("filename not escaped")
	}
}

func TestWhoamiAndOpenAPI(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("laptop", "upload", "delete")
	req, _ := http.NewRequest("GET", e.Srv.URL+"/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	json.NewDecoder(resp.Body).Decode(&w)
	resp.Body.Close()
	if w.Name != "laptop" || strings.Join(w.Scopes, ",") != "upload,delete" {
		t.Fatalf("whoami = %+v", w)
	}
	resp, b := get(t, e.Srv.URL+"/api/v1/openapi.yaml")
	if resp.StatusCode != 200 || !strings.HasPrefix(string(b), "openapi: 3.") {
		t.Fatalf("openapi: %d", resp.StatusCode)
	}
	if resp, b := get(t, e.Srv.URL+"/api/v1/nope"); resp.StatusCode != 404 || !strings.Contains(string(b), `"error"`) {
		t.Fatalf("unknown api: %d %s", resp.StatusCode, b)
	}
}

func TestEmptyUpload(t *testing.T) {
	e := testutil.New(t, nil)
	if resp, _ := upload(t, e, e.Token("t"), "", nil); resp.StatusCode != 400 {
		t.Fatalf("empty: %d", resp.StatusCode)
	}
}

func TestContentDisposition(t *testing.T) {
	cases := map[string]string{
		"x.bin":         `attachment; filename="x.bin"`,
		"季度 报告(终版).txt": `attachment; filename="ID.txt"; filename*=UTF-8''%E5%AD%A3%E5%BA%A6%20%E6%8A%A5%E5%91%8A%28%E7%BB%88%E7%89%88%29.txt`,
		`a"b\c;d.txt`:   `attachment; filename="a_b_c_d.txt"; filename*=UTF-8''a%22b%5Cc%3Bd.txt`,
		"题壁":            `attachment; filename="ID.txt"; filename*=UTF-8''%E9%A2%98%E5%A3%81`,
	}
	for in, want := range cases {
		if got := disposition.Attachment(in, "ID.txt"); got != want {
			t.Errorf("%q:\n got %s\nwant %s", in, got, want)
		}
	}
}
