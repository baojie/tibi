package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

// api sends an authenticated API request and returns status and body.
func api(t *testing.T, e *testutil.Env, tok, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.Srv.URL+path, r)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// getAs fetches a public URL, optionally with a bearer token, without
// following redirects.
func getAs(t *testing.T, url, tok string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

type fullItem struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	RawURL        string `json:"raw_url"`
	DownloadURL   string `json:"download_url"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Lang          string `json:"lang"`
	Visibility    string `json:"visibility"`
	Slug          string `json:"slug"`
	SlugURL       string `json:"slug_url"`
	BurnAfterRead bool   `json:"burn_after_read"`
	Source        string `json:"source"`
	ExpiresAt     *int64 `json:"expires_at"`
	CreatedAt     int64  `json:"created_at"`
}

func up(t *testing.T, e *testutil.Env, tok, query string, body []byte) fullItem {
	t.Helper()
	resp, b := upload(t, e, tok, query, body)
	if resp.StatusCode != 201 {
		t.Fatalf("upload %s: %d %s", query, resp.StatusCode, b)
	}
	var it fullItem
	json.Unmarshal(b, &it)
	return it
}

func status(t *testing.T, url, tok string) int {
	t.Helper()
	resp, _ := getAs(t, url, tok)
	return resp.StatusCode
}

func TestExpiryAndGC(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := up(t, e, tok, "?expires=1s", []byte("short-lived"))
	if it.ExpiresAt == nil {
		t.Fatal("expires_at not set")
	}
	keep := up(t, e, tok, "?expires=1s", []byte("expiring but shared"))
	up(t, e, tok, "", []byte("expiring but shared")) // same blob, permanent

	for _, u := range []string{it.URL, it.RawURL, it.DownloadURL} {
		if status(t, u, "") != 200 {
			t.Fatalf("%s not served before expiry", u)
		}
	}
	if resp, _ := getAs(t, it.RawURL, ""); !strings.HasPrefix(resp.Header.Get("Cache-Control"), "public, max-age=") || strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("expiring raw Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
	blobs := e.BlobCount()

	e.Advance(2 * time.Second)
	for _, u := range []string{it.URL, it.RawURL, it.DownloadURL, keep.URL} {
		if status(t, u, "") != 404 {
			t.Fatalf("%s served after expiry", u)
		}
	}
	if code, _ := api(t, e, tok, "GET", "/api/v1/items/"+it.ID, nil); code != 404 {
		t.Fatalf("API get after expiry: %d", code)
	}
	res := e.GC()
	if res.Items != 2 || res.Blobs != 1 {
		t.Fatalf("gc = %+v, want 2 items, 1 blob", res)
	}
	if e.BlobCount() != blobs-1 {
		t.Fatalf("blobs %d → %d", blobs, e.BlobCount())
	}
	// The shared blob is still served for the permanent item.
	if e.GC() != (struct{ Items, Blobs int }{}) {
		t.Fatal("second GC pass not a no-op")
	}
	// Invalid expiry → 400; default_expires never.
	if resp, _ := upload(t, e, tok, "?expires=soon", []byte("x")); resp.StatusCode != 400 {
		t.Fatalf("bad expiry: %d", resp.StatusCode)
	}
}

func TestDeleteKeepsSharedBlob(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete")
	a := up(t, e, tok, "", []byte("identical"))
	b := up(t, e, tok, "", []byte("identical"))
	if e.BlobCount() != 1 {
		t.Fatal("expected one blob")
	}
	if code, _ := api(t, e, tok, "DELETE", "/api/v1/items/"+a.ID, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if status(t, a.URL, "") != 404 || status(t, b.RawURL, "") != 200 {
		t.Fatal("wrong visibility after delete")
	}
	if code, _ := api(t, e, tok, "DELETE", "/api/v1/items/"+a.ID, nil); code != 404 {
		t.Fatalf("double delete: %d", code)
	}
	// Soft-deleted rows are kept for a day.
	if res := e.GC(); res.Items != 0 {
		t.Fatalf("gc purged too early: %+v", res)
	}
	e.Advance(25 * time.Hour)
	if res := e.GC(); res.Items != 1 || res.Blobs != 0 || e.BlobCount() != 1 {
		t.Fatalf("gc after a day: %+v, blobs %d", res, e.BlobCount())
	}
	if _, body := get(t, b.RawURL); string(body) != "identical" {
		t.Fatal("surviving item lost its blob")
	}
	api(t, e, tok, "DELETE", "/api/v1/items/"+b.ID, nil)
	e.Advance(25 * time.Hour)
	if res := e.GC(); res.Items != 1 || res.Blobs != 1 || e.BlobCount() != 0 {
		t.Fatalf("final gc: %+v, blobs %d", res, e.BlobCount())
	}
	// delete scope is required.
	up2 := e.Token("uploader")
	c := up(t, e, up2, "", []byte("x"))
	if code, _ := api(t, e, up2, "DELETE", "/api/v1/items/"+c.ID, nil); code != 403 {
		t.Fatalf("delete without scope: %d", code)
	}
}

func TestPrivate(t *testing.T) {
	e := testutil.New(t, nil)
	uploader := e.Token("t")
	reader := e.Token("reader", "read_private")
	it := up(t, e, uploader, "?visibility=private&filename=secret.txt", []byte("secret"))
	if it.Visibility != "private" {
		t.Fatalf("%+v", it)
	}
	for _, u := range []string{it.URL, it.RawURL, it.DownloadURL} {
		if c := status(t, u, ""); c != 404 {
			t.Errorf("%s without token: %d", u, c)
		}
		if c := status(t, u, uploader); c != 404 {
			t.Errorf("%s with upload-only token: %d", u, c)
		}
		if c := status(t, u, "tibi_invalidinvalidinvalidinvalidinva"); c != 404 {
			t.Errorf("%s with invalid token: %d", u, c)
		}
		if c := status(t, u, reader); c != 200 {
			t.Errorf("%s with read_private: %d", u, c)
		}
	}
	resp, body := getAs(t, it.RawURL, reader)
	if string(body) != "secret" || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private raw: %q %q", body, resp.Header.Get("Cache-Control"))
	}
	// Listed and fetchable through the API by the owner.
	code, b := api(t, e, uploader, "GET", "/api/v1/items", nil)
	if code != 200 || !strings.Contains(string(b), it.ID) {
		t.Fatalf("list: %d", code)
	}
	// Make it unlisted via PATCH → public.
	if code, b := api(t, e, uploader, "PATCH", "/api/v1/items/"+it.ID, map[string]string{"visibility": "unlisted"}); code != 200 {
		t.Fatalf("patch: %d %s", code, b)
	}
	if status(t, it.URL, "") != 200 {
		t.Fatal("unlisted after patch not public")
	}
	if code, _ := api(t, e, uploader, "PATCH", "/api/v1/items/"+it.ID, map[string]string{"visibility": "public"}); code != 400 {
		t.Fatalf("bad visibility: %d", code)
	}
	if resp, _ := upload(t, e, uploader, "?visibility=secret", []byte("x")); resp.StatusCode != 400 {
		t.Fatalf("bad visibility on upload: %d", resp.StatusCode)
	}
}

func TestSlugs(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete", "read_private")
	a := up(t, e, tok, "?slug=q3-report", []byte("A"))
	if a.Slug != "q3-report" || a.SlugURL != e.Cfg.BaseURL+"/s/q3-report" {
		t.Fatalf("%+v", a)
	}
	resp, _ := getAs(t, e.Srv.URL+"/s/q3-report", "")
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/"+a.ID {
		t.Fatalf("redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Reassigning the slug moves the pointer.
	b := up(t, e, tok, "", []byte("B"))
	code, body := api(t, e, tok, "PATCH", "/api/v1/items/"+b.ID, map[string]string{"slug": "Q3-Report"})
	if code != 200 {
		t.Fatalf("patch slug: %d %s", code, body)
	}
	if resp, _ := getAs(t, e.Srv.URL+"/s/q3-report", ""); resp.Header.Get("Location") != "/"+b.ID {
		t.Fatal("slug not moved")
	}
	_, ab := api(t, e, tok, "GET", "/api/v1/items/"+a.ID, nil)
	if strings.Contains(string(ab), `"slug"`) {
		t.Fatal("old item still has the slug")
	}
	// Reserved and malformed slugs are rejected.
	for _, bad := range []string{"api", "static", "ab", "has space", "-lead", "under_score", strings.Repeat("x", 65)} {
		if resp, _ := upload(t, e, tok, "?slug="+bad, []byte("x")); resp.StatusCode != 400 {
			t.Errorf("slug %q on upload: %d", bad, resp.StatusCode)
		}
		if code, _ := api(t, e, tok, "PATCH", "/api/v1/items/"+b.ID, map[string]string{"slug": bad}); code != 400 {
			t.Errorf("slug %q on patch: %d", bad, code)
		}
	}
	if status(t, e.Srv.URL+"/s/nope-nope", "") != 404 {
		t.Fatal("unknown slug")
	}
	// Clearing, and deletion releasing, a slug.
	api(t, e, tok, "PATCH", "/api/v1/items/"+b.ID, map[string]string{"slug": ""})
	if status(t, e.Srv.URL+"/s/q3-report", "") != 404 {
		t.Fatal("cleared slug still resolves")
	}
	c := up(t, e, tok, "?slug=temp-link", []byte("C"))
	api(t, e, tok, "DELETE", "/api/v1/items/"+c.ID, nil)
	if status(t, e.Srv.URL+"/s/temp-link", "") != 404 {
		t.Fatal("deleted item's slug resolves")
	}
	// A slug on a private item does not reveal it.
	p := up(t, e, tok, "?slug=hidden-one&visibility=private", []byte("P"))
	if status(t, e.Srv.URL+"/s/hidden-one", "") != 404 || status(t, e.Srv.URL+"/s/hidden-one", tok) != 302 {
		t.Fatal("private slug access wrong")
	}
	_ = p
	// One slug per upload request.
	ct, mb := multipartBody(t, map[string]string{"slug": "two-files"}, [2]string{"a", "1"}, [2]string{"b", "2"})
	if resp, _ := postMultipart(t, e, tok, ct, mb); resp.StatusCode != 400 {
		t.Fatalf("slug with two files: %d", resp.StatusCode)
	}
}

func TestBurnAfterRead(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	reader := e.Token("reader", "read_private")
	secret := "the eagle lands at midnight"
	it := up(t, e, tok, "?burn_after_read=1&filename=msg.txt", []byte(secret))
	if !it.BurnAfterRead {
		t.Fatalf("%+v", it)
	}
	// A chat app prefetches the raw URLs and unfurls the view page: nothing
	// is consumed and the secret is not exposed.
	for _, u := range []string{it.RawURL, it.DownloadURL, e.Srv.URL + "/" + it.ID + "/raw"} {
		resp, body := getAs(t, u, "")
		if resp.StatusCode != 404 || strings.Contains(string(body), secret) {
			t.Fatalf("raw prefetch %s: %d", u, resp.StatusCode)
		}
	}
	for i := 0; i < 3; i++ {
		resp, body := getAs(t, it.URL, "")
		if resp.StatusCode != 200 || strings.Contains(string(body), secret) || !strings.Contains(string(body), `action="/`+it.ID+`/reveal"`) {
			t.Fatalf("interstitial: %d", resp.StatusCode)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("interstitial cacheable")
		}
	}
	// The owner (read_private token) can look without burning it.
	if _, body := getAs(t, it.RawURL, reader); string(body) != secret {
		t.Fatal("owner cannot read burn item")
	}
	// Reveal once.
	resp, err := http.Post(e.Srv.URL+"/"+it.ID+"/reveal", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "the eagle lands") || !strings.Contains(string(body), "deleted from the server") {
		t.Fatalf("reveal: %d", resp.StatusCode)
	}
	// Gone for everyone afterwards.
	for _, u := range []string{it.URL, it.RawURL} {
		if status(t, u, "") != 404 {
			t.Fatalf("%s after burn", u)
		}
	}
	if resp, _ := http.Post(e.Srv.URL+"/"+it.ID+"/reveal", "", nil); resp.StatusCode != 404 {
		t.Fatalf("second reveal: %d", resp.StatusCode)
	}
	// GET on /reveal is not allowed (no accidental burns).
	if c := status(t, e.Srv.URL+"/"+it.ID+"/reveal", ""); c != 405 {
		t.Fatalf("GET reveal: %d", c)
	}
	// Non-burn items cannot be "revealed".
	plain := up(t, e, tok, "", []byte("plain"))
	if resp, _ := http.Post(e.Srv.URL+"/"+plain.ID+"/reveal", "", nil); resp.StatusCode != 404 {
		t.Fatalf("reveal non-burn: %d", resp.StatusCode)
	}
}

func TestBurnConcurrentRevealAndKinds(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := up(t, e, tok, "?burn_after_read=1", []byte("only once"))
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(e.Srv.URL+"/"+it.ID+"/reveal", "", nil)
			if err != nil {
				return
			}
			resp.Body.Close()
			mu.Lock()
			codes[resp.StatusCode]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[200] != 1 || codes[404] != 9 {
		t.Fatalf("concurrent reveals: %v", codes)
	}

	// Image: inlined as a data URI so it survives its own deletion.
	img := up(t, e, tok, "?burn_after_read=1", testimg.PNG(4, 4))
	resp, _ := http.Post(e.Srv.URL+"/"+img.ID+"/reveal", "", nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `src="data:image/png;base64,`) {
		t.Fatal("burned image not inlined")
	}
	// File: the reveal is the download.
	bin := up(t, e, tok, "?burn_after_read=1&filename=key.bin", []byte{0, 1, 2, 3})
	resp, _ = http.Post(e.Srv.URL+"/"+bin.ID+"/reveal", "", nil)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(b, []byte{0, 1, 2, 3}) || !strings.Contains(resp.Header.Get("Content-Disposition"), "key.bin") {
		t.Fatalf("burned file download: %q %q", b, resp.Header.Get("Content-Disposition"))
	}
}

func TestListPagination(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	var ids []string
	for i := 0; i < 25; i++ {
		src := "cli"
		if i%5 == 0 {
			src = "chrome"
		}
		it := up(t, e, tok, "?source="+src, []byte(fmt.Sprintf("item %d", i)))
		ids = append(ids, it.ID)
	}
	up(t, e, tok, "", testimg.PNG(2, 2))
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	var prev int64 = 1 << 62
	for {
		path := "/api/v1/items?limit=10&kind=text"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		code, b := api(t, e, tok, "GET", path, nil)
		if code != 200 {
			t.Fatalf("list: %d %s", code, b)
		}
		var page struct {
			Items      []fullItem `json:"items"`
			NextCursor *string    `json:"next_cursor"`
		}
		json.Unmarshal(b, &page)
		pages++
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			if it.Kind != "text" || it.CreatedAt > prev {
				t.Fatalf("filter/order broken: %+v", it)
			}
			prev = it.CreatedAt
			seen[it.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 25 || pages != 3 {
		t.Fatalf("saw %d items in %d pages", len(seen), pages)
	}
	_, b := api(t, e, tok, "GET", "/api/v1/items?source=chrome&limit=100", nil)
	if n := strings.Count(string(b), `"source":"chrome"`); n != 5 {
		t.Fatalf("source filter: %d", n)
	}
	for _, bad := range []string{"?cursor=!!!", "?limit=0", "?limit=101", "?limit=x"} {
		if code, _ := api(t, e, tok, "GET", "/api/v1/items"+bad, nil); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	if code, _ := api(t, e, "", "GET", "/api/v1/items", nil); code != 401 {
		t.Fatalf("unauthenticated list: %d", code)
	}
}

func TestPatchFields(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := up(t, e, tok, "?filename=x.txt", []byte("def f(): pass\n"))
	code, b := api(t, e, tok, "PATCH", "/api/v1/items/"+it.ID, map[string]string{"title": "Notes", "lang": "py", "expires": "1h"})
	var got fullItem
	json.Unmarshal(b, &got)
	if code != 200 || got.Title != "Notes" || got.Lang != "python" || got.ExpiresAt == nil {
		t.Fatalf("patch: %d %s", code, b)
	}
	_, b = api(t, e, tok, "PATCH", "/api/v1/items/"+it.ID, map[string]string{"expires": "never"})
	json.Unmarshal(b, &got)
	if got.ExpiresAt != nil {
		t.Fatal("expires never not applied")
	}
	if _, page := get(t, it.URL); !strings.Contains(string(page), "Notes") || !strings.Contains(string(page), `<span class="k">def</span>`) {
		t.Fatal("view page not updated (title/lang)")
	}
	for _, bad := range []map[string]string{{"content": "x"}, {"size": "1"}, {"lang": "nosuch"}, {"expires": "soon"}} {
		if code, _ := api(t, e, tok, "PATCH", "/api/v1/items/"+it.ID, bad); code != 400 {
			t.Errorf("patch %v: %d", bad, code)
		}
	}
	img := up(t, e, tok, "", testimg.PNG(2, 2))
	if code, _ := api(t, e, tok, "PATCH", "/api/v1/items/"+img.ID, map[string]string{"lang": "go"}); code != 400 {
		t.Fatalf("lang on image: %d", code)
	}
	if code, _ := api(t, e, tok, "PATCH", "/api/v1/items/zzzzzzzz", map[string]string{"title": "x"}); code != 404 {
		t.Fatalf("patch missing: %d", code)
	}
}

func TestShortExpiryNeverEarly(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	// Upload just before a second boundary: a truncated expires_at would
	// already be (almost) in the past.
	for time.Now().Nanosecond() < 900_000_000 {
		time.Sleep(10 * time.Millisecond)
	}
	before := time.Now()
	it := up(t, e, tok, "?expires=1s", []byte("x"))
	if exp := time.Unix(*it.ExpiresAt, 0); exp.Before(before.Add(time.Second)) {
		t.Fatalf("expires_at %v is less than 1s after upload at %v", exp, before)
	}
}
