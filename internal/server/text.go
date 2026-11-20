package server

import (
	"bytes"
	"context"
	"html/template"
	"io"
	"net/http"
	"strconv"

	"tibi/internal/blob"
	"tibi/internal/render"
	"tibi/internal/store"
	"tibi/internal/units"
)

// textView is the rendered body of a text item.
type textView struct {
	Code       template.HTML // numbered, highlighted lines
	Rendered   template.HTML // Markdown only
	IsMarkdown bool
	Lines      int
	LangName   string
	Truncated  bool
	ShownSize  string
}

// loadTextView renders a text item. Burned items are not cached, so their
// content does not linger in memory.
func (s *Server) loadTextView(ctx context.Context, it *store.Item, cache bool) (*textView, error) {
	return s.loadTextBlock(ctx, it, cache, "")
}

// loadTextBlock renders a text item with line IDs prefixed by prefix.
func (s *Server) loadTextBlock(ctx context.Context, it *store.Item, cache bool, prefix string) (*textView, error) {
	limit := s.cfg.MaxTextBytes
	key := it.SHA256 + "|" + it.Lang + "|" + strconv.FormatInt(limit, 10) + "|" + prefix
	if v, ok := s.renderCache.Get(key); ok && cache {
		return v.(*textView), nil
	}
	f, err := s.blobs.Open(ctx, blob.Key(it.SHA256))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	tv := &textView{LangName: render.LangName(it.Lang)}
	if int64(len(data)) > limit {
		data = data[:limit]
		if i := bytes.LastIndexByte(data, '\n'); i > 0 {
			data = data[:i+1]
		}
		tv.Truncated = true
		tv.ShownSize = units.FormatBytes(int64(len(data)))
	}
	tv.Code, tv.Lines = render.CodeBlock(string(data), it.Lang, prefix)
	if it.Lang == render.Markdown && !tv.Truncated {
		if html, err := render.RenderMarkdown(data); err == nil {
			tv.Rendered, tv.IsMarkdown = html, true
		}
	}
	if cache {
		s.renderCache.Put(key, tv, len(tv.Code)+len(tv.Rendered))
	}
	return tv, nil
}

func (s *Server) handleChromaCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(s.chromaCSS)
}

// markdownOf renders a Markdown item (for collection pages).
func (s *Server) markdownOf(r *http.Request, it *store.Item) (template.HTML, error) {
	f, err := s.blobs.Open(r.Context(), blob.Key(it.SHA256))
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, s.cfg.MaxTextBytes))
	if err != nil {
		return "", err
	}
	return render.RenderMarkdown(data)
}
