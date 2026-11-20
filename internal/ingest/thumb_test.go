package ingest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"runtime"
	"testing"
)

// A small PNG claiming 9999×9999 at 16 bits per channel would decode to
// ~800 MB: no thumbnail, and nothing that large is allocated.
func TestThumbDecompressionBomb(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewNRGBA64(image.Rect(0, 0, 1, 1))
	png.Encode(&buf, img) // a real 1×1 16-bit PNG; patch its IHDR size
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b[16:], 9999)
	binary.BigEndian.PutUint32(b[20:], 9999)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29])) // IHDR CRC
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := MakeThumb(b, "image/png"); err != errNoThumb {
		t.Fatalf("err = %v, want errNoThumb", err)
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Fatalf("allocated %d MB for a refused image", grew>>20)
	}
	// An 8-bit image of the same pixel count is refused too (400 MB).
	for _, c := range []struct {
		model       color.Model
		w, h        int
		progressive bool
		ok          bool
	}{
		{color.NRGBAModel, 7999, 7999, false, false}, // the reviewer's 64 MP RGBA bomb
		{color.YCbCrModel, 8064, 6048, false, true},  // a 48 MP phone JPEG
		{color.YCbCrModel, 8064, 6048, true, false},  // …progressive: coefficients too
		{color.YCbCrModel, 4032, 3024, true, true},   // a 12 MP progressive JPEG
		{color.NRGBAModel, 4000, 3000, false, true},  // a 12 MP PNG
	} {
		got := decodedBytes(image.Config{ColorModel: c.model, Width: c.w, Height: c.h}, c.progressive) <= maxThumbBytes
		if got != c.ok {
			t.Errorf("%dx%d progressive=%v: thumbnail allowed %v, want %v", c.w, c.h, c.progressive, got, c.ok)
		}
	}
}

func TestIsProgressiveJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil) // Go writes baseline
	if isProgressiveJPEG(buf.Bytes()) {
		t.Fatal("baseline JPEG taken for progressive")
	}
	if !isProgressiveJPEG([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x04, 0x00, 0x00, 0xFF, 0xC2, 0x00, 0x04, 0, 0}) {
		t.Fatal("SOF2 not detected")
	}
	if isProgressiveJPEG([]byte{0xFF, 0xD8, 0xFF}) {
		t.Fatal("truncated input")
	}
}
