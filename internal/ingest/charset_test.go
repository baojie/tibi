package ingest

import (
	"crypto/rand"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
)

func enc(t *testing.T, e encoding.Encoding, s string) []byte {
	t.Helper()
	b, err := e.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	zhHans = "题壁——唐宋时人在公共墙壁上写诗，任何路过的人都可以读。这是一个自托管的图床和粘贴板。\n第二行：中文测试。\n"
	zhHant = "題壁——唐宋時人在公共牆壁上寫詩，任何路過的人都可以讀。這是一個自託管的圖床。\n"
	ja     = "これは日本語のテキストです。東京は日本の首都です。今日はいい天気ですね。\n"
	ko     = "이것은 한국어 텍스트입니다. 서울은 한국의 수도입니다. 오늘 날씨가 좋네요.\n"
	fr     = "Café au lait, naïve résumé à la française. Où est la bibliothèque? Très bien.\n"
)

func TestTranscode(t *testing.T) {
	cases := []struct {
		name, text, charset string
		e                   encoding.Encoding
	}{
		{"gbk", zhHans, "GBK", simplifiedchinese.GBK},
		{"big5", zhHant, "Big5", traditionalchinese.Big5},
		{"sjis", ja, "Shift_JIS", japanese.ShiftJIS},
		{"euckr", ko, "EUC-KR", korean.EUCKR},
		{"latin1", fr, "ISO-8859-1", charmap.ISO8859_1},
		{"utf16le", "hello 题壁\n", "UTF-16LE", xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM)},
		{"utf16be", "hello 题壁\n", "UTF-16BE", xunicode.UTF16(xunicode.BigEndian, xunicode.UseBOM)},
	}
	for _, c := range cases {
		out, cs, ok := Transcode(enc(t, c.e, c.text))
		if !ok || string(out) != c.text {
			t.Errorf("%s: ok=%v out=%q", c.name, ok, out)
			continue
		}
		if cs != c.charset && !(c.name == "latin1" && cs == "windows-1252") {
			t.Errorf("%s: charset %q, want %q", c.name, cs, c.charset)
		}
	}
	// GB18030-only characters are reported as GB18030.
	gb := enc(t, simplifiedchinese.GB18030, zhHans+"𠀀\n")
	if out, cs, ok := Transcode(gb); !ok || cs != "GB18030" || string(out) != zhHans+"𠀀\n" {
		t.Errorf("gb18030: %v %q", ok, cs)
	}
	// Binary never becomes text.
	for i := 0; i < 50; i++ {
		b := make([]byte, 2000)
		rand.Read(b)
		if _, cs, ok := Transcode(b); ok {
			t.Fatalf("random bytes transcoded as %s", cs)
		}
	}
}
