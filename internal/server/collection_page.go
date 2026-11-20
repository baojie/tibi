package server

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"tibi/internal/id"
	"tibi/internal/render"
	"tibi/internal/store"
	"tibi/internal/units"
)

type collEntry struct {
	Item        *store.Item
	Name        string
	URL         string
	EmbedURL    template.URL
	ThumbURL    string
	DownloadURL string
	Size        string
	Text        *textView
	Anchor      string
}

type collView struct {
	Title   string
	Coll    *store.Collection
	URL     string
	Grid    bool
	Entries []collEntry
}

// handleCollectionPage renders /c/{id}: a thumbnail grid when every item is
// an image, otherwise the items stacked in order (multi-file paste).
func (s *Server) handleCollectionPage(w http.ResponseWriter, r *http.Request, cid string) {
	ctx := r.Context()
	err := store.ErrNotFound
	var c *store.Collection
	if id.Valid(cid) {
		c, err = s.store.GetCollection(ctx, cid)
	}
	var acc access
	if err == nil && c.Visibility == store.Private {
		if acc = s.requestAccess(r); !acc.privileged {
			err = store.ErrNotFound
		}
	} else if err == nil {
		acc = s.requestAccess(r)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	items, err := s.store.CollectionItems(ctx, c.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	vm := collView{Title: c.Title, Coll: c, URL: s.cfg.BaseURL + "/c/" + c.ID, Grid: true}
	if vm.Title == "" {
		vm.Title = strconv.Itoa(c.ItemCount) + " items"
	}
	for _, it := range items {
		if (it.Visibility == store.Private && !acc.privileged) || it.BurnAfterRead {
			continue
		}
		j := s.itemJSON(it)
		e := collEntry{
			Item: it, Name: downloadName(it), URL: j.URL, EmbedURL: template.URL(j.RawURL),
			ThumbURL: j.ThumbnailURL, DownloadURL: j.DownloadURL, Size: units.FormatBytes(it.Size),
			Anchor: "f" + strconv.Itoa(len(vm.Entries)+1),
		}
		if it.Title != "" {
			e.Name = it.Title
		}
		if it.Kind != store.KindImage {
			vm.Grid = false
		}
		if it.Kind == store.KindText {
			tv, err := s.loadTextBlock(ctx, it, true, e.Anchor+"-")
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				s.fail(w, err)
				return
			}
			if tv != nil && it.Lang == render.Markdown && !tv.Truncated {
				if html, err := s.markdownOf(r, it); err == nil {
					tv.Rendered, tv.IsMarkdown = html, true
				}
			}
			e.Text = tv
		}
		vm.Entries = append(vm.Entries, e)
	}
	if len(vm.Entries) == 0 {
		vm.Grid = false
	}
	s.writePage(w, "collection", vm, c.Visibility == store.Private)
}
