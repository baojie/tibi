package ingest

import (
	"bytes"
	"image"
	"testing"

	"tibi/internal/store"
	"tibi/internal/testutil/testimg"
)

func TestSniffMedia(t *testing.T) {
	cases := []struct {
		name, file string
		data       []byte
		mime, kind string
		durationMS int64
	}{
		{"mp4", "clip.mp4", testimg.MP4("isom", 12345, 100, false), "video/mp4", "video", 12345},
		{"mp4 moov last", "", testimg.MP4("mp42", 61000, 5000, true), "video/mp4", "video", 61000},
		{"iphone mov", "IMG_0001.MOV", testimg.MP4("qt  ", 3000, 10, false), "video/quicktime", "video", 3000},
		{"m4a", "song.m4a", testimg.MP4("M4A ", 200000, 10, false), "audio/mp4", "audio", 200000},
		{"wav", "a.wav", testimg.WAV(1500), "audio/wav", "audio", 1500},
		{"flac", "", append([]byte("fLaC"), make([]byte, 60)...), "audio/flac", "audio", 0},
		{"mp3 id3", "", append([]byte("ID3\x04\x00"), make([]byte, 60)...), "audio/mpeg", "audio", 0},
		{"opus", "voice.opus", append([]byte("OggS\x00\x02"), append(make([]byte, 22), []byte("OpusHead")...)...), "audio/opus", "audio", 0},
		{"webm", "", append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x42, 0x82, 0x84}, []byte("webm")...), "video/webm", "video", 0},
		{"mkv", "", append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x88}, []byte("matroska")...), "video/x-matroska", "file", 0},
		{"heic", "x.heic", testimg.HEIC(), "image/heic", "file", 0},
	}
	for _, c := range cases {
		cl := classify(t, c.file, c.data)
		if cl.Mime != c.mime || cl.Kind != c.kind {
			t.Errorf("%s: %s/%s, want %s/%s", c.name, cl.Mime, cl.Kind, c.mime, c.kind)
			continue
		}
		if d := MediaDuration(bytes.NewReader(c.data), int64(len(c.data)), cl.Mime); d != c.durationMS {
			t.Errorf("%s: duration %d, want %d", c.name, d, c.durationMS)
		}
	}
	if e := Ext(store.KindVideo, "IMG_0001.MOV", "video/quicktime"); e != "mov" {
		t.Errorf("ext = %q", e)
	}
}

func TestThumb(t *testing.T) {
	// Large landscape → 400 wide.
	th, err := MakeThumb(testimg.PNG(1200, 600), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(th))
	if err != nil || format != "webp" || cfg.Width != 400 || cfg.Height != 200 {
		t.Fatalf("thumb: %v %s %+v", err, format, cfg)
	}
	// Small images are not upscaled.
	th, _ = MakeThumb(testimg.PNG(50, 30), "image/png")
	cfg, _, _ = image.DecodeConfig(bytes.NewReader(th))
	if cfg.Width != 50 || cfg.Height != 30 {
		t.Fatalf("small: %+v", cfg)
	}
	// EXIF orientation 6 (rotate 90°): a 800×400 JPEG becomes a portrait thumb.
	th, err = MakeThumb(testimg.JPEGWithEXIF(800, 400, 6), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ = image.DecodeConfig(bytes.NewReader(th))
	if cfg.Width != 400 || cfg.Height != 800 {
		t.Fatalf("rotated: %+v", cfg)
	}
	if _, err := MakeThumb([]byte("not an image"), "image/png"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := MakeThumb(nil, "image/avif"); err != errNoThumb {
		t.Fatalf("avif: %v", err)
	}
}

func TestOrientAllEight(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	src.Pix[0], src.Pix[3] = 255, 255 // mark pixel (0,0)
	for o := 1; o <= 8; o++ {
		b := orient(src, o).Bounds()
		w, h := 3, 2
		if o >= 5 {
			w, h = 2, 3
		}
		if b.Dx() != w || b.Dy() != h {
			t.Errorf("orientation %d: %v", o, b)
		}
	}
}
