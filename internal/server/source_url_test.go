package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/baojie/tibi/internal/testutil"
)

// source_url is kept with the item and shown only to the token that
// uploaded it and to read_private tokens: never on public pages.
func TestSourceURL(t *testing.T) {
	e := testutil.New(t, nil)
	owner := e.Token("laptop", "upload")
	other := e.Token("phone", "upload")
	admin := e.Token("admin", "upload", "read_private")
	const src = "https://example.com/photos/sunset.jpg?size=large"
	srcOf := func(tok, id string) string {
		code, b := api(t, e, tok, "GET", "/api/v1/items/"+id, nil)
		var it struct {
			SourceURL *string `json:"source_url"`
		}
		if code != 200 || json.Unmarshal(b, &it) != nil {
			t.Fatalf("get: %d %s", code, b)
		}
		if it.SourceURL == nil {
			return ""
		}
		return *it.SourceURL
	}

	// Query option; the upload answer shows it to the uploader.
	resp, b := upload(t, e, owner, "?filename=a.txt&source_url="+strings.ReplaceAll(src, "?", "%3F"), []byte("hello"))
	var up struct {
		ID        string `json:"id"`
		SourceURL string `json:"source_url"`
	}
	json.Unmarshal(b, &up)
	if resp.StatusCode != 201 || up.SourceURL != src {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	id := up.ID
	if srcOf(owner, id) != src || srcOf(admin, id) != src || srcOf(other, id) != "" {
		t.Fatalf("visibility: owner %q, admin %q, other %q", srcOf(owner, id), srcOf(admin, id), srcOf(other, id))
	}
	// In the list and search too, for the owner only.
	for _, tok := range []string{owner, other} {
		_, lb := api(t, e, tok, "GET", "/api/v1/items", nil)
		_, sb := api(t, e, tok, "GET", "/api/v1/search?q=hello", nil)
		has := strings.Contains(string(lb), "example.com") && strings.Contains(string(sb), "example.com")
		if has != (tok == owner) {
			t.Fatalf("list/search for %s: %s %s", tok[:4], lb, sb)
		}
	}
	// Never on the public page.
	if _, page := get(t, e.Srv.URL+"/"+id); strings.Contains(string(page), "example.com") {
		t.Fatal("public page shows the source URL")
	}

	// Header option (X-Tibi-Source-Url) and tus metadata.
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items", bytes.NewReader([]byte("x")))
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("X-Tibi-Source-Url", "http://example.org/page")
	hr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := io.ReadAll(hr.Body)
	hr.Body.Close()
	if hr.StatusCode != 201 || !strings.Contains(string(hb), `"source_url":"http://example.org/page"`) {
		t.Fatalf("header: %d %s", hr.StatusCode, hb)
	}
	tr := tusCreate(t, e, owner, "3", map[string]string{"filename": "t.txt", "source_url": "https://example.net/t"})
	if tr.StatusCode != 201 {
		t.Fatalf("tus create: %d", tr.StatusCode)
	}
	loc := tr.Header.Get("Location")
	if !strings.HasPrefix(loc, "http") {
		loc = e.Srv.URL + loc
	}
	if r := tusPatch(t, owner, loc, 0, []byte("abc")); r.StatusCode != 204 {
		t.Fatalf("tus patch: %d", r.StatusCode)
	}
	if _, lb := api(t, e, owner, "GET", "/api/v1/items", nil); !strings.Contains(string(lb), `"source_url":"https://example.net/t"`) {
		t.Fatalf("tus upload lost source_url: %s", lb)
	}

	// Only absolute http(s) URLs.
	for _, bad := range []string{"javascript:alert(1)", "/relative", "ftp://example.com/x", "https://", "https://example.com/" + strings.Repeat("a", 2048)} {
		if r, b := upload(t, e, owner, "?source_url="+strings.ReplaceAll(bad, ":", "%3A"), []byte("x")); r.StatusCode != 400 {
			t.Fatalf("%q accepted: %d %s", bad, r.StatusCode, b)
		}
	}
}
