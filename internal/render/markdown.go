package render

import (
	"bytes"
	"html/template"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// md renders Markdown with GFM. Raw HTML is never passed through (goldmark
// omits it unless WithUnsafe is set) and dangerous link schemes are dropped.
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		highlighting.NewHighlighting(
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		),
	),
	goldmark.WithRendererOptions(html.WithXHTML()),
)

// RenderMarkdown converts Markdown source to safe HTML.
func RenderMarkdown(src []byte) (template.HTML, error) {
	var b bytes.Buffer
	if err := md.Convert(src, &b); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil
}
