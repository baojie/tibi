package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baojie/tibi/internal/client"
	"github.com/baojie/tibi/internal/config"
	"github.com/baojie/tibi/internal/e2e"
	"github.com/baojie/tibi/internal/mcp"
	"github.com/baojie/tibi/internal/testutil"
	"github.com/baojie/tibi/internal/testutil/testimg"
)

// harness is a minimal MCP client talking to the server over pipes.
type harness struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Scanner
	id  int
}

// start runs a server whose toolbox offers every tool and may upload from
// the temp directory (where tests keep their files).
func start(t *testing.T, mk func() (*client.Client, error)) *harness {
	t.Helper()
	return startWith(t, &mcp.Toolbox{Client: mk, MaxFileBytes: 1 << 20,
		Paths: mcp.PathPolicy{TempDir: os.TempDir()}, AllowDelete: true})
}

func startWith(t *testing.T, tb *mcp.Toolbox) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := &mcp.Server{Name: "tibi", Version: "test", Tools: tb}
	go func() {
		srv.Serve(context.Background(), inR, outW)
		outW.Close()
	}()
	t.Cleanup(func() { inW.Close() })
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	return &harness{t: t, in: inW, out: sc}
}

func (h *harness) send(v any) {
	b, _ := json.Marshal(v)
	h.in.Write(append(b, '\n'))
}

type reply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (h *harness) recv() reply {
	h.t.Helper()
	if !h.out.Scan() {
		h.t.Fatal("server closed the stream")
	}
	var r reply
	if err := json.Unmarshal(h.out.Bytes(), &r); err != nil {
		h.t.Fatalf("bad JSON from server: %s", h.out.Bytes())
	}
	return r
}

func (h *harness) call(method string, params any) reply {
	h.t.Helper()
	h.id++
	h.send(map[string]any{"jsonrpc": "2.0", "id": h.id, "method": method, "params": params})
	r := h.recv()
	if string(r.ID) != strings.TrimSpace(string(mustJSON(h.id))) {
		h.t.Fatalf("reply id %s for request %d", r.ID, h.id)
	}
	return r
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Structured map[string]any `json:"structuredContent"`
	IsError    bool           `json:"isError"`
}

func (h *harness) tool(name string, args any) toolResult {
	h.t.Helper()
	r := h.call("tools/call", map[string]any{"name": name, "arguments": args})
	if r.Error != nil {
		h.t.Fatalf("%s: protocol error %+v", name, r.Error)
	}
	var tr toolResult
	json.Unmarshal(r.Result, &tr)
	return tr
}

func (h *harness) initialize() {
	h.t.Helper()
	r := h.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if r.Error != nil {
		h.t.Fatal(r.Error)
	}
	h.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func fetch(t *testing.T, url string) (int, []byte) {
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func connected(e *testutil.Env, token string) func() (*client.Client, error) {
	return func() (*client.Client, error) { return client.New(e.Srv.URL, token), nil }
}

func TestProtocol(t *testing.T) {
	e := testutil.New(t, nil)
	h := start(t, connected(e, e.Token("t")))
	r := h.call("initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "old", "version": "1"}})
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
		Instructions    string         `json:"instructions"`
	}
	json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != "2024-11-05" || init.Capabilities["tools"] == nil || init.ServerInfo["name"] != "tibi" || init.Instructions == "" {
		t.Fatalf("initialize: %s", r.Result)
	}
	// Notifications get no reply: the next reply belongs to ping.
	h.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if r := h.call("ping", nil); r.Error != nil || string(r.Result) != "{}" {
		t.Fatalf("ping: %+v", r)
	}
	r = h.call("tools/list", nil)
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	json.Unmarshal(r.Result, &list)
	names := []string{}
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" || tl.InputSchema["type"] != "object" {
			t.Errorf("tool %s: incomplete definition", tl.Name)
		}
	}
	if strings.Join(names, ",") != "upload_file,paste_text,list_items,search_items,delete_item" {
		t.Fatalf("tools: %v", names)
	}
	// JSON-RPC errors.
	if r := h.call("nope/nope", nil); r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("unknown method: %+v", r)
	}
	if r := h.call("tools/call", map[string]any{"name": "format_disk"}); r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("unknown tool: %+v", r)
	}
	h.in.Write([]byte("{not json\n"))
	if r := h.recv(); r.Error == nil || r.Error.Code != -32700 {
		t.Fatalf("parse error: %+v", r)
	}
	// Still alive afterwards.
	if r := h.call("ping", nil); r.Error != nil {
		t.Fatal("server died after errors")
	}
}

func TestPasteAndUpload(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete", "read_private")
	h := start(t, connected(e, tok))
	h.initialize()

	// paste_text → a working URL with the exact text.
	r := h.tool("paste_text", map[string]any{"text": "func main() {}\n", "lang": "go", "title": "From the agent"})
	if r.IsError {
		t.Fatalf("paste_text: %+v", r)
	}
	url := strings.SplitN(r.Content[0].Text, "\n", 2)[0]
	if r.Structured["url"] != url || !strings.HasPrefix(url, e.Cfg.BaseURL+"/") {
		t.Fatalf("paste url %q / %v", url, r.Structured)
	}
	if code, body := fetch(t, url+"/raw"); code != 200 || string(body) != "func main() {}\n" {
		t.Fatalf("paste raw: %d %q", code, body)
	}
	if _, page := fetch(t, url); !strings.Contains(string(page), "From the agent") || !strings.Contains(string(page), `<span class="kd">func</span>`) {
		t.Fatal("paste page lacks title or Go highlighting")
	}

	// upload_file → a working URL with the exact bytes, plus Markdown.
	png := testimg.PNG(30, 20)
	p := filepath.Join(t.TempDir(), "chart.png")
	os.WriteFile(p, png, 0o644)
	r = h.tool("upload_file", map[string]any{"path": p, "expires": "1h"})
	if r.IsError || r.Structured["kind"] != "image" || r.Structured["expires_at"] == nil {
		t.Fatalf("upload_file: %+v", r)
	}
	if !strings.Contains(r.Content[0].Text, "markdown: ![](") {
		t.Fatalf("no markdown in %q", r.Content[0].Text)
	}
	if code, body := fetch(t, r.Structured["raw_url"].(string)); code != 200 || !bytes.Equal(body, png) {
		t.Fatalf("uploaded bytes: %d", code)
	}
	// Uploads are tagged source=mcp.
	_, b := httpGet(t, e.Srv.URL+"/api/v1/items?source=mcp", tok)
	if strings.Count(string(b), `"source":"mcp"`) != 2 {
		t.Fatalf("source=mcp: %s", b)
	}

	// private
	r = h.tool("paste_text", map[string]any{"text": "secret", "private": true})
	if r.Structured["visibility"] != "private" {
		t.Fatalf("private: %+v", r)
	}
	if code, _ := fetch(t, r.Structured["url"].(string)); code != 404 {
		t.Fatal("private paste is public")
	}

	// encrypted: the link carries the key; the server has only ciphertext.
	r = h.tool("paste_text", map[string]any{"text": "api_key=abc123", "encrypt": true})
	link, _ := r.Structured["url"].(string)
	pageURL, frag, found := strings.Cut(link, "#k=")
	if r.IsError || !found || r.Structured["encrypted"] != true {
		t.Fatalf("encrypted paste: %+v", r)
	}
	_, ct := fetch(t, pageURL+"/raw")
	key, _ := e2e.ParseKey(frag)
	if _, plain, err := e2e.Decrypt(ct, key, ""); err != nil || string(plain) != "api_key=abc123" || bytes.Contains(ct, []byte("abc123")) {
		t.Fatalf("encrypted paste content: %v %q", err, plain)
	}

	// list, search, delete
	if r := h.tool("list_items", map[string]any{"limit": 2}); r.IsError || len(r.Structured["items"].([]any)) != 2 {
		t.Fatalf("list_items: %+v", r)
	}
	if r := h.tool("search_items", map[string]any{"query": "main"}); r.IsError || !strings.Contains(r.Content[0].Text, url) {
		t.Fatalf("search_items: %+v", r)
	}
	if r := h.tool("delete_item", map[string]any{"id": url}); r.IsError {
		t.Fatalf("delete_item: %+v", r)
	}
	if code, _ := fetch(t, url); code != 404 {
		t.Fatal("deleted item still served")
	}
}

func httpGet(t *testing.T, url, tok string) (int, []byte) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// Failures come back as tool errors (isError) the model can read — never
// as crashes or protocol errors.
func TestToolErrors(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) { c.MaxUploadBytes = 1000 })
	good := e.Token("t")

	bad := start(t, connected(e, "tibi_notavalidtokennotavalidtokennot"))
	bad.initialize()
	r := bad.tool("paste_text", map[string]any{"text": "hi"})
	if !r.IsError || !strings.Contains(r.Content[0].Text, "token") {
		t.Fatalf("bad token: %+v", r)
	}

	h := start(t, connected(e, good))
	h.initialize()
	big := filepath.Join(t.TempDir(), "big.bin")
	os.WriteFile(big, make([]byte, 5000), 0o644)
	cases := []struct {
		tool string
		args any
		want string
	}{
		{"upload_file", map[string]any{"path": big}, "too large"},
		{"paste_text", map[string]any{"text": strings.Repeat("x", 2000)}, "too large"},
		{"upload_file", map[string]any{"path": filepath.Join(t.TempDir(), "file.png")}, "cannot open"},
		{"upload_file", map[string]any{"path": "/no/such/file.png"}, "outside"},
		{"upload_file", map[string]any{"path": t.TempDir()}, "not a regular file"},
		{"upload_file", map[string]any{}, "path is required"},
		{"paste_text", map[string]any{"text": "  "}, "empty"},
		{"paste_text", map[string]any{"text": "x", "expires": "soon"}, "expiry"},
		{"paste_text", map[string]any{"text": "x", "lang": "klingon"}, "unknown lang"},
		{"delete_item", map[string]any{"id": "zzzzzzzz"}, "permission"}, // upload-only token
		{"search_items", map[string]any{"query": ""}, "empty"},
		{"paste_text", "not an object", "invalid arguments"},
	}
	for _, c := range cases {
		r := h.tool(c.tool, c.args)
		if !r.IsError || !strings.Contains(r.Content[0].Text, c.want) {
			t.Errorf("%s %v: isError=%v %q (want %q)", c.tool, c.args, r.IsError, r.Content[0].Text, c.want)
		}
	}

	deleter := start(t, connected(e, e.Token("d", "upload", "delete")))
	deleter.initialize()
	if r := deleter.tool("delete_item", map[string]any{"id": "zzzzzzzz"}); !r.IsError || !strings.Contains(r.Content[0].Text, "not found") {
		t.Fatalf("delete missing: %+v", r)
	}

	// Server unreachable, or not logged in at all.
	down := start(t, func() (*client.Client, error) { return client.New("http://127.0.0.1:1", good), nil })
	down.initialize()
	if r := down.tool("list_items", nil); !r.IsError || !strings.Contains(r.Content[0].Text, "cannot reach") {
		t.Fatalf("unreachable: %+v", r)
	}
	none := start(t, func() (*client.Client, error) { return nil, os.ErrNotExist })
	none.initialize()
	if r := none.tool("list_items", nil); !r.IsError {
		t.Fatal("no client: not an error")
	}
	// After all that, the session still works.
	if r := h.tool("paste_text", map[string]any{"text": "still fine"}); r.IsError {
		t.Fatalf("session broken: %+v", r)
	}
}

// TestAgentDefaults: delete_item is off unless allowed, uploads expire by
// default, refusals come back as tool errors (design 2026-11-21 §1–3).
func TestAgentDefaults(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t", "upload", "delete")
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	key := filepath.Join(home, ".ssh", "id_ed25519")
	os.WriteFile(key, []byte("PRIVATE KEY"), 0o600)
	tb := &mcp.Toolbox{Client: connected(e, tok), MaxFileBytes: 1 << 20, DefaultExpires: "7d",
		Paths: mcp.PathPolicy{WorkDir: home, Home: home, TempDir: os.TempDir()}}
	h := startWith(t, tb)
	h.initialize()

	r := h.call("tools/list", nil)
	if strings.Contains(string(r.Result), "delete_item") || !strings.Contains(string(r.Result), "Default: 7d.") {
		t.Fatalf("tools/list: %s", r.Result)
	}
	if r := h.call("tools/call", map[string]any{"name": "delete_item", "arguments": map[string]any{"id": "zzzzzzzz"}}); r.Error == nil {
		t.Fatal("delete_item callable without --allow-delete")
	}

	r2 := h.tool("paste_text", map[string]any{"text": "hello"})
	exp, _ := r2.Structured["expires_at"].(float64)
	if r2.IsError || exp == 0 || int64(exp)-time.Now().Unix() > 7*86400+60 || int64(exp)-time.Now().Unix() < 7*86400-60 {
		t.Fatalf("default expiry: %+v", r2)
	}
	if r := h.tool("paste_text", map[string]any{"text": "keep", "expires": "never"}); r.IsError || r.Structured["expires_at"] != nil {
		t.Fatalf("explicit never: %+v", r)
	}

	r3 := h.tool("upload_file", map[string]any{"path": "~/.ssh/id_ed25519"})
	if !r3.IsError || !strings.Contains(r3.Content[0].Text, "never uploaded") {
		t.Fatalf("key upload: %+v", r3)
	}
	_, b := httpGet(t, e.Srv.URL+"/api/v1/items", tok)
	if strings.Count(string(b), `"id"`) != 2 {
		t.Fatalf("something besides the two pastes was uploaded: %s", b)
	}
}
