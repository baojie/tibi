package server_test

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baojie/tibi/internal/e2e"
	"github.com/baojie/tibi/internal/testutil"
)

const marker = "TIBI-PLAINTEXT-MARKER-7f3a"

// grepDataDir reports files under the instance's data dir containing s.
func grepDataDir(t *testing.T, dir, s string) []string {
	t.Helper()
	var hits []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, []byte(s)) {
			hits = append(hits, strings.TrimPrefix(p, dir))
		}
		return nil
	})
	return hits
}

func TestEncryptedNoPlaintext(t *testing.T) {
	e := testutil.New(t, nil)
	if e.Backend() != "fs" {
		t.Skip("greps the local data dir")
	}
	tok := e.Token("t", "upload", "read_private")
	key, frag := e2e.NewKey()
	secret := "password=hunter2 " + marker + "\n"
	blob, err := e2e.Encrypt(key, "", e2e.Header{Name: marker + ".txt", Type: "text/plain", Lang: "ini"}, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	it := up(t, e, tok, "?kind=encrypted&lang=ini&filename="+url.QueryEscape(marker+"-name.txt"), blob)
	if it.Kind != "encrypted" || it.Lang != "ini" {
		t.Fatalf("%+v", it)
	}
	// Also a control: the same marker uploaded in the clear IS found, so
	// the grep would catch a leak.
	if hits := grepDataDir(t, e.Cfg.DataDir, marker); len(hits) != 0 {
		t.Fatalf("plaintext found in %v", hits)
	}
	up(t, e, tok, "?filename=control.txt", []byte("control "+marker))
	e.Instance.Store.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if hits := grepDataDir(t, e.Cfg.DataDir, marker); len(hits) == 0 {
		t.Fatal("control upload not found by the grep: the check is blind")
	}

	// The server hands back exactly the ciphertext, and it decrypts.
	_, raw := get(t, it.RawURL)
	if !bytes.Equal(raw, blob) {
		t.Fatal("stored bytes differ from the ciphertext")
	}
	h, plain, err := e2e.Decrypt(raw, key, "")
	if err != nil || string(plain) != secret || h.Lang != "ini" {
		t.Fatalf("decrypt: %v", err)
	}

	// The page carries no content, only the decryptor; no preview tags;
	// the client-sent file name was dropped.
	_, page := get(t, it.URL+"#k="+frag)
	s := string(page)
	if strings.Contains(s, marker) || !strings.Contains(s, "/static/e2e.js") || !strings.Contains(s, `data-raw="/`+it.ID+`/raw"`) {
		t.Fatal("encrypted page wrong")
	}
	if ogRe.MatchString(s) {
		t.Fatal("encrypted page has OG tags")
	}
	code, b := api(t, e, tok, "GET", "/api/v1/items/"+it.ID, nil)
	if code != 200 || strings.Contains(string(b), "filename") {
		t.Fatalf("filename kept: %s", b)
	}
	// Excluded from search (even by its public language hint or ID).
	for _, q := range []string{marker, "hunter2", "ini", it.ID} {
		if strings.Contains(string(mustSearch(t, e, tok, q)), it.ID) {
			t.Errorf("encrypted item found by search %q", q)
		}
	}
}

func mustSearch(t *testing.T, e *testutil.Env, tok, q string) []byte {
	_, b := api(t, e, tok, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil)
	return b
}

func TestEncryptedValidationAndBurn(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	for _, body := range [][]byte{[]byte("this is not encrypted at all, just text"), {2, 0, 1, 2, 3}, {1}} {
		if resp, b := upload(t, e, tok, "?kind=encrypted", body); resp.StatusCode != 400 || !strings.Contains(string(b), "encrypted format") {
			t.Errorf("bad ciphertext accepted: %d %s", resp.StatusCode, b)
		}
	}
	key, _ := e2e.NewKey()
	blob, _ := e2e.Encrypt(key, "", e2e.Header{}, []byte("burn me"))
	it := up(t, e, tok, "?kind=encrypted&burn_after_read=1", blob)
	// Reveal: the ciphertext is embedded, since the raw URL is gone after.
	resp, err := httpPost(e.Srv.URL + "/" + it.ID + "/reveal")
	if err != nil || resp.code != 200 || !strings.Contains(resp.body, `data-raw="/`+it.ID+`/burned/`) {
		t.Fatalf("burned encrypted reveal: %+v %v", resp, err)
	}
	if status(t, it.RawURL, "") != 404 {
		t.Fatal("burned ciphertext still served")
	}
	// Images keep no thumbnails or metadata processing.
	pblob, _ := e2e.Encrypt(key, "", e2e.Header{Type: "image/png"}, []byte("\x89PNG fake"))
	if it := up(t, e, tok, "?kind=encrypted", pblob); it.Kind != "encrypted" {
		t.Fatalf("%+v", it)
	}
}

type postResp struct {
	code int
	body string
}

func httpPost(u string) (postResp, error) {
	resp, err := httpClientPost(u)
	if err != nil {
		return postResp{}, err
	}
	return resp, nil
}

func httpClientPost(u string) (postResp, error) {
	r, err := http.Post(u, "", nil)
	if err != nil {
		return postResp{}, err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return postResp{r.StatusCode, string(b)}, nil
}
