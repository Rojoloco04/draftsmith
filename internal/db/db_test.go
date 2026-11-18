package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/fixture"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

func specs() []fixture.Spec {
	base := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	var out []fixture.Spec
	for i := 0; i < 12; i++ {
		out = append(out, fixture.Spec{
			ID: fmt.Sprintf("NA1_%d", 100+i), StartMs: base.Add(time.Duration(i) * 24 * time.Hour).UnixMilli(),
			Champion: []string{"Ahri", "Syndra"}[i%2], Opponent: "Zed", Win: i%3 != 0,
			Kills: 5, Deaths: 3, Assists: 7, GoldLead: 300 * (i%3 - 1), CSLead: i - 5, EarlyDeathsEnemySide: i % 3,
			NoTimeline: i == 4,
		})
	}
	out[11].Queue = riot.QueueFlex
	out[10].Seconds = 200 // remake
	return out
}

func setup(t *testing.T) (*DB, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.Archive(s, specs(), true); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if !d.Rebuilt {
		t.Error("new database should report Rebuilt")
	}
	return d, s
}

// TestIndexMatchesExtract checks that a game read back from SQLite is
// exactly what analysis.Extract computes from the raw JSON.
func TestIndexMatchesExtract(t *testing.T) {
	ctx := context.Background()
	d, s := setup(t)
	res, err := d.Index(ctx, s, fixture.MePUUID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 13 || res.Updated != 0 || res.Skipped != 0 {
		t.Fatalf("first index: %+v", res)
	}

	want, err := analysis.LoadGames(s, fixture.MePUUID, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Games(ctx, GameFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d games, want %d", len(got), len(want))
	}
	for i := range want {
		w := want[i]
		w.DeathSpots = nil // Games leaves these out; Match fills them
		if !reflect.DeepEqual(got[i], w) {
			t.Errorf("game %s differs\n got %+v\nwant %+v", w.MatchID, got[i], w)
		}
		g, _, err := d.Match(ctx, w.MatchID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(g, want[i]) {
			t.Errorf("Match(%s) differs\n got %+v\nwant %+v", w.MatchID, g, want[i])
		}
	}

	// Second run is a no-op.
	res, err = d.Index(ctx, s, fixture.MePUUID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Updated != 0 {
		t.Fatalf("second index: %+v", res)
	}
}

func TestMatchDetail(t *testing.T) {
	ctx := context.Background()
	d, s := setup(t)
	if _, err := d.Index(ctx, s, fixture.MePUUID); err != nil {
		t.Fatal(err)
	}
	m, tl := fixture.Real()
	wantDet, _ := analysis.ExtractDetail(&m, &tl, fixture.MePUUID)
	g, det, err := d.Match(ctx, fixture.RealMatchID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Champion != "Vladimir" || len(det.Players) != 10 || len(det.Lane) != len(tl.Info.Frames) {
		t.Fatalf("champion=%s players=%d frames=%d", g.Champion, len(det.Players), len(det.Lane))
	}
	if !reflect.DeepEqual(det, wantDet) {
		t.Errorf("detail differs\n got %+v\nwant %+v", det, wantDet)
	}
	me := 0
	for _, p := range det.Players {
		if p.Me {
			me++
			if p.Champion != "Vladimir" || p.Position != "MIDDLE" {
				t.Errorf("me = %+v", p)
			}
		}
	}
	if me != 1 {
		t.Errorf("%d players marked me", me)
	}
	if last := det.Lane[len(det.Lane)-1]; !last.HasOpp || last.Gold == 0 || last.OppGold == 0 {
		t.Errorf("last lane point = %+v", last)
	}

	if _, _, err := d.Match(ctx, "NA1_nope"); err != ErrNotFound {
		t.Errorf("missing match: err = %v", err)
	}
}

// TestNoIdentityStored makes sure no PUUID or Riot ID from the raw match
// ends up anywhere in the database.
func TestNoIdentityStored(t *testing.T) {
	ctx := context.Background()
	d, s := setup(t)
	if _, err := d.Index(ctx, s, fixture.MePUUID); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(fixture.RealMatchJSON(), &m)
	var secrets []string
	for _, p := range m["info"].(map[string]any)["participants"].([]any) {
		pm := p.(map[string]any)
		for _, k := range []string{"puuid", "riotIdGameName", "summonerName", "summonerId"} {
			if v, _ := pm[k].(string); len(v) > 3 && v != fixture.MePUUID {
				secrets = append(secrets, v)
			}
		}
	}
	if len(secrets) < 20 {
		t.Fatalf("only found %d identity strings in the fixture", len(secrets))
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	for _, tbl := range tables {
		dump := dumpTable(t, d.sql, tbl)
		for _, sec := range secrets {
			if strings.Contains(dump, sec) {
				t.Errorf("table %s contains %q", tbl, sec)
			}
		}
	}
}

func dumpTable(t *testing.T, s *sql.DB, table string) string {
	rows, err := s.Query(`SELECT * FROM "` + table + `"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var b strings.Builder
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		rows.Scan(ptrs...)
		fmt.Fprintln(&b, vals...)
	}
	return b.String()
}

func TestFiltersAndPaging(t *testing.T) {
	ctx := context.Background()
	d, s := setup(t)
	if _, err := d.Index(ctx, s, fixture.MePUUID); err != nil {
		t.Fatal(err)
	}
	solo, err := d.Games(ctx, GameFilter{Queue: riot.QueueSoloDuo, NoRemakes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(solo) != 11 { // 12 synthetic + real - flex - remake
		t.Errorf("solo non-remake games = %d", len(solo))
	}
	ahri, _ := d.Games(ctx, GameFilter{Champion: "Ahri"})
	if len(ahri) != 6 {
		t.Errorf("Ahri games = %d", len(ahri))
	}
	page, total, err := d.GamePage(ctx, GameFilter{}, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if total != 13 || len(page) != 5 || !page[0].Start.After(page[1].Start) {
		t.Errorf("page: total=%d len=%d", total, len(page))
	}
	page, _, _ = d.GamePage(ctx, GameFilter{}, 10, 5)
	if len(page) != 3 {
		t.Errorf("last page len = %d", len(page))
	}
	qs, _ := d.Queues(ctx)
	if !reflect.DeepEqual(qs, []int{riot.QueueSoloDuo, riot.QueueFlex}) {
		t.Errorf("queues = %v", qs)
	}
}

func TestLateTimelineReindexes(t *testing.T) {
	ctx := context.Background()
	d, s := setup(t)
	if _, err := d.Index(ctx, s, fixture.MePUUID); err != nil {
		t.Fatal(err)
	}
	sp := specs()[4]
	g, _, _ := d.Match(ctx, sp.ID)
	if g.HasTimeline {
		t.Fatal("game 4 should start without a timeline")
	}
	s.PutTimeline(sp.ID, fixture.MustJSON(fixture.Timeline(sp)))
	res, err := d.Index(ctx, s, fixture.MePUUID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Added != 0 {
		t.Fatalf("res = %+v", res)
	}
	g, det, _ := d.Match(ctx, sp.ID)
	if !g.HasTimeline || g.At15 == nil || len(det.Lane) == 0 {
		t.Errorf("timeline not picked up: %+v", g)
	}
}

func TestRanksAndSchemaRebuild(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, _ := store.Open(dir)
	fixture.Archive(s, specs()[:2], false)
	fixture.RankSnapshot(s, time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local), "EMERALD", "IV", 40, 20, 20)
	fixture.RankSnapshot(s, time.Date(2026, 11, 17, 0, 0, 0, 0, time.Local), "EMERALD", "IV", 56, 21, 20)

	path := filepath.Join(dir, FileName)
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Index(ctx, s, fixture.MePUUID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ranks != 2 {
		t.Errorf("ranks = %d", res.Ranks)
	}
	hist, err := d.RankHistory(ctx, "RANKED_SOLO_5x5")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[1].Day != "2026-11-17" || hist[1].LP != 56 {
		t.Errorf("history = %+v", hist)
	}
	d.Close()

	// Reopening with the same version keeps the data.
	d, _ = Open(ctx, path)
	if d.Rebuilt {
		t.Error("reopen rebuilt the schema")
	}
	// Pretend the file came from an older schema.
	d.sql.Exec("PRAGMA user_version = 0")
	d.Close()
	d, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.Rebuilt {
		t.Error("old schema version was not rebuilt")
	}
	games, _ := d.Games(ctx, GameFilter{})
	if len(games) != 0 {
		t.Errorf("rebuilt schema still has %d games", len(games))
	}
	res, _ = d.Index(ctx, s, fixture.MePUUID)
	if res.Added != 2 {
		t.Errorf("reindex after rebuild: %+v", res)
	}

	// A different tracked player clears the index.
	res, err = d.Index(ctx, s, "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 2 {
		t.Errorf("other player: %+v", res)
	}
	games, _ = d.Games(ctx, GameFilter{})
	if len(games) != 0 {
		t.Errorf("other player's index has %d games", len(games))
	}
}

func TestMetaCacheSurvivesRebuild(t *testing.T) {
	ctx := context.Background()
	d, s := setup(t)
	if _, err := d.MetaEntry(ctx, "tierlist"); err != ErrNotFound {
		t.Fatalf("empty cache: got %v, want ErrNotFound", err)
	}
	at := time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	for _, body := range []string{"first", "second"} {
		if err := d.PutMetaEntry(ctx, MetaEntry{Key: "tierlist", FetchedAt: at, Patch: "16.22", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Rebuild(ctx, s, fixture.MePUUID); err != nil {
		t.Fatal(err)
	}
	e, err := d.MetaEntry(ctx, "tierlist")
	if err != nil {
		t.Fatal(err)
	}
	if !e.FetchedAt.Equal(at) || e.Patch != "16.22" || e.Body != "second" {
		t.Errorf("got %+v", e)
	}
}
