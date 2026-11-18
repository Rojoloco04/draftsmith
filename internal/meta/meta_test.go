package meta

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/opgg"
)

// fakeOPGG answers from the real responses captured in opgg/testdata.
type fakeOPGG struct {
	t     *testing.T
	calls []string
	args  []map[string]any
	down  bool
	// body overrides the answer for a tool.
	body map[string]string
}

func (f *fakeOPGG) CallTool(_ context.Context, name string, args map[string]any) (opgg.CallResult, error) {
	f.calls = append(f.calls, name)
	f.args = append(f.args, args)
	if f.down {
		return opgg.CallResult{}, errors.New("opgg tools/call: HTTP 503")
	}
	if b, ok := f.body[name]; ok {
		return opgg.CallResult{Text: b}, nil
	}
	file := map[string]string{
		"lol_list_lane_meta_champions": "tierlist_all.txt",
		"lol_list_champions":           "champions.txt",
		"lol_get_champion_analysis":    "analysis_ahri_mid.txt",
	}[name]
	b, err := os.ReadFile("../opgg/testdata/" + file)
	if err != nil {
		f.t.Fatal(err)
	}
	return opgg.CallResult{Text: string(b)}, nil
}

func (f *fakeOPGG) count(tool string) int {
	n := 0
	for _, c := range f.calls {
		if c == tool {
			n++
		}
	}
	return n
}

type memCache map[string]db.MetaEntry

func (m memCache) MetaEntry(_ context.Context, key string) (db.MetaEntry, error) {
	e, ok := m[key]
	if !ok {
		return db.MetaEntry{}, db.ErrNotFound
	}
	return e, nil
}

func (m memCache) PutMetaEntry(_ context.Context, e db.MetaEntry) error {
	m[e.Key] = e
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTest(t *testing.T) (*Service, *fakeOPGG, memCache, *clock) {
	f := &fakeOPGG{t: t, body: map[string]string{}}
	cache := memCache{}
	clk := &clock{time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC)}
	return NewWith(cache, func() Caller { return f }, clk.now), f, cache, clk
}

func TestTierList(t *testing.T) {
	ctx := context.Background()
	s, f, _, _ := newTest(t)
	tl, err := s.TierList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Stale() || tl.ChampionsErr != nil {
		t.Errorf("fresh fetch marked stale: %v / %v", tl.RefreshErr, tl.ChampionsErr)
	}
	for _, p := range Positions {
		es := tl.Position(p)
		if len(es) < 30 || es[0].Rank != 1 {
			t.Errorf("%s: %d entries", p, len(es))
		}
	}
	top := tl.Position("top")[0]
	if top.Name != "Malphite" || top.Key != "Malphite" || top.Play != 172610 || top.Win != 89146 || top.Tier != 1 {
		t.Errorf("top[0] = %+v", top)
	}
	if got := top.WinRate(); math.Abs(got-89146.0/172610) > 1e-12 {
		t.Errorf("win rate %v", got)
	}
	// Display names join to Riot keys through the champion table.
	for _, c := range []struct{ name, key string }{{"Dr. Mundo", "DrMundo"}, {"Cho'Gath", "Chogath"}, {"Wukong", "MonkeyKing"}, {"K'Sante", "KSante"}} {
		found := false
		for _, e := range tl.Entries {
			if e.Name == c.name {
				found = true
				if e.Key != c.key {
					t.Errorf("%s: key %q, want %q", c.name, e.Key, c.key)
				}
			}
		}
		if !found {
			t.Errorf("%s not in tier list", c.name)
		}
	}
	champs, err := s.Champions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range tl.Entries {
		if _, ok := champs.byName[normalize(e.Name)]; !ok {
			t.Errorf("%q is not in the champion table", e.Name)
		}
	}
	if _, ok := tl.Find("monkeyking", "top"); !ok {
		t.Error("Find should ignore case")
	}
	// One call, every position, identified as Draftsmith.
	if f.count("lol_list_lane_meta_champions") != 1 || f.args[0]["position"] != "all" || f.args[0]["client_app"] != "Draftsmith" {
		t.Errorf("calls %v args %v", f.calls, f.args[0])
	}
}

func TestCacheAndStaleFallback(t *testing.T) {
	ctx := context.Background()
	s, f, cache, clk := newTest(t)
	first, err := s.TierList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(23 * time.Hour)
	if _, err := s.TierList(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.count("lol_list_lane_meta_champions"); n != 1 {
		t.Fatalf("a day-old cache should be used, got %d calls", n)
	}

	// Past a day OP.GG is asked again; when it's down the old copy is served.
	clk.t = clk.t.Add(2 * time.Hour)
	f.down = true
	tl, err := s.TierList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !tl.Stale() || !tl.FetchedAt.Equal(first.FetchedAt) || len(tl.Entries) != len(first.Entries) {
		t.Errorf("want stale copy from %v, got stale=%v fetched %v, %d entries", first.FetchedAt, tl.Stale(), tl.FetchedAt, len(tl.Entries))
	}
	// Within RetryAfter there's no new attempt.
	calls := len(f.calls)
	clk.t = clk.t.Add(time.Minute)
	if tl, err := s.TierList(ctx); err != nil || !tl.Stale() {
		t.Errorf("got %v, stale=%v", err, tl.Stale())
	}
	if len(f.calls) != calls {
		t.Errorf("retried within RetryAfter: %v", f.calls[calls:])
	}
	// Once OP.GG is back it refreshes.
	clk.t = clk.t.Add(RetryAfter)
	f.down = false
	tl, err = s.TierList(ctx)
	if err != nil || tl.Stale() || !tl.FetchedAt.Equal(clk.t) || !cache["tierlist"].FetchedAt.Equal(clk.t) {
		t.Errorf("got %v, stale=%v, fetched %v", err, tl.Stale(), tl.FetchedAt)
	}
}

func TestNoCacheNoNetwork(t *testing.T) {
	s, f, _, _ := newTest(t)
	f.down = true
	if _, err := s.TierList(context.Background()); err == nil {
		t.Fatal("want an error with no cache and OP.GG down")
	}
}

func TestCacheOnly(t *testing.T) {
	ctx := context.Background()
	co := NewCacheOnly(memCache{})
	if _, err := co.TierList(ctx); !errors.Is(err, ErrNotCached) {
		t.Fatalf("empty cache: err %v, want ErrNotCached", err)
	}

	// Whatever is cached is served, however old, with no network call (the
	// cache-only service has no client to call).
	s, _, cache, _ := newTest(t)
	want, err := s.TierList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for k, e := range cache {
		e.FetchedAt = e.FetchedAt.Add(-90 * 24 * time.Hour)
		cache[k] = e
	}
	tl, err := NewCacheOnly(cache).TierList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Stale() || tl.ChampionsErr != nil || len(tl.Entries) != len(want.Entries) || !tl.FetchedAt.Equal(want.FetchedAt.Add(-90*24*time.Hour)) {
		t.Errorf("stale=%v champsErr=%v, %d entries, fetched %v", tl.Stale(), tl.ChampionsErr, len(tl.Entries), tl.FetchedAt)
	}
	if e, _ := tl.Find("MonkeyKing", "top"); e.Name != "Wukong" {
		t.Errorf("champion table not used from the cache: %+v", e)
	}

	// Without the champion table, keys are guessed but the tier list still works.
	delete(cache, "champions")
	tl, err = NewCacheOnly(cache).TierList(ctx)
	if err != nil || !errors.Is(tl.ChampionsErr, ErrNotCached) {
		t.Errorf("err %v, champsErr %v", err, tl.ChampionsErr)
	}
}

func TestPoolMarkdown(t *testing.T) {
	fetched := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tl := TierList{Freshness: Freshness{FetchedAt: fetched}, Entries: []TierEntry{{Key: "Ahri", Position: "mid", Tier: 1}}}
	var games []analysis.Game
	for i := 0; i < 10; i++ {
		games = append(games, analysis.Game{Champion: "Ahri", Role: "MIDDLE", Win: i < 8})
	}
	for i := 0; i < 4; i++ {
		games = append(games, analysis.Game{Champion: "Zed", Role: "MIDDLE", Win: i < 1})
	}
	games = append(games, analysis.Game{Champion: "Lux", Role: "MIDDLE"})
	fits := PoolFit(games, 0.5, tl, 1)

	md := PoolMarkdown(fits, tl, 0.5, fetched.Add(time.Hour))
	for _, want := range []string{
		"## Your pool vs the meta",
		"as of Nov 17 2026. Champions with 3+ games",
		"overall 50%",
		"| Ahri | Mid | 10 | 65% | 1 | Strong for you and strong in the meta (tier 1 Mid). Keep playing it. |",
		"| Zed | Mid | 4 | 43% | unlisted | Not working for you, and OP.GG doesn't list it Mid.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "Lux") || strings.Contains(md, "refreshes it") {
		t.Errorf("Lux has too few games, and a fresh list needs no age warning:\n%s", md)
	}
	if md := PoolMarkdown(fits, tl, 0.5, fetched.Add(3*24*time.Hour)); !strings.Contains(md, "(cached; `draftsmith serve` refreshes it)") {
		t.Errorf("old tier list not flagged:\n%s", md)
	}
	if md := PoolMarkdown(nil, tl, 0.5, fetched); md != "" {
		t.Errorf("no champions should render nothing, got %q", md)
	}
}

func TestSchemaChangeIsNotCached(t *testing.T) {
	ctx := context.Background()
	s, f, cache, clk := newTest(t)
	if _, err := s.TierList(ctx); err != nil {
		t.Fatal(err)
	}
	good := cache["tierlist"]
	clk.t = clk.t.Add(25 * time.Hour)
	// The server skips a field it no longer has and says so.
	f.body["lol_list_lane_meta_champions"] = `class LolListLaneMetaChampions: data,_field_diagnostics
class Data: positions
class Positions: top
class Top: champion
class FieldDiagnostics: unmatched_fields,hint

LolListLaneMetaChampions(Data(Positions([Top("Malphite")])),FieldDiagnostics(["data.positions.mid[].win"],"skipped"))`
	tl, err := s.TierList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var fe *opgg.FieldError
	if !errors.As(tl.RefreshErr, &fe) {
		t.Errorf("want a FieldError, got %v", tl.RefreshErr)
	}
	if cache["tierlist"] != good || len(tl.Position("mid")) == 0 {
		t.Error("a response with missing fields replaced the good copy")
	}
}

func TestChampionTableDown(t *testing.T) {
	s, f, _, _ := newTest(t)
	f.body["lol_list_champions"] = "class X: a\n\nX(1)" // parses, but no champions
	tl, err := s.TierList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tl.ChampionsErr == nil {
		t.Error("ChampionsErr not set")
	}
	if e, ok := tl.Find("DrMundo", "top"); !ok || e.Name != "Dr. Mundo" {
		t.Errorf("keys should fall back to stripped names, got %+v %v", e, ok)
	}
}

func TestAnalysis(t *testing.T) {
	s, f, cache, _ := newTest(t)
	a, err := s.Analysis(context.Background(), "Ahri", "mid")
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch != "16.22" || a.Play != 305981 || a.Tier != 1 || a.DamageType != "AP" || a.Runes.PrimaryStyle != 8100 || len(a.Runes.Primary) != 4 {
		t.Errorf("got %+v", a)
	}
	if len(a.Good) != 3 || a.Good[0].Key != "Galio" || len(a.Bad) != 3 || a.Bad[0] != (Counter{"Katarina", "Katarina", 7187, 3481}) {
		t.Errorf("counters %+v / %+v", a.Good, a.Bad)
	}
	if wr := a.Bad[0].WinRate(); wr > 0.49 || wr < 0.48 {
		t.Errorf("Ahri into Katarina %v, want about 0.484", wr)
	}
	if args := f.args[0]; args["champion"] != "AHRI" || args["tier"] != Bracket || args["position"] != "mid" {
		t.Errorf("args %v", args)
	}
	if e, ok := cache["analysis/AHRI/mid/emerald_plus/"+analysisVersion]; !ok || e.Patch != "16.22" {
		t.Errorf("cache entry %+v", e)
	}
	if _, err := s.Analysis(context.Background(), "Ahri", "middle"); err == nil {
		t.Error("want an error for an unknown position")
	}
}

func TestPoolFit(t *testing.T) {
	tl := TierList{Entries: []TierEntry{
		{Key: "Ahri", Position: "mid", Tier: 1},
		{Key: "Syndra", Position: "mid", Tier: 5},
		{Key: "Fiddlesticks", Position: "jungle", Tier: 4},
	}}
	var games []analysis.Game
	add := func(champ, role string, wins, losses int) {
		for i := 0; i < wins+losses; i++ {
			games = append(games, analysis.Game{Champion: champ, Role: role, Win: i < wins})
		}
	}
	add("Ahri", "MIDDLE", 8, 2)
	add("Ahri", "TOP", 0, 1)
	add("Syndra", "MIDDLE", 1, 5)
	add("FiddleSticks", "JUNGLE", 2, 2)
	add("Zed", "MIDDLE", 3, 0)
	add("Lux", "MIDDLE", 1, 0) // too few games
	games = append(games, analysis.Game{Champion: "Lux", Role: "MIDDLE", Remake: true}, analysis.Game{Champion: "Lux", Role: "MIDDLE", Remake: true})

	fits := PoolFit(games, 0.5, tl, PoolMinGames)
	got := map[string]Fit{}
	var order []string
	for _, f := range fits {
		got[f.Champion] = f
		order = append(order, f.Champion)
	}
	if strings.Join(order, ",") != "Ahri,Syndra,FiddleSticks,Zed" {
		t.Errorf("order %v", order)
	}
	check := func(champ, pos, mine, read, note string) {
		t.Helper()
		f := got[champ]
		if f.Position != pos || f.Mine != mine || f.MetaRead() != read || !strings.Contains(f.Note, note) {
			t.Errorf("%s: %s %s %s %q", champ, f.Position, f.Mine, f.MetaRead(), f.Note)
		}
	}
	check("Ahri", "mid", "good", "strong", "Strong for you and strong in the meta (tier 1 Mid)")
	check("Syndra", "mid", "bad", "weak", "easiest one to drop")
	check("FiddleSticks", "jungle", "even", "weak", "Even for you, weak this patch (tier 4 Jungle)")
	check("Zed", "mid", "good", "unlisted", "pocket pick")
}
