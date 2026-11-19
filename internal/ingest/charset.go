package ingest

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
)

// candidate is a legacy charset we are willing to transcode from, with the
// minimum chardet confidence required (PLAN A5 step 3).
type candidate struct {
	name    string
	enc     encoding.Encoding
	minConf int
}

var candidates = map[string]candidate{
	"GB-18030":     {"GB18030", simplifiedchinese.GB18030, 50},
	"Big5":         {"Big5", traditionalchinese.Big5, 50},
	"Shift_JIS":    {"Shift_JIS", japanese.ShiftJIS, 50},
	"EUC-JP":       {"EUC-JP", japanese.EUCJP, 50},
	"EUC-KR":       {"EUC-KR", korean.EUCKR, 50},
	"ISO-8859-1":   {"ISO-8859-1", charmap.ISO8859_1, 30},
	"windows-1252": {"windows-1252", charmap.Windows1252, 30},
}

// detectSample bounds how much text chardet looks at.
const detectSample = 64 << 10

// Transcode converts legacy-encoded text to UTF-8. It returns the UTF-8
// bytes and the original charset name, or ok=false if b is not confidently
// text in a supported charset. The result never contains NUL, U+FFFD or
// non-whitespace C0 controls, which keeps binary data out.
func Transcode(b []byte) (out []byte, charset string, ok bool) {
	// UTF-16 with BOM.
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return decodeCheck(b, xunicode.UTF16(xunicode.LittleEndian, xunicode.ExpectBOM), "UTF-16LE")
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return decodeCheck(b, xunicode.UTF16(xunicode.BigEndian, xunicode.ExpectBOM), "UTF-16BE")
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		// UTF-8 with BOM that still failed validation: not text.
		return nil, "", false
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return nil, "", false
	}
	sample := b
	if len(sample) > detectSample {
		sample = sample[:detectSample]
	}
	results, err := chardet.NewTextDetector().DetectAll(sample)
	if err != nil {
		return nil, "", false
	}
	for _, r := range results {
		c, known := candidates[r.Charset]
		if !known || r.Confidence < c.minConf {
			continue
		}
		out, name, ok := decodeCheck(b, c.enc, c.name)
		if !ok {
			continue
		}
		if name == "GB18030" {
			// Most such files are plain GBK; say so when that is exact.
			if g, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil && bytes.Equal(g, out) {
				name = "GBK"
			}
		}
		return out, name, true
	}
	return nil, "", false
}

func decodeCheck(b []byte, enc encoding.Encoding, name string) ([]byte, string, bool) {
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil || !plausibleText(out) {
		return nil, "", false
	}
	return out, name, true
}

// plausibleText rejects decoded output that looks like binary.
func plausibleText(s []byte) bool {
	if !utf8.Valid(s) {
		return false
	}
	for _, r := range string(s) {
		switch {
		case r == utf8.RuneError, r == 0:
			return false
		case r < 0x20 && !strings.ContainsRune("\t\n\r\f\v\x1b", r):
			return false
		case r >= 0x80 && r < 0xA0: // C1 controls: Latin-1 misread of binary
			return false
		case unicode.Is(unicode.Co, r): // private use: typical of mis-decoded binary
			return false
		}
	}
	return true
}
