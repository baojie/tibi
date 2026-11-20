package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ChunkSize is how much one resumable PATCH request sends.
const ChunkSize = 16 << 20

// Resumable describes a resumable upload of one local file.
type Resumable struct {
	Path     string
	Options  UploadOptions
	StateDir string                  // where upload URLs are kept between runs ("" = no resume)
	Progress func(sent, total int64) // called as bytes go out (may be nil)
	Resumed  func(offset int64)      // called when an earlier upload is continued
}

// stateKey identifies "this file, unchanged, to this server".
func (c *Client) stateKey(path string, st os.FileInfo) string {
	abs, _ := filepath.Abs(path)
	h := sha256.Sum256([]byte(c.Server + "\x00" + abs + "\x00" + strconv.FormatInt(st.Size(), 10) + "\x00" + st.ModTime().UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h[:16])
}

// UploadResumable uploads a file with the tus protocol, continuing an
// earlier interrupted upload of the same unchanged file if there is one.
func (c *Client) UploadResumable(ctx context.Context, u Resumable) (*Item, json.RawMessage, error) {
	f, err := os.Open(u.Path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	size := st.Size()
	var statePath string
	if u.StateDir != "" {
		if err := os.MkdirAll(u.StateDir, 0o700); err == nil {
			statePath = filepath.Join(u.StateDir, c.stateKey(u.Path, st))
		}
	}

	location, offset := "", int64(0)
	if statePath != "" {
		if b, err := os.ReadFile(statePath); err == nil {
			loc := strings.TrimSpace(string(b))
			if off, err := c.tusOffset(ctx, loc, size); err == nil {
				location, offset = loc, off
				if u.Resumed != nil {
					u.Resumed(off)
				}
			}
		}
	}
	if location == "" {
		if u.Options.Filename == "" {
			u.Options.Filename = filepath.Base(u.Path)
		}
		if location, err = c.tusCreate(ctx, size, u.Options); err != nil {
			return nil, nil, err
		}
		if statePath != "" {
			os.WriteFile(statePath, []byte(location), 0o600)
		}
	}

	var itemID string
	retries := 0
	for offset < size || size == 0 {
		n := min(int64(ChunkSize), size-offset)
		next, id, err := c.tusPatch(ctx, location, f, offset, n, size, u.Progress)
		if err != nil {
			var ae *Error
			if ctx.Err() != nil || (errors.As(err, &ae) && ae.Status < 500) || retries >= 5 {
				return nil, nil, err
			}
			// Transient failure: find out what arrived and continue.
			retries++
			time.Sleep(time.Duration(retries) * time.Second)
			if off, herr := c.tusOffset(ctx, location, size); herr == nil {
				offset = off
			}
			continue
		}
		retries = 0
		offset, itemID = next, id
		if size == 0 {
			break
		}
	}
	if statePath != "" {
		os.Remove(statePath)
	}
	return c.uploadItem(ctx, location, itemID)
}

func (c *Client) tusRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return req, nil
}

// tusError turns a tus (text/plain) or tibi (JSON) error response into an Error.
func tusError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var env struct {
		Error Error `json:"error"`
	}
	if json.Unmarshal(b, &env) == nil && env.Error.Message != "" {
		env.Error.Status = resp.StatusCode
		return &env.Error
	}
	msg := strings.TrimSpace(string(b))
	if i := strings.Index(msg, ": "); strings.HasPrefix(msg, "ERR_") && i > 0 {
		msg = msg[i+2:]
	}
	return &Error{Status: resp.StatusCode, Message: msg}
}

func (c *Client) tusCreate(ctx context.Context, size int64, o UploadOptions) (string, error) {
	req, err := c.tusRequest(ctx, http.MethodPost, c.Server+"/api/v1/uploads/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Upload-Length", strconv.FormatInt(size, 10))
	q := o.query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var meta []string
	for _, k := range keys {
		meta = append(meta, k+" "+base64.StdEncoding.EncodeToString([]byte(q.Get(k))))
	}
	req.Header.Set("Upload-Metadata", strings.Join(meta, ","))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", tusError(resp)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", errors.New("server did not return an upload URL")
	}
	if strings.HasPrefix(loc, "/") {
		loc = c.Server + loc
	}
	return loc, nil
}

// tusOffset asks how much of an upload the server has.
func (c *Client) tusOffset(ctx context.Context, location string, size int64) (int64, error) {
	req, err := c.tusRequest(ctx, http.MethodHead, location, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return 0, &Error{Status: resp.StatusCode, Message: "upload not found"}
	}
	if l := resp.Header.Get("Upload-Length"); l != strconv.FormatInt(size, 10) {
		return 0, errors.New("upload length differs")
	}
	return strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
}

type progressReader struct {
	r        io.Reader
	sent     int64
	total    int64
	progress func(int64, int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.sent += int64(n)
	if p.progress != nil && n > 0 {
		p.progress(p.sent, p.total)
	}
	return n, err
}

// tusPatch sends n bytes from offset; it returns the new offset and, when
// the upload completed, the item ID the server reported.
func (c *Client) tusPatch(ctx context.Context, location string, f *os.File, offset, n, size int64, progress func(int64, int64)) (int64, string, error) {
	body := &progressReader{r: io.NewSectionReader(f, offset, n), sent: offset, total: size, progress: progress}
	req, err := c.tusRequest(ctx, http.MethodPatch, location, body)
	if err != nil {
		return offset, "", err
	}
	req.ContentLength = n
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return offset, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return offset, "", tusError(resp)
	}
	next, err := strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		return offset, "", fmt.Errorf("bad Upload-Offset in response")
	}
	return next, resp.Header.Get("Tibi-Item-Id"), nil
}

// uploadItem fetches the item a finished upload became.
func (c *Client) uploadItem(ctx context.Context, location, _ string) (*Item, json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(location, "/")+"/item", nil)
	if err != nil {
		return nil, nil, err
	}
	var it Item
	raw, err := c.do(req, &it)
	if err != nil {
		return nil, nil, err
	}
	return &it, raw, nil
}
