package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"tibi/internal/app"
	"tibi/internal/config"
	"tibi/internal/migrate"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

func upload(t *testing.T, e *testutil.Env, tok, query string, body []byte) string {
	t.Helper()
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items"+query, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var it struct {
		RawURL string `json:"raw_url"`
	}
	json.NewDecoder(resp.Body).Decode(&it)
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d", resp.StatusCode)
	}
	return strings.TrimPrefix(it.RawURL, e.Cfg.BaseURL)
}

func fetch(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return b
}

// A populated fs instance migrated to S3 serves every raw URL
// byte-identically, and the migration is idempotent and reversible.
func TestMigrateFSToS3(t *testing.T) {
	var dataDir string
	fsEnv := testutil.New(t, func(c *config.Server) {
		testutil.UseS3(t, c) // S3 settings present for the destination; skips without S3
		c.Storage.Backend = "fs"
		dataDir = c.DataDir
	})
	tok := fsEnv.Token("t", "upload", "delete")
	big := make([]byte, 2<<20)
	rand.Read(big)
	bodies := [][]byte{
		testimg.PNG(32, 32), testimg.JPEGWithEXIF(20, 10, 6), []byte("package main\n"),
		big, []byte("duplicate"), []byte("duplicate"), []byte("题壁 utf-8 text\n"),
	}
	var paths []string
	for _, b := range bodies {
		paths = append(paths, upload(t, fsEnv, tok, "", b))
	}
	paths = append(paths, upload(t, fsEnv, tok, "?expires=1h&visibility=unlisted", []byte("expiring")))
	want := map[string][]byte{}
	for _, p := range paths {
		want[p] = fetch(t, fsEnv.Srv.URL+p)
	}

	ctx := context.Background()
	s3, err := app.OpenBlobs(fsEnv.Cfg, "s3")
	if err != nil {
		t.Fatal(err)
	}
	res, err := migrate.Run(ctx, fsEnv.Instance.Store, fsEnv.Instance.Blobs, s3, 4, nil)
	if err != nil || res.Total != 7 || res.Copied != 7 || res.Failed+res.Missing != 0 {
		t.Fatalf("migrate: %+v %v", res, err)
	}
	// Re-running is a no-op (resumable).
	res, err = migrate.Run(ctx, fsEnv.Instance.Store, fsEnv.Instance.Blobs, s3, 4, nil)
	if err != nil || res.Skipped != 7 || res.Copied != 0 {
		t.Fatalf("second run: %+v %v", res, err)
	}

	// Switch the same data dir to S3 and compare every URL.
	s3Env := testutil.New(t, func(c *config.Server) {
		*c = fsEnv.Cfg
		c.Storage.Backend = "s3"
		c.DataDir = dataDir
	})
	for p, b := range want {
		if got := fetch(t, s3Env.Srv.URL+p); !bytes.Equal(got, b) {
			t.Fatalf("%s differs after migration", p)
		}
	}

	// And back to an empty fs store.
	fs2, err := app.OpenBlobs(config.Server{DataDir: t.TempDir()}, "fs")
	if err != nil {
		t.Fatal(err)
	}
	res, err = migrate.Run(ctx, s3Env.Instance.Store, s3Env.Instance.Blobs, fs2, 2, nil)
	if err != nil || res.Copied != 7 {
		t.Fatalf("s3 → fs: %+v %v", res, err)
	}
}

func TestMigrateReportsMissing(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.Storage.Backend = "fs" })
	tok := e.Token("t")
	upload(t, e, tok, "", []byte("present"))
	upload(t, e, tok, "", []byte("will vanish"))
	// Lose one blob from the source.
	var victim string
	e.Instance.Store.BlobHashes(context.Background(), func(sha, _ string) error { victim = sha; return nil })
	e.Instance.Blobs.Delete(context.Background(), victim[:2]+"/"+victim[2:4]+"/"+victim)
	dst, _ := app.OpenBlobs(config.Server{DataDir: t.TempDir()}, "fs")
	res, err := migrate.Run(context.Background(), e.Instance.Store, e.Instance.Blobs, dst, 2, nil)
	if err == nil || res.Missing != 1 || res.Copied != 1 {
		t.Fatalf("missing blob not reported: %+v %v", res, err)
	}
}
