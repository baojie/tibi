package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"tibi/internal/config"
	"tibi/internal/ingest"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

type imgItem struct {
	item
	Width    *int   `json:"width"`
	Height   *int   `json:"height"`
	Markdown string `json:"markdown"`
}

func mustUploadImg(t *testing.T, e *testutil.Env, tok, query string, body []byte) imgItem {
	t.Helper()
	resp, b := upload(t, e, tok, query, body)
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	var it imgItem
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func TestPNGRoundTrip(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	src := testimg.PNG(64, 32)
	it := mustUploadImg(t, e, tok, "?filename=shot.jpg", src) // misleading name
	if it.Kind != "image" || it.Mime != "image/png" {
		t.Fatalf("kind/mime: %+v", it)
	}
	if !strings.HasSuffix(it.RawURL, "/"+it.ID+".png") {
		t.Fatalf("raw_url = %s", it.RawURL)
	}
	if it.Width == nil || *it.Width != 64 || *it.Height != 32 {
		t.Fatalf("dims: %v %v", it.Width, it.Height)
	}
	if it.Markdown != "![]("+it.RawURL+")" {
		t.Fatalf("markdown = %q", it.Markdown)
	}
	resp, b := get(t, it.RawURL)
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !bytes.Equal(b, src) {
		t.Fatal("PNG bytes changed")
	}
	_, page := get(t, it.URL)
	if !strings.Contains(string(page), `<img src="`+it.RawURL+`"`) || !strings.Contains(string(page), "/static/copy.js") {
		t.Fatal("image view page lacks <img> or copy script")
	}
	if strings.Contains(string(page), "<script>") {
		t.Fatal("inline script on view page")
	}
}

func TestJPEGMetadataStripped(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	src := testimg.JPEGWithEXIF(40, 20, 6)

	it := mustUploadImg(t, e, tok, "?filename=photo.jpg", src)
	_, got := get(t, it.DownloadURL)
	if bytes.Contains(got, []byte(testimg.GPSMarker)) || bytes.Contains(got, []byte{0x88, 0x25}) {
		t.Fatal("GPS/EXIF present on download")
	}
	if ingest.JPEGOrientation(got) != 6 {
		t.Fatal("orientation lost")
	}
	if it.Size != int64(len(got)) {
		t.Fatalf("size %d != stored %d", it.Size, len(got))
	}
	if *it.Width != 20 || *it.Height != 40 {
		t.Fatalf("dims should be display-oriented: %dx%d", *it.Width, *it.Height)
	}

	// keep_meta=1 preserves the original bytes (query and header forms).
	kept := mustUploadImg(t, e, tok, "?keep_meta=1", src)
	if _, b := get(t, kept.DownloadURL); !bytes.Equal(b, src) {
		t.Fatal("keep_meta=1 did not preserve bytes")
	}
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items", bytes.NewReader(src))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Tibi-Keep-Meta", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var viaHeader imgItem
	json.NewDecoder(resp.Body).Decode(&viaHeader)
	resp.Body.Close()
	if _, b := get(t, viaHeader.DownloadURL); !bytes.Equal(b, src) {
		t.Fatal("X-Tibi-Keep-Meta did not preserve bytes")
	}
	// Two different blobs: stripped and original.
	n := e.BlobCount()
	if n != 2 {
		t.Fatalf("blobs = %d, want 2", n)
	}
}

func TestStripDisabledByConfig(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.StripImageMeta = false })
	src := testimg.JPEGWithEXIF(8, 8, 1)
	it := mustUploadImg(t, e, e.Token("t"), "", src)
	if _, b := get(t, it.DownloadURL); !bytes.Equal(b, src) {
		t.Fatal("strip_image_meta=false still stripped")
	}
}

func TestWebPStrippedSameImageDedup(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	a := mustUploadImg(t, e, tok, "", testimg.WebPWithMeta())
	b := mustUploadImg(t, e, tok, "", testimg.WebPWithMeta())
	if a.ID == b.ID || a.Kind != "image" || !strings.HasSuffix(a.RawURL, ".webp") {
		t.Fatalf("%+v %+v", a, b)
	}
	if _, body := get(t, a.RawURL); bytes.Contains(body, []byte(testimg.GPSMarker)) {
		t.Fatal("WebP metadata served")
	}
	n := e.BlobCount()
	if n != 1 {
		t.Fatalf("blobs = %d, want 1", n)
	}
}

func TestSVGIsFile(t *testing.T) {
	e := testutil.New(t, nil)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><rect width="10" height="10"/></svg>`)
	for _, name := range []string{"logo.svg", "logo.png", ""} {
		it := mustUploadImg(t, e, e.Token("t-"+name), "?filename="+name, svg)
		if it.Kind != "file" || it.Mime != "image/svg+xml" {
			t.Fatalf("%q: %s %s", name, it.Kind, it.Mime)
		}
		for _, u := range []string{it.RawURL, it.DownloadURL, e.Srv.URL + "/" + it.ID + "/raw"} {
			resp, _ := get(t, u)
			if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "svg") || strings.Contains(ct, "xml") {
				t.Fatalf("%s served as %q", u, ct)
			}
		}
		if _, page := get(t, it.URL); strings.Contains(string(page), "<img") {
			t.Fatal("SVG rendered as <img>")
		}
	}
}

func TestKindClassificationAndForce(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	if it := mustUploadImg(t, e, tok, "", []byte("plain text\n")); it.Kind != "text" || !strings.HasSuffix(it.RawURL, ".txt") {
		t.Fatalf("text: %+v", it)
	}
	if it := mustUploadImg(t, e, tok, "?kind=file", []byte("plain text\n")); it.Kind != "file" {
		t.Fatalf("forced file: %+v", it)
	}
	if it := mustUploadImg(t, e, tok, "?kind=text&filename=a.html", []byte("<html><b>x</b></html>")); it.Kind != "text" {
		t.Fatalf("forced text: %+v", it)
	} else if resp, _ := get(t, it.RawURL); !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatal("forced-text HTML not served as text/plain")
	}
	if resp, b := upload(t, e, tok, "?kind=image", []byte("x")); resp.StatusCode != 400 {
		t.Fatalf("kind=image: %d %s", resp.StatusCode, b)
	}
	if resp, _ := upload(t, e, tok, "?kind=text", []byte{0xff, 0xfe, 0}); resp.StatusCode != 400 {
		t.Fatalf("kind=text on binary: %d", resp.StatusCode)
	}
	// Corrupt "PNG": signature only → not rendered as image.
	if it := mustUploadImg(t, e, tok, "", []byte("\x89PNG\r\n\x1a\nGARBAGE")); it.Kind != "file" {
		t.Fatalf("corrupt png kind %s", it.Kind)
	}
	// Markdown for non-images is a link.
	it := mustUploadImg(t, e, tok, "?filename=notes%5B1%5D.txt", []byte("x"))
	if it.Markdown != `[notes\[1\].txt](`+it.URL+`)` {
		t.Fatalf("markdown = %q", it.Markdown)
	}
}

func multipartBody(t *testing.T, fields map[string]string, files ...[2]string) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	for _, f := range files {
		w, _ := mw.CreateFormFile("file", f[0])
		io.WriteString(w, f[1])
	}
	mw.Close()
	return mw.FormDataContentType(), &buf
}

func postMultipart(t *testing.T, e *testutil.Env, tok, ct string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestMultipart(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.MaxUploadBytes = 4096 })
	tok := e.Token("t")

	// Multiple files → array, in order, each with its own filename.
	ct, body := multipartBody(t, map[string]string{"title": "batch"},
		[2]string{"a.png", string(testimg.PNG(8, 8))},
		[2]string{"b.txt", "hello"},
		[2]string{"c.bin", "\x00\x01"})
	resp, b := postMultipart(t, e, tok, ct, body)
	if resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	var items []imgItem
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatalf("not an array: %s", b)
	}
	if len(items) != 3 || items[0].Kind != "image" || items[1].Kind != "text" || items[2].Kind != "file" {
		t.Fatalf("items: %+v", items)
	}
	if _, got := get(t, items[1].DownloadURL); string(got) != "hello" {
		t.Fatal("b.txt content")
	}
	resp, _ = get(t, items[2].DownloadURL)
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "c.bin") {
		t.Fatal("part filename not used")
	}

	// Single file → object. Fields after the file still apply.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("file", "x.txt")
	io.WriteString(w, "x")
	mw.WriteField("kind", "file")
	mw.Close()
	resp, b = postMultipart(t, e, tok, mw.FormDataContentType(), &buf)
	var one imgItem
	if resp.StatusCode != 201 || json.Unmarshal(b, &one) != nil || one.Kind != "file" {
		t.Fatalf("single: %d %s", resp.StatusCode, b)
	}

	// Total over the limit → 413, nothing stored.
	before := countItems(t, e)
	ct, body = multipartBody(t, nil, [2]string{"a", strings.Repeat("a", 3000)}, [2]string{"b", strings.Repeat("b", 3000)})
	if resp, _ := postMultipart(t, e, tok, ct, body); resp.StatusCode != 413 {
		t.Fatalf("over limit: %d", resp.StatusCode)
	}
	if countItems(t, e) != before {
		t.Fatal("partial upload stored")
	}
	// No file part → 400.
	ct, body = multipartBody(t, map[string]string{"title": "x"})
	if resp, _ := postMultipart(t, e, tok, ct, body); resp.StatusCode != 400 {
		t.Fatalf("no file: %d", resp.StatusCode)
	}
}

func countItems(t *testing.T, e *testutil.Env) int {
	var n int
	e.Instance.Store.DB().QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n)
	return n
}
