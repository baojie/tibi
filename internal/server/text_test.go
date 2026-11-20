package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/baojie/tibi/internal/config"
	"github.com/baojie/tibi/internal/testutil"
)

type textItem struct {
	item
	Lang    string `json:"lang"`
	Charset string `json:"charset"`
}

func mustUploadText(t *testing.T, e *testutil.Env, tok, query string, body []byte) textItem {
	t.Helper()
	resp, b := upload(t, e, tok, query, body)
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	var it textItem
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func TestPasteHighlighted(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := mustUploadText(t, e, tok, "?kind=text&lang=python", []byte("print(1)\n"))
	if it.Kind != "text" || it.Lang != "python" || !strings.HasSuffix(it.RawURL, ".py") {
		t.Fatalf("%+v", it)
	}
	resp, page := get(t, it.URL)
	s := string(page)
	if !strings.Contains(s, `<span class="nb">print</span>`) {
		t.Fatalf("no highlighted python on view page")
	}
	for _, want := range []string{`id="L1"`, `href="#L1"`, "/static/text.js", "/static/chroma.css", "Python", "wrap-toggle", "data-copy-url"} {
		if !strings.Contains(s, want) {
			t.Errorf("view page lacks %q", want)
		}
	}
	if strings.Contains(s, "<script>") || strings.Contains(s, "style=") {
		t.Error("inline script or style attribute on view page (CSP)")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	resp, css := get(t, e.Srv.URL+"/static/chroma.css")
	if resp.StatusCode != 200 || !strings.Contains(string(css), ".chroma .nb") || !strings.Contains(string(css), "prefers-color-scheme: dark") {
		t.Fatalf("chroma.css: %d", resp.StatusCode)
	}
	// Raw view stays exact.
	if _, b := get(t, e.Srv.URL+"/"+it.ID+"/raw"); string(b) != "print(1)\n" {
		t.Fatalf("raw = %q", b)
	}
}

func TestLangInferenceAndErrors(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	gosrc := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if it := mustUploadText(t, e, tok, "", []byte(gosrc)); it.Lang != "go" || !strings.HasSuffix(it.RawURL, ".go") {
		t.Fatalf("piped Go: %+v", it)
	}
	if it := mustUploadText(t, e, tok, "?filename=build.rs", []byte("x")); it.Lang != "rust" || !strings.HasSuffix(it.RawURL, ".rs") {
		t.Fatalf("by extension: %+v", it)
	}
	if it := mustUploadText(t, e, tok, "?filename=main.go&lang=text", []byte(gosrc)); it.Lang != "text" {
		t.Fatalf("explicit lang should win: %+v", it)
	}
	if it := mustUploadText(t, e, tok, "", []byte("just some words\n")); it.Lang != "text" || !strings.HasSuffix(it.RawURL, ".txt") {
		t.Fatalf("plain: %+v", it)
	}
	if resp, b := upload(t, e, tok, "?lang=nosuchlang", []byte("x")); resp.StatusCode != 400 || !strings.Contains(string(b), "unknown lang") {
		t.Fatalf("unknown lang: %d %s", resp.StatusCode, b)
	}
	// Images ignore lang.
	if it := mustUploadText(t, e, tok, "?lang=python", []byte("\x00\x01binary")); it.Kind != "file" || it.Lang != "" {
		t.Fatalf("binary with lang: %+v", it)
	}
}

func TestGBKTranscoded(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	zh := "题壁——唐宋时人在公共墙壁上写诗，任何路过的人都可以读。\n这是一个自托管的图床和粘贴板。\n"
	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(zh))
	it := mustUploadText(t, e, tok, "?filename=notes.txt", gbk)
	if it.Kind != "text" || it.Charset != "GBK" || it.Mime != "text/plain" {
		t.Fatalf("%+v", it)
	}
	if it.Size != int64(len(zh)) {
		t.Fatalf("size %d, want UTF-8 size %d", it.Size, len(zh))
	}
	resp, raw := get(t, it.RawURL)
	if string(raw) != zh || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("raw: %q %s", raw, resp.Header.Get("Content-Type"))
	}
	if _, page := get(t, it.URL); !strings.Contains(string(page), "唐宋时人在公共墙壁上写诗") || !strings.Contains(string(page), "converted from GBK") {
		t.Fatal("view page lacks Chinese text or charset note")
	}
	// The DB records the charset too.
	var cs string
	e.Instance.Store.DB().QueryRow(`SELECT charset FROM items WHERE id = ?`, it.ID).Scan(&cs)
	if cs != "GBK" {
		t.Fatalf("db charset = %q", cs)
	}
	// Forced text works for GBK; forced text on binary is rejected.
	if it := mustUploadText(t, e, tok, "?kind=text", gbk); it.Charset != "GBK" {
		t.Fatalf("forced: %+v", it)
	}
}

func TestLargeTextTruncated(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.MaxUploadBytes = 8 << 20 })
	tok := e.Token("t")
	var b bytes.Buffer
	for i := 0; b.Len() < 5<<20; i++ {
		fmt.Fprintf(&b, "2026-11-20 05:40:%02d INFO request %d handled in 12ms path=/api/v1/items\n", i%60, i)
	}
	log := b.Bytes()
	it := mustUploadText(t, e, tok, "?filename=server.log", log)
	if it.Kind != "text" || it.Lang != "text" {
		t.Fatalf("%+v", it)
	}
	_, page := get(t, it.URL)
	s := string(page)
	if !strings.Contains(s, "showing the first") || !strings.Contains(s, `href="`+e.Cfg.BaseURL+"/"+it.ID+`/raw"`) {
		t.Fatal("no truncation notice with raw link")
	}
	if len(page) > 20<<20 {
		t.Fatalf("view page too large: %d bytes", len(page))
	}
	if strings.Contains(s, fmt.Sprintf("request %d handled", bytes.Count(log, []byte("\n"))-1)) {
		t.Fatal("last line rendered in truncated view")
	}
	if _, raw := get(t, e.Srv.URL+"/"+it.ID+"/raw"); !bytes.Equal(raw, log) {
		t.Fatal("raw link does not return the full text")
	}
}

func TestMarkdownView(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	src := "# Hello\n\nSome **bold** text.\n\n<script>alert(1)</script>\n\n```go\nfunc f() {}\n```\n"
	it := mustUploadText(t, e, tok, "?filename=README.md", []byte(src))
	if it.Lang != "markdown" {
		t.Fatalf("%+v", it)
	}
	_, page := get(t, it.URL)
	s := string(page)
	for _, want := range []string{`<h1 id=`, "<strong>bold</strong>", `id="view-rendered"`, `id="view-source" hidden`, `data-view="source"`, `id="L1"`} {
		if !strings.Contains(s, want) && !(want == `<h1 id=` && strings.Contains(s, "<h1>")) {
			t.Errorf("markdown page lacks %q", want)
		}
	}
	if strings.Contains(s, "<script>alert") {
		t.Fatal("raw HTML passed through")
	}
}
