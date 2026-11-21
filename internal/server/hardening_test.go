package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/baojie/tibi/internal/testutil"
)

// TestBurnNotSearchable: another device's upload token must not read a
// burn-after-read paste through search, nor learn its ID from the list
// (design 2026-11-21 §5).
func TestBurnNotSearchable(t *testing.T) {
	e := testutil.New(t, nil)
	owner := e.Token("laptop", "upload")
	phone := e.Token("phone", "upload")
	full := e.Token("admin", "upload", "read_private")
	burn := up(t, e, owner, "?burn_after_read=1&title=handover", []byte("db_password=example-secret\n"))
	plain := up(t, e, owner, "", []byte("db_password is in the vault\n"))

	if got := search(t, e, phone, "db_password", ""); !eq(got, ids(plain)) {
		t.Errorf("phone search: %v, want only %s", got, plain.ID)
	}
	for _, tok := range []string{owner, full} {
		// Its content is never indexed, for anyone; its title still is.
		if got := search(t, e, tok, "example-secret", ""); len(got) != 0 {
			t.Errorf("burn content indexed: %v", got)
		}
		if got := search(t, e, tok, "handover", ""); !eq(got, ids(burn)) {
			t.Errorf("burn title search: %v", got)
		}
	}
	_, b := api(t, e, phone, "GET", "/api/v1/items", nil)
	if strings.Contains(string(b), burn.ID) || !strings.Contains(string(b), plain.ID) {
		t.Errorf("phone list: %s", b)
	}
	for _, tok := range []string{owner, full} {
		if _, b := api(t, e, tok, "GET", "/api/v1/items", nil); !strings.Contains(string(b), burn.ID) {
			t.Errorf("owner/read_private list lacks the burn item: %s", b)
		}
	}
	// Still unread: the recipient's reveal works.
	if resp, err := httpPost(e.Srv.URL + "/" + burn.ID + "/reveal"); err != nil || resp.code != 200 || !strings.Contains(resp.body, "example-secret") {
		t.Fatalf("reveal after the searches: %+v %v", resp, err)
	}
}

// TestUploadIntoPrivateCollection: a token that cannot see a private
// collection cannot add to it or learn about it (design 2026-11-21 §6).
func TestUploadIntoPrivateCollection(t *testing.T) {
	e := testutil.New(t, nil)
	owner := e.Token("laptop", "upload")
	phone := e.Token("phone", "upload")
	full := e.Token("admin", "upload", "read_private")
	code, b := api(t, e, owner, "POST", "/api/v1/collections", map[string]any{"title": "family", "visibility": "private"})
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	var c struct{ ID string }
	json.Unmarshal(b, &c)

	resp, body := upload(t, e, phone, "?collection="+c.ID, []byte("injected"))
	if resp.StatusCode != 400 || strings.Contains(string(body), "family") {
		t.Fatalf("phone into private collection: %d %s", resp.StatusCode, body)
	}
	for _, tok := range []string{owner, full} {
		if resp, body := upload(t, e, tok, "?collection="+c.ID, []byte("ok")); resp.StatusCode != 201 {
			t.Fatalf("allowed upload: %d %s", resp.StatusCode, body)
		}
	}
	_, b = api(t, e, owner, "GET", "/api/v1/collections/"+c.ID, nil)
	if strings.Contains(string(b), "injected") || strings.Count(string(b), `"kind"`) != 2 {
		t.Fatalf("collection contents: %s", b)
	}
}
