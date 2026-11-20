package apispec_test

import (
	"os"
	"strings"
	"testing"

	"tibi/api"
	"tibi/internal/apispec"
)

func load(t *testing.T) *apispec.Spec {
	s, err := apispec.Load(api.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const item = `{"id":"aZ3k9QxP","kind":"image","url":"http://x/aZ3k9QxP","raw_url":"http://x/aZ3k9QxP.png",
"download_url":"http://x/aZ3k9QxP/dl","markdown":"![](x)","mime":"image/png","size":1,"width":2,"height":3,
"visibility":"unlisted","burn_after_read":false,"view_count":0,"expires_at":null,"created_at":1}`

// The validator must reject responses that diverge from the spec; this is
// what makes the suite-wide response check a contract test.
func TestValidatorCatchesDivergence(t *testing.T) {
	s := load(t)
	ok := func(method, path string, status int, body string) {
		t.Helper()
		if _, err := s.ValidateResponse(method, path, status, "application/json", []byte(body)); err != nil {
			t.Errorf("valid response rejected: %v", err)
		}
	}
	bad := func(method, path string, status int, ct, body, want string) {
		t.Helper()
		_, err := s.ValidateResponse(method, path, status, ct, []byte(body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
	ok("GET", "/api/v1/items/aZ3k9QxP", 200, item)
	ok("POST", "/api/v1/items", 201, item)
	ok("POST", "/api/v1/items", 201, "["+item+","+item+"]")
	ok("GET", "/api/v1/items", 200, `{"items":[`+item+`],"next_cursor":null}`)
	ok("GET", "/api/v1/search", 200, `{"items":[`+strings.TrimSuffix(item, "}")+`,"snippet":"x"}],"next_cursor":"abc"}`)
	ok("GET", "/api/v1/whoami", 401, `{"error":{"code":"unauthorized","message":"no"}}`)

	undocumented := strings.Replace(item, `"size":1`, `"size":1,"secret":true`, 1)
	bad("GET", "/api/v1/items/aZ3k9QxP", 200, "application/json", undocumented, `"secret" is not in the spec`)
	missing := strings.Replace(item, `"mime":"image/png",`, "", 1)
	bad("GET", "/api/v1/items/aZ3k9QxP", 200, "application/json", missing, `required property "mime" missing`)
	wrongType := strings.Replace(item, `"size":1`, `"size":"1"`, 1)
	bad("GET", "/api/v1/items/aZ3k9QxP", 200, "application/json", wrongType, "$.size: type string")
	badEnum := strings.Replace(item, `"kind":"image"`, `"kind":"picture"`, 1)
	bad("GET", "/api/v1/items/aZ3k9QxP", 200, "application/json", badEnum, "not one of")
	bad("GET", "/api/v1/items/aZ3k9QxP", 200, "application/json", strings.Replace(item, `"aZ3k9QxP","kind"`, `"short","kind"`, 1), "does not match")
	bad("GET", "/api/v1/items/aZ3k9QxP", 418, "application/json", `{}`, "status 418 is not documented")
	bad("GET", "/api/v1/items/aZ3k9QxP", 200, "text/html", item, "content type")
	bad("PUT", "/api/v1/items", 200, "application/json", `{}`, "not documented")
	bad("POST", "/api/v1/items", 201, "application/json", "["+item+"]", "oneOf") // one file must be an object
	bad("GET", "/api/v1/whoami", 401, "application/json", `{"error":{"code":"teapot","message":"x"}}`, "not one of")
	bad("GET", "/api/v1/search", 200, "application/json", `{"items":[`+strings.TrimSuffix(item, "}")+`,"rank":1}],"next_cursor":null}`, `"rank" is not in the spec`)
	bad("DELETE", "/api/v1/items/aZ3k9QxP", 204, "", "oops", "body not allowed")
}

func TestOperations(t *testing.T) {
	ops := load(t).Operations()
	// healthz, openapi, whoami, upload, list, get, patch, delete, fork,
	// share-link, search, and five collection operations
	if len(ops) != 16 {
		t.Errorf("documented operations = %d", len(ops))
	}
}

// docs/API.md must be regenerated whenever the spec changes.
func TestAPIDocUpToDate(t *testing.T) {
	want := load(t).Markdown()
	got, err := os.ReadFile("../../docs/API.md")
	if err != nil || string(got) != want {
		t.Fatal("docs/API.md is out of date; run: go run ./tools/apidoc > docs/API.md")
	}
}
