package apispec

import (
	"fmt"
	"sort"
	"strings"
)

// Markdown renders the spec as docs/API.md.
func (s *Spec) Markdown() string {
	var b strings.Builder
	info := s.doc["info"].(map[string]any)
	fmt.Fprintf(&b, "# %s %v\n\n", info["title"], info["version"])
	b.WriteString("<!-- Generated from api/openapi.yaml by `go run ./tools/apidoc`. Do not edit. -->\n\n")
	b.WriteString(strings.TrimSpace(fmt.Sprint(info["description"])) + "\n\n")

	// Operations in document order of paths, grouped by tag.
	paths := s.doc["paths"].(map[string]any)
	pathNames := make([]string, 0, len(paths))
	for p := range paths {
		pathNames = append(pathNames, p)
	}
	sort.SliceStable(pathNames, func(i, j int) bool { return pathOrder(pathNames[i]) < pathOrder(pathNames[j]) })

	b.WriteString("## Endpoints\n\n| method | path | summary |\n|---|---|---|\n")
	methods := []string{"get", "post", "patch", "put", "delete"}
	for _, p := range pathNames {
		for _, m := range methods {
			if op, ok := paths[p].(map[string]any)[m].(map[string]any); ok {
				fmt.Fprintf(&b, "| `%s` | [`%s`](#%s) | %s |\n", strings.ToUpper(m), p, anchor(m, p), op["summary"])
			}
		}
	}
	b.WriteString("\n")

	for _, p := range pathNames {
		pi := paths[p].(map[string]any)
		shared, _ := pi["parameters"].([]any)
		for _, m := range methods {
			op, ok := pi[m].(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "<a id=\"%s\"></a>\n### `%s %s`\n\n", anchor(m, p), strings.ToUpper(m), p)
			fmt.Fprintf(&b, "**%s**\n\n", op["summary"])
			if d, ok := op["description"].(string); ok {
				b.WriteString(strings.TrimSpace(d) + "\n\n")
			}
			if sec, ok := op["security"].([]any); ok && len(sec) == 0 {
				b.WriteString("No authentication.\n\n")
			}
			params := append(append([]any{}, shared...), asList(op["parameters"])...)
			if len(params) > 0 {
				b.WriteString("| parameter | in | type | description |\n|---|---|---|---|\n")
				for _, pr := range params {
					p := s.resolve(pr)
					in := fmt.Sprint(p["in"])
					if p["required"] == true {
						in += ", required"
					}
					fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", p["name"], in, s.typeName(p["schema"]), cell(p["description"]))
				}
				b.WriteString("\n")
			}
			if rb, ok := op["requestBody"].(map[string]any); ok {
				b.WriteString("Request body: ")
				var cts []string
				for ct, media := range rb["content"].(map[string]any) {
					cts = append(cts, fmt.Sprintf("`%s` (%s)", ct, s.typeName(media.(map[string]any)["schema"])))
				}
				sort.Strings(cts)
				b.WriteString(strings.Join(cts, ", ") + "\n\n")
			}
			b.WriteString("| status | meaning | body |\n|---|---|---|\n")
			resps := op["responses"].(map[string]any)
			codes := make([]string, 0, len(resps))
			for c := range resps {
				codes = append(codes, c)
			}
			sort.Strings(codes)
			for _, c := range codes {
				r := s.resolve(resps[c])
				body := "—"
				if content, ok := r["content"].(map[string]any); ok {
					var parts []string
					for ct, media := range content {
						parts = append(parts, fmt.Sprintf("`%s` %s", ct, s.typeName(media.(map[string]any)["schema"])))
					}
					sort.Strings(parts)
					body = strings.Join(parts, ", ")
				}
				fmt.Fprintf(&b, "| %s | %s | %s |\n", c, cell(r["description"]), body)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Schemas\n\n")
	schemas := s.doc["components"].(map[string]any)["schemas"].(map[string]any)
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := s.merge(schemas[n].(map[string]any))
		fmt.Fprintf(&b, "<a id=\"schema-%s\"></a>\n### %s\n\n", strings.ToLower(n), n)
		if d, ok := schemas[n].(map[string]any)["description"].(string); ok {
			b.WriteString(d + "\n\n")
		}
		if enum, ok := sc["enum"].([]any); ok {
			fmt.Fprintf(&b, "One of: %s.\n\n", joinCode(enum))
			continue
		}
		props, _ := sc["properties"].(map[string]any)
		if len(props) == 0 {
			continue
		}
		req := map[string]bool{}
		for _, r := range asList(sc["required"]) {
			req[r.(string)] = true
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if req[keys[i]] != req[keys[j]] {
				return req[keys[i]]
			}
			return keys[i] < keys[j]
		})
		b.WriteString("| field | type | required | description |\n|---|---|---|---|\n")
		for _, k := range keys {
			p, _ := props[k].(map[string]any)
			r := ""
			if req[k] {
				r = "yes"
			}
			desc := cell(p["description"])
			if enum, ok := s.resolve(p)["enum"].([]any); ok && p["$ref"] == nil {
				desc = strings.TrimSpace(desc + " One of: " + joinCode(enum) + ".")
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", k, s.typeName(p), r, desc)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func pathOrder(p string) string {
	if strings.HasPrefix(p, "/api/v1/items") {
		return "0" + p
	}
	if strings.HasPrefix(p, "/api/v1/search") {
		return "1" + p
	}
	return "2" + p
}

func anchor(method, path string) string {
	r := strings.NewReplacer("/", "-", "{", "", "}", "", ".", "-")
	return method + strings.TrimRight(r.Replace(path), "-")
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func cell(v any) string {
	if v == nil {
		return ""
	}
	return strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(v)), "\n", " ")
}

func joinCode(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = "`" + fmt.Sprint(v) + "`"
	}
	return strings.Join(parts, ", ")
}

// typeName describes a schema briefly, linking named schemas.
func (s *Spec) typeName(node any) string {
	m, _ := node.(map[string]any)
	if m == nil {
		return ""
	}
	if ref, ok := m["$ref"].(string); ok {
		name := ref[strings.LastIndexByte(ref, '/')+1:]
		return fmt.Sprintf("[%s](#schema-%s)", name, strings.ToLower(name))
	}
	if one, ok := m["oneOf"].([]any); ok {
		parts := make([]string, len(one))
		for i, o := range one {
			parts[i] = s.typeName(o)
		}
		return strings.Join(parts, " or ")
	}
	switch t := m["type"].(type) {
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = fmt.Sprint(x)
		}
		return strings.Join(parts, " or ")
	case string:
		if t == "array" {
			return "array of " + s.typeName(m["items"])
		}
		if f, ok := m["format"].(string); ok {
			return t + " (" + f + ")"
		}
		return t
	}
	return "object"
}
