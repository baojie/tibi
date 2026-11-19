// Package disposition builds Content-Disposition headers.
package disposition

import (
	"net/url"
	"path"
	"strings"
)

// Attachment builds an attachment header carrying both an ASCII
// filename (for tools such as curl -J and wget that ignore RFC 5987) and the
// exact UTF-8 name in filename* (used by browsers).
func Attachment(name, fallback string) string {
	ascii := asciiName(name)
	if ascii != name && alnum(strings.TrimSuffix(ascii, path.Ext(ascii))) < 2 {
		ascii = fallback // e.g. an all-Chinese name: "_ _(_).txt" helps nobody
	}
	v := `attachment; filename="` + ascii + `"`
	if ascii != name {
		v += "; filename*=UTF-8''" + url.PathEscape(name)
	}
	return v
}

// asciiName replaces characters that are not safe in a quoted ASCII
// filename, collapsing runs into one underscore and keeping the extension.
func asciiName(name string) string {
	var b strings.Builder
	under := false
	for _, r := range name {
		ok := r > 0x20 && r < 0x7f && r != '"' && r != '\\' && r != '/' && r != ';' && r != '%'
		if ok || r == ' ' {
			b.WriteRune(r)
			under = false
		} else if !under {
			b.WriteByte('_')
			under = true
		}
	}
	return strings.TrimSpace(b.String())
}

func alnum(s string) int {
	n := 0
	for _, r := range s {
		if r < 0x80 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			n++
		}
	}
	return n
}
