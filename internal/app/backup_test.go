package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tibi/internal/app"
	"tibi/internal/backup"
	"tibi/internal/config"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

func call(t *testing.T, method, url, tok string, body io.Reader, ct string) []byte {
	t.Helper()
	req, _ := http.NewRequest(method, url, body)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != 302 {
		t.Fatalf("%s %s: %d %s", method, url, resp.StatusCode, b)
	}
	return b
}

type surface struct {
	paths []string          // relative URLs to compare byte for byte
	pages map[string]string // relative page URL → text that must appear
}

// populate fills an instance with every kind of thing that has a URL: an
// image, code, a private file, a video, a binary, a fork and a two-image
// collection (8 live items), plus one deleted item that must not travel.
func populate(t *testing.T, e *testutil.Env, tok string) surface {
	base := e.Srv.URL
	var it struct {
		ID           string `json:"id"`
		RawURL       string `json:"raw_url"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	add := func(query string, body []byte) (string, string, string) {
		b := call(t, "POST", base+"/api/v1/items"+query, tok, bytes.NewReader(body), "")
		json.Unmarshal(b, &it)
		return it.ID, strings.TrimPrefix(it.RawURL, e.Cfg.BaseURL), strings.TrimPrefix(it.ThumbnailURL, e.Cfg.BaseURL)
	}
	s := surface{pages: map[string]string{}}
	img, imgRaw, imgThumb := add("?filename=photo.jpg&title=Photo", testimg.JPEGWithEXIF(120, 80, 6))
	s.paths = append(s.paths, imgRaw, "/"+img+"/dl", imgThumb)
	s.pages["/"+img] = "Photo"
	txt, txtRaw, _ := add("?filename=main.go&slug=main-go", []byte("package main\n\nfunc main() { println(\"题壁\") }\n"))
	s.paths = append(s.paths, txtRaw, "/"+txt+"/raw")
	s.pages["/"+txt] = `<span class="kd">func</span>`
	priv, _, _ := add("?visibility=private&filename=secret.txt", []byte("private words"))
	vid, vidRaw, _ := add("?filename=clip.mp4", testimg.MP4("isom", 5000, 3000, false))
	s.paths = append(s.paths, vidRaw)
	s.pages["/"+vid] = "<video"
	_, binRaw, _ := add("?filename=data.bin", bytes.Repeat([]byte{1, 2, 3, 250}, 5000))
	s.paths = append(s.paths, binRaw)
	gone, _, _ := add("", []byte("deleted before export"))
	call(t, "DELETE", base+"/api/v1/items/"+gone, tok, nil, "")
	// A fork and a collection.
	call(t, "POST", base+"/api/v1/items/"+txt+"/fork", tok, strings.NewReader("package main // v2\n"), "")
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	mw.WriteField("collection", "new")
	mw.WriteField("collection_title", "Trip")
	for _, f := range []string{"a.png", "b.png"} {
		w, _ := mw.CreateFormFile("file", f)
		w.Write(testimg.PNG(40, 30))
	}
	mw.Close()
	var coll struct {
		Collection struct{ ID string } `json:"collection"`
	}
	json.Unmarshal(call(t, "POST", base+"/api/v1/items", tok, &mp, mw.FormDataContentType()), &coll)
	s.pages["/c/"+coll.Collection.ID] = "Trip"
	// A share link for the private item.
	var link struct{ URL string }
	json.Unmarshal(call(t, "POST", base+"/api/v1/items/"+priv+"/share-link?ttl=7d", tok, nil, ""), &link)
	s.pages[strings.TrimPrefix(link.URL, e.Cfg.BaseURL)] = "secret.txt"
	s.pages["/s/main-go"] = "main.go" // slug (followed redirect)
	return s
}

func roundTrip(t *testing.T, srcBackend, dstBackend string) {
	setBackend := func(backend string) func(*config.Server) {
		return func(c *config.Server) {
			if backend == "s3" {
				testutil.UseS3(t, c) // skips without S3
			} else {
				c.Storage.Backend = "fs"
			}
		}
	}
	a := testutil.New(t, setBackend(srcBackend))
	tok := a.Token("owner", "upload", "delete", "read_private")
	s := populate(t, a, tok)
	want := map[string][]byte{}
	for _, p := range s.paths {
		want[p] = call(t, "GET", a.Srv.URL+p, "", nil, "")
	}

	archive := filepath.Join(t.TempDir(), "export.tar.zst")
	f, _ := os.Create(archive)
	m, err := backup.Export(context.Background(), a.Instance.Store, a.Instance.Blobs, a.Cfg.SecretKeyPath(), "test", f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if m.Items != 8 || m.Collections != 1 || m.Tokens != 1 {
		t.Fatalf("manifest: %+v", m)
	}

	// Import into an empty instance B, then start B's server on it.
	var bDir string
	b0 := testutil.New(t, func(c *config.Server) { setBackend(dstBackend)(c); bDir = c.DataDir })
	in, _ := os.Open(archive)
	res, err := backup.Import(context.Background(), b0.Instance.Store, b0.Instance.Blobs, b0.Cfg.SecretKeyPath(), b0.Instance.Tmp, in)
	in.Close()
	if err != nil || res.Items != 8 {
		t.Fatalf("import: %+v %v", res, err)
	}
	b := testutil.New(t, func(c *config.Server) { *c = b0.Cfg; c.DataDir = bDir }) // restart: loads the imported key

	for p, w := range want {
		if got := call(t, "GET", b.Srv.URL+p, "", nil, ""); !bytes.Equal(got, w) {
			t.Errorf("%s differs after import", p)
		}
	}
	for p, text := range s.pages {
		if got := call(t, "GET", b.Srv.URL+p, "", nil, ""); !strings.Contains(string(got), text) {
			t.Errorf("%s: page lacks %q", p, text)
		}
	}
	// Tokens keep working, search works (index rebuilt), deleted items stay gone.
	if got := call(t, "GET", b.Srv.URL+"/api/v1/search?q=%E9%A2%98%E5%A3%81", tok, nil, ""); !strings.Contains(string(got), "main.go") {
		t.Errorf("search after import: %s", got)
	}
	var page struct{ Items []json.RawMessage }
	json.Unmarshal(call(t, "GET", b.Srv.URL+"/api/v1/items?limit=100", tok, nil, ""), &page)
	if len(page.Items) != 8 {
		t.Errorf("items after import: %d", len(page.Items))
	}

	// Importing twice is refused.
	in, _ = os.Open(archive)
	defer in.Close()
	if _, err := backup.Import(context.Background(), b.Instance.Store, b.Instance.Blobs, "", b.Instance.Tmp, in); err != backup.ErrNotEmpty {
		t.Fatalf("second import: %v", err)
	}
}

func TestExportImportFS(t *testing.T)   { roundTrip(t, "fs", "fs") }
func TestExportImportToS3(t *testing.T) { roundTrip(t, "fs", "s3") }
func TestExportImportS3(t *testing.T)   { roundTrip(t, "s3", "s3") }

func TestAdminCommands(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.toml")
	os.WriteFile(cfgPath, []byte("data_dir = \""+filepath.Join(dir, "data")+"\"\nbase_url = \"http://example.test\"\n"), 0o600)
	var out, errb bytes.Buffer
	if err := app.Admin([]string{"token", "create", "--name", "x", "--config", cfgPath}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := app.Admin([]string{"stats", "--config", cfgPath}, &out, &errb); err != nil || !strings.Contains(out.String(), "storage used:") {
		t.Fatalf("stats: %v %s", err, out.String())
	}
	archive := filepath.Join(dir, "x.tar.zst")
	out.Reset()
	if err := app.Admin([]string{"export", "--out", archive, "--config", cfgPath}, &out, &errb); err != nil || !strings.Contains(out.String(), "exported 0 items") {
		t.Fatalf("export: %v %s", err, out.String())
	}
	if err := app.Admin([]string{"export", "--out", archive, "--config", cfgPath}, &out, &errb); err == nil {
		t.Fatal("export overwrote an existing file")
	}
	cfg2 := filepath.Join(dir, "b.toml")
	os.WriteFile(cfg2, []byte("data_dir = \""+filepath.Join(dir, "b")+"\"\nbase_url = \"http://example.test\"\n"), 0o600)
	out.Reset()
	if err := app.Admin([]string{"import", archive, "--config", cfg2}, &out, &errb); err != nil || !strings.Contains(out.String(), "imported 0 items") {
		t.Fatalf("import: %v %s", err, out.String())
	}
}
