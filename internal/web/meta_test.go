package web

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/meta"
)

type fakeMeta struct {
	tl       meta.TierList
	tlErr    error
	analysis map[string]meta.Analysis // key/position
	asked    []string
}

func (f *fakeMeta) TierList(context.Context) (meta.TierList, error) { return f.tl, f.tlErr }

func (f *fakeMeta) Analysis(_ context.Context, key, pos string) (meta.Analysis, error) {
	f.asked = append(f.asked, key+"/"+pos)
	a, ok := f.analysis[key+"/"+pos]
	if !ok {
		return meta.Analysis{}, meta.ErrNoData
	}
	return a, nil
}

func newFakeMeta() *fakeMeta {
	fetched := time.Now().Add(-3 * time.Hour)
	e := func(name, key, pos string, tier, rank, play, win int) meta.TierEntry {
		return meta.TierEntry{Name: name, Key: key, Position: pos, Tier: tier, Rank: rank, Play: play, Win: win, PickRate: 0.05, BanRate: 0.02}
	}
	return &fakeMeta{
		tl: meta.TierList{Freshness: meta.Freshness{FetchedAt: fetched}, Entries: []meta.TierEntry{
			e("Malphite", "Malphite", "top", 1, 1, 170000, 88000),
			e("Ahri", "Ahri", "mid", 1, 1, 290000, 149000),
			e("Zed", "Zed", "mid", 2, 2, 200000, 99000),
			e("Wukong", "MonkeyKing", "mid", 4, 30, 20000, 9900),
			e("Syndra", "Syndra", "mid", 5, 40, 230000, 112000),
		}},
		analysis: map[string]meta.Analysis{
			"Ahri/mid": {
				Freshness: meta.Freshness{FetchedAt: fetched, Patch: "16.22"}, Key: "Ahri", Position: "mid", Bracket: meta.Bracket,
				Play: 305981, WinRate: 0.51, PickRate: 0.12, BanRate: 0.04, Tier: 1, Rank: 3, DamageType: "AP",
				Good: []meta.Counter{{Name: "Zed", Key: "Zed", Play: 5020, Win: 2791}},
				Bad:  []meta.Counter{{Name: "Katarina", Key: "Katarina", Play: 7187, Win: 3481}},
			},
		},
	}
}

func TestTierListPage(t *testing.T) {
	fm := newFakeMeta()
	ts := newTestServerWith(t, fm)

	// Defaults to my main role (the fixtures are all mid).
	code, body := get(t, ts, "/tier-list")
	if code != 200 {
		t.Fatalf("status %d\n%s", code, body)
	}
	expect(t, body, `position=mid&amp;sort=rank" aria-current="true"`, "Ahri", "Wukong", "Syndra", "updated 3 hours ago",
		`href="/champions/MonkeyKing?queue=solo"`, `class="tierb t1">OP<`, `class="tierb t5">T5<`)
	if strings.Contains(body, "Malphite") {
		t.Error("top laner on the mid list")
	}
	// Win rate is win/play to one decimal, not OP.GG's rounded figure.
	expect(t, body, "51.4%") // Ahri 149000/290000
	// Zed isn't in my pool, so no link and no record.
	if strings.Contains(body, `/champions/Zed`) {
		t.Error("unplayed champion linked")
	}

	_, body = get(t, ts, "/tier-list?position=top&sort=wr")
	expect(t, body, "Malphite", `sort=wr" aria-current="true"`)
	if strings.Contains(body, "Syndra") {
		t.Error("mid laner on the top list")
	}

	_, body = get(t, ts, "/tier-list?sort=wr")
	if strings.Index(body, ">Ahri") > strings.Index(body, ">Syndra") {
		t.Error("sorting by win rate should put Ahri (51.4%) above Syndra (48.7%)")
	}
}

func TestTierListStaleAndDown(t *testing.T) {
	fm := newFakeMeta()
	fm.tl.RefreshErr = errors.New("opgg tools/call: HTTP 503")
	ts := newTestServerWith(t, fm)
	_, body := get(t, ts, "/tier-list")
	expect(t, body, "Showing the cached copy", "HTTP 503", "Ahri")

	fm.tl, fm.tlErr = meta.TierList{}, errors.New("dial tcp: no such host")
	code, body := get(t, ts, "/tier-list")
	if code != 200 {
		t.Errorf("status %d", code)
	}
	expect(t, body, "Couldn't load OP.GG data", "no such host")
	_, body = get(t, ts, "/champions")
	expect(t, body, "Ahri") // the pool table still works
	if strings.Contains(body, "pool vs the meta") {
		t.Error("pool card without a tier list")
	}
}

func TestMetaOff(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts, "/tier-list")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	expect(t, body, "turned off")
	_, body = get(t, ts, "/champions/Ahri")
	expect(t, body, "turned off", "Matchups")
}

func TestChampionsMeta(t *testing.T) {
	fm := newFakeMeta()
	ts := newTestServerWith(t, fm)
	_, body := get(t, ts, "/champions")
	expect(t, body, "Your pool vs the meta",
		"Strong for you and strong in the meta (tier 1 Mid)", // Ahri, unbeaten
		"Losing for you and weak this patch (tier 5 Mid)",    // Syndra, winless
		"weak this patch (tier 4 Mid)",                       // Wukong
		`title="OP.GG Mid, rank 40"`)

	_, body = get(t, ts, "/champions/Ahri")
	expect(t, body, "OP.GG Mid", "patch 16.22", "Does best into", "Struggles against", "Katarina", "55.6%", "AP")
	// My record into Zed shows next to OP.GG's row: the fixtures have Ahri
	// against Zed 5 times, all wins.
	if i := strings.Index(body, "Does best into"); i < 0 || !strings.Contains(body[i:], "5-0") {
		t.Error("my record against Zed missing")
	}
	if len(fm.asked) != 1 || fm.asked[0] != "Ahri/mid" {
		t.Errorf("asked %v", fm.asked)
	}

	_, body = get(t, ts, "/champions/Syndra")
	expect(t, body, "Couldn't load OP.GG data", "Matchups")
}
