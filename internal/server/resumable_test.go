package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baojie/tibi/internal/client"
	"github.com/baojie/tibi/internal/config"
	"github.com/baojie/tibi/internal/metrics"
	"github.com/baojie/tibi/internal/testutil"
)

func bigFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	rand.Read(data)
	p := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p, data
}

// An upload interrupted halfway resumes without re-sending the first half
// and produces the exact file.
func TestResumableInterruptAndResume(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	path, data := bigFile(t, 48<<20)
	cl := client.New(e.Srv.URL, tok)
	state := t.TempDir()

	before := metrics.ResumableBytes.Load()
	ctx, cancel := context.WithCancel(context.Background())
	_, _, err := cl.UploadResumable(ctx, client.Resumable{
		Path: path, StateDir: state, Options: client.UploadOptions{Title: "Big one"},
		Progress: func(sent, total int64) {
			if sent >= total/2 {
				cancel() // "kill" the client halfway
			}
		},
	})
	if err == nil {
		t.Fatal("interrupted upload reported success")
	}
	if entries, _ := os.ReadDir(state); len(entries) != 1 {
		t.Fatalf("resume state not kept: %d entries", len(entries))
	}
	time.Sleep(200 * time.Millisecond) // let the server persist the last bytes

	var resumedAt int64 = -1
	it, raw, err := cl.UploadResumable(context.Background(), client.Resumable{
		Path: path, StateDir: state, Resumed: func(off int64) { resumedAt = off },
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumedAt < int64(len(data))/3 {
		t.Fatalf("resumed at %d of %d: the first half was sent again", resumedAt, len(data))
	}
	received := metrics.ResumableBytes.Load() - before
	// Every byte arrives once; at most the chunk in flight is sent again.
	if received < int64(len(data)) || received > int64(len(data))+(16<<20) {
		t.Fatalf("server received %d bytes for a %d-byte file", received, len(data))
	}
	if it.Title != "Big one" || it.Filename != "big.bin" || it.Size != int64(len(data)) || len(raw) == 0 {
		t.Fatalf("item: %+v", it)
	}
	if _, body := get(t, it.DownloadURL); !bytes.Equal(body, data) {
		t.Fatal("resumed upload differs from the file")
	}
	if entries, _ := os.ReadDir(state); len(entries) != 0 {
		t.Fatal("resume state not cleared after success")
	}
}

func tusCreate(t *testing.T, e *testutil.Env, tok string, length string, meta map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/uploads/", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+tok)
	if length != "" {
		req.Header.Set("Upload-Length", length)
	} else {
		req.Header.Set("Upload-Defer-Length", "1")
	}
	var parts []string
	for k, v := range meta {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}
	req.Header.Set("Upload-Metadata", strings.Join(parts, ","))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func tusPatch(t *testing.T, tok, loc string, offset int, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PATCH", loc, bytes.NewReader(body))
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", itoa(offset))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func itoa(n int) string   { return strconv.Itoa(n) }
func fmtInt(n int) string { return strconv.Itoa(n) }

func TestResumableProtocol(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.MaxResumableBytes = 1 << 20 })
	tok := e.Token("t")
	other := e.Token("other")
	data := []byte("hello resumable world\n")

	resp := tusCreate(t, e, tok, fmtInt(len(data)), map[string]string{"filename": "hello.txt", "visibility": "private", "expires": "1h"})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 201 || !strings.HasPrefix(loc, e.Cfg.BaseURL+"/api/v1/uploads/") {
		t.Fatalf("create: %d %q", resp.StatusCode, loc)
	}
	// Unfinished: item endpoint says so; other tokens see nothing.
	if code, b := api(t, e, tok, "GET", strings.TrimPrefix(loc, e.Cfg.BaseURL)+"/item", nil); code != 409 || !strings.Contains(string(b), "0 of 22 bytes") {
		t.Fatalf("incomplete item: %d %s", code, b)
	}
	if code, _ := api(t, e, other, "GET", strings.TrimPrefix(loc, e.Cfg.BaseURL)+"/item", nil); code != 404 {
		t.Fatalf("other token sees the upload: %d", code)
	}
	if r := tusPatch(t, other, loc, 0, data); r.StatusCode != 404 {
		t.Fatalf("other token can PATCH: %d", r.StatusCode)
	}
	// Two PATCHes; the second finishes and reports the item.
	if r := tusPatch(t, tok, loc, 0, data[:10]); r.StatusCode != 204 || r.Header.Get("Upload-Offset") != "10" {
		t.Fatalf("patch 1: %d %q", r.StatusCode, r.Header.Get("Upload-Offset"))
	}
	if r := tusPatch(t, tok, loc, 5, data[5:]); r.StatusCode != 409 {
		t.Fatalf("wrong offset: %d", r.StatusCode)
	}
	r := tusPatch(t, tok, loc, 10, data[10:])
	itemID := r.Header.Get("Tibi-Item-Id")
	if r.StatusCode != 204 || itemID == "" || r.Header.Get("Tibi-Item-Url") != e.Cfg.BaseURL+"/"+itemID {
		t.Fatalf("final patch: %d %v", r.StatusCode, r.Header)
	}
	code, b := api(t, e, tok, "GET", strings.TrimPrefix(loc, e.Cfg.BaseURL)+"/item", nil)
	if code != 200 || !strings.Contains(string(b), `"id":"`+itemID+`"`) || !strings.Contains(string(b), `"visibility":"private"`) ||
		!strings.Contains(string(b), `"filename":"hello.txt"`) || strings.Contains(string(b), `"expires_at":null`) {
		t.Fatalf("item: %d %s", code, b)
	}
	// The tus files are gone once ingested.
	entries, _ := os.ReadDir(filepath.Join(e.Cfg.DataDir, "uploads"))
	if len(entries) != 0 {
		t.Fatalf("leftover tus files: %d", len(entries))
	}

	// Creation errors.
	if r := tusCreate(t, e, tok, "", nil); r.StatusCode != 400 {
		t.Fatalf("deferred length: %d", r.StatusCode)
	}
	if r := tusCreate(t, e, tok, fmtInt(2<<20), nil); r.StatusCode != 413 {
		t.Fatalf("over max_resumable_bytes: %d", r.StatusCode)
	}
	if r := tusCreate(t, e, tok, "10", map[string]string{"expires": "soon"}); r.StatusCode != 400 {
		t.Fatalf("bad metadata: %d", r.StatusCode)
	}
	if r := tusCreate(t, e, "", "10", nil); r.StatusCode != 401 {
		t.Fatalf("no token: %d", r.StatusCode)
	}
	q := int64(5)
	quotaTok, _, _ := e.Instance.Store.CreateToken(context.Background(), "q", []string{"upload"}, &q)
	if r := tusCreate(t, e, quotaTok, "10", nil); r.StatusCode != 413 {
		t.Fatalf("over quota: %d", r.StatusCode)
	}

	// A finished upload that cannot be stored (binary forced to text).
	resp = tusCreate(t, e, tok, "3", map[string]string{"kind": "text"})
	loc = resp.Header.Get("Location")
	if r := tusPatch(t, tok, loc, 0, []byte{0, 1, 2}); r.StatusCode != 400 {
		t.Fatalf("ingest failure: %d", r.StatusCode)
	}
	if code, b := api(t, e, tok, "GET", strings.TrimPrefix(loc, e.Cfg.BaseURL)+"/item", nil); code != 400 || !strings.Contains(string(b), "could not be stored") {
		t.Fatalf("failed item: %d %s", code, b)
	}
	if code, _ := api(t, e, tok, "GET", "/api/v1/uploads/nosuchupload/item", nil); code != 404 {
		t.Fatalf("unknown upload: %d", code)
	}
}

// Abandoned partial uploads are removed after a day.
func TestResumableCleanup(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	resp := tusCreate(t, e, tok, "100", nil)
	loc := resp.Header.Get("Location")
	tusPatch(t, tok, loc, 0, make([]byte, 40))
	dir := filepath.Join(e.Cfg.DataDir, "uploads")
	if entries, _ := os.ReadDir(dir); len(entries) != 2 { // data + .info
		t.Fatalf("partial upload files: %d", len(entries))
	}
	if n, _ := e.Server.CleanupUploads(context.Background()); n != 0 {
		t.Fatal("fresh upload cleaned up")
	}
	e.Advance(25 * time.Hour)
	if n, err := e.Server.CleanupUploads(context.Background()); n != 1 || err != nil {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left: %d", len(entries))
	}
	req, _ := http.NewRequest("HEAD", loc, nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+tok)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 404 {
		t.Fatalf("HEAD after cleanup: %d", r.StatusCode)
	}
}

// Unfinished resumable uploads count against the quota, so two of them
// cannot share the same remaining space (security review, 0.1.0).
func TestResumableQuota(t *testing.T) {
	e := testutil.New(t, nil)
	q := int64(1000)
	tok, _, err := e.Instance.Store.CreateToken(context.Background(), "q", []string{"upload"}, &q)
	if err != nil {
		t.Fatal(err)
	}
	first := tusCreate(t, e, tok, "900", nil)
	first.Body.Close()
	if first.StatusCode != 201 {
		t.Fatalf("first create: %d", first.StatusCode)
	}
	second := tusCreate(t, e, tok, "900", nil) // (the helper closes the body)
	if second.StatusCode != 413 {
		t.Fatalf("second create over quota: %d", second.StatusCode)
	}
	// A plain upload is refused too while the first one is pending.
	if resp, _ := upload(t, e, tok, "", make([]byte, 200)); resp.StatusCode != 413 {
		t.Fatalf("plain upload beside a pending one: %d", resp.StatusCode)
	}
	if resp, _ := upload(t, e, tok, "", make([]byte, 100)); resp.StatusCode != 201 {
		t.Fatalf("upload that fits: %d", resp.StatusCode)
	}
	// Cancelling the pending upload gives its space back.
	req, _ := http.NewRequest("DELETE", first.Header.Get("Location"), nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+tok)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 204 {
		t.Fatalf("cancel: %v %v", r, err)
	}
	again := tusCreate(t, e, tok, "800", nil)
	if again.StatusCode != 201 {
		t.Fatalf("create after cancelling: %d", again.StatusCode)
	}
}
