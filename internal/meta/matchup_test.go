package meta

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/opgg"
)

// matchupOPGG serves the captured Galio mid guides: into Katarina, and the
// mirror. Other tools come from fakeOPGG.
type matchupOPGG struct {
	fakeOPGG
}

func (f *matchupOPGG) CallTool(ctx context.Context, name string, args map[string]any) (opgg.CallResult, error) {
	if name != "lol_get_lane_matchup_guide" {
		return f.fakeOPGG.CallTool(ctx, name, args)
	}
	f.calls = append(f.calls, name)
	f.args = append(f.args, args)
	file := map[string]string{"KATARINA": "matchup_galio_katarina_mid.json", "GALIO": "matchup_galio_mirror_mid.json"}[args["opponent_champion"].(string)]
	b, err := os.ReadFile("../opgg/testdata/" + file)
	if err != nil {
		f.t.Fatal(err)
	}
	return opgg.CallResult{Text: string(b)}, nil
}

func TestMatchup(t *testing.T) {
	ctx := context.Background()
	f := &matchupOPGG{fakeOPGG{t: t, body: map[string]string{}}}
	s := NewWith(memCache{}, func() Caller { return f }, func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) })

	m, err := s.Matchup(ctx, "Galio", "Katarina", "mid")
	if err != nil {
		t.Fatal(err)
	}
	row, ok := Counter{}, false
	for _, c := range m.Table {
		if c.Key == "Katarina" {
			row, ok = c, true
		}
	}
	// 2191 of 4213: Katarina's own table has Galio at 2022 of 4213.
	if !ok || row.Play != 4213 || row.Win != 2191 || len(m.Table) != 53 {
		t.Errorf("table: %d rows, Katarina %+v", len(m.Table), row)
	}
	if len(m.Runes) == 0 || m.LaneAdvantage != "EVEN" || m.PlayStyle != "defensive" || !strings.Contains(m.Tip, "Katarina") {
		t.Errorf("guide: %d rune pages, %q %q %q", len(m.Runes), m.LaneAdvantage, m.PlayStyle, m.Tip)
	}
	want := "Sorcery: Stormraider's Surge, Manaflow Band, Transcendence, Scorch / Resolve: Unflinching, Second Wind / Adaptive Force, Adaptive Force, Health"
	if got := m.Runes[0].String(); got != want {
		t.Errorf("runes\n got %s\nwant %s", got, want)
	}
	var a map[string]any
	for i, c := range f.calls {
		if c == "lol_get_lane_matchup_guide" {
			a = f.args[i]
		}
	}
	if a["my_champion"] != "GALIO" || a["opponent_champion"] != "KATARINA" || a["position"] != "mid" {
		t.Errorf("args %v", a)
	}

	// The table alone comes from the mirror request, and is cached.
	table, err := s.MatchupTable(ctx, "Galio", "mid")
	if err != nil || len(table) != 53 {
		t.Fatalf("table: %d rows, %v", len(table), err)
	}
	n := f.count("lol_get_lane_matchup_guide")
	if _, err := s.MatchupTable(ctx, "Galio", "mid"); err != nil || f.count("lol_get_lane_matchup_guide") != n {
		t.Errorf("second MatchupTable call went to OP.GG (%v)", err)
	}
	if _, err := s.Matchup(ctx, "Galio", "Katarina", "middle"); err == nil {
		t.Error("Riot's lane name should be refused; positions are OP.GG's")
	}
}

func TestRunePageStringUnknownIDs(t *testing.T) {
	r := RunePage{PrimaryStyle: 9999, Primary: []int{1, 2}, PrimaryNames: []string{"One"}, SubStyle: 8100, Sub: []int{3}, StatMods: []int{5008, 42}}
	if got := r.String(); got != "9999: One, 2 / Domination: 3 / Adaptive Force, 42" {
		t.Errorf("got %s", got)
	}
}
