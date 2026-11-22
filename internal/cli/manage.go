package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/baojie/tibi/internal/client"
	"github.com/baojie/tibi/internal/id"
	"github.com/baojie/tibi/internal/units"
)

// collectionID recognizes a collection URL (…/c/{id}) or "c/{id}".
func collectionID(arg string) (string, bool) {
	s := strings.TrimSpace(arg)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		s = u.Path
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) == 2 && parts[0] == "c" && id.Valid(parts[1]) {
		return parts[1], true
	}
	return "", false
}

// itemID accepts an ID or any item URL (/{id}, /{id}.{ext}, /{id}/raw).
func itemID(arg string) (string, error) {
	s := strings.TrimSpace(arg)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 && parts[0] == "s" {
			return "", fmt.Errorf("%s is a slug link; use the item ID or URL", arg)
		}
		s = parts[0]
	}
	s, _, _ = strings.Cut(s, ".")
	if !id.Valid(s) {
		return "", fmt.Errorf("%q is not an item ID or URL", arg)
	}
	return s, nil
}

func (a *App) ls(ctx context.Context, args []string) error {
	fs := a.flagSet("ls")
	n := fs.IntP("number", "n", 20, "how many items")
	kind := fs.String("kind", "", "only this kind (image, text, file, …)")
	source := fs.String("source", "", "only items from this source (cli, chrome, …)")
	asJSON := fs.Bool("json", false, "print one JSON object per line")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *n < 1 {
		return usageError{"-n must be at least 1"}
	}
	cl, _, err := a.client()
	if err != nil {
		return err
	}
	var items []client.Item
	cursor := ""
	for len(items) < *n {
		page, err := cl.List(ctx, client.ListOptions{Limit: min(*n-len(items), 100), Cursor: cursor, Kind: *kind, Source: *source})
		if err != nil {
			return err
		}
		items = append(items, page.Items...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if *asJSON {
		enc := json.NewEncoder(a.Stdout)
		for _, it := range items {
			enc.Encode(it)
		}
		return nil
	}
	if len(items) == 0 {
		fmt.Fprintln(a.Stderr, "no items")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	for _, it := range items {
		// The free-form label goes last: wide (CJK) characters would
		// otherwise misalign the columns after it.
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", it.URL, it.Kind, units.FormatBytes(it.Size),
			ago(it.CreatedAt), orDash(flags(it)), label(it))
	}
	return tw.Flush()
}

func (a *App) search(ctx context.Context, args []string) error {
	fs := a.flagSet("search")
	n := fs.IntP("number", "n", 20, "how many results")
	kind := fs.String("kind", "", "only this kind (image, text, file, …)")
	asJSON := fs.Bool("json", false, "print one JSON object per line")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	q := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(q) == "" {
		return usageError{"usage: tibi search QUERY"}
	}
	cl, _, err := a.client()
	if err != nil {
		return err
	}
	var hits []client.SearchHit
	cursor := ""
	for len(hits) < *n {
		page, err := cl.Search(ctx, q, client.ListOptions{Limit: min(*n-len(hits), 100), Cursor: cursor, Kind: *kind})
		if err != nil {
			return err
		}
		hits = append(hits, page.Items...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if *asJSON {
		enc := json.NewEncoder(a.Stdout)
		for _, h := range hits {
			enc.Encode(h)
		}
		return nil
	}
	if len(hits) == 0 {
		fmt.Fprintln(a.Stderr, "no matches")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	for _, h := range hits {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", h.URL, h.Kind, ago(h.CreatedAt), label(h.Item))
		if h.Snippet != "" {
			fmt.Fprintf(tw, "    %s\n", h.Snippet) // no tabs: not part of the columns
		}
	}
	return tw.Flush()
}

func label(it client.Item) string {
	switch {
	case it.Title != "":
		return it.Title
	case it.Filename != "":
		return it.Filename
	}
	return "-"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func flags(it client.Item) string {
	var f []string
	if it.Visibility == "private" {
		f = append(f, "private")
	}
	if it.BurnAfterRead {
		f = append(f, "burn")
	}
	if it.ExpiresAt != nil {
		f = append(f, "expires "+until(*it.ExpiresAt))
	}
	if it.Slug != "" {
		f = append(f, "/s/"+it.Slug)
	}
	return strings.Join(f, " ")
}

func ago(unix int64) string {
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return time.Unix(unix, 0).Format("2006-01-02")
}

func until(unix int64) string {
	d := time.Until(time.Unix(unix, 0)).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "in <1m"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	}
	return "in " + fmt.Sprint(int(d.Hours()/24)) + "d"
}

func (a *App) info(ctx context.Context, args []string) error {
	fs := a.flagSet("info")
	asJSON := fs.Bool("json", false, "print the API response")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() != 1 {
		return usageError{"usage: tibi info ID"}
	}
	iid, err := itemID(fs.Arg(0))
	if err != nil {
		return usageError{err.Error()}
	}
	cl, _, err := a.client()
	if err != nil {
		return err
	}
	it, raw, err := cl.Get(ctx, iid)
	if err != nil {
		return err
	}
	if *asJSON {
		a.Stdout.Write(raw)
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s:\t%s\n", k, v)
		}
	}
	row("url", it.URL)
	row("raw", it.RawURL)
	row("slug", it.SlugURL)
	row("kind", it.Kind)
	row("type", it.Mime)
	row("size", units.FormatBytes(it.Size))
	if it.Width != nil {
		row("dimensions", fmt.Sprintf("%d×%d", *it.Width, *it.Height))
	}
	row("filename", it.Filename)
	row("title", it.Title)
	row("lang", it.Lang)
	row("charset", it.Charset)
	row("visibility", it.Visibility)
	if it.BurnAfterRead {
		row("burn after read", "yes")
	}
	row("source", it.Source)
	row("source url", it.SourceURL)
	row("created", time.Unix(it.CreatedAt, 0).Format(time.RFC3339))
	if it.ExpiresAt != nil {
		row("expires", time.Unix(*it.ExpiresAt, 0).Format(time.RFC3339)+" ("+until(*it.ExpiresAt)+")")
	} else {
		row("expires", "never")
	}
	return tw.Flush()
}

func (a *App) rm(ctx context.Context, args []string) error {
	fs := a.flagSet("rm")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() == 0 {
		return usageError{"usage: tibi rm ID..."}
	}
	cl, _, err := a.client()
	if err != nil {
		return err
	}
	failed := 0
	for _, arg := range fs.Args() {
		var err error
		var iid string
		if cid, ok := collectionID(arg); ok {
			iid = "collection " + cid
			err = cl.DeleteCollection(ctx, cid)
		} else if iid, err = itemID(arg); err == nil {
			err = cl.Delete(ctx, iid)
		}
		if err != nil {
			a.errf("%s: %v", arg, err)
			failed++
			continue
		}
		fmt.Fprintf(a.Stderr, "deleted %s\n", iid)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d deletions failed", failed, fs.NArg())
	}
	return nil
}

func (a *App) open(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError{"usage: tibi open ID"}
	}
	iid, err := itemID(args[0])
	if err != nil {
		return usageError{err.Error()}
	}
	cl, _, err := a.client()
	if err != nil {
		return err
	}
	it, _, err := cl.Get(ctx, iid) // confirms it exists before opening
	if err != nil {
		return err
	}
	if a.OpenURL != nil {
		return a.OpenURL(it.URL)
	}
	return openBrowser(it.URL)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

func (a *App) slugCmd(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return usageError{`usage: tibi slug ID NAME   (NAME "" removes the slug)`}
	}
	iid, err := itemID(args[0])
	if err != nil {
		return usageError{err.Error()}
	}
	cl, _, err := a.client()
	if err != nil {
		return err
	}
	it, err := cl.Patch(ctx, iid, map[string]string{"slug": args[1]})
	if err != nil {
		return err
	}
	if it.SlugURL == "" {
		fmt.Fprintln(a.Stderr, "slug removed")
		return nil
	}
	fmt.Fprintln(a.Stdout, it.SlugURL)
	return nil
}

func (a *App) share(ctx context.Context, args []string) error {
	fs := a.flagSet("share")
	ttl := fs.String("ttl", "1h", "how long the link works, e.g. 10m, 1h, 7d")
	noCopy := fs.Bool("no-copy", false, "do not copy to clipboard")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() != 1 {
		return usageError{"usage: tibi share ID [--ttl 1h]"}
	}
	iid, err := itemID(fs.Arg(0))
	if err != nil {
		return usageError{err.Error()}
	}
	cl, cfg, err := a.client()
	if err != nil {
		return err
	}
	l, err := cl.Share(ctx, iid, *ttl)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, l.URL)
	fmt.Fprintf(a.Stderr, "valid until %s\n", time.Unix(l.ExpiresAt, 0).Format(time.RFC3339))
	a.copyResult(cfg, &outFlags{nocpy: *noCopy}, []string{l.URL})
	return nil
}
