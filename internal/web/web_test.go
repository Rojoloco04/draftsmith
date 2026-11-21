package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/fixture"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

// newTestServer indexes 30 synthetic games (Ahri/Syndra/MonkeyKing mid,
// two of them flex) plus the real scrubbed Vladimir game.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServerWith(t, nil)
}

// newTestServerWith is newTestServer with OP.GG data from m (nil for off).
func newTestServerWith(t *testing.T, m MetaSource) *httptest.Server {
	t.Helper()
	_, ts := newTestApp(t, m)
	return ts
}

// newTestApp is newTestServerWith, also returning the Server.
func newTestApp(t *testing.T, m MetaSource) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	var specs []fixture.Spec
	for i := 0; i < 30; i++ {
		sp := fixture.Spec{
			ID: fmt.Sprintf("NA1_%d", 1000+i), StartMs: base.Add(time.Duration(i) * 24 * time.Hour).UnixMilli(),
			Champion: []string{"Ahri", "Syndra", "MonkeyKing"}[i%3], Opponent: []string{"Zed", "Yone"}[i%2],
			Win: i%3 != 1, Kills: 6, Deaths: 3, Assists: 8, GoldLead: 200 * (i%5 - 2), CSLead: i%7 - 3,
			EarlyDeathsEnemySide: i % 2,
		}
		if i >= 28 {
			sp.Queue = riot.QueueFlex
		}
		specs = append(specs, sp)
	}
	if err := fixture.Archive(s, specs, true); err != nil {
		t.Fatal(err)
	}
	fixture.RankSnapshot(s, time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local), "EMERALD", "IV", 40, 20, 20)
	fixture.RankSnapshot(s, time.Date(2026, 11, 17, 0, 0, 0, 0, time.Local), "EMERALD", "III", 12, 22, 20)

	ctx := context.Background()
	d, err := db.OpenDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Index(ctx, s, []string{fixture.MePUUID}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(d, []Account{{PUUID: fixture.MePUUID, RiotID: "Tester#NA1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetStatus(func(st *Status) { st.LastSync = time.Now().Add(-5 * time.Minute) })
	if m != nil {
		srv.SetMeta(m)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func get(t *testing.T, ts *httptest.Server, path string, hdr ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func expect(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestOverview(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts, "/")
	if code != 200 {
		t.Fatalf("status %d\n%s", code, body)
	}
	expect(t, body,
		"Tester#NA1", "Emerald III", "12 LP", "Rank history", // LP chart
		`data-nav="overview" aria-current="page"`, "Last sync 5 minutes ago",
		`class="hexstrip"`, `href="/matches/NA1_1027"`, // recent matches
		"Champion pool", `class="pool"`, "Wukong", `href="/champions/MonkeyKing?queue=solo"`, // pool plot, display name for MonkeyKing
		"ranked-emblem/emblem-emerald.png", "ranked-mini-crests/emerald.svg", // rank icons
		"/img/champion/splash/", // banner art
	)
	for _, gone := range []string{"What stands out", "Top champions", "Recent form", "Roles", "Where you die", "Gold lead at 15"} {
		if strings.Contains(body, gone) {
			t.Errorf("overview still shows %q", gone)
		}
	}
	// Solo queue only by default: 28 synthetic solo games + the real one.
	if !strings.Contains(body, "<strong>29</strong>") {
		t.Error("overview should count 29 solo games")
	}
	_, all := get(t, ts, "/?queue=all")
	if !strings.Contains(all, "<strong>31</strong>") {
		t.Error("queue=all should count 31 games")
	}
}

func TestMatchesAndFilters(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts, "/matches")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expect(t, body, "<!doctype html>", "29 games · page 1 of 2", "Older →", `href="/matches/NA1_1027"`, "Vladimir")
	if n := strings.Count(body, `class="mrow`); n != pageSize {
		t.Errorf("%d rows on page 1", n)
	}
	// Every non-remake row carries my DS score.
	if n := strings.Count(body, `<span class="ds" data-tip="DS score`); n != pageSize {
		t.Errorf("%d rows with an DS score on page 1", n)
	}

	_, body = get(t, ts, "/matches?queue=solo&page=2")
	expect(t, body, "page 2 of 2", "← Newer")
	if n := strings.Count(body, `class="mrow`); n != 9 {
		t.Errorf("%d rows on page 2", n)
	}

	// htmx filter request returns only the results fragment.
	code, body = get(t, ts, "/matches?queue=solo&champion=Ahri", "HX-Request", "true")
	if code != 200 || strings.Contains(body, "<!doctype html>") || !strings.HasPrefix(strings.TrimSpace(body), `<div id="results">`) {
		t.Fatalf("partial response wrong (%d):\n%.300s", code, body)
	}
	expect(t, body, "10 games")
	if strings.Contains(body, "Syndra") {
		t.Error("champion filter leaked other champions")
	}
	_, body = get(t, ts, "/matches?queue=all&role=TOP")
	expect(t, body, "0 games", "No games match")
}

func TestMatchDetail(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts, "/matches/"+fixture.RealMatchID)
	if code != 200 {
		t.Fatalf("status %d\n%s", code, body)
	}
	expect(t, body, "Vladimir", "Defeat", "Yone", "My team", "Enemy team",
		"Gold vs lane opponent", "Where I died", `class="death"`,
		"-788", // gold diff at 15, pinned by analysis.TestExtractRealGame
		"/img/item/", "16.22.1",
		// My spells and rune page: Ghost + Flash, Stormraider's Surge (Sorcery) with Precision.
		"/img/spell/SummonerHaste.png", "/img/spell/SummonerFlash.png", "StormraidersSurgeRuneIcon2.png",
		"perk-images/Styles/7201_Precision.png", "StatModsCDRScalingIcon.png",
	)
	// DS score for all ten players: one MVP and one SVP.
	if n := strings.Count(body, `<span class="ds" data-tip`); n != 10 {
		t.Errorf("%d players with an DS score", n)
	}
	if strings.Count(body, `class="dsb mvp">MVP<`) != 1 || strings.Count(body, `class="dsb svp">SVP<`) != 1 {
		t.Error("want exactly one MVP and one SVP")
	}
	if n := strings.Count(body, "<tr class=\"me\">"); n != 1 {
		t.Errorf("%d rows marked as me", n)
	}

	// Other players are champion + position only. The fixture's scrubbed
	// names stand in for real ones.
	var m map[string]any
	json.Unmarshal(fixture.RealMatchJSON(), &m)
	for _, p := range m["info"].(map[string]any)["participants"].([]any) {
		pm := p.(map[string]any)
		for _, k := range []string{"puuid", "riotIdGameName", "summonerName", "summonerId"} {
			v, _ := pm[k].(string)
			if len(v) > 2 && strings.Contains(body, v) {
				t.Errorf("match page shows %s %q", k, v)
			}
		}
	}

	code, _ = get(t, ts, "/matches/NA1_404")
	if code != 404 {
		t.Errorf("missing match status %d", code)
	}
}

func TestChampions(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts, "/champions?sort=adj")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expect(t, body, "Ahri", "Syndra", "Wukong", "Vladimir", `sort=adj" aria-current="true"`,
		"Roles", `href="#r-mid"`) // roles card with its glyph

	code, body = get(t, ts, "/champions/MonkeyKing")
	if code != 200 {
		t.Fatalf("champion page status %d\n%s", code, body)
	}
	expect(t, body, "Wukong", "Matchups", "Zed", "Yone", "Adjusted win rate")
	if n := strings.Count(body, `class="mrow`); n != 9 {
		t.Errorf("%d Wukong solo games listed", n)
	}

	code, _ = get(t, ts, "/champions/Teemo")
	if code != 404 {
		t.Errorf("unplayed champion status %d", code)
	}
}

func TestStubsStaticAndHeaders(t *testing.T) {
	ts := newTestServer(t)
	for _, p := range []string{"/champ-select"} {
		code, body := get(t, ts, p)
		if code != 200 || !strings.Contains(body, "--no-watch") {
			t.Errorf("%s: %d", p, code)
		}
	}
	for _, p := range []string{"/static/htmx.min.js", "/static/style.css", "/static/app.js"} {
		code, _ := get(t, ts, p)
		if code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
	code, _ := get(t, ts, "/nope")
	if code != 404 {
		t.Errorf("unknown path: %d", code)
	}
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestEmptyIndex(t *testing.T) {
	ctx := context.Background()
	d, err := db.OpenDir(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	srv, err := New(d, []Account{{PUUID: "nobody", RiotID: "Nobody#NA1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetStatus(func(st *Status) { st.SyncDisabled = "RIOT_API_KEY is not set" })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, p := range []string{"/", "/matches", "/champions"} {
		code, body := get(t, ts, p)
		if code != 200 {
			t.Errorf("%s: %d\n%s", p, code, body)
		}
		if p == "/" {
			expect(t, body, "No games yet", "Background sync off: RIOT_API_KEY is not set")
		}
	}
}

func TestLadderPoints(t *testing.T) {
	cases := []struct {
		tier, div string
		lp        int
		want      float64
		label     string
	}{
		{"IRON", "IV", 0, 0, "Iron IV"},
		{"EMERALD", "IV", 56, 2056, "Emerald IV"},
		{"EMERALD", "I", 99, 2399, "Emerald I"},
		{"DIAMOND", "II", 10, 2610, "Diamond II"},
		{"MASTER", "I", 120, 2920, "Master+"},
	}
	for _, c := range cases {
		got := ladderPoints(c.tier, c.div, c.lp)
		if got != c.want || ladderLabel(got) != c.label {
			t.Errorf("%s %s %d: %v %q", c.tier, c.div, c.lp, got, ladderLabel(got))
		}
	}
}

func TestNiceRange(t *testing.T) {
	cases := []struct {
		lo, hi   float64
		n        int
		wlo, whi float64
		wn       int
	}{
		{0, 3100, 4, 0, 4000, 4},
		{0, 70, 4, 0, 80, 4},
		{-788, 833, 4, -1000, 1000, 4},
		{0.31, 0.74, 4, 0.2, 0.8, 3},
		{5, 5, 4, 5, 5, 4},
	}
	for _, c := range cases {
		lo, hi, n := niceRange(c.lo, c.hi, c.n)
		if math.Abs(lo-c.wlo) > 1e-9 || math.Abs(hi-c.whi) > 1e-9 || n != c.wn {
			t.Errorf("niceRange(%v, %v, %d) = %v, %v, %d", c.lo, c.hi, c.n, lo, hi, n)
		}
	}
}
