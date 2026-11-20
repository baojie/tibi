// Package apispec loads api/openapi.yaml and checks HTTP responses against
// it. It implements the subset of OpenAPI 3.1 / JSON Schema that the tibi
// contract uses: $ref, type (including type lists such as
// [integer, "null"]), properties, required, additionalProperties: false,
// items, minItems, enum, pattern, minimum/maximum, oneOf, and allOf merged
// with unevaluatedProperties: false.
package apispec

import (
	"encoding/json"
	"fmt"
	"mime"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is a parsed OpenAPI document.
type Spec struct {
	doc   map[string]any
	paths []pathTemplate
}

type pathTemplate struct {
	raw string
	re  *regexp.Regexp
}

// Load parses an OpenAPI document.
func Load(b []byte) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	s := &Spec{doc: doc}
	paths, _ := doc["paths"].(map[string]any)
	for p := range paths {
		re := "^" + regexp.MustCompile(`\\\{[^}]+\\\}`).ReplaceAllString(regexp.QuoteMeta(p), `[^/]+`) + "$"
		s.paths = append(s.paths, pathTemplate{p, regexp.MustCompile(re)})
	}
	// Literal paths before templated ones.
	sort.Slice(s.paths, func(i, j int) bool {
		return strings.Count(s.paths[i].raw, "{") < strings.Count(s.paths[j].raw, "{")
	})
	return s, nil
}

// Doc returns the raw document.
func (s *Spec) Doc() map[string]any { return s.doc }

// Operation identifies a documented method + path template.
type Operation struct {
	Method string // upper case
	Path   string // template, e.g. /api/v1/items/{id}
}

// Operations lists every documented operation with its response statuses.
func (s *Spec) Operations() map[Operation][]string {
	out := map[Operation][]string{}
	paths, _ := s.doc["paths"].(map[string]any)
	for p, pi := range paths {
		for m, op := range pi.(map[string]any) {
			if m == "parameters" {
				continue
			}
			resp, _ := op.(map[string]any)["responses"].(map[string]any)
			var codes []string
			for c := range resp {
				codes = append(codes, c)
			}
			sort.Strings(codes)
			out[Operation{strings.ToUpper(m), p}] = codes
		}
	}
	return out
}

// Match finds the documented operation for a request path.
func (s *Spec) Match(method, path string) (Operation, bool) {
	paths := s.doc["paths"].(map[string]any)
	for _, pt := range s.paths {
		if pt.re.MatchString(path) {
			if _, ok := paths[pt.raw].(map[string]any)[strings.ToLower(method)]; ok {
				return Operation{strings.ToUpper(method), pt.raw}, true
			}
		}
	}
	return Operation{}, false
}

// ValidateResponse checks a response to method+path against the spec: the
// operation and status must be documented, the content type must be one the
// response declares, and a JSON body must match its schema.
func (s *Spec) ValidateResponse(method, path string, status int, contentType string, body []byte) (Operation, error) {
	op, ok := s.Match(method, path)
	if !ok {
		return op, fmt.Errorf("%s %s is not documented", method, path)
	}
	opDoc := s.doc["paths"].(map[string]any)[op.Path].(map[string]any)[strings.ToLower(method)].(map[string]any)
	responses, _ := opDoc["responses"].(map[string]any)
	r, ok := responses[strconv.Itoa(status)]
	if !ok {
		r, ok = responses["default"]
	}
	if !ok {
		return op, fmt.Errorf("%s %s: status %d is not documented", method, op.Path, status)
	}
	resp := s.resolve(r)
	content, _ := resp["content"].(map[string]any)
	if len(content) == 0 {
		if len(body) > 0 {
			return op, fmt.Errorf("%s %s %d: body not allowed", method, op.Path, status)
		}
		return op, nil
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	media, ok := content[mt]
	if !ok {
		return op, fmt.Errorf("%s %s %d: content type %q not documented", method, op.Path, status, mt)
	}
	if mt != "application/json" {
		return op, nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return op, fmt.Errorf("%s %s %d: invalid JSON: %v", method, op.Path, status, err)
	}
	schema, _ := media.(map[string]any)["schema"]
	if err := s.validate(schema, v, "$"); err != nil {
		return op, fmt.Errorf("%s %s %d: %v", method, op.Path, status, err)
	}
	return op, nil
}

// Validate checks a decoded JSON value against a schema (for tests).
func (s *Spec) Validate(schemaRef string, v any) error {
	return s.validate(map[string]any{"$ref": schemaRef}, v, "$")
}

// resolve follows $ref chains to the referenced object.
func (s *Spec) resolve(node any) map[string]any {
	m, _ := node.(map[string]any)
	for i := 0; i < 16 && m != nil; i++ {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		var cur any = s.doc
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cur = cur.(map[string]any)[part]
		}
		m, _ = cur.(map[string]any)
	}
	return m
}

// merge combines an allOf schema into one object schema.
func (s *Spec) merge(schema map[string]any) map[string]any {
	all, ok := schema["allOf"].([]any)
	if !ok {
		return schema
	}
	out := map[string]any{"type": "object"}
	props := map[string]any{}
	var required []any
	own := map[string]any{}
	for k, v := range schema {
		if k != "allOf" {
			own[k] = v
		}
	}
	parts := append([]any{}, all...)
	parts = append(parts, own)
	for _, p := range parts {
		ps := s.merge(s.resolve(p))
		pp, _ := ps["properties"].(map[string]any)
		for k, v := range pp {
			props[k] = v
		}
		if r, ok := ps["required"].([]any); ok {
			required = append(required, r...)
		}
	}
	out["properties"] = props
	out["required"] = required
	if schema["unevaluatedProperties"] == false {
		out["additionalProperties"] = false
	}
	return out
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		if _, err := x.Int64(); err == nil && !strings.ContainsAny(x.String(), ".eE") {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func (s *Spec) validate(node any, v any, at string) error {
	schema := s.resolve(node)
	if schema == nil {
		return nil
	}
	if _, ok := schema["allOf"]; ok {
		schema = s.merge(schema)
	}
	if one, ok := schema["oneOf"].([]any); ok {
		matches, firstErr := 0, error(nil)
		for _, sub := range one {
			if err := s.validate(sub, v, at); err == nil {
				matches++
			} else if firstErr == nil {
				firstErr = err
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: matches %d of the oneOf schemas (first mismatch: %v)", at, matches, firstErr)
		}
		return nil
	}
	got := typeOf(v)
	if t, ok := schema["type"]; ok {
		var allowed []string
		switch tt := t.(type) {
		case string:
			allowed = []string{tt}
		case []any:
			for _, x := range tt {
				allowed = append(allowed, x.(string))
			}
		}
		okType := false
		for _, a := range allowed {
			if a == got || (a == "number" && got == "integer") {
				okType = true
			}
		}
		if !okType {
			return fmt.Errorf("%s: type %s, want %v", at, got, allowed)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if fmt.Sprint(e) == fmt.Sprint(v) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: %v is not one of %v", at, v, enum)
		}
	}
	switch got {
	case "string":
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(v.(string)) {
			return fmt.Errorf("%s: %q does not match %s", at, v, p)
		}
	case "integer", "number":
		f, _ := v.(json.Number).Float64()
		if m, ok := num(schema["minimum"]); ok && f < m {
			return fmt.Errorf("%s: %v < minimum %v", at, f, m)
		}
		if m, ok := num(schema["maximum"]); ok && f > m {
			return fmt.Errorf("%s: %v > maximum %v", at, f, m)
		}
	case "array":
		arr := v.([]any)
		if m, ok := num(schema["minItems"]); ok && float64(len(arr)) < m {
			return fmt.Errorf("%s: %d items < minItems %v", at, len(arr), m)
		}
		for i, el := range arr {
			if err := s.validate(schema["items"], el, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case "object":
		obj := v.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					return fmt.Errorf("%s: required property %q missing", at, r)
				}
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ps, known := props[k]
			if !known {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: property %q is not in the spec", at, k)
				}
				continue
			}
			if err := s.validate(ps, obj[k], at+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
