package render

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// HighlightLimit is the largest text that is syntax highlighted; larger
// text is shown with line numbers but without colors.
const HighlightLimit = 256 << 10

// Code renders text as numbered lines, one <div id="L{n}"> each, with
// chroma token classes. Line numbers are empty links (#L{n}) drawn with a CSS
// counter so they are not part of copied text. It returns the HTML and the
// line count.
func Code(text, lang string) (template.HTML, int) {
	return CodeBlock(text, lang, "")
}

// CodeBlock is Code with line IDs prefixed (e.g. "f2-" → id="f2-L5"), so
// several blocks can share a page.
func CodeBlock(text, lang, prefix string) (template.HTML, int) {
	var lexer chroma.Lexer
	if lang != PlainText && lang != "" && len(text) <= HighlightLimit {
		lexer = lexers.Get(lang)
	}
	var lines [][]chroma.Token
	if lexer != nil {
		it, err := chroma.Coalesce(lexer).Tokenise(nil, text)
		if err == nil {
			lines = chroma.SplitTokensIntoLines(it.Tokens())
		}
	}
	if lines == nil {
		for _, l := range strings.SplitAfter(text, "\n") {
			lines = append(lines, []chroma.Token{{Type: chroma.Text, Value: l}})
		}
	}
	// A trailing newline does not start another line.
	if n := len(lines); n > 1 && tokensEmpty(lines[n-1]) {
		lines = lines[:n-1]
	}

	var b bytes.Buffer
	b.Grow(len(text) * 2)
	if prefix == "" {
		b.WriteString(`<div class="code chroma" id="code">`)
	} else {
		b.WriteString(`<div class="code chroma" data-prefix="` + template.HTMLEscapeString(prefix) + `">`)
	}
	for i, line := range lines {
		n := prefix + "L" + strconv.Itoa(i+1)
		b.WriteString(`<div id="` + n + `"><a href="#` + n + `"></a><span>`)
		for _, t := range line {
			v := strings.TrimSuffix(t.Value, "\n")
			if v == "" {
				continue
			}
			if cls := tokenClass(t.Type); cls != "" {
				b.WriteString(`<span class="` + cls + `">`)
				template.HTMLEscape(&b, []byte(v))
				b.WriteString(`</span>`)
			} else {
				template.HTMLEscape(&b, []byte(v))
			}
		}
		b.WriteString("\n</span></div>")
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String()), len(lines)
}

func tokensEmpty(ts []chroma.Token) bool {
	for _, t := range ts {
		if t.Value != "" {
			return false
		}
	}
	return true
}

func tokenClass(tt chroma.TokenType) string {
	for _, t := range []chroma.TokenType{tt, tt.SubCategory(), tt.Category()} {
		if c, ok := chroma.StandardTypes[t]; ok {
			return c
		}
	}
	return ""
}

// CSS returns the token stylesheet: a light theme with a dark override.
func CSS() []byte {
	f := chromahtml.New(chromahtml.WithClasses(true))
	var b bytes.Buffer
	f.WriteCSS(&b, styles.Get("github"))
	var dark bytes.Buffer
	f.WriteCSS(&dark, styles.Get("github-dark"))
	b.WriteString("\n@media (prefers-color-scheme: dark) {\n")
	b.Write(dark.Bytes())
	b.WriteString("}\n")
	return b.Bytes()
}
