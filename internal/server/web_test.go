package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"tibi/internal/store"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

// unfurl fetches a page the way Slack's and Discord's link expanders do.
func unfurl(t *testing.T, u string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

var ogRe = regexp.MustCompile(`<meta (property|name)="(og|twitter):[^"]+" content="[^"]*">`)

func ogTag(page, prop string) string {
	m := regexp.MustCompile(`<meta (?:property|name)="` + regexp.QuoteMeta(prop) + `" content="([^"]*)">`).FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestOpenGraph(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	reader := e.Token("r", "read_private")

	img := up(t, e, tok, "?filename=sunset.png&title=Sunset", testimg.PNG(640, 480))
	code, page := unfurl(t, img.URL)
	if code != 200 || ogTag(page, "og:image") != img.RawURL || ogTag(page, "og:title") != "Sunset" ||
		ogTag(page, "og:image:width") != "640" || ogTag(page, "twitter:card") != "summary_large_image" || ogTag(page, "og:url") != img.URL {
		t.Fatalf("unlisted image lacks OG tags:\n%s", strings.Join(ogRe.FindAllString(page, -1), "\n"))
	}
	txt := up(t, e, tok, "?filename=notes.txt", []byte("Line one of the notes.\nLine two.\n"))
	if _, page := unfurl(t, txt.URL); ogTag(page, "og:description") != "Line one of the notes. Line two." {
		t.Fatalf("text description = %q", ogTag(page, "og:description"))
	}

	// Private: 404 for the unfurler; with a share link the page renders but
	// still carries no preview tags.
	priv := up(t, e, tok, "?visibility=private", testimg.PNG(10, 10))
	if code, page := unfurl(t, priv.URL); code != 404 || ogRe.MatchString(page) {
		t.Fatalf("private: %d", code)
	}
	_, body := api(t, e, tok, "POST", "/api/v1/items/"+priv.ID+"/share-link", nil)
	var link struct{ URL string }
	json.Unmarshal(body, &link)
	if code, page := unfurl(t, link.URL); code != 200 || ogRe.MatchString(page) {
		t.Fatalf("private via share link: %d, OG present=%v", code, ogRe.MatchString(page))
	}
	if _, page := getAs(t, priv.URL, reader); ogRe.Match(page) {
		t.Fatal("private item (owner view) has OG tags")
	}
	// Burn after reading: the unfurler sees only the interstitial.
	burn := up(t, e, tok, "?burn_after_read=1", testimg.PNG(10, 10))
	if code, page := unfurl(t, burn.URL); code != 200 || ogRe.MatchString(page) {
		t.Fatalf("burn: %d, OG present=%v", code, ogRe.MatchString(page))
	}
	if status(t, burn.URL, "") != 200 {
		t.Fatal("unfurl consumed the burn item")
	}
	// Encrypted items (created directly; client-side encryption arrives later).
	enc := &store.Item{Kind: store.KindEncrypted, Mime: "application/octet-stream", Ext: "bin", Size: 1,
		SHA256: strings.Repeat("e", 64), TokenID: 1}
	if err := e.Instance.Store.CreateItem(context.Background(), enc); err != nil {
		t.Fatal(err)
	}
	if code, page := unfurl(t, e.Srv.URL+"/"+enc.ID); code != 200 || ogRe.MatchString(page) {
		t.Fatalf("encrypted: %d, OG present=%v", code, ogRe.MatchString(page))
	}

	// Collections preview with their first thumbnail.
	c, items := albumUpload(t, e, tok, map[string]string{"collection": "new", "collection_title": "Album"},
		[2]string{"a.png", string(testimg.PNG(20, 20))})
	if _, page := unfurl(t, c.URL); ogTag(page, "og:image") != e.Cfg.BaseURL+"/"+items[0].ID+"/thumb" || ogTag(page, "og:title") != "Album" {
		t.Fatal("collection preview")
	}
}

func TestShareLink(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	it := up(t, e, tok, "?visibility=private&filename=secret.png", testimg.PNG(64, 64))
	if status(t, it.URL, "") != 404 {
		t.Fatal("private item public")
	}
	code, body := api(t, e, tok, "POST", "/api/v1/items/"+it.ID+"/share-link?ttl=2h", nil)
	var link struct {
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	json.Unmarshal(body, &link)
	if code != 200 || !strings.HasPrefix(link.URL, it.URL+"?exp=") || link.ExpiresAt < time.Now().Add(119*time.Minute).Unix() {
		t.Fatalf("share-link: %d %s", code, body)
	}
	// The page and the image on it load through the link.
	resp, pageB := get(t, link.URL)
	page := string(pageB)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("signed page: %d %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	src := regexp.MustCompile(`<img src="([^"]+)"`).FindStringSubmatch(page)
	if src == nil || !strings.Contains(src[1], "sig=") {
		t.Fatal("image on signed page lacks the signature")
	}
	imgURL := strings.ReplaceAll(src[1], "&amp;", "&")
	if resp, _ := get(t, imgURL); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("signed raw: %d", resp.StatusCode)
	}
	q, _ := url.Parse(link.URL)
	for _, path := range []string{"/dl", "/thumb"} {
		if s := status(t, e.Srv.URL+"/"+it.ID+path+"?"+q.RawQuery, ""); s != 200 {
			t.Errorf("signed %s: %d", path, s)
		}
	}
	// Tampering or reuse for another item fails.
	other := up(t, e, tok, "?visibility=private", []byte("other secret"))
	if status(t, e.Srv.URL+"/"+other.ID+"?"+q.RawQuery, "") != 404 {
		t.Fatal("signature valid for another item")
	}
	vals := q.Query()
	vals.Set("exp", "9999999999")
	if status(t, it.URL+"?"+vals.Encode(), "") != 404 {
		t.Fatal("extended expiry accepted")
	}
	// Works until expiry, then 404s.
	e.Advance(119 * time.Minute)
	if status(t, link.URL, "") != 200 {
		t.Fatal("link expired early")
	}
	e.Advance(2 * time.Minute)
	if status(t, link.URL, "") != 404 || status(t, imgURL, "") != 404 {
		t.Fatal("expired link still works")
	}
	// A link never outlives its item; bad TTLs are rejected.
	short := up(t, e, tok, "?visibility=private&expires=10m", []byte("x"))
	_, body = api(t, e, tok, "POST", "/api/v1/items/"+short.ID+"/share-link?ttl=7d", nil)
	json.Unmarshal(body, &link)
	if link.ExpiresAt != *short.ExpiresAt {
		t.Fatalf("link outlives item: %d vs %d", link.ExpiresAt, *short.ExpiresAt)
	}
	for _, bad := range []string{"never", "soon", "400d"} {
		if code, _ := api(t, e, tok, "POST", "/api/v1/items/"+it.ID+"/share-link?ttl="+bad, nil); code != 400 {
			t.Errorf("ttl %s: %d", bad, code)
		}
	}
	if code, _ := api(t, e, tok, "POST", "/api/v1/items/zzzzzzzz/share-link", nil); code != 404 {
		t.Errorf("missing item: %d", code)
	}
}

func getLang(t *testing.T, u, lang string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestChineseUI(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("t")
	img := up(t, e, tok, "?filename=a.png", testimg.PNG(8, 8))
	txt := up(t, e, tok, "?filename=a.go", []byte("package main\n"))

	resp, page := getLang(t, img.URL, "zh-CN,zh;q=0.9,en;q=0.8")
	if !strings.Contains(page, `<html lang="zh-CN">`) || !strings.Contains(page, ">下载<") || !strings.Contains(page, ">复制<") || !strings.Contains(page, "<dt>大小</dt>") {
		t.Fatal("image page not in Chinese")
	}
	if resp.Header.Get("Content-Language") != "zh-CN" || !strings.Contains(resp.Header.Get("Vary"), "Accept-Language") {
		t.Fatalf("headers: %q %q", resp.Header.Get("Content-Language"), resp.Header.Get("Vary"))
	}
	if _, page := getLang(t, txt.URL, "zh-CN"); !strings.Contains(page, "1 行") || !strings.Contains(page, ">自动换行<") {
		t.Fatal("text page not in Chinese")
	}
	for _, p := range []string{"/upload", "/recent"} {
		if resp, page := getLang(t, e.Srv.URL+p, "zh-CN"); resp.StatusCode != 200 || !strings.Contains(page, `lang="zh-CN"`) || !strings.Contains(page, "令牌") && p == "/upload" {
			t.Fatalf("%s in Chinese: %d", p, resp.StatusCode)
		}
	}
	// Strings for scripts are translated too.
	if !strings.Contains(page, "data-i18n=") {
		t.Fatal("no script strings")
	}
	// English otherwise, and as the default.
	for _, lang := range []string{"", "en-US", "fr-FR"} {
		if _, page := getLang(t, img.URL, lang); !strings.Contains(page, `<html lang="en">`) || !strings.Contains(page, ">Download<") {
			t.Fatalf("lang %q: not English", lang)
		}
	}
}

func TestOwnerPages(t *testing.T) {
	e := testutil.New(t, nil)
	for path, script := range map[string]string{"/upload": "/static/upload.js", "/recent": "/static/recent.js"} {
		resp, page := get(t, e.Srv.URL+path)
		if resp.StatusCode != 200 || !strings.Contains(string(page), script) || !strings.Contains(string(page), "/static/i18n.js") {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "blob:") || strings.Contains(csp, "unsafe-inline") {
			t.Fatalf("%s CSP: %q", path, csp)
		}
		if strings.Contains(string(page), "<script>") {
			t.Fatalf("%s has an inline script", path)
		}
	}
	for _, f := range []string{"upload.js", "recent.js", "i18n.js", "nav.js"} {
		if resp, _ := get(t, e.Srv.URL+"/static/"+f); resp.StatusCode != 200 {
			t.Fatalf("/static/%s: %d", f, resp.StatusCode)
		}
	}
}
