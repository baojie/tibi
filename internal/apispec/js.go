package apispec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// JS renders JSDoc type definitions for every schema plus a table of the
// operations, for JavaScript clients (the Chrome extension). Editors and
// `tsc --checkJs` pick the types up.
func (s *Spec) JS() string {
	var b strings.Builder
	info := s.doc["info"].(map[string]any)
	fmt.Fprintf(&b, "// Generated from api/openapi.yaml (tibi API %v) by `go run ./tools/apidoc -js`. Do not edit.\n\n", info["version"])
	schemas := s.doc["components"].(map[string]any)["schemas"].(map[string]any)
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := s.merge(schemas[n].(map[string]any))
		if enum, ok := sc["enum"].([]any); ok {
			parts := make([]string, len(enum))
			for i, v := range enum {
				parts[i] = fmt.Sprintf("%q", v)
			}
			fmt.Fprintf(&b, "/** @typedef {%s} %s */\n\n", strings.Join(parts, "|"), n)
			continue
		}
		fmt.Fprintf(&b, "/**\n * @typedef {Object} %s\n", n)
		props, _ := sc["properties"].(map[string]any)
		req := map[string]bool{}
		for _, r := range asList(sc["required"]) {
			req[r.(string)] = true
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, _ := props[k].(map[string]any)
			name := k
			if !req[k] {
				name = "[" + k + "]"
			}
			desc := ""
			if d, ok := p["description"].(string); ok {
				desc = " - " + strings.ReplaceAll(strings.TrimSpace(d), "\n", " ")
			}
			fmt.Fprintf(&b, " * @property {%s} %s%s\n", s.jsType(p), name, desc)
		}
		b.WriteString(" */\n\n")
	}
	b.WriteString("/** Operations: method, path (with {params}) and required scope. */\nexport const ENDPOINTS = Object.freeze({\n")
	paths := s.doc["paths"].(map[string]any)
	pathNames := make([]string, 0, len(paths))
	for p := range paths {
		pathNames = append(pathNames, p)
	}
	sort.Strings(pathNames)
	scopeRe := regexp.MustCompile("[Rr]equires scope `([a-z_]+)`")
	for _, p := range pathNames {
		for _, m := range []string{"get", "post", "patch", "head", "delete"} {
			op, ok := paths[p].(map[string]any)[m].(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			scope := ""
			if sm := scopeRe.FindStringSubmatch(fmt.Sprint(op["description"])); sm != nil {
				scope = sm[1]
			}
			fmt.Fprintf(&b, "  %s: { method: %q, path: %q, scope: %q },\n", id, strings.ToUpper(m), p, scope)
		}
	}
	b.WriteString("});\n")
	return b.String()
}

func (s *Spec) jsType(node any) string {
	m, _ := node.(map[string]any)
	if m == nil {
		return "*"
	}
	if ref, ok := m["$ref"].(string); ok {
		return ref[strings.LastIndexByte(ref, '/')+1:]
	}
	if one, ok := m["oneOf"].([]any); ok {
		parts := make([]string, len(one))
		for i, o := range one {
			parts[i] = s.jsType(o)
		}
		return strings.Join(parts, "|")
	}
	conv := func(t string) string {
		switch t {
		case "integer":
			return "number"
		case "array":
			return "Array<" + s.jsType(m["items"]) + ">"
		case "object":
			return "Object<string, *>"
		}
		return t
	}
	if enum, ok := m["enum"].([]any); ok {
		parts := make([]string, len(enum))
		for i, v := range enum {
			parts[i] = fmt.Sprintf("%q", v)
		}
		return strings.Join(parts, "|")
	}
	switch t := m["type"].(type) {
	case string:
		return conv(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = conv(fmt.Sprint(x))
		}
		return strings.Join(parts, "|")
	}
	return "*"
}
