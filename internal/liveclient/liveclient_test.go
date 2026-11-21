package liveclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshot(t *testing.T) {
	var gotRiotID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/liveclientdata/activeplayer":
			w.Write([]byte(`{"level":4,"riotId":"Me#NA1","currentGold":512.5,
				"abilities":{"Q":{"abilityLevel":2},"W":{"abilityLevel":1},"E":{"abilityLevel":1},"R":{"abilityLevel":0},"Passive":{}}}`))
		case "/liveclientdata/playeritems":
			gotRiotID = r.URL.Query().Get("riotId")
			w.Write([]byte(`[{"itemID":1056,"displayName":"Doran's Ring","count":1,"slot":0},
				{"itemID":2003,"displayName":"Health Potion","count":2,"slot":1},
				{"itemID":3340,"displayName":"Stealth Ward","count":1,"slot":6}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	s, err := NewWith(ts.URL, ts.Client()).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotRiotID != "Me#NA1" {
		t.Errorf("items asked for %q", gotRiotID)
	}
	if s.Level != 4 || s.Spent() != 4 || s.Skills["Q"] != 2 || len(s.Items) != 3 || !s.Has(1056) || s.Has(3020) {
		t.Errorf("snapshot: %+v", s)
	}
}

func TestSnapshotNotReady(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	if _, err := NewWith(ts.URL, ts.Client()).Snapshot(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Errorf("404: %v", err)
	}
	ts.Close()
	// Nothing listening (no game) is not ready either, and the error
	// carries no URL.
	if _, err := NewWith(ts.URL, ts.Client()).Snapshot(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Errorf("closed: %v", err)
	}
}
