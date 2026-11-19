package ingest

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	_ "golang.org/x/image/webp"

	"tibi/internal/store"
	"tibi/internal/testutil/testimg"
)

func classify(t *testing.T, name string, b []byte) Class {
	t.Helper()
	head := b
	if len(head) > 512 {
		head = head[:512]
	}
	return Classify(head, name, bytes.NewReader(b), int64(len(b)))
}

func TestClassify(t *testing.T) {
	avif := append([]byte{0, 0, 0, 0x1c}, []byte("ftypavif\x00\x00\x00\x00avifmif1miaf")...)
	heic := append([]byte{0, 0, 0, 0x18}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	cases := []struct {
		name, file string
		data       []byte
		mime, kind string
	}{
		{"png", "a.png", testimg.PNG(4, 4), "image/png", "image"},
		{"png named jpg", "a.jpg", testimg.PNG(4, 4), "image/png", "image"},
		{"jpeg", "", testimg.JPEGWithEXIF(4, 4, 1), "image/jpeg", "image"},
		{"webp", "", testimg.WebPWithMeta(), "image/webp", "image"},
		{"avif", "x.avif", avif, "image/avif", "image"},
		{"heic", "x.heic", heic, "image/heic", "file"},
		{"svg by content", "x.txt", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml", "file"},
		{"svg by ext", "x.svg", []byte(`hello`), "image/svg+xml", "file"},
		{"html", "", []byte("<!DOCTYPE html><html><body>x"), "text/html", "file"},
		{"html ext", "a.htm", []byte("hello"), "text/html", "file"},
		{"js", "app.js", []byte("alert(1)"), "text/javascript", "file"},
		{"xhtml", "", []byte(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"></html>`), "application/xhtml+xml", "file"},
		{"text", "", []byte("hello world\n"), "text/plain", "text"},
		{"utf8 cjk", "", []byte("题壁 hello"), "text/plain", "text"},
		{"xml", "pom.xml", []byte(`<?xml version="1.0"?><project/>`), "text/xml", "text"},
		{"binary", "", []byte{0, 1, 2, 3, 0xff}, "application/octet-stream", "file"},
		{"latin1", "", []byte("caf\xe9 au lait"), "application/octet-stream", "file"},
		{"pdf", "", []byte("%PDF-1.4\n..."), "application/pdf", "file"},
	}
	for _, c := range cases {
		got := classify(t, c.file, c.data)
		if got.Mime != c.mime || got.Kind != c.kind {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, got.Mime, got.Kind, c.mime, c.kind)
		}
	}
	// Invalid UTF-8 beyond the sniffed head still disqualifies text.
	long := append(bytes.Repeat([]byte("a"), 2000), 0xff)
	if got := classify(t, "", long); got.Kind != store.KindFile {
		t.Errorf("late invalid byte: kind %s", got.Kind)
	}
}

func TestStripJPEG(t *testing.T) {
	in := testimg.JPEGWithEXIF(40, 20, 6)
	if !bytes.Contains(in, []byte(testimg.GPSMarker)) || JPEGOrientation(in) != 6 {
		t.Fatal("fixture broken")
	}
	out, err := StripMeta("image/jpeg", in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(testimg.GPSMarker)) {
		t.Fatal("EXIF description survived")
	}
	if bytes.Contains(out, []byte{0x88, 0x25}) {
		t.Fatal("GPS IFD pointer survived")
	}
	if got := JPEGOrientation(out); got != 6 {
		t.Fatalf("orientation = %d, want 6", got)
	}
	// Pixels are bit-identical: decode both and compare.
	a, err1 := jpeg.Decode(bytes.NewReader(in))
	b, err2 := jpeg.Decode(bytes.NewReader(out))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !bytes.Equal(a.(*image.YCbCr).Y, b.(*image.YCbCr).Y) {
		t.Fatal("pixels changed")
	}
	// Orientation 1 → no EXIF at all.
	out, _ = StripMeta("image/jpeg", testimg.JPEGWithEXIF(8, 8, 1))
	if bytes.Contains(out, []byte("Exif\x00\x00")) {
		t.Fatal("EXIF kept for orientation 1")
	}
	// Trailing data after EOI (e.g. MPF images) is dropped.
	out, _ = StripMeta("image/jpeg", append(testimg.JPEGWithEXIF(8, 8, 1), []byte("\xff\xd8EXTRA"+testimg.GPSMarker)...))
	if bytes.Contains(out, []byte(testimg.GPSMarker)) {
		t.Fatal("trailing data kept")
	}
	// Truncated inside image data: still stripped and terminated.
	out, err = StripMeta("image/jpeg", in[:len(in)-40])
	if err != nil || bytes.Contains(out, []byte(testimg.GPSMarker)) || !bytes.HasSuffix(out, []byte{0xFF, 0xD9}) {
		t.Fatalf("truncated JPEG: %v", err)
	}
	// Truncated inside the headers: rejected.
	if _, err := StripMeta("image/jpeg", in[:30]); err == nil {
		t.Fatal("header-truncated JPEG accepted")
	}
}

func TestStripPNG(t *testing.T) {
	src := testimg.PNG(5, 3)
	// Insert tEXt and eXIf chunks after IHDR (8 sig + 25 IHDR).
	in := append([]byte{}, src[:33]...)
	in = append(in, pngChunk("tEXt", []byte("Comment\x00"+testimg.GPSMarker))...)
	in = append(in, pngChunk("eXIf", []byte("MM\x00\x2a"+testimg.GPSMarker))...)
	in = append(in, src[33:]...)
	if _, err := png.Decode(bytes.NewReader(in)); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	out, err := StripMeta("image/png", in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(testimg.GPSMarker)) {
		t.Fatal("metadata survived")
	}
	if !bytes.Equal(out, src) {
		t.Fatal("stripped PNG should equal the original encoder output")
	}
}

func TestStripWebP(t *testing.T) {
	in := testimg.WebPWithMeta()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(in))
	if err != nil || cfg.Width != 1 {
		t.Fatalf("fixture: %v %+v", err, cfg)
	}
	out, err := StripMeta("image/webp", in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(testimg.GPSMarker)) {
		t.Fatal("metadata survived")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(out)); err != nil {
		t.Fatalf("stripped WebP invalid: %v", err)
	}
	if out[20]&0x0c != 0 {
		t.Fatal("VP8X metadata flags not cleared")
	}
}

func TestExt(t *testing.T) {
	cases := []struct{ kind, file, mime, want string }{
		{"image", "x.jpg", "image/png", "png"},
		{"image", "", "image/jpeg", "jpg"},
		{"text", "main.go", "text/plain", "go"},
		{"text", "", "text/plain", "txt"},
		{"file", "weird.name!!", "application/octet-stream", "bin"},
		{"file", "evil.html", "text/html", "html"},
	}
	for _, c := range cases {
		if got := Ext(c.kind, c.file, c.mime); got != c.want {
			t.Errorf("Ext(%q,%q,%q) = %q, want %q", c.kind, c.file, c.mime, got, c.want)
		}
	}
}
