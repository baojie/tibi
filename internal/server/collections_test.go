package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/baojie/tibi/internal/config"
	"github.com/baojie/tibi/internal/testutil"
	"github.com/baojie/tibi/internal/testutil/testimg"
)

type collJSON struct {
	ID         string     `json:"id"`
	URL        string     `json:"url"`
	Title      string     `json:"title"`
	Visibility string     `json:"visibility"`
	ItemCount  int        `json:"item_count"`
	Items      []itemColl `json:"items"`
}

type itemColl struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	Kind          string `json:"kind"`
	Filename      string `json:"filename"`
	CollectionID  string `json:"collection_id"`
	CollectionURL string `json:"collection_url"`
	Position      *int   `json:"position"`
	ForkedFrom    string `json:"forked_from"`
	Title         string `json:"title"`
	Lang          string `json:"lang"`
	Visibility    string `json:"visibility"`
}

func albumUpload(t *testing.T, e *testutil.Env, tok string, fields map[string]string, files ...[2]string) (collJSON, []itemColl) {
	t.Helper()
	ct, body := multipartBody(t, fields, files...)
	resp, b := postMultipart(t, e, tok, ct, body)
	if resp.StatusCode != 201 {
		t.Fatalf("album upload: %d %s", resp.StatusCode, b)
	}
	var out struct {
		Collection collJSON   `json:"collection"`
		Items      []itemColl `json:"items"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Collection, out.Items
}

func TestAlbumUpload(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete")
	c, items := albumUpload(t, e, tok, map[string]string{"collection": "new", "collection_title": "Trip"},
		[2]string{"a.png", string(testimg.PNG(30, 20))},
		[2]string{"b.png", string(testimg.PNG(20, 30))},
		[2]string{"c.txt", "notes from the trip\n"})
	if c.Title != "Trip" || c.ItemCount != 3 || c.URL != e.Cfg.BaseURL+"/c/"+c.ID || len(items) != 3 {
		t.Fatalf("collection: %+v %d items", c, len(items))
	}
	for i, it := range items {
		if it.CollectionID != c.ID || it.Position == nil || *it.Position != i || it.CollectionURL != c.URL {
			t.Fatalf("item %d: %+v", i, it)
		}
	}
	// The page shows all three in order (stacked: not all images).
	_, page := get(t, c.URL)
	s := string(page)
	ia, ib, ic := strings.Index(s, "a.png"), strings.Index(s, "b.png"), strings.Index(s, "notes from the trip")
	if ia < 0 || ib < ia || ic < ib {
		t.Fatalf("collection page order: %d %d %d", ia, ib, ic)
	}
	if !strings.Contains(s, `id="f3-L1"`) || strings.Contains(s, `class="grid"`) {
		t.Fatal("stacked view lacks prefixed line anchors (or rendered as grid)")
	}
	// Item pages link back.
	if _, p := get(t, items[0].URL); !strings.Contains(string(p), `/c/`+c.ID) {
		t.Fatal("item page lacks collection link")
	}

	// API: get, list by collection, reorder.
	code, b := api(t, e, tok, "GET", "/api/v1/collections/"+c.ID, nil)
	var got collJSON
	json.Unmarshal(b, &got)
	if code != 200 || len(got.Items) != 3 || got.Items[2].Filename != "c.txt" {
		t.Fatalf("get: %d %s", code, b)
	}
	_, b = api(t, e, tok, "GET", "/api/v1/items?collection="+c.ID, nil)
	if strings.Count(string(b), `"collection_id":"`+c.ID+`"`) != 3 {
		t.Fatalf("list by collection: %s", b)
	}
	order := []string{items[2].ID, items[0].ID, items[1].ID}
	code, b = api(t, e, tok, "PATCH", "/api/v1/collections/"+c.ID, map[string]any{"items": order, "title": "Trip 2"})
	json.Unmarshal(b, &got)
	if code != 200 || got.Title != "Trip 2" || got.Items[0].ID != items[2].ID {
		t.Fatalf("reorder: %d %s", code, b)
	}
	_, page = get(t, c.URL)
	if strings.Index(string(page), "notes from the trip") > strings.Index(string(page), "a.png") {
		t.Fatal("page not reordered")
	}
	for _, bad := range []map[string]any{
		{"items": order[:2]},                              // missing one
		{"items": []string{order[0], order[0], order[1]}}, // duplicate
		{"items": "nope"},
		{"visibility": "public"},
		{"expires": "soon"},
		{"owner": "x"},
	} {
		if code, _ := api(t, e, tok, "PATCH", "/api/v1/collections/"+c.ID, bad); code != 400 {
			t.Errorf("patch %v: %d", bad, code)
		}
	}

	// Append to the collection with a later upload.
	_, more := albumUpload(t, e, tok, map[string]string{"collection": c.ID}, [2]string{"d.txt", "appendix"})
	if *more[0].Position != 3 {
		t.Fatalf("appended position: %v", *more[0].Position)
	}

	// Deleting the collection soft-deletes its items.
	if code, _ := api(t, e, tok, "DELETE", "/api/v1/collections/"+c.ID, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	for _, it := range append(items, more...) {
		if status(t, it.URL, "") != 404 {
			t.Fatalf("item %s survived collection deletion", it.ID)
		}
	}
	if status(t, c.URL, "") != 404 {
		t.Fatal("collection page survived deletion")
	}
	if code, _ := api(t, e, tok, "GET", "/api/v1/collections/"+c.ID, nil); code != 404 {
		t.Fatalf("get deleted: %d", code)
	}
	if code, _ := api(t, e, tok, "PATCH", "/api/v1/collections/"+c.ID, map[string]string{"title": "x"}); code != 404 {
		t.Fatalf("patch deleted: %d", code)
	}
	if code, _ := api(t, e, tok, "DELETE", "/api/v1/collections/"+c.ID, nil); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
	// GC purges the collection row once its items are gone.
	e.Advance(25 * time.Hour)
	e.GC()
	var n int
	e.Instance.Store.DB().QueryRow(`SELECT COUNT(*) FROM collections`).Scan(&n)
	if n != 0 {
		t.Fatalf("collections left after GC: %d", n)
	}

	// Errors: unknown collection, burn + collection.
	ct, body := multipartBody(t, map[string]string{"collection": "zzzzzzzz"}, [2]string{"x", "x"})
	if resp, _ := postMultipart(t, e, tok, ct, body); resp.StatusCode != 400 {
		t.Fatalf("unknown collection: %d", resp.StatusCode)
	}
	if resp, _ := upload(t, e, tok, "?collection=new&burn_after_read=1", []byte("x")); resp.StatusCode != 400 {
		t.Fatalf("burn in collection: %d", resp.StatusCode)
	}
	if resp, _ := upload(t, e, tok, "?collection=bad!", []byte("x")); resp.StatusCode != 400 {
		t.Fatalf("bad collection id: %d", resp.StatusCode)
	}
}

func TestImageAlbumGrid(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	c, items := albumUpload(t, e, tok, map[string]string{"collection": "new"},
		[2]string{"1.png", string(testimg.PNG(500, 300))}, [2]string{"2.png", string(testimg.PNG(40, 40))})
	_, page := get(t, c.URL)
	s := string(page)
	if !strings.Contains(s, `class="grid"`) || !strings.Contains(s, `/`+items[0].ID+`/thumb"`) {
		t.Fatal("image album not shown as a thumbnail grid")
	}
	if !strings.Contains(s, "2 items") {
		t.Fatal("untitled album lacks item count title")
	}
}

func TestCollectionsAPI(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	reader := e.Token("r", "read_private")
	a := up(t, e, tok, "", []byte("first"))
	b := up(t, e, tok, "", []byte("second"))
	code, body := api(t, e, tok, "POST", "/api/v1/collections", map[string]any{"title": "Picked", "items": []string{b.ID, a.ID}, "visibility": "private"})
	var c collJSON
	json.Unmarshal(body, &c)
	if code != 201 || c.ItemCount != 2 || c.Items[0].ID != b.ID || c.Visibility != "private" {
		t.Fatalf("create: %d %s", code, body)
	}
	// Private collection page: 404 without a read_private token.
	if status(t, c.URL, "") != 404 || status(t, c.URL, reader) != 200 {
		t.Fatal("private collection access")
	}
	// Items can belong to only one collection.
	if code, _ := api(t, e, tok, "POST", "/api/v1/collections", map[string]any{"items": []string{a.ID}}); code != 400 {
		t.Fatalf("item in two collections: %d", code)
	}
	for _, bad := range []any{map[string]any{"items": []string{"zzzzzzzz"}}, map[string]any{"visibility": "x"}, map[string]any{"expires": "x"}, map[string]any{"nope": 1}} {
		if code, _ := api(t, e, tok, "POST", "/api/v1/collections", bad); code != 400 {
			t.Errorf("create %v: %d", bad, code)
		}
	}
	// An empty collection can be created and listed.
	if code, _ := api(t, e, tok, "POST", "/api/v1/collections", nil); code != 201 {
		t.Fatalf("empty create: %d", code)
	}
	code, body = api(t, e, tok, "GET", "/api/v1/collections?limit=1", nil)
	var page struct {
		Collections []collJSON `json:"collections"`
		NextCursor  *string    `json:"next_cursor"`
	}
	json.Unmarshal(body, &page)
	if code != 200 || len(page.Collections) != 1 || page.NextCursor == nil {
		t.Fatalf("list: %d %s", code, body)
	}
	_, body = api(t, e, tok, "GET", "/api/v1/collections?cursor="+*page.NextCursor, nil)
	if !strings.Contains(string(body), c.ID) {
		t.Fatal("second page lacks the first collection")
	}
	for _, bad := range []string{"?limit=0", "?cursor=***"} {
		if code, _ := api(t, e, tok, "GET", "/api/v1/collections"+bad, nil); code != 400 {
			t.Errorf("list %s: %d", bad, code)
		}
	}
}

func TestFork(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.MaxUploadBytes = 1000 })
	tok := e.Token("t")
	orig := up(t, e, tok, "?filename=hello.py&title=Greeting&visibility=private", []byte("print('hello')\n"))
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items/"+orig.ID+"/fork", strings.NewReader("print('hello, world')\n"))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var f itemColl
	json.Unmarshal(b, &f)
	if resp.StatusCode != 201 || f.ForkedFrom != orig.ID || f.ID == orig.ID {
		t.Fatalf("fork: %d %s", resp.StatusCode, b)
	}
	// Defaults come from the original.
	if f.Title != "Greeting" || f.Lang != "python" || f.Filename != "hello.py" || f.Visibility != "private" {
		t.Fatalf("fork defaults: %+v", f)
	}
	// The original is unchanged.
	reader := e.Token("r", "read_private")
	if _, raw := getAs(t, orig.RawURL, reader); string(raw) != "print('hello')\n" {
		t.Fatalf("original changed: %q", raw)
	}
	if _, raw := getAs(t, e.Srv.URL+"/"+f.ID+"/raw", reader); string(raw) != "print('hello, world')\n" {
		t.Fatalf("fork content: %q", raw)
	}
	// The fork's page links to the original; text views carry the (hidden)
	// fork button for browsers that hold a token.
	_, page := getAs(t, e.Srv.URL+"/"+f.ID, reader)
	if !strings.Contains(string(page), `Forked from <a href="/`+orig.ID+`">`) || !strings.Contains(string(page), `id="fork-button" hidden`) {
		t.Fatal("fork page lacks backlink or fork button")
	}
	// Overrides, errors.
	fork := func(id, q string, body []byte) (int, []byte) {
		req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items/"+id+"/fork"+q, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if code, b := fork(orig.ID, "?title=New+title&visibility=unlisted", []byte("x = 1\n")); code != 201 || !strings.Contains(string(b), `"title":"New title"`) || !strings.Contains(string(b), `"visibility":"unlisted"`) {
		t.Fatalf("override: %d %s", code, b)
	}
	if code, _ := fork("zzzzzzzz", "", []byte("x")); code != 404 {
		t.Fatalf("fork missing: %d", code)
	}
	if code, _ := fork(orig.ID, "", nil); code != 400 {
		t.Fatalf("fork empty: %d", code)
	}
	if code, _ := fork(orig.ID, "", bytes.Repeat([]byte("x"), 2000)); code != 413 {
		t.Fatalf("fork too large: %d", code)
	}
	if code, _ := fork(orig.ID, "", []byte{0, 1, 2}); code != 400 {
		t.Fatalf("fork binary into a text item: %d", code)
	}
}
