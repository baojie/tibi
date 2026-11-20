package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/baojie/tibi/internal/client"
	"github.com/baojie/tibi/internal/e2e"
)

// Toolbox implements the tools on top of the tibi API client.
type Toolbox struct {
	// Client returns a logged-in API client (read from the CLI config each
	// call, so `tibi login` takes effect without restarting the agent).
	Client func() (*client.Client, error)
	// MaxFileBytes bounds upload_file (the server enforces its own limit).
	MaxFileBytes int64
}

type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var (
	expiresProp = str("How long the link works: e.g. 10m, 1h, 7d, 30d, or never. Default: the user's configured default.")
	privateProp = map[string]any{"type": "boolean", "description": "Only viewable with a token that can read private items (or a share link). Default false: anyone with the URL can view."}
	titleProp   = str("Title shown on the page.")
)

var tools = []tool{
	{
		Name:  "upload_file",
		Title: "Upload a file",
		Description: "Upload a local file (image, video, PDF, log, archive, anything) to tibi and get a shareable URL. " +
			"Images also get a direct embeddable link and Markdown.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": str("Path of the local file (absolute, or relative to the working directory)."), "title": titleProp, "expires": expiresProp, "private": privateProp},
			"required":   []string{"path"},
		},
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": true},
	},
	{
		Name:        "paste_text",
		Title:       "Paste text or code",
		Description: "Publish text or code as a paste with syntax highlighting and get a shareable URL.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":  str("The text or code."),
				"lang":  str("Language for highlighting, e.g. go, python, markdown, diff (default: detected)."),
				"title": titleProp, "expires": expiresProp, "private": privateProp,
				"encrypt": map[string]any{"type": "boolean", "description": "End-to-end encrypt: the server stores only ciphertext and the returned link carries the key after #k=. Use for secrets (credentials, logs with personal data). The title and lang are NOT encrypted: keep secrets out of them. The returned link contains the key, so treat it as the secret itself. Default false."},
			},
			"required": []string{"text"},
		},
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": true},
	},
	{
		Name:        "list_items",
		Title:       "List recent items",
		Description: "List the most recent uploads, newest first.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "How many (default 20)."},
				"kind":  map[string]any{"type": "string", "enum": []string{"image", "video", "audio", "text", "file"}, "description": "Only this kind."},
			},
		},
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
	},
	{
		Name:        "search_items",
		Title:       "Search items",
		Description: "Find uploads whose title, file name or text contains every word of the query (Chinese and code identifiers work too).",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": str("Words to search for.")},
			"required":   []string{"query"},
		},
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
	},
	{
		Name:        "delete_item",
		Title:       "Delete an item",
		Description: "Delete an upload by ID or URL. Its links stop working at once.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": str("The item ID (8 characters) or its URL.")},
			"required":   []string{"id"},
		},
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
	},
}

// List returns the tool definitions.
func (t *Toolbox) List() []tool { return tools }

// Has reports whether name is a tool.
func (t *Toolbox) Has(name string) bool {
	for _, x := range tools {
		if x.Name == name {
			return true
		}
	}
	return false
}

// result is an MCP CallToolResult.
type result struct {
	Content           []map[string]string `json:"content"`
	StructuredContent any                 `json:"structuredContent,omitempty"`
	IsError           bool                `json:"isError"`
}

func ok(text string, structured any) result {
	return result{Content: []map[string]string{{"type": "text", "text": text}}, StructuredContent: structured}
}

// fail is a tool error: reported to the model as a result, so it can react,
// not as a protocol error.
func fail(format string, args ...any) result {
	return result{Content: []map[string]string{{"type": "text", "text": fmt.Sprintf(format, args...)}}, IsError: true}
}

type args struct {
	Path    string `json:"path"`
	Text    string `json:"text"`
	Lang    string `json:"lang"`
	Title   string `json:"title"`
	Expires string `json:"expires"`
	Private bool   `json:"private"`
	Encrypt bool   `json:"encrypt"`
	Limit   int    `json:"limit"`
	Kind    string `json:"kind"`
	Query   string `json:"query"`
	ID      string `json:"id"`
}

// Call runs a tool. It never panics on bad input or server errors.
func (t *Toolbox) Call(ctx context.Context, name string, raw json.RawMessage) (res result) {
	defer func() {
		if r := recover(); r != nil {
			res = fail("internal error in %s: %v", name, r)
		}
	}()
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return fail("invalid arguments: %v", err)
		}
	}
	cl, err := t.Client()
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	switch name {
	case "upload_file":
		return t.uploadFile(ctx, cl, a)
	case "paste_text":
		if strings.TrimSpace(a.Text) == "" {
			return fail("text is empty")
		}
		if a.Encrypt {
			return pasteEncrypted(ctx, cl, a)
		}
		it, _, err := cl.Upload(ctx, strings.NewReader(a.Text), int64(len(a.Text)), client.UploadOptions{
			Title: a.Title, Lang: a.Lang, Kind: "text", Expires: a.Expires, Private: a.Private, Source: "mcp",
		})
		if err != nil {
			return apiFail("paste", err)
		}
		return itemResult(it)
	case "list_items":
		p, err := cl.List(ctx, client.ListOptions{Limit: a.Limit, Kind: a.Kind})
		if err != nil {
			return apiFail("list", err)
		}
		return listResult(p.Items, "No items yet.")
	case "search_items":
		if strings.TrimSpace(a.Query) == "" {
			return fail("query is empty")
		}
		p, err := cl.Search(ctx, a.Query, client.ListOptions{Limit: 20})
		if err != nil {
			return apiFail("search", err)
		}
		items := make([]client.Item, len(p.Items))
		for i, h := range p.Items {
			items[i] = h.Item
		}
		return listResult(items, "No matches.")
	case "delete_item":
		id := a.ID
		if i := strings.LastIndexByte(id, '/'); i >= 0 {
			id = id[i+1:]
		}
		id, _, _ = strings.Cut(id, ".")
		if id == "" {
			return fail("id is required")
		}
		if err := cl.Delete(ctx, id); err != nil {
			return apiFail("delete", err)
		}
		return ok("Deleted "+id+".", map[string]any{"deleted": id})
	}
	return fail("unknown tool %s", name)
}

func (t *Toolbox) uploadFile(ctx context.Context, cl *client.Client, a args) result {
	if a.Path == "" {
		return fail("path is required")
	}
	p := a.Path
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return fail("cannot open %s: %v", a.Path, errors.Unwrap(err))
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return fail("%s is not a regular file", a.Path)
	}
	if t.MaxFileBytes > 0 && st.Size() > t.MaxFileBytes {
		return fail("%s is too large (%d bytes, limit %d)", a.Path, st.Size(), t.MaxFileBytes)
	}
	it, _, err := cl.Upload(ctx, f, st.Size(), client.UploadOptions{
		Filename: filepath.Base(p), Title: a.Title, Expires: a.Expires, Private: a.Private, Source: "mcp",
	})
	if err != nil {
		return apiFail("upload", err)
	}
	return itemResult(it)
}

// apiFail explains API errors in words a model can act on.
func apiFail(what string, err error) result {
	var ae *client.Error
	if errors.As(err, &ae) {
		switch ae.Status {
		case 401:
			return fail("%s failed: the tibi token is missing, invalid or revoked (run `tibi login`): %s", what, ae.Message)
		case 403:
			return fail("%s failed: the tibi token lacks permission: %s", what, ae.Message)
		case 413:
			return fail("%s failed: too large: %s", what, ae.Message)
		case 404:
			return fail("%s failed: not found", what)
		}
		return fail("%s failed: %v", what, err)
	}
	return fail("%s failed: cannot reach the tibi server: %v", what, err)
}

func itemResult(it *client.Item) result {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n", it.URL)
	if it.Kind == "image" || it.Kind == "video" || it.Kind == "audio" {
		fmt.Fprintf(&b, "direct link: %s\n", it.RawURL)
	}
	if it.Kind == "image" {
		fmt.Fprintf(&b, "markdown: %s\n", it.Markdown)
	}
	fmt.Fprintf(&b, "kind: %s, %d bytes", it.Kind, it.Size)
	if it.Visibility == "private" {
		b.WriteString(", private")
	}
	if it.ExpiresAt != nil {
		fmt.Fprintf(&b, ", expires %s", time.Unix(*it.ExpiresAt, 0).UTC().Format(time.RFC3339))
	}
	return ok(b.String(), map[string]any{
		"id": it.ID, "url": it.URL, "raw_url": it.RawURL, "markdown": it.Markdown, "kind": it.Kind,
		"size": it.Size, "visibility": it.Visibility, "expires_at": it.ExpiresAt,
	})
}

func listResult(items []client.Item, empty string) result {
	if len(items) == 0 {
		return ok(empty, map[string]any{"items": []any{}})
	}
	var b strings.Builder
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		name := it.Title
		if name == "" {
			name = it.Filename
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s\n", it.URL, it.Kind, time.Unix(it.CreatedAt, 0).UTC().Format("2006-01-02 15:04"), name)
		out = append(out, map[string]any{"id": it.ID, "url": it.URL, "kind": it.Kind, "name": name, "created_at": it.CreatedAt})
	}
	return ok(strings.TrimRight(b.String(), "\n"), map[string]any{"items": out})
}

// pasteEncrypted encrypts locally; only ciphertext reaches the server, and
// the key is only in the returned link.
func pasteEncrypted(ctx context.Context, cl *client.Client, a args) result {
	key, frag := e2e.NewKey()
	blob, err := e2e.Encrypt(key, "", e2e.Header{Name: "paste.txt", Type: "text/plain; charset=utf-8", Lang: a.Lang}, []byte(a.Text))
	if err != nil {
		return fail("encrypt: %v", err)
	}
	it, _, err := cl.Upload(ctx, bytes.NewReader(blob), int64(len(blob)), client.UploadOptions{
		Title: a.Title, Lang: a.Lang, Kind: "encrypted", Expires: a.Expires, Private: a.Private, Source: "mcp",
	})
	if err != nil {
		return apiFail("paste", err)
	}
	link := it.URL + "#k=" + frag
	text := link + "\nend-to-end encrypted: anyone with this full link (including the part after #) can read it; the server cannot."
	return ok(text, map[string]any{"id": it.ID, "url": link, "kind": it.Kind, "size": it.Size, "visibility": it.Visibility, "expires_at": it.ExpiresAt, "encrypted": true})
}
