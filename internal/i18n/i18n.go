// Package i18n holds UI string catalogs (web/i18n/*.toml) and picks a
// locale from Accept-Language.
package i18n

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/text/language"
)

// Fallback is the locale every catalog falls back to.
const Fallback = "en"

// Catalog maps locale → key → message.
type Catalog struct {
	msgs    map[string]map[string]string
	locales []string
	matcher language.Matcher
}

// Load reads every <locale>.toml in dir of fsys.
func Load(fsys fs.FS, dir string) (*Catalog, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.toml"))
	if err != nil {
		return nil, err
	}
	c := &Catalog{msgs: map[string]map[string]string{}}
	for _, f := range files {
		loc := strings.TrimSuffix(path.Base(f), ".toml")
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		if err := toml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("i18n %s: %w", f, err)
		}
		c.msgs[loc] = m
	}
	if _, ok := c.msgs[Fallback]; !ok {
		return nil, fmt.Errorf("i18n: %s.toml missing", Fallback)
	}
	// Fallback first: it is the matcher's default.
	c.locales = append(c.locales, Fallback)
	for loc := range c.msgs {
		if loc != Fallback {
			c.locales = append(c.locales, loc)
		}
	}
	sort.Strings(c.locales[1:])
	tags := make([]language.Tag, len(c.locales))
	for i, l := range c.locales {
		tags[i] = language.MustParse(l)
	}
	c.matcher = language.NewMatcher(tags)
	return c, nil
}

// Locales lists available locales, the fallback first.
func (c *Catalog) Locales() []string { return c.locales }

// Has reports whether locale exists.
func (c *Catalog) Has(locale string) bool { _, ok := c.msgs[locale]; return ok }

// Match picks the best locale for an Accept-Language header, or def when
// nothing matches well.
func (c *Catalog) Match(acceptLanguage, def string) string {
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return c.orDefault(def)
	}
	_, idx, conf := c.matcher.Match(tags...)
	if conf == language.No {
		return c.orDefault(def)
	}
	return c.locales[idx]
}

func (c *Catalog) orDefault(def string) string {
	if c.Has(def) {
		return def
	}
	return Fallback
}

// T returns the message for key in locale, formatted with args.
func (c *Catalog) T(locale, key string, args ...any) string {
	msg, ok := c.msgs[locale][key]
	if !ok {
		msg, ok = c.msgs[Fallback][key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// N picks key.one or key.other for n.
func (c *Catalog) N(locale, key string, n int) string {
	if n == 1 {
		return c.T(locale, key+".one", n)
	}
	return c.T(locale, key+".other", n)
}

// Keys lists the keys of a locale.
func (c *Catalog) Keys(locale string) []string {
	var keys []string
	for k := range c.msgs[locale] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Subset returns the messages whose keys start with any of prefixes (for
// passing to page scripts).
func (c *Catalog) Subset(locale string, prefixes ...string) map[string]string {
	out := map[string]string{}
	for _, k := range c.Keys(Fallback) {
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				out[k] = c.T(locale, k)
			}
		}
	}
	return out
}
