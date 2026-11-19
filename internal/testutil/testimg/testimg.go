// Package testimg builds image fixtures (with metadata) for tests.
package testimg

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// GPSMarker is embedded in EXIF test data so tests can grep for leaks.
const GPSMarker = "TIBI-TEST-GPS-MARKER"

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

// PNG returns a w×h PNG.
func PNG(w, h int) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, testImage(w, h))
	return buf.Bytes()
}

// JPEGWithEXIF returns a w×h JPEG carrying an EXIF block with the given
// orientation, an ImageDescription containing GPSMarker, and a GPS IFD.
func JPEGWithEXIF(w, h, orientation int) []byte {
	var buf bytes.Buffer
	jpeg.Encode(&buf, testImage(w, h), &jpeg.Options{Quality: 80})
	j := buf.Bytes()

	desc := append([]byte(GPSMarker), 0)
	if len(desc)%2 == 1 {
		desc = append(desc, 0)
	}
	be := binary.BigEndian
	t := []byte{'M', 'M', 0, 42, 0, 0, 0, 8}
	t = be.AppendUint16(t, 3)
	descOff := 8 + 2 + 3*12 + 4
	gpsOff := descOff + len(desc)
	entry := func(tag, typ uint16, count, val uint32) {
		t = be.AppendUint16(t, tag)
		t = be.AppendUint16(t, typ)
		t = be.AppendUint32(t, count)
		t = be.AppendUint32(t, val)
	}
	entry(0x010E, 2, uint32(len(GPSMarker)+1), uint32(descOff)) // ImageDescription
	entry(0x0112, 3, 1, uint32(orientation)<<16)                // Orientation (SHORT, left-justified)
	entry(0x8825, 4, 1, uint32(gpsOff))                         // GPS IFD pointer
	t = be.AppendUint32(t, 0)
	t = append(t, desc...)
	t = be.AppendUint16(t, 1)
	entry(0x0001, 2, 2, uint32('N')<<24) // GPSLatitudeRef "N"
	t = be.AppendUint32(t, 0)

	payload := append([]byte("Exif\x00\x00"), t...)
	seg := []byte{0xFF, 0xE1}
	seg = be.AppendUint16(seg, uint16(len(payload)+2))
	seg = append(seg, payload...)

	out := append([]byte{}, j[:2]...) // SOI
	out = append(out, seg...)
	return append(out, j[2:]...)
}

// webp1x1 is a 1×1 lossless WebP.
const webp1x1 = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

// WebPWithMeta returns a 1×1 extended WebP with EXIF and XMP chunks that
// contain GPSMarker.
func WebPWithMeta() []byte {
	simple, _ := base64.StdEncoding.DecodeString(webp1x1)
	vp8l := simple[12:]
	chunk := func(fourcc string, data []byte) []byte {
		c := append([]byte(fourcc), 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(c[4:], uint32(len(data)))
		c = append(c, data...)
		if len(data)%2 == 1 {
			c = append(c, 0)
		}
		return c
	}
	body := []byte("WEBP")
	body = append(body, chunk("VP8X", []byte{0x08 | 0x04, 0, 0, 0, 0, 0, 0, 0, 0, 0})...)
	body = append(body, vp8l...)
	body = append(body, chunk("EXIF", []byte("MM\x00\x2a"+GPSMarker))...)
	body = append(body, chunk("XMP ", []byte("<x:xmpmeta>"+GPSMarker+"</x:xmpmeta>"))...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}

func box(typ string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := binary.BigEndian.AppendUint32(nil, uint32(n))
	b = append(b, typ...)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

// MP4 returns a minimal ISO-BMFF file (ftyp brand, moov/mvhd with the given
// duration, and an mdat of padding). It is not playable; it exercises
// sniffing, duration parsing and Range. moovLast puts moov after mdat, as
// many cameras write it.
func MP4(brand string, durationMS uint32, mdatSize int, moovLast bool) []byte {
	ftyp := box("ftyp", []byte(brand), []byte{0, 0, 2, 0}, []byte(brand), []byte("mp41"))
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)       // timescale
	binary.BigEndian.PutUint32(mvhd[16:], durationMS) // duration
	moov := box("moov", box("mvhd", mvhd))
	mdat := box("mdat", make([]byte, mdatSize))
	if moovLast {
		return append(append(ftyp, mdat...), moov...)
	}
	return append(append(ftyp, moov...), mdat...)
}

// WAV returns a PCM WAV of the given length (8 kHz, mono, 16-bit).
func WAV(durationMS int) []byte {
	data := make([]byte, 8000*2*durationMS/1000)
	le := binary.LittleEndian
	b := []byte("RIFF")
	b = le.AppendUint32(b, uint32(36+len(data)))
	b = append(b, "WAVEfmt "...)
	b = le.AppendUint32(b, 16)
	b = le.AppendUint16(b, 1)    // PCM
	b = le.AppendUint16(b, 1)    // mono
	b = le.AppendUint32(b, 8000) // sample rate
	b = le.AppendUint32(b, 16000)
	b = le.AppendUint16(b, 2)
	b = le.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = le.AppendUint32(b, uint32(len(data)))
	return append(b, data...)
}

// HEIC returns the header of a HEIC file: enough to be classified as one.
func HEIC() []byte {
	return append(box("ftyp", []byte("heic"), []byte{0, 0, 0, 0}, []byte("mif1heic")), make([]byte, 64)...)
}
