package lcu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakePerks is the client's rune page API: pages, inventory, delete, add.
type fakePerks struct {
	mu      sync.Mutex
	pages   string // GET /lol-perks/v1/pages
	canAdd  string // "true", "false" or "" (field missing)
	deleted []string
	posted  map[string]any
}

func (f *fakePerks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, p, _ := r.BasicAuth(); u != "riot" || p != "pw" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/lol-perks/v1/pages":
		io.WriteString(w, f.pages)
	case r.Method == "GET" && r.URL.Path == "/lol-perks/v1/inventory":
		if f.canAdd == "" {
			io.WriteString(w, `{"ownedPageCount":3}`)
		} else {
			io.WriteString(w, `{"canAddCustomPage":`+f.canAdd+`}`)
		}
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/lol-perks/v1/pages/"):
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/lol-perks/v1/pages/"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "POST" && r.URL.Path == "/lol-perks/v1/pages":
		json.NewDecoder(r.Body).Decode(&f.posted)
		io.WriteString(w, `{"id":99}`)
	default:
		http.NotFound(w, r)
	}
}

var galioPage = RunePage{PrimaryStyle: 8200, SubStyle: 8400,
	Perks: []int{8230, 8226, 8210, 8237, 8473, 8242, 5008, 5008, 5011}}

func TestImportRunesReplacesOwnPage(t *testing.T) {
	f := &fakePerks{pages: `[{"id":1,"name":"Mine","isEditable":true,"isDeletable":true},
		{"id":7,"name":"Draftsmith Imported","isEditable":true,"isDeletable":true},
		{"id":9,"name":"Draftsmith Ahri","isEditable":true,"isDeletable":true},
		{"id":8,"name":"Preset","isEditable":false,"isDeletable":false}]`}
	ts := httptest.NewServer(f)
	defer ts.Close()
	c := newClient(ts.URL, "pw", ts.Client())
	if err := c.ImportRunes(context.Background(), galioPage); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.deleted, ",") != "7,9" {
		t.Errorf("deleted %v, want only Draftsmith's pages", f.deleted)
	}
	if f.posted["name"] != "Draftsmith Imported" || f.posted["current"] != true || f.posted["primaryStyleId"] != 8200.0 ||
		len(f.posted["selectedPerkIds"].([]any)) != 9 {
		t.Errorf("posted %v", f.posted)
	}
}

func TestImportRunesNoRoom(t *testing.T) {
	f := &fakePerks{pages: `[{"id":1,"name":"Mine","isEditable":true,"isDeletable":true}]`, canAdd: "false"}
	ts := httptest.NewServer(f)
	defer ts.Close()
	err := newClient(ts.URL, "pw", ts.Client()).ImportRunes(context.Background(), galioPage)
	if !errors.Is(err, ErrNoRoom) || len(f.deleted) != 0 || f.posted != nil {
		t.Errorf("err %v, deleted %v, posted %v", err, f.deleted, f.posted)
	}
	// Room left (or the client doesn't say): add a page.
	f.canAdd = ""
	if err := newClient(ts.URL, "pw", ts.Client()).ImportRunes(context.Background(), galioPage); err != nil || f.posted == nil {
		t.Errorf("err %v, posted %v", err, f.posted)
	}
}

func TestImportRunesIncomplete(t *testing.T) {
	p := galioPage
	p.Perks = p.Perks[:6]
	if err := newClient("http://unused", "pw", http.DefaultClient).ImportRunes(context.Background(), p); err == nil {
		t.Error("a page without shards should be refused before calling the client")
	}
}
