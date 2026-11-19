// Package render turns stored text into HTML: syntax highlighting, Markdown,
// and language inference.
package render

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// PlainText is the language of unrecognized text.
const PlainText = "text"

// Markdown is the canonical name for Markdown.
const Markdown = "markdown"

// ResolveLang maps a user-supplied language name (alias, name, or file
// extension) to its canonical alias. ok is false for unknown names.
func ResolveLang(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "", "text", "txt", "plain", "plaintext", "none":
		return PlainText, true
	case "md":
		return Markdown, true
	}
	l := lexers.Get(n)
	if l == nil {
		l = lexers.Match("x." + n)
	}
	if l == nil {
		return "", false
	}
	return canonical(l), true
}

// canonical is the lexer's lowercase name when that is itself a valid key
// ("go", "python", "markdown", "typescript"), else its first alias.
func canonical(l chroma.Lexer) string {
	c := l.Config()
	if n := strings.ToLower(c.Name); !strings.ContainsAny(n, " /+#") && lexers.Get(n) != nil {
		return n
	}
	if len(c.Aliases) > 0 {
		return strings.ToLower(c.Aliases[0])
	}
	return strings.ToLower(c.Name)
}

// InferLang guesses the language: file name first, then content.
func InferLang(filename string, sample []byte) string {
	if filename != "" {
		if ext := strings.ToLower(path.Ext(filename)); ext == ".txt" || ext == ".log" {
			return PlainText
		}
		if l := lexers.Match(path.Base(filename)); l != nil {
			return canonical(l)
		}
	}
	return AnalyseLang(sample)
}

var (
	shebangRe = regexp.MustCompile(`^#!\s*(?:/usr/bin/env\s+(?:-S\s+)?)?(?:\S*/)?([A-Za-z]+)`)
	mdRe      = regexp.MustCompile(`(?m)^(#{1,6} \S|[-*] \S|\d+\. \S|> \S|` + "```" + `)`)
	mdLinkRe  = regexp.MustCompile(`\[[^\]\n]+\]\([^)\n]+\)`)
	diffRe    = regexp.MustCompile(`(?m)^(diff --git |--- \S.*\n\+\+\+ \S|@@ -\d+(,\d+)? \+\d+(,\d+)? @@)`)
	goPkgRe   = regexp.MustCompile(`(?m)^package [a-z_][a-z0-9_]*\s*$`)
	goDeclRe  = regexp.MustCompile(`(?m)^(import \(|import "|func |type \w+ (struct|interface)|var |const )`)
	dockerRe  = regexp.MustCompile(`(?m)^FROM \S+`)
	dockerRe2 = regexp.MustCompile(`(?m)^(RUN|COPY|CMD|ENTRYPOINT|WORKDIR|ENV) `)
	makeRe    = regexp.MustCompile(`(?m)^[\w.%/-]+( [\w.%/-]+)*:([^=\n].*)?\n\t\S`)
	pyRe      = regexp.MustCompile(`(?m)^(def \w+\(.*\):|class \w+(\(.*\))?:|from \w+(\.\w+)* import |import \w+$|if __name__ == )`)
	rustRe    = regexp.MustCompile(`(?m)^\s*(fn \w+\(|let mut |use \w+::|impl\b|pub fn )`)
	jsRe      = regexp.MustCompile(`(?m)^\s*(const \w+ = |function \w+\(|export (default |const |function )|import .* from ['"])`)
	cRe       = regexp.MustCompile(`(?m)^#include [<"]`)
	sqlRe     = regexp.MustCompile(`(?i)^\s*(SELECT\s.+\sFROM\s|INSERT\s+INTO\s|CREATE\s+(TABLE|INDEX|VIEW)\s|UPDATE\s+\w+\s+SET\s)`)
)

var shebangLangs = map[string]string{
	"python": "python", "python3": "python", "python2": "python",
	"bash": "bash", "sh": "bash", "zsh": "bash", "ksh": "bash", "dash": "bash",
	"node": "javascript", "deno": "typescript", "bun": "javascript",
	"ruby": "ruby", "perl": "perl", "php": "php", "lua": "lua", "fish": "fish",
}

// AnalyseLang guesses a language from content alone.
func AnalyseLang(sample []byte) string {
	text := sample
	if len(text) > 64<<10 {
		text = text[:64<<10]
	}
	trimmed := bytes.TrimSpace(text)
	if len(trimmed) == 0 {
		return PlainText
	}
	if m := shebangRe.FindSubmatch(trimmed); m != nil {
		if l, ok := shebangLangs[strings.ToLower(string(m[1]))]; ok {
			return l
		}
	}
	if bytes.HasPrefix(trimmed, []byte("<?php")) {
		return "php"
	}
	if (trimmed[0] == '{' || trimmed[0] == '[') && len(sample) == len(text) && json.Valid(trimmed) {
		return "json"
	}
	if bytes.HasPrefix(trimmed, []byte("<?xml")) {
		return "xml"
	}
	if diffRe.Match(text) {
		return "diff"
	}
	switch {
	case dockerRe.Match(text) && dockerRe2.Match(text):
		return "docker"
	case makeRe.Match(text):
		return "makefile"
	case goPkgRe.Match(text) && goDeclRe.Match(text):
		return "go"
	case pyRe.Match(text):
		return "python"
	case rustRe.Match(text):
		return "rust"
	case cRe.Match(text):
		return "c"
	case jsRe.Match(text):
		return "javascript"
	case sqlRe.Match(text):
		return "sql"
	}
	if len(mdRe.FindAll(text, 3)) >= 2 || (mdRe.Match(text) && mdLinkRe.Match(text)) {
		return Markdown
	}
	// chroma's analysers also fire for niche languages on ordinary text, so
	// only trust them for common ones.
	if l := lexers.Analyse(string(text)); l != nil {
		if c := canonical(l); analyseTrusted[c] {
			return c
		}
	}
	return PlainText
}

var analyseTrusted = map[string]bool{
	"go": true, "python": true, "bash": true, "c": true, "cpp": true, "java": true,
	"javascript": true, "typescript": true, "rust": true, "ruby": true, "php": true,
	"html": true, "xml": true, "css": true, "yaml": true, "toml": true, "ini": true,
	"docker": true, "makefile": true, "lua": true, "perl": true, "kotlin": true,
	"swift": true, "csharp": true, "sql": true, "powershell": true, "haskell": true,
}

// ExtForLang is a file extension for lang, used for /{id}.{ext} of pastes.
func ExtForLang(lang string) string {
	switch lang {
	case PlainText, "":
		return "txt"
	case Markdown:
		return "md"
	}
	l := lexers.Get(lang)
	if l == nil {
		return "txt"
	}
	for _, g := range l.Config().Filenames {
		if strings.HasPrefix(g, "*.") && !strings.ContainsAny(g[2:], "*?[") {
			return strings.ToLower(g[2:])
		}
	}
	return "txt"
}

// LangName is a human-readable language name for display.
func LangName(lang string) string {
	switch lang {
	case PlainText, "":
		return "Plain text"
	case Markdown:
		return "Markdown"
	}
	if l := lexers.Get(lang); l != nil {
		return l.Config().Name
	}
	return lang
}
