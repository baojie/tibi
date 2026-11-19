package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"tibi/internal/blob"
	"tibi/internal/config"
	"tibi/internal/gc"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

func TestRange(t *testing.T) {
	e := testutil.New(t, nil)
	data := make([]byte, 5000)
	rand.Read(data)
	it := up(t, e, e.Token("t"), "?filename=v.bin", data)
	for _, u := range []string{it.RawURL, it.DownloadURL, e.Srv.URL + "/" + it.ID + "/raw"} {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("Range", "bytes=0-99")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 206 || len(b) != 100 || !bytes.Equal(b, data[:100]) {
			t.Fatalf("%s: %d, %d bytes", u, resp.StatusCode, len(b))
		}
		if cr := resp.Header.Get("Content-Range"); cr != "bytes 0-99/5000" {
			t.Fatalf("Content-Range = %q", cr)
		}
		// A middle range seeks within the blob.
		req.Header.Set("Range", "bytes=4000-4099")
		resp, _ = http.DefaultClient.Do(req)
		b, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 206 || !bytes.Equal(b, data[4000:4100]) {
			t.Fatalf("%s middle range: %d", u, resp.StatusCode)
		}
	}
}

func noFollow() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestRedirectDelivery(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) {
		testutil.UseS3(t, c) // skips without S3
		c.BlobDelivery = "redirect"
	})
	tok := e.Token("t")
	reader := e.Token("r", "read_private")
	png := testimg.PNG(16, 16)
	img := up(t, e, tok, "?filename=pic.png", png)

	resp, err := noFollow().Get(img.RawURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.Contains(loc, "X-Amz-Signature") {
		t.Fatalf("image raw: %d %q", resp.StatusCode, loc)
	}
	resp, _ = http.Get(loc)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(b, png) || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("presigned image: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// Text stays proxied as text/plain, with tibi's headers.
	txt := up(t, e, tok, "", []byte("<html>not rendered</html>\n"))
	resp, _ = noFollow().Get(txt.RawURL)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("text raw: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Active content is proxied even for /dl.
	html := up(t, e, tok, "?filename=x.html", []byte("<!doctype html><script>1</script>"))
	if resp, _ := noFollow().Get(html.DownloadURL); resp.StatusCode != 200 {
		t.Fatalf("html /dl: %d", resp.StatusCode)
	}

	// /dl of a binary redirects with an attachment disposition.
	bin := up(t, e, tok, "?filename=data.bin", []byte{1, 2, 3, 4})
	resp, _ = noFollow().Get(bin.DownloadURL)
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("bin /dl: %d", resp.StatusCode)
	}
	resp, _ = http.Get(bin.DownloadURL)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(b, []byte{1, 2, 3, 4}) || !strings.Contains(resp.Header.Get("Content-Disposition"), "data.bin") {
		t.Fatalf("followed /dl: %q", resp.Header.Get("Content-Disposition"))
	}
	// Inline raw of a non-media file is proxied.
	if resp, _ := noFollow().Get(bin.RawURL); resp.StatusCode != 200 {
		t.Fatalf("bin raw: %d", resp.StatusCode)
	}

	// Private and burn items are never handed out as presigned links.
	priv := up(t, e, tok, "?visibility=private", testimg.PNG(3, 3))
	if resp, _ := getAs(t, priv.RawURL, reader); resp.StatusCode != 200 {
		t.Fatalf("private image: %d", resp.StatusCode)
	}
	burn := up(t, e, tok, "?burn_after_read=1", testimg.PNG(5, 5))
	if resp, _ := getAs(t, burn.RawURL, reader); resp.StatusCode != 200 {
		t.Fatalf("burn image (owner): %d", resp.StatusCode)
	}
	// Range requests work through the redirect.
	req, _ := http.NewRequest("GET", img.RawURL, nil)
	req.Header.Set("Range", "bytes=0-7")
	resp, _ = http.DefaultClient.Do(req)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(b, png[:8]) {
		t.Fatalf("range via redirect: %d", resp.StatusCode)
	}
}

func TestRedirectFallsBackOnFS(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) {
		c.Storage.Backend = "fs"
		c.BlobDelivery = "redirect"
	})
	img := up(t, e, e.Token("t"), "", testimg.PNG(4, 4))
	if resp, _ := noFollow().Get(img.RawURL); resp.StatusCode != 200 {
		t.Fatalf("fs redirect mode should proxy: %d", resp.StatusCode)
	}
}

func TestOrphans(t *testing.T) {
	e := testutil.New(t, nil)
	ctx := context.Background()
	kept := up(t, e, e.Token("t"), "", []byte("referenced"))
	orphan := strings.Repeat("ab", 32)
	e.Instance.Blobs.Put(ctx, blob.Key(orphan), strings.NewReader("lost"), 4, "")
	e.Instance.Blobs.Put(ctx, "thumb/ab/ab/"+orphan+"-200.webp", strings.NewReader("t"), 1, "image/webp")
	e.Instance.Blobs.Put(ctx, "unrelated/readme.txt", strings.NewReader("x"), 1, "")

	// Fresh orphans are protected (an upload may be about to insert its row).
	if n, err := gc.Orphans(ctx, e.Instance.Store, e.Instance.Blobs, time.Hour); err != nil || n != 0 {
		t.Fatalf("fresh orphans: %d %v", n, err)
	}
	n, err := gc.Orphans(ctx, e.Instance.Store, e.Instance.Blobs, -time.Minute)
	if err != nil || n != 2 {
		t.Fatalf("orphans removed = %d, %v", n, err)
	}
	if ok, _ := e.Instance.Blobs.Exists(ctx, blob.Key(orphan)); ok {
		t.Fatal("orphan survived")
	}
	if ok, _ := e.Instance.Blobs.Exists(ctx, "unrelated/readme.txt"); !ok {
		t.Fatal("foreign key deleted")
	}
	if _, b := get(t, kept.RawURL); string(b) != "referenced" {
		t.Fatal("referenced blob deleted")
	}
}
