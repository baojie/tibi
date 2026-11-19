package ingest

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// StripMeta losslessly removes EXIF (incl. GPS), XMP, IPTC and text metadata
// from JPEG, PNG and WebP images. Pixel data is copied byte-for-byte. For
// JPEG, the EXIF Orientation tag is kept (as a minimal EXIF block) so photos
// still display upright. Other types are returned unchanged.
func StripMeta(mt string, b []byte) ([]byte, error) {
	return stripMeta(mt, b, true)
}

// stripMeta is StripMeta with control over the JPEG orientation tag. Images
// converted from HEIC already have their rotation applied to the pixels, so
// their orientation tag must go too.
func stripMeta(mt string, b []byte, keepOrientation bool) ([]byte, error) {
	switch mt {
	case "image/jpeg":
		return stripJPEG(b, keepOrientation)
	case "image/png":
		return stripPNG(b)
	case "image/webp":
		return stripWebP(b)
	}
	return b, nil
}

var errCorrupt = errors.New("corrupt image")

// ---- JPEG ----

func stripJPEG(b []byte, keepOrientation bool) ([]byte, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, errCorrupt
	}
	out := make([]byte, 0, len(b))
	out = append(out, 0xFF, 0xD8)
	orientation := 0
	exifInserted := false
	insertExif := func() {
		if keepOrientation && !exifInserted && orientation > 1 && orientation <= 8 {
			out = append(out, orientationAPP1(orientation)...)
		}
		exifInserted = true
	}
	i := 2
	inScan, sawSOS := false, false
	for i < len(b) {
		if inScan {
			// Entropy-coded data: copy until a real marker.
			start := i
			for i < len(b) {
				if b[i] == 0xFF && i+1 < len(b) {
					n := b[i+1]
					if n == 0x00 || (n >= 0xD0 && n <= 0xD7) || n == 0xFF {
						i += 2
						if n == 0xFF {
							i-- // fill byte; re-examine
						}
						continue
					}
					break
				}
				i++
			}
			out = append(out, b[start:i]...)
			inScan = false
			continue
		}
		if b[i] != 0xFF {
			return nil, errCorrupt
		}
		for i < len(b) && b[i] == 0xFF { // fill bytes
			i++
		}
		if i >= len(b) {
			return nil, errCorrupt
		}
		marker := b[i]
		i++
		if marker == 0xD9 { // EOI: drop anything after (e.g. MPF secondary images)
			out = append(out, 0xFF, 0xD9)
			return out, nil
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			out = append(out, 0xFF, marker)
			continue
		}
		if i+2 > len(b) {
			return nil, errCorrupt
		}
		segLen := int(binary.BigEndian.Uint16(b[i:]))
		if segLen < 2 || i+segLen > len(b) {
			return nil, errCorrupt
		}
		payload := b[i+2 : i+segLen]
		seg := b[i-2 : i+segLen]
		i += segLen

		switch {
		case marker == 0xE0: // APP0 JFIF/JFXX: keep, EXIF goes after it
			out = append(out, seg...)
			continue
		case marker == 0xE1: // APP1: EXIF or XMP — drop, remember orientation
			if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) && orientation == 0 {
				orientation = exifOrientation(payload[6:])
			}
			continue
		case marker == 0xE2 && bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00")):
			// ICC color profile: keep, it affects rendering.
		case marker == 0xEE: // APP14 Adobe: keep, it affects color decoding
		case marker >= 0xE2 && marker <= 0xEF: // other APPn (MPF, IPTC, Ducky, …): drop
			continue
		case marker == 0xFE: // COM: drop (cameras put serials here)
			continue
		}
		insertExif()
		out = append(out, seg...)
		if marker == 0xDA { // SOS
			inScan, sawSOS = true, true
		}
	}
	if sawSOS {
		// Truncated after image data started (common for partial downloads;
		// browsers still render them). All metadata segments precede it.
		return append(out, 0xFF, 0xD9), nil
	}
	return nil, errCorrupt
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF-structured EXIF block.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 && bo.Uint16(t[e+2:]) == 3 {
			return int(bo.Uint16(t[e+8:]))
		}
	}
	return 0
}

// orientationAPP1 builds an APP1 segment whose EXIF holds only Orientation.
func orientationAPP1(o int) []byte {
	tiff := []byte{
		'M', 'M', 0, 42, 0, 0, 0, 8, // header, IFD0 at 8
		0, 1, // 1 entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, byte(o), 0, 0, // Orientation SHORT 1
		0, 0, 0, 0, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// JPEGOrientation returns the EXIF orientation (1–8) of a JPEG, or 0.
func JPEGOrientation(b []byte) int {
	i := 2
	for i+4 <= len(b) && b[i] == 0xFF {
		marker := b[i+1]
		if marker == 0xDA || marker == 0xD9 {
			break
		}
		l := int(binary.BigEndian.Uint16(b[i+2:]))
		if l < 2 || i+2+l > len(b) {
			break
		}
		p := b[i+4 : i+2+l]
		if marker == 0xE1 && bytes.HasPrefix(p, []byte("Exif\x00\x00")) {
			return exifOrientation(p[6:])
		}
		i += 2 + l
	}
	return 0
}

// ---- PNG ----

var pngSig = []byte("\x89PNG\r\n\x1a\n")

var pngDrop = map[string]bool{"eXIf": true, "tEXt": true, "zTXt": true, "iTXt": true, "tIME": true}

func stripPNG(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, pngSig) {
		return nil, errCorrupt
	}
	out := make([]byte, 0, len(b))
	out = append(out, pngSig...)
	i := len(pngSig)
	for i+12 <= len(b) {
		n := int(binary.BigEndian.Uint32(b[i:]))
		if n < 0 || i+12+n > len(b) {
			return nil, errCorrupt
		}
		typ := string(b[i+4 : i+8])
		chunk := b[i : i+12+n]
		i += 12 + n
		if !pngDrop[typ] {
			out = append(out, chunk...)
		}
		if typ == "IEND" {
			return out, nil
		}
	}
	return nil, errCorrupt
}

// pngChunk builds a PNG chunk (used by tests).
func pngChunk(typ string, data []byte) []byte {
	c := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(c, uint32(len(data)))
	copy(c[4:], typ)
	c = append(c, data...)
	return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(c[4:]))
}

// ---- WebP ----

func stripWebP(b []byte) ([]byte, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil, errCorrupt
	}
	out := make([]byte, 12, len(b))
	copy(out, b[:12])
	vp8x := -1
	i := 12
	for i+8 <= len(b) {
		fourcc := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		end := i + 8 + n + n%2
		if n < 0 || i+8+n > len(b) {
			return nil, errCorrupt
		}
		if end > len(b) {
			end = len(b)
		}
		chunk := b[i:end]
		i = end
		switch fourcc {
		case "EXIF", "XMP ":
			continue
		case "VP8X":
			vp8x = len(out)
		}
		out = append(out, chunk...)
	}
	if vp8x >= 0 && vp8x+8 < len(out) {
		out[vp8x+8] &^= 0x08 | 0x04 // clear EXIF and XMP flags
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out, nil
}
