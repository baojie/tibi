package ingest

import (
	"bufio"
	"bytes"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"tibi/internal/store"
)

// Class is the result of sniffing an upload.
type Class struct {
	Mime string
	Kind string
	// MaybeText is set for text-looking content that is not UTF-8; the
	// pipeline then tries charset detection and transcoding.
	MaybeText bool
}

// imageMimes are rendered as images (PLAN A5 step 3).
var imageMimes = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
	"image/avif": "avif",
}

// activeByExt maps extensions of active content to their type; these are
// always stored as kind file and never rendered (PLAN A8.1).
var activeByExt = map[string]string{
	"html":  "text/html",
	"htm":   "text/html",
	"xhtml": "application/xhtml+xml",
	"xht":   "application/xhtml+xml",
	"svg":   "image/svg+xml",
	"svgz":  "image/svg+xml",
	"js":    "text/javascript",
	"mjs":   "text/javascript",
	"cjs":   "text/javascript",
}

// Classify sniffs head (the first bytes of the upload) plus the filename
// extension. full is the whole upload, used for the UTF-8 text check.
func Classify(head []byte, filename string, full io.ReaderAt, size int64) Class {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))

	if mt := sniffISOBMFF(head); mt != "" {
		if _, ok := imageMimes[mt]; ok {
			return Class{Mime: mt, Kind: store.KindImage}
		}
		return Class{Mime: mt, Kind: store.KindFile} // HEIC: converted in 0.0.6
	}

	detected, _, _ := mime.ParseMediaType(http.DetectContentType(head))
	if _, ok := imageMimes[detected]; ok {
		return Class{Mime: detected, Kind: store.KindImage}
	}
	if mt, ok := activeByExt[ext]; ok {
		return Class{Mime: mt, Kind: store.KindFile}
	}
	if detected == "text/html" {
		return Class{Mime: "text/html", Kind: store.KindFile}
	}
	lower := bytes.ToLower(head)
	if bytes.Contains(lower, []byte("<svg")) {
		return Class{Mime: "image/svg+xml", Kind: store.KindFile}
	}
	if bytes.Contains(lower, []byte("http://www.w3.org/1999/xhtml")) {
		return Class{Mime: "application/xhtml+xml", Kind: store.KindFile}
	}
	switch {
	case strings.HasPrefix(detected, "video/"), strings.HasPrefix(detected, "audio/"),
		detected == "application/ogg":
		// Media kinds arrive in 0.0.6; keep the real type for inline raw.
		return Class{Mime: detected, Kind: store.KindFile}
	}
	textual := detected == "text/xml" || http.DetectContentType(head) == "text/plain; charset=utf-8"
	if textual && IsUTF8Text(io.NewSectionReader(full, 0, size)) {
		if detected == "text/xml" {
			return Class{Mime: "text/xml", Kind: store.KindText}
		}
		return Class{Mime: "text/plain", Kind: store.KindText}
	}
	if detected == "text/plain" {
		// Not UTF-8: maybe UTF-16 with BOM or a legacy charset such as GBK.
		return Class{Mime: "application/octet-stream", Kind: store.KindFile, MaybeText: true}
	}
	return Class{Mime: detected, Kind: store.KindFile}
}

// sniffISOBMFF recognizes AVIF and HEIC/HEIF by their ftyp box.
func sniffISOBMFF(head []byte) string {
	if len(head) < 16 || string(head[4:8]) != "ftyp" {
		return ""
	}
	boxLen := int(head[0])<<24 | int(head[1])<<16 | int(head[2])<<8 | int(head[3])
	if boxLen < 16 || boxLen > len(head) {
		boxLen = len(head)
	}
	brands := []string{string(head[8:12])}
	for i := 16; i+4 <= boxLen; i += 4 {
		brands = append(brands, string(head[i:i+4]))
	}
	has := func(set ...string) bool {
		for _, b := range brands {
			for _, s := range set {
				if b == s {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has("avif", "avis"):
		return "image/avif"
	case has("heic", "heix", "heim", "heis", "hevc", "hevx", "hevm", "hevs"):
		return "image/heic"
	case brands[0] == "mif1" || brands[0] == "msf1":
		return "image/heif"
	}
	return ""
}

// IsUTF8Text reports whether r is entirely valid UTF-8 without NUL bytes.
func IsUTF8Text(r io.Reader) bool {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		c, size, err := br.ReadRune()
		if err == io.EOF {
			return true
		}
		if err != nil || c == 0 || (c == utf8.RuneError && size == 1) {
			return false
		}
	}
}
