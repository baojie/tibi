package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"tibi/internal/config"
	"tibi/internal/server"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

func getRef(t *testing.T, u, referer string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func TestDailyCaps(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) {
		c.MaxItemBytesDay = 2500
		c.MaxTotalBytesDay = 6000
	})
	tok := e.Token("t")
	img := up(t, e, tok, "?filename=pic.png", testimg.PNG(30, 30))
	data := make([]byte, 1000)
	a := up(t, e, tok, "?filename=a.bin", data)

	// 1000 bytes per fetch: the third fetch reaches 3000 ≥ 2500.
	for i := 0; i < 3; i++ {
		if resp := getRef(t, a.RawURL, ""); resp.StatusCode != 200 {
			t.Fatalf("fetch %d: %d", i+1, resp.StatusCode)
		}
	}
	resp := getRef(t, a.RawURL, "")
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("over item cap: %d %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if resp := getRef(t, a.DownloadURL, ""); resp.StatusCode != 429 {
		t.Fatalf("download over cap: %d", resp.StatusCode)
	}
	// The view page still loads and explains.
	_, page := get(t, a.URL)
	if !strings.Contains(string(page), "bandwidth limit for today") {
		t.Fatal("view page does not explain the cap")
	}
	if _, page := getLang(t, a.URL, "zh-CN"); !strings.Contains(page, "流量已达上限") {
		t.Fatal("cap notice not translated")
	}
	// Other items still work until the instance total is reached.
	b := up(t, e, tok, "?filename=b.bin", make([]byte, 1001))
	for getRef(t, b.RawURL, "").StatusCode == 200 {
	}
	if resp := getRef(t, img.RawURL, ""); resp.StatusCode != 429 {
		t.Fatalf("instance cap not enforced for other items: %d", resp.StatusCode)
	}
	if resp := getRef(t, img.URL, ""); resp.StatusCode != 200 {
		t.Fatal("view page blocked by instance cap")
	}
	// A new UTC day resets the allowance.
	e.Advance(24 * time.Hour)
	if resp := getRef(t, a.RawURL, ""); resp.StatusCode != 200 {
		t.Fatalf("next day: %d", resp.StatusCode)
	}
}

func TestHotlinkProtection(t *testing.T) {
	e := testutil.New(t, func(c *config.Server) {
		c.AllowedReferers = []string{"blog.example.com", "*.friends.org"}
	})
	tok := e.Token("t")
	img := up(t, e, tok, "?filename=pic.png", testimg.PNG(8, 8))
	txt := up(t, e, tok, "", []byte("plain text"))
	cases := []struct {
		url, ref string
		want     int
	}{
		{img.RawURL, "", 200},                                // direct visit, chat apps
		{img.RawURL, "https://evil.example.net/page", 403},   // hotlink
		{img.RawURL, "https://blog.example.com/post/1", 200}, // allowed
		{img.RawURL, "https://www.friends.org/", 200},        // wildcard
		{img.RawURL, "https://friends.org.evil.com/", 403},   // not a subdomain
		{img.RawURL, e.Cfg.BaseURL + "/" + img.ID, 200},      // tibi's own page
		{e.Srv.URL + "/" + img.ID + "/thumb", "https://evil.example.net/", 403},
		{img.DownloadURL, "https://evil.example.net/", 200}, // downloads are not embeds
		{txt.RawURL, "https://evil.example.net/", 200},      // only media is protected
	}
	for _, c := range cases {
		if got := getRef(t, c.url, c.ref).StatusCode; got != c.want {
			t.Errorf("%s with Referer %q: %d, want %d", c.url, c.ref, got, c.want)
		}
	}
}

func TestQuota(t *testing.T) {
	e := testutil.New(t, nil)
	ctx := context.Background()
	q := int64(1000)
	secret, _, err := e.Instance.Store.CreateToken(ctx, "limited", []string{"upload", "delete"}, &q)
	if err != nil {
		t.Fatal(err)
	}
	a := up(t, e, secret, "", make([]byte, 600))
	resp, b := upload(t, e, secret, "", make([]byte, 600))
	if resp.StatusCode != 413 || !strings.Contains(string(b), "quota exceeded") || !strings.Contains(string(b), "600 B of 1000 B") {
		t.Fatalf("over quota: %d %s", resp.StatusCode, b)
	}
	// Multipart counts the stored sizes too.
	ct, body := multipartBody(t, nil, [2]string{"x", strings.Repeat("x", 300)}, [2]string{"y", strings.Repeat("y", 300)})
	if resp, _ := postMultipart(t, e, secret, ct, body); resp.StatusCode != 413 {
		t.Fatalf("multipart over quota: %d", resp.StatusCode)
	}
	_, w := api(t, e, secret, "GET", "/api/v1/whoami", nil)
	if !strings.Contains(string(w), `"used_bytes":600`) || !strings.Contains(string(w), `"quota_bytes":1000`) {
		t.Fatalf("whoami: %s", w)
	}
	// Forks count as well.
	req, _ := http.NewRequest("POST", e.Srv.URL+"/api/v1/items/"+a.ID+"/fork", strings.NewReader(strings.Repeat("z", 500)))
	req.Header.Set("Authorization", "Bearer "+secret)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 413 {
		t.Fatalf("fork over quota: %d", r.StatusCode)
	}
	// Deleting frees quota.
	api(t, e, secret, "DELETE", "/api/v1/items/"+a.ID, nil)
	if resp, b := upload(t, e, secret, "", make([]byte, 900)); resp.StatusCode != 201 {
		t.Fatalf("after delete: %d %s", resp.StatusCode, b)
	}
	// Tokens without a quota are unlimited.
	if resp, _ := upload(t, e, e.Token("free"), "", make([]byte, 5000)); resp.StatusCode != 201 {
		t.Fatal("unlimited token refused")
	}
}

func TestCountersStatsMetrics(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	admin := e.Token("root", "admin")
	it := up(t, e, tok, "?filename=data.bin", make([]byte, 2000))
	get(t, it.URL)    // view page: a view, no bytes
	get(t, it.RawURL) // a view and 2000 bytes
	get(t, it.DownloadURL)
	req, _ := http.NewRequest("GET", it.RawURL, nil) // a seek: bytes, no view
	req.Header.Set("Range", "bytes=1000-1499")
	if r, _ := http.DefaultClient.Do(req); r != nil {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
	}

	code, b := api(t, e, admin, "GET", "/api/v1/stats", nil)
	var st server.StatsJSON
	json.Unmarshal(b, &st)
	if code != 200 || st.Items != 1 || st.ItemsByKind["file"] != 1 || st.BytesServedToday != 4500 || st.BytesServedTotal != 4500 {
		t.Fatalf("stats: %d %s", code, b)
	}
	if len(st.TopItems) != 1 || st.TopItems[0].ViewCount != 3 || st.TopItems[0].BytesServed != 4500 {
		t.Fatalf("top items: %+v", st.TopItems)
	}
	if code, _ := api(t, e, tok, "GET", "/api/v1/stats", nil); code != 403 {
		t.Fatalf("stats without admin: %d", code)
	}

	// /metrics: allowed from this machine (tests connect via loopback)...
	resp, body := get(t, e.Srv.URL+"/metrics")
	m := string(body)
	if resp.StatusCode != 200 || !strings.Contains(m, `tibi_items{kind="file"} 1`) || !strings.Contains(m, "tibi_served_bytes_today 4500") ||
		!strings.Contains(m, "# TYPE tibi_http_requests_total counter") || !strings.Contains(m, `tibi_http_requests_total{method="GET",code="200"}`) {
		t.Fatalf("metrics: %d\n%s", resp.StatusCode, m)
	}
	// ...but not through a reverse proxy, unless with an admin token.
	req, _ = http.NewRequest("GET", e.Srv.URL+"/metrics", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("proxied metrics: %d", r.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer "+admin)
	r, _ = http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("proxied metrics with admin token: %d", r.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	r, _ = http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("proxied metrics with non-admin token: %d", r.StatusCode)
	}
}
