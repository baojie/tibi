package i18n_test

import (
	"os"
	"testing"

	"github.com/baojie/tibi/internal/i18n"
)

func load(t *testing.T) *i18n.Catalog {
	c, err := i18n.Load(os.DirFS("../../web"), "i18n")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogsComplete(t *testing.T) {
	c := load(t)
	en := c.Keys("en")
	for _, loc := range c.Locales() {
		have := map[string]bool{}
		for _, k := range c.Keys(loc) {
			have[k] = true
		}
		for _, k := range en {
			if !have[k] {
				t.Errorf("%s.toml lacks %q", loc, k)
			}
		}
		if len(c.Keys(loc)) != len(en) {
			t.Errorf("%s.toml has %d keys, en has %d", loc, len(c.Keys(loc)), len(en))
		}
	}
	if !c.Has("zh-CN") {
		t.Fatal("zh-CN catalog missing")
	}
}

func TestMatch(t *testing.T) {
	c := load(t)
	cases := map[string]string{
		"zh-CN":                   "zh-CN",
		"zh-CN,zh;q=0.9,en;q=0.8": "zh-CN",
		"zh":                      "zh-CN",
		"zh-Hans-CN":              "zh-CN",
		"en-US,en;q=0.9":          "en",
		"fr-FR":                   "en",
		"":                        "en",
		"garbage;;;":              "en",
	}
	for h, want := range cases {
		if got := c.Match(h, "en"); got != want {
			t.Errorf("Match(%q) = %s, want %s", h, got, want)
		}
	}
	if got := c.Match("fr", "zh-CN"); got != "zh-CN" {
		t.Errorf("default locale not used: %s", got)
	}
	if c.T("zh-CN", "meta.size") != "大小" || c.N("en", "meta.lines", 1) != "1 line" || c.N("en", "meta.lines", 3) != "3 lines" {
		t.Error("lookup")
	}
	if c.T("en", "no.such.key") != "no.such.key" {
		t.Error("missing key should echo the key")
	}
}
