package ingest

import (
	"bytes"
	"encoding/binary"
	"io"
)

// mediaMimes are played inline: type → extension.
var mediaMimes = map[string]string{
	"video/mp4":       "mp4",
	"video/webm":      "webm",
	"video/quicktime": "mov",
	"audio/mpeg":      "mp3",
	"audio/ogg":       "ogg",
	"audio/opus":      "opus",
	"audio/mp4":       "m4a",
	"audio/wav":       "wav",
	"audio/flac":      "flac",
}

// isoBrands maps ISO base media file brands (the ftyp box) to types.
// HEIC and AVIF are handled first by sniffISOBMFF.
var isoBrands = map[string]string{
	"isom": "video/mp4", "iso2": "video/mp4", "iso4": "video/mp4", "iso5": "video/mp4",
	"iso6": "video/mp4", "mp41": "video/mp4", "mp42": "video/mp4", "avc1": "video/mp4",
	"dash": "video/mp4", "mmp4": "video/mp4", "MSNV": "video/mp4", "M4V ": "video/mp4",
	"M4VH": "video/mp4", "M4VP": "video/mp4",
	"M4A ": "audio/mp4", "M4B ": "audio/mp4", "M4P ": "audio/mp4",
	"qt  ": "video/quicktime",
}

// sniffMedia recognizes audio/video containers from the first bytes. The
// filename extension refines Ogg (Opus) and MP4 audio-only files.
func sniffMedia(head []byte, ext string) string {
	switch {
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		mt := isoBrands[string(head[8:12])]
		if mt == "video/mp4" && (ext == "m4a" || ext == "m4b") {
			mt = "audio/mp4"
		}
		return mt
	case bytes.HasPrefix(head, []byte("fLaC")):
		return "audio/flac"
	case bytes.HasPrefix(head, []byte("OggS")):
		if bytes.Contains(head, []byte("OpusHead")) {
			if ext == "opus" {
				return "audio/opus"
			}
			return "audio/ogg"
		}
		if bytes.Contains(head, []byte("vorbis")) || bytes.Contains(head, []byte("FLAC")) {
			return "audio/ogg"
		}
		if bytes.Contains(head, []byte("theora")) {
			return "" // Ogg video: not supported for inline play
		}
		return "audio/ogg"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return "audio/wav"
	case bytes.HasPrefix(head, []byte("ID3")),
		len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0 && head[1]&0x06 != 0:
		return "audio/mpeg"
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		if bytes.Contains(head, []byte("webm")) {
			return "video/webm"
		}
		return "" // Matroska (mkv): stored as a file
	}
	return ""
}

// MediaDuration returns the duration in milliseconds when it is cheap to
// read from the container header (MP4/MOV/M4A, WAV), else 0.
func MediaDuration(r io.ReaderAt, size int64, mt string) int64 {
	switch mt {
	case "video/mp4", "video/quicktime", "audio/mp4":
		return mp4Duration(r, size)
	case "audio/wav":
		return wavDuration(r, size)
	}
	return 0
}

// mp4Duration reads moov/mvhd. moov may sit at the end of the file, so the
// top-level boxes are walked by their sizes.
func mp4Duration(r io.ReaderAt, size int64) int64 {
	moov, n := findBox(r, 0, size, "moov")
	if moov < 0 {
		return 0
	}
	mvhd, _ := findBox(r, moov, moov+n, "mvhd")
	if mvhd < 0 {
		return 0
	}
	b := make([]byte, 32)
	if _, err := r.ReadAt(b, mvhd); err != nil {
		return 0
	}
	var timescale, duration uint64
	if b[0] == 1 { // version 1: 64-bit times
		timescale = uint64(binary.BigEndian.Uint32(b[20:24]))
		duration = binary.BigEndian.Uint64(b[24:32])
	} else {
		timescale = uint64(binary.BigEndian.Uint32(b[12:16]))
		duration = uint64(binary.BigEndian.Uint32(b[16:20]))
	}
	if timescale == 0 || duration == 0 || duration == 0xFFFFFFFF {
		return 0
	}
	return int64(duration * 1000 / timescale)
}

// findBox returns the payload offset and length of the first box of type
// typ within [start, end), or -1.
func findBox(r io.ReaderAt, start, end int64, typ string) (int64, int64) {
	hdr := make([]byte, 16)
	for off := start; off+8 <= end; {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return -1, 0
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		hlen := int64(8)
		switch size {
		case 1:
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return -1, 0
			}
			size, hlen = int64(binary.BigEndian.Uint64(hdr[8:16])), 16
		case 0:
			size = end - off
		}
		if size < hlen || off+size > end {
			return -1, 0
		}
		if string(hdr[4:8]) == typ {
			return off + hlen, size - hlen
		}
		off += size
	}
	return -1, 0
}

func wavDuration(r io.ReaderAt, size int64) int64 {
	b := make([]byte, 44)
	if _, err := r.ReadAt(b, 0); err != nil {
		return 0
	}
	byteRate := int64(binary.LittleEndian.Uint32(b[28:32]))
	if byteRate == 0 {
		return 0
	}
	data, n := findChunk(r, 12, size, "data")
	if data < 0 {
		return 0
	}
	return n * 1000 / byteRate
}

func findChunk(r io.ReaderAt, start, end int64, id string) (int64, int64) {
	hdr := make([]byte, 8)
	for off := start; off+8 <= end; {
		if _, err := r.ReadAt(hdr, off); err != nil {
			return -1, 0
		}
		n := int64(binary.LittleEndian.Uint32(hdr[4:]))
		if string(hdr[:4]) == id {
			return off + 8, min(n, end-off-8)
		}
		off += 8 + n + n%2
	}
	return -1, 0
}
