package server_test

import (
	"bytes"
	"encoding/json"
	"image"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/baojie/tibi/internal/config"
	"github.com/baojie/tibi/internal/ingest"
	"github.com/baojie/tibi/internal/testutil"
	"github.com/baojie/tibi/internal/testutil/testimg"
)

type mediaItem struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Mime         string `json:"mime"`
	OrigMime     string `json:"orig_mime"`
	URL          string `json:"url"`
	RawURL       string `json:"raw_url"`
	DownloadURL  string `json:"download_url"`
	DurationMS   *int64 `json:"duration_ms"`
	ThumbnailURL string `json:"thumbnail_url"`
	Width        *int   `json:"width"`
	Height       *int   `json:"height"`
	Filename     string `json:"filename"`
}

func upMedia(t *testing.T, e *testutil.Env, tok, query string, body []byte) mediaItem {
	t.Helper()
	resp, b := upload(t, e, tok, query, body)
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, b)
	}
	var it mediaItem
	json.Unmarshal(b, &it)
	return it
}

func TestVideoAndAudio(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	mp4 := testimg.MP4("isom", 95_000, 200_000, true)
	v := upMedia(t, e, tok, "?filename=clip.mp4", mp4)
	if v.Kind != "video" || v.Mime != "video/mp4" || !strings.HasSuffix(v.RawURL, ".mp4") || v.DurationMS == nil || *v.DurationMS != 95_000 {
		t.Fatalf("video: %+v", v)
	}
	_, page := get(t, v.URL)
	if !strings.Contains(string(page), `<video class="player" controls preload="metadata" src="`+v.RawURL+`"`) || !strings.Contains(string(page), "1:35") {
		t.Fatal("view page lacks <video> or duration")
	}
	// Seeking: the player requests byte ranges.
	req, _ := http.NewRequest("GET", v.RawURL, nil)
	req.Header.Set("Range", "bytes=100000-100099")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || resp.Header.Get("Content-Type") != "video/mp4" || !bytes.Equal(b, mp4[100000:100100]) {
		t.Fatalf("range on video: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		// ServeContent advertises ranges on full responses.
		resp, _ := http.Get(v.RawURL)
		resp.Body.Close()
		if resp.Header.Get("Accept-Ranges") != "bytes" {
			t.Fatal("Accept-Ranges missing")
		}
	}

	a := upMedia(t, e, tok, "?filename=note.wav", testimg.WAV(2500))
	if a.Kind != "audio" || a.Mime != "audio/wav" || *a.DurationMS != 2500 {
		t.Fatalf("audio: %+v", a)
	}
	if _, page := get(t, a.URL); !strings.Contains(string(page), `<audio class="player"`) {
		t.Fatal("no <audio> on view page")
	}
	if resp, _ := get(t, a.RawURL); resp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("audio raw type %q", resp.Header.Get("Content-Type"))
	}
}

func TestThumbnails(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete")
	img := upMedia(t, e, tok, "?filename=big.png", testimg.PNG(1600, 800))
	if img.ThumbnailURL != e.Cfg.BaseURL+"/"+img.ID+"/thumb" {
		t.Fatalf("thumbnail_url = %q", img.ThumbnailURL)
	}
	resp, b := get(t, img.ThumbnailURL)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/webp" || err != nil || format != "webp" || cfg.Width != ingest.ThumbWidth || cfg.Height != 200 {
		t.Fatalf("thumb: %d %q %v %+v", resp.StatusCode, resp.Header.Get("Content-Type"), err, cfg)
	}
	if e.ThumbCount() != 1 {
		t.Fatalf("thumbs = %d", e.ThumbCount())
	}
	// A second upload of the same image reuses the thumbnail.
	dup := upMedia(t, e, tok, "", testimg.PNG(1600, 800))
	if dup.ThumbnailURL == "" || e.ThumbCount() != 1 {
		t.Fatalf("dup thumb: %q, %d thumbs", dup.ThumbnailURL, e.ThumbCount())
	}
	// Every image kind gets one; non-images do not.
	for _, b := range [][]byte{testimg.JPEGWithEXIF(30, 20, 1), testimg.WebPWithMeta()} {
		if it := upMedia(t, e, tok, "", b); it.ThumbnailURL == "" {
			t.Fatalf("%s without thumbnail", it.Mime)
		}
	}
	txt := upMedia(t, e, tok, "", []byte("text"))
	if txt.ThumbnailURL != "" || status(t, e.Srv.URL+"/"+txt.ID+"/thumb", "") != 404 {
		t.Fatal("text has a thumbnail")
	}
	// Thumbnails are GC'd with their image, once nothing references it.
	before := e.ThumbCount()
	api(t, e, tok, "DELETE", "/api/v1/items/"+img.ID, nil)
	e.Advance(25 * time.Hour)
	e.GC()
	if e.ThumbCount() != before {
		t.Fatal("thumbnail deleted while another item still uses it")
	}
	api(t, e, tok, "DELETE", "/api/v1/items/"+dup.ID, nil)
	e.Advance(25 * time.Hour)
	e.GC()
	if e.ThumbCount() != before-1 {
		t.Fatalf("thumbnail not GC'd: %d → %d", before, e.ThumbCount())
	}
	// Burn items keep their thumbnail private too.
	burn := upMedia(t, e, tok, "?burn_after_read=1", testimg.PNG(10, 10))
	if status(t, e.Srv.URL+"/"+burn.ID+"/thumb", "") != 404 {
		t.Fatal("burn thumbnail served")
	}
}

// fakeConverter writes a script that "converts" HEIC by copying a prepared
// JPEG (with EXIF orientation 6 and GPS data, as a converter might leave).
func fakeConverter(t *testing.T) string {
	dir := t.TempDir()
	jpg := filepath.Join(dir, "out.jpg")
	os.WriteFile(jpg, testimg.JPEGWithEXIF(64, 32, 6), 0o644)
	script := filepath.Join(dir, "fake-heic")
	os.WriteFile(script, []byte("#!/bin/sh\ncp '"+jpg+"' \"$2\"\n"), 0o755)
	return script
}

func TestHEICConverted(t *testing.T) {
	conv := fakeConverter(t)
	e := testutil.New(t, func(c *config.Server) { c.HEICConverter = conv })
	it := upMedia(t, e, e.Token("t"), "?filename=IMG_1234.HEIC", testimg.HEIC())
	if it.Kind != "image" || it.Mime != "image/jpeg" || it.OrigMime != "image/heic" || !strings.HasSuffix(it.RawURL, ".jpg") || it.Filename != "IMG_1234.jpg" {
		t.Fatalf("%+v", it)
	}
	resp, b := get(t, it.RawURL)
	if resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("raw type %q", resp.Header.Get("Content-Type"))
	}
	if bytes.Contains(b, []byte(testimg.GPSMarker)) || ingest.JPEGOrientation(b) != 0 {
		t.Fatal("converted JPEG keeps EXIF/orientation (libheif already rotated the pixels)")
	}
	if *it.Width != 64 || *it.Height != 32 || it.ThumbnailURL == "" {
		t.Fatalf("dims/thumb: %+v", it)
	}
	if _, page := get(t, it.URL); !strings.Contains(string(page), "converted from image/heic") {
		t.Fatal("view page lacks conversion note")
	}
	// kind=file opts out of conversion.
	if f := upMedia(t, e, e.Token("t2"), "?kind=file", testimg.HEIC()); f.Mime != "image/heic" {
		t.Fatalf("kind=file converted: %+v", f)
	}
}

func TestHEICWithoutConverter(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.HEICConverter = "off" })
	heic := testimg.HEIC()
	it := upMedia(t, e, e.Token("t"), "?filename=IMG_1.heic", heic)
	if it.Kind != "file" || it.Mime != "image/heic" || it.OrigMime != "" || !strings.HasSuffix(it.RawURL, ".heic") {
		t.Fatalf("%+v", it)
	}
	_, page := get(t, it.URL)
	if !strings.Contains(string(page), "HEIC photo") || !strings.Contains(string(page), it.DownloadURL) {
		t.Fatal("no HEIC note / download on view page")
	}
	if _, b := get(t, it.DownloadURL); !bytes.Equal(b, heic) {
		t.Fatal("HEIC not stored as-is")
	}
	// A failing converter falls back to storing as-is.
	bad := filepath.Join(t.TempDir(), "broken")
	os.WriteFile(bad, []byte("#!/bin/sh\nexit 3\n"), 0o755)
	e2 := testutil.New(t, func(c *config.Server) { c.HEICConverter = bad })
	if it := upMedia(t, e2, e2.Token("t"), "", heic); it.Mime != "image/heic" {
		t.Fatalf("broken converter: %+v", it)
	}
}

// With a real HEIC encoder and converter installed (CI installs
// libheif-examples), convert a genuine HEIC photo end to end.
func TestHEICRealConverter(t *testing.T) {
	enc, err := exec.LookPath("heif-enc")
	if err != nil {
		t.Skip("heif-enc not installed")
	}
	conv, _ := ingest.DetectConverter("auto")
	if conv == nil {
		t.Skip("no HEIC converter installed")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "in.png"), testimg.PNG(120, 80), 0o644)
	if out, err := exec.Command(enc, "-o", filepath.Join(dir, "photo.heic"), filepath.Join(dir, "in.png")).CombinedOutput(); err != nil {
		t.Skipf("heif-enc cannot encode here: %v %s", err, out)
	}
	heic, _ := os.ReadFile(filepath.Join(dir, "photo.heic"))
	e := testutil.New(t, func(c *config.Server) { c.HEICConverter = "auto" })
	it := upMedia(t, e, e.Token("t"), "?filename=photo.heic", heic)
	if it.Mime != "image/jpeg" || it.OrigMime != "image/heic" || *it.Width != 120 || *it.Height != 80 {
		t.Fatalf("real conversion via %s: %+v", conv.Name, it)
	}
	_, b := get(t, it.RawURL)
	if cfg, format, err := image.DecodeConfig(bytes.NewReader(b)); err != nil || format != "jpeg" || cfg.Width != 120 {
		t.Fatalf("converted file: %v %s", err, format)
	}
}
