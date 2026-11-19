package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/fixture"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

// newTwoAccountApp serves an archive with Ahri games on the first account
// (Tester#NA1) and Sylas games on an alt (Alt#SLU), each with a rank.
func newTwoAccountApp(t *testing.T) *httptest.Server {
	t.Helper()
	const alt = "alt-puuid"
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	var specs []fixture.Spec
	for i := 0; i < 6; i++ {
		sp := fixture.Spec{ID: fmt.Sprintf("NA1_%d", 2000+i), StartMs: base.Add(time.Duration(i) * time.Hour).UnixMilli(), Champion: "Ahri", Opponent: "Zed", Win: true}
		if i%2 == 1 {
			sp.Champion, sp.PUUID, sp.Win = "Sylas", alt, false
		}
		specs = append(specs, sp)
	}
	if err := fixture.Archive(s, specs, false); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 17, 0, 0, 0, 0, time.Local)
	fixture.RankSnapshot(s, day, "EMERALD", "III", 12, 22, 20)
	s.PutRankSnapshot(alt, day.AddDate(0, 0, 1), []riot.LeagueEntry{{QueueType: "RANKED_SOLO_5x5", Tier: "GOLD", Rank: "I", LeaguePoints: 75, Wins: 5, Losses: 9}})

	ctx := context.Background()
	d, err := db.OpenDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Index(ctx, s, []string{fixture.MePUUID, alt}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(d, []Account{{PUUID: fixture.MePUUID, RiotID: "Tester#NA1"}, {PUUID: alt, RiotID: "Alt#SLU"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAccountSwitch(t *testing.T) {
	ts := newTwoAccountApp(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	page := func(path string) string {
		t.Helper()
		resp, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d\n%s", path, resp.StatusCode, b)
		}
		return string(b)
	}

	// All accounts by default: both in the menu, both ranks, both pools.
	body := page("/")
	expect(t, body, `class="acct-menu"`, "All accounts", "2 accounts", "Tester#NA1", "Alt#SLU",
		"Emerald III", "Gold I", `class="acct-rank"`, `class="legend"`)
	body = page("/champions")
	expect(t, body, "Ahri", "Sylas")

	// Switching to the alt shows only its games and rank, and goes back.
	resp, err := c.Get(ts.URL + "/account?id=alt%23slu&back=%2Fchampions%3Fqueue%3Dsolo")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Request.URL.Path != "/champions" || resp.Request.URL.RawQuery != "queue=solo" {
		t.Errorf("switch went to %s", resp.Request.URL)
	}
	if strings.Contains(string(b), "Ahri") || !strings.Contains(string(b), "Sylas") {
		t.Errorf("alt's champions page shows the wrong games")
	}
	body = page("/")
	expect(t, body, "Alt#SLU", "Gold I")
	if strings.Contains(body, "Emerald III") || strings.Contains(body, `class="acct-rank"`) {
		t.Error("alt's overview shows the other account's rank")
	}
	body = page("/matches")
	if strings.Contains(body, "Ahri") {
		t.Error("alt's match history has the other account's games")
	}

	// Back to all of them.
	page("/account?id=all&back=/")
	expect(t, page("/champions"), "Ahri", "Sylas")
}

func TestAccountSwitchStaysOnSite(t *testing.T) {
	ts := newTwoAccountApp(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for back, want := range map[string]string{
		"/matches?queue=flex":  "/matches?queue=flex",
		"//evil.example/":      "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
		"":                     "/",
	} {
		resp, err := c.Get(ts.URL + "/account?id=all&back=" + urlQuery(back))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("back=%q: redirected to %q, want %q", back, got, want)
		}
	}
}

func urlQuery(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "?", "%3F", "=", "%3D", "#", "%23", "\\", "%5C", "/", "%2F", ":", "%3A")
	return r.Replace(s)
}

func TestOneAccountHasNoSwitch(t *testing.T) {
	ts := newTestServer(t)
	_, body := get(t, ts, "/")
	if strings.Contains(body, "acct-menu") {
		t.Error("a single account shouldn't get the account menu")
	}
	expect(t, body, "Tester#NA1", "Emerald III")
}
