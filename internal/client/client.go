// Package client is a Go client for the tibi /api/v1 API.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	RawURL      string `json:"raw_url"`
	DownloadURL string `json:"download_url"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	Width       *int   `json:"width,omitempty"`
	Height      *int   `json:"height,omitempty"`
	Markdown    string `json:"markdown"`
	Filename    string `json:"filename,omitempty"`
	Title       string `json:"title,omitempty"`
	Lang        string `json:"lang,omitempty"`
	Charset     string `json:"charset,omitempty"`
	Visibility  string `json:"visibility"`
	ExpiresAt   *int64 `json:"expires_at"`
	CreatedAt   int64  `json:"created_at"`
}

// Whoami mirrors the API Whoami schema.
type Whoami struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	QuotaBytes *int64   `json:"quota_bytes"`
}

// UploadOptions are optional upload parameters.
type UploadOptions struct {
	Filename string
	Title    string
	Source   string
	Kind     string
	Lang     string
	KeepMeta bool
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

// Upload sends body as a raw-body upload. size may be -1 if unknown.
// It returns the decoded item and the raw JSON response.
func (c *Client) Upload(ctx context.Context, body io.Reader, size int64, o UploadOptions) (*Item, json.RawMessage, error) {
	q := url.Values{}
	if o.Filename != "" {
		q.Set("filename", o.Filename)
	}
	if o.Title != "" {
		q.Set("title", o.Title)
	}
	if o.Source != "" {
		q.Set("source", o.Source)
	}
	if o.Kind != "" {
		q.Set("kind", o.Kind)
	}
	if o.Lang != "" {
		q.Set("lang", o.Lang)
	}
	if o.KeepMeta {
		q.Set("keep_meta", "1")
	}
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
