// Package client is a Go client for the tibi /api/v1 API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client talks to a tibi server.
type Client struct {
	Server string // base URL, e.g. https://tibi.example.com
	Token  string
	HTTP   *http.Client
}

// New returns a client.
func New(server, token string) *Client {
	return &Client{Server: strings.TrimRight(server, "/"), Token: token, HTTP: http.DefaultClient}
}

// Error is an API error response.
type Error struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// Item mirrors the API Item schema.
type Item struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	URL           string `json:"url"`
	RawURL        string `json:"raw_url"`
	DownloadURL   string `json:"download_url"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	Width         *int   `json:"width,omitempty"`
	Height        *int   `json:"height,omitempty"`
	Markdown      string `json:"markdown"`
	Filename      string `json:"filename,omitempty"`
	Title         string `json:"title,omitempty"`
	Lang          string `json:"lang,omitempty"`
	Charset       string `json:"charset,omitempty"`
	Visibility    string `json:"visibility"`
	Slug          string `json:"slug,omitempty"`
	SlugURL       string `json:"slug_url,omitempty"`
	CollectionID  string `json:"collection_id,omitempty"`
	CollectionURL string `json:"collection_url,omitempty"`
	Position      *int   `json:"position,omitempty"`
	ForkedFrom    string `json:"forked_from,omitempty"`
	BurnAfterRead bool   `json:"burn_after_read"`
	Source        string `json:"source,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	ViewCount     int64  `json:"view_count"`
	ExpiresAt     *int64 `json:"expires_at"`
	CreatedAt     int64  `json:"created_at"`
}

// Whoami mirrors the API Whoami schema.
type Whoami struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	QuotaBytes *int64   `json:"quota_bytes"`
}

// UploadOptions are optional upload parameters.
type UploadOptions struct {
	Filename  string
	Title     string
	Source    string
	SourceURL string // where the content came from (http(s) URL)
	Kind      string
	Lang      string
	KeepMeta  bool
	Expires   string // e.g. "1h", "7d", "never"; "" = server default
	Private   bool
	Slug      string
	Burn      bool
}

func (c *Client) do(req *http.Request, out any) (json.RawMessage, error) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Error Error `json:"error"`
		}
		_ = json.Unmarshal(body, &env)
		env.Error.Status = resp.StatusCode
		return nil, &env.Error
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}
	return body, nil
}

// Whoami verifies the token.
func (c *Client) Whoami(ctx context.Context) (*Whoami, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/v1/whoami", nil)
	if err != nil {
		return nil, err
	}
	var w Whoami
	_, err = c.do(req, &w)
	return &w, err
}

func (o UploadOptions) query() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{
		"filename": o.Filename, "title": o.Title, "source": o.Source, "kind": o.Kind,
		"lang": o.Lang, "expires": o.Expires, "slug": o.Slug, "source_url": o.SourceURL,
	} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if o.KeepMeta {
		q.Set("keep_meta", "1")
	}
	if o.Private {
		q.Set("visibility", "private")
	}
	if o.Burn {
		q.Set("burn_after_read", "1")
	}
	return q
}

// Upload sends body as a raw-body upload. size may be -1 if unknown.
// It returns the decoded item and the raw JSON response.
func (c *Client) Upload(ctx context.Context, body io.Reader, size int64, o UploadOptions) (*Item, json.RawMessage, error) {
	q := o.query()
	u := c.Server + "/api/v1/items"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var it Item
	raw, err := c.do(req, &it)
	if err != nil {
		return nil, nil, err
	}
	return &it, raw, nil
}

// ListOptions filter List.
type ListOptions struct {
	Limit  int
	Cursor string
	Kind   string
	Source string
}

// ListPage is one page of items.
type ListPage struct {
	Items      []Item  `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// List returns items, newest first.
func (c *Client) List(ctx context.Context, o ListOptions) (*ListPage, error) {
	q := url.Values{}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	for k, v := range map[string]string{"cursor": o.Cursor, "kind": o.Kind, "source": o.Source} {
		if v != "" {
			q.Set(k, v)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/v1/items?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var p ListPage
	_, err = c.do(req, &p)
	return &p, err
}

// Get returns an item's metadata and the raw JSON.
func (c *Client) Get(ctx context.Context, id string) (*Item, json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/v1/items/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, nil, err
	}
	var it Item
	raw, err := c.do(req, &it)
	return &it, raw, err
}

// Patch updates metadata fields (title, lang, visibility, expires, slug).
func (c *Client) Patch(ctx context.Context, id string, fields map[string]string) (*Item, error) {
	body, _ := json.Marshal(fields)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.Server+"/api/v1/items/"+url.PathEscape(id), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var it Item
	_, err = c.do(req, &it)
	return &it, err
}

// Delete deletes an item.
func (c *Client) Delete(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Server+"/api/v1/items/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	_, err = c.do(req, nil)
	return err
}

// SearchHit is an item with the matching line of its text.
type SearchHit struct {
	Item
	Snippet string `json:"snippet,omitempty"`
}

// SearchPage is one page of search results.
type SearchPage struct {
	Items      []SearchHit `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

// Search finds items whose title, filename or text contain every word of q.
func (c *Client) Search(ctx context.Context, q string, o ListOptions) (*SearchPage, error) {
	v := url.Values{"q": {q}}
	if o.Limit > 0 {
		v.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Cursor != "" {
		v.Set("cursor", o.Cursor)
	}
	if o.Kind != "" {
		v.Set("kind", o.Kind)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/v1/search?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var p SearchPage
	_, err = c.do(req, &p)
	return &p, err
}

// Collection mirrors the API Collection schema.
type Collection struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	Visibility string `json:"visibility"`
	ItemCount  int    `json:"item_count"`
	ExpiresAt  *int64 `json:"expires_at"`
	CreatedAt  int64  `json:"created_at"`
	Items      []Item `json:"items,omitempty"`
}

// CollectionUpload is the response of an upload into a collection.
type CollectionUpload struct {
	Collection Collection `json:"collection"`
	Items      []Item     `json:"items"`
}

// File is one part of a multipart upload.
type File struct {
	Name string
	Body io.Reader
}

// UploadAlbum uploads files in one multipart request into a new collection
// titled collTitle (o.Title is ignored for items).
func (c *Client) UploadAlbum(ctx context.Context, files []File, collTitle string, o UploadOptions) (*CollectionUpload, json.RawMessage, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			fields := o.query()
			fields.Del("filename")
			fields.Del("title")
			fields.Set("collection", "new")
			if collTitle != "" {
				fields.Set("collection_title", collTitle)
			}
			for k, vs := range fields {
				if err := mw.WriteField(k, vs[0]); err != nil {
					return err
				}
			}
			for _, f := range files {
				w, err := mw.CreateFormFile("file", f.Name)
				if err != nil {
					return err
				}
				if _, err := io.Copy(w, f.Body); err != nil {
					return err
				}
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+"/api/v1/items", pr)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out CollectionUpload
	raw, err := c.do(req, &out)
	if err != nil {
		return nil, nil, err
	}
	return &out, raw, nil
}

// Fork uploads body as a new version of item id.
func (c *Client) Fork(ctx context.Context, id string, body io.Reader, size int64, o UploadOptions) (*Item, json.RawMessage, error) {
	u := c.Server + "/api/v1/items/" + url.PathEscape(id) + "/fork"
	if q := o.query(); len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	var it Item
	raw, err := c.do(req, &it)
	if err != nil {
		return nil, nil, err
	}
	return &it, raw, nil
}

// Raw downloads an item's content (with the token, so private items work).
func (c *Client) Raw(ctx context.Context, it *Item, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/"+url.PathEscape(it.ID)+"/raw", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &Error{Status: resp.StatusCode, Message: "cannot fetch item content"}
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// DeleteCollection deletes a collection and its items.
func (c *Client) DeleteCollection(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Server+"/api/v1/collections/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	_, err = c.do(req, nil)
	return err
}

// ShareLink is a signed, time-limited URL.
type ShareLink struct {
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
}

// Share returns a signed link to item id valid for ttl (e.g. "1h"; "" = 1h).
func (c *Client) Share(ctx context.Context, id, ttl string) (*ShareLink, error) {
	u := c.Server + "/api/v1/items/" + url.PathEscape(id) + "/share-link"
	if ttl != "" {
		u += "?ttl=" + url.QueryEscape(ttl)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, err
	}
	var l ShareLink
	_, err = c.do(req, &l)
	return &l, err
}
