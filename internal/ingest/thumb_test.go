package ingest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
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
	cfg := image.Config{ColorModel: color.NRGBAModel, Width: 10000, Height: 10000}
	if decodedBytes(cfg) <= maxThumbBytes {
		t.Fatal("100 MP RGBA under the limit")
	}
}
