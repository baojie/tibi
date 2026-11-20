package server_test

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/baojie/tibi/internal/ingest"
	"github.com/baojie/tibi/internal/testutil"
	"github.com/baojie/tibi/internal/testutil/testimg"
)

type searchResult struct {
	Items []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Snippet string `json:"snippet"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func search(t *testing.T, e *testutil.Env, tok, q, extra string) []string {
	t.Helper()
	code, b := api(t, e, tok, "GET", "/api/v1/search?q="+url.QueryEscape(q)+extra, nil)
	if code != 200 {
		t.Fatalf("search %q: %d %s", q, code, b)
	}
	var r searchResult
	json.Unmarshal(b, &r)
	ids := []string{}
	for _, it := range r.Items {
		ids = append(ids, it.ID)
	}
	sort.Strings(ids)
	return ids
}

func ids(items ...fullItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestSearch(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete")
	poem := up(t, e, tok, "?filename=poem.txt", []byte("Writing poems on public walls was common.\n题壁诗是唐宋时期的一种传统。\n"))
	code := up(t, e, tok, "?filename=server.go", []byte("package server\n\nfunc (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {\n\tingest.Prepare(ctx)\n}\n"))
	notes := up(t, e, tok, "?title=Meeting+notes", []byte("Agenda: budget, hiring, the WALL project.\n"))
	jp := up(t, e, tok, "", []byte("東京は日本の首都です。\n"))
	img := up(t, e, tok, "?filename=walls-of-xian.png", testimg.PNG(4, 4))

	cases := []struct {
		q    string
		want []string
	}{
		{"poems", ids(poem)},                     // English word
		{"POEMS", ids(poem)},                     // case-insensitive
		{"handleUpload", ids(code)},              // code identifier
		{"Upload", ids(code)},                    // identifier substring
		{"ingest.Prepare", ids(code)},            // dotted identifier
		{"题壁", ids(poem)},                        // 2-character Chinese substring
		{"唐宋时期", ids(poem)},                      // longer Chinese
		{"诗", ids(poem)},                         // single character
		{"首都", ids(jp)},                          // Japanese kanji
		{"wall", ids(poem, notes, img)},          // substring in content, title-less text, filename
		{"meeting", ids(notes)},                  // title
		{"walls poems", ids(poem)},               // all words must match
		{"poems 唐宋", ids(poem)},                  // mixed scripts
		{"nothing-matches-this-xyz", []string{}}, // no hits
	}
	for _, c := range cases {
		if got := search(t, e, tok, c.q, ""); !eq(got, c.want) {
			t.Errorf("search %q = %v, want %v", c.q, got, c.want)
		}
	}
	// kind filter
	if got := search(t, e, tok, "wall", "&kind=image"); !eq(got, ids(img)) {
		t.Errorf("kind filter: %v", got)
	}
	// Snippet: the matching line.
	_, b := api(t, e, tok, "GET", "/api/v1/search?q=handleUpload", nil)
	var r searchResult
	json.Unmarshal(b, &r)
	if len(r.Items) != 1 || !strings.HasPrefix(r.Items[0].Snippet, "func (s *Server) handleUpload(") {
		t.Errorf("snippet = %+v", r.Items)
	}

	// Deleted and expired items never appear.
	api(t, e, tok, "DELETE", "/api/v1/items/"+notes.ID, nil)
	if got := search(t, e, tok, "meeting", ""); len(got) != 0 {
		t.Errorf("deleted item found: %v", got)
	}
	exp := up(t, e, tok, "?expires=1h", []byte("ephemeral zebra note"))
	if got := search(t, e, tok, "zebra", ""); !eq(got, ids(exp)) {
		t.Fatalf("before expiry: %v", got)
	}
	e.Advance(2 * time.Hour)
	if got := search(t, e, tok, "zebra", ""); len(got) != 0 {
		t.Errorf("expired item found: %v", got)
	}
	e.GC() // purged rows leave the index too
	var n int
	e.Instance.Store.DB().QueryRow(`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'zebra'`).Scan(&n)
	if n != 0 {
		t.Errorf("purged item still indexed")
	}

	// Renaming re-indexes the title but keeps the content searchable.
	api(t, e, tok, "PATCH", "/api/v1/items/"+code.ID, map[string]string{"title": "Upload handler 上传"})
	if got := search(t, e, tok, "上传", ""); !eq(got, ids(code)) {
		t.Errorf("new title not indexed: %v", got)
	}
	if got := search(t, e, tok, "ResponseWriter", ""); !eq(got, ids(code)) {
		t.Errorf("body lost after title change: %v", got)
	}

	// Private items are searchable by their owner (the API needs a token).
	priv := up(t, e, tok, "?visibility=private", []byte("private walrus"))
	if got := search(t, e, tok, "walrus", ""); !eq(got, ids(priv)) {
		t.Errorf("private: %v", got)
	}
	if code, _ := api(t, e, "", "GET", "/api/v1/search?q=walrus", nil); code != 401 {
		t.Errorf("unauthenticated search: %d", code)
	}
	// Bad queries.
	for _, q := range []string{"", "   ", "!!!", strings.Repeat("a ", 20)} {
		if code, _ := api(t, e, tok, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil); code != 400 {
			t.Errorf("query %q: %d", q, code)
		}
	}
	// FTS syntax in user input is treated literally, never as operators.
	for _, q := range []string{`"`, `a OR b`, `NEAR(x y)`, `col:value`, `*`, `x"y`} {
		if code, b := api(t, e, tok, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil); code != 200 && code != 400 {
			t.Errorf("query %q: %d %s", q, code, b)
		}
	}
}

func TestSearchPagination(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	for i := 0; i < 23; i++ {
		up(t, e, tok, "", []byte("common needle "+strings.Repeat("x", i)))
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; ; page++ {
		path := "/api/v1/search?q=needle&limit=10"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		_, b := api(t, e, tok, "GET", path, nil)
		var r searchResult
		json.Unmarshal(b, &r)
		for _, it := range r.Items {
			if seen[it.ID] {
				t.Fatal("duplicate across pages")
			}
			seen[it.ID] = true
		}
		if r.NextCursor == nil {
			break
		}
		cursor = *r.NextCursor
	}
	if len(seen) != 23 {
		t.Fatalf("paged through %d results", len(seen))
	}
}

func TestSearchBackfill(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := up(t, e, tok, "?filename=legacy.txt", []byte("uploaded before search existed: aardvark"))
	// Simulate a pre-0.0.7 database: no index rows.
	e.Instance.Store.DB().Exec(`DELETE FROM items_fts`)
	e.Instance.Store.DB().Exec(`DELETE FROM items_tri`)
	if got := search(t, e, tok, "aardvark", ""); len(got) != 0 {
		t.Fatal("index not cleared")
	}
	n, err := ingest.Backfill(context.Background(), e.Instance.Store, e.Instance.Blobs)
	if err != nil || n != 1 {
		t.Fatalf("backfill: %d %v", n, err)
	}
	if got := search(t, e, tok, "aardvark", ""); !eq(got, ids(it)) {
		t.Fatalf("after backfill: %v", got)
	}
	if n, _ := ingest.Backfill(context.Background(), e.Instance.Store, e.Instance.Blobs); n != 0 {
		t.Fatalf("second backfill re-indexed %d", n)
	}
}

// Characters whose lower case is longer in UTF-8 (Ⱥ → ⱥ) once crashed every
// search matching the paste (security review, 0.1.0).
func TestSearchSnippetCaseLengthChange(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := up(t, e, tok, "", []byte(strings.Repeat("Ⱥ", 40)+"zqymarker tail"))
	for _, q := range []string{"zqymarker", "ZQYMARKER", "ⱥⱥ"} {
		code, b := api(t, e, tok, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil)
		if code != 200 || !strings.Contains(string(b), it.ID) {
			t.Fatalf("%q: %d %s", q, code, b)
		}
	}
}

// Long words cost many trigram lookups; they are refused (security review).
func TestSearchTermLength(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	up(t, e, tok, "", []byte(strings.Repeat("a", 64)))
	code, b := api(t, e, tok, "GET", "/api/v1/search?q="+strings.Repeat("a", 65), nil)
	if code != 400 || !strings.Contains(string(b), "limited to 64 characters") {
		t.Fatalf("65-character word: %d %s", code, b)
	}
	if code, b := api(t, e, tok, "GET", "/api/v1/search?q="+strings.Repeat("a", 64), nil); code != 200 {
		t.Fatalf("64-character word: %d %s", code, b)
	}
}
