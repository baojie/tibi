package render

import (
	"strings"
	"testing"
)

func TestInferLang(t *testing.T) {
	cases := []struct{ file, text, want string }{
		{"main.go", "", "go"},
		{"a.py", "", "python"},
		{"README.md", "", "markdown"},
		{"x.log", "package main", PlainText},
		{"notes.txt", "def f():\n  pass\n", PlainText},
		{"", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(1)\n}\n", "go"},
		{"", "#!/usr/bin/env python3\nprint(1)\n", "python"},
		{"", "#!/bin/sh\necho hi\n", "bash"},
		{"", "#!/usr/bin/env node\nconsole.log(1)\n", "javascript"},
		{"", `{"a": 1, "b": [true, null]}`, "json"},
		{"", "<?php echo 1;", "php"},
		{"", "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n", "diff"},
		{"", "import os\n\ndef main():\n    print(os.getcwd())\n", "python"},
		{"", "use std::io;\n\nfn main() {\n    let mut s = String::new();\n}\n", "rust"},
		{"", "#include <stdio.h>\nint main(void) { return 0; }\n", "c"},
		{"", "SELECT id, name FROM users WHERE id = 1;\n", "sql"},
		{"", "# Title\n\nSome text with a [link](https://example.com).\n\n- one\n- two\n", "markdown"},
		{"", "2026-01-01 12:00:00 INFO server started\n2026-01-01 12:00:01 WARN slow\n", PlainText},
		{"", "hello world\n", PlainText},
		{"", "<?xml version=\"1.0\"?>\n<project>\n</project>\n", "xml"},
		{"", "# build image\nFROM golang:1.22\n# deps\nRUN go build ./...\n", "docker"},
		{"", "# Makefile\n# targets\nall: build\n\nbuild:\n\tgo build ./...\n", "makefile"},
		// A Go file without fmt: chroma alone guesses GDScript.
		{"", "package server\n\nimport (\n\t\"context\"\n)\n\ntype ctxKey int\n\nfunc tokenFrom(ctx context.Context) int {\n\treturn 0\n}\n", "go"},
		{"", "", PlainText},
	}
	for _, c := range cases {
		if got := InferLang(c.file, []byte(c.text)); got != c.want {
			t.Errorf("InferLang(%q, %.30q) = %q, want %q", c.file, c.text, got, c.want)
		}
	}
}

func TestResolveLang(t *testing.T) {
	for in, want := range map[string]string{"python": "python", "py": "python", "Go": "go", "golang": "go", "md": "markdown", "": "text", "plain": "text", "JS": "javascript", "ts": "typescript"} {
		got, ok := ResolveLang(in)
		if !ok || got != want {
			t.Errorf("ResolveLang(%q) = %q,%v; want %q", in, got, ok, want)
		}
	}
	if _, ok := ResolveLang("no-such-language-xyz"); ok {
		t.Error("unknown lang resolved")
	}
	if e := ExtForLang("python"); e != "py" {
		t.Errorf("ExtForLang(python) = %q", e)
	}
	if e := ExtForLang("text"); e != "txt" {
		t.Errorf("ExtForLang(text) = %q", e)
	}
}

func TestCode(t *testing.T) {
	html, n := Code("print(1)\n<script>alert('x')</script>\n", "python")
	s := string(html)
	if n != 2 {
		t.Fatalf("lines = %d", n)
	}
	if !strings.Contains(s, `id="L1"`) || !strings.Contains(s, `href="#L2"`) || strings.Contains(s, `id="L3"`) {
		t.Fatalf("line anchors: %s", s)
	}
	if strings.Contains(s, "<script>") {
		t.Fatal("unescaped code")
	}
	if !strings.Contains(s, `<span class="nb">print</span>`) {
		t.Fatalf("no python builtin highlighting: %s", s)
	}
	// Plain text and oversized text are not tokenized but keep line numbers.
	html, n = Code("a\nb\nc", PlainText)
	if n != 3 || strings.Contains(string(html), `class="n`) {
		t.Fatalf("plain: %d %s", n, html)
	}
	big := strings.Repeat("x = 1\n", HighlightLimit/6+10)
	html, n = Code(big, "python")
	if n != HighlightLimit/6+10 || strings.Contains(string(html), `class="n"`) {
		t.Fatalf("big text: %d lines", n)
	}
}

func TestMarkdownSafe(t *testing.T) {
	html, err := RenderMarkdown([]byte("# Hi\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc f() {}\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(s, bad) {
			t.Errorf("markdown output contains %q", bad)
		}
	}
	for _, good := range []string{"<h1", "<table>", `class="chroma"`} {
		if !strings.Contains(s, good) {
			t.Errorf("markdown output lacks %q", good)
		}
	}
}

func TestCache(t *testing.T) {
	c := NewCache(100)
	c.Put("a", 1, 20)
	c.Put("b", 2, 20)
	c.Get("a")
	for i := 0; i < 4; i++ {
		c.Put(string(rune('c'+i)), i, 20)
	}
	if _, ok := c.Get("b"); ok {
		t.Error("LRU entry b should have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("recently used a evicted")
	}
	c.Put("huge", 0, 60)
	if _, ok := c.Get("huge"); ok {
		t.Error("oversized entry cached")
	}
}
