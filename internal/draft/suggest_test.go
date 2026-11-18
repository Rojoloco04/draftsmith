package draft

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

func games(champ, role string, wins, losses int) []analysis.Game {
	var out []analysis.Game
	for i := 0; i < wins+losses; i++ {
		out = append(out, analysis.Game{Champion: champ, Role: role, Win: i < wins})
	}
	return out
}

// galioTable is Galio's real mid matchup table (Galio's wins).
func galioTable(t *testing.T) []meta.Counter {
	t.Helper()
	table, err := testService(t, false).MatchupTable(context.Background(), "Galio", "mid")
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestSuggestRealDraft(t *testing.T) {
	tl := tierList(t)
	var mine []analysis.Game
	mine = append(mine, games("Galio", "MIDDLE", 6, 2)...)
	mine = append(mine, games("Galio", "TOP", 0, 3)...) // other lanes don't count
	mine = append(mine, games("Vladimir", "MIDDLE", 3, 1)...)
	mine = append(mine, games("Ahri", "MIDDLE", 2, 0)...)
	mine = append(mine, games("Zed", "MIDDLE", 1, 0)...) // too few games
	mine = append(mine, analysis.Game{Champion: "Vladimir", Role: "MIDDLE", Remake: true})

	in := Input{
		Position: "mid", Games: mine, Base: 0.5, MinGames: 2, TierList: tl,
		Odds:     Roles(tl, []string{"Seraphine", "Xayah", "Katarina", "Rengar", "Renekton"}, nil),
		Matchups: map[string][]meta.Counter{"Galio": galioTable(t)},
		Taken:    []string{"Ahri"},
		Hovered:  []string{"vladimir"},
	}
	got := Suggest(in)
	if len(got) != 2 || got[0].Champion != "Galio" || got[1].Champion != "Vladimir" {
		t.Fatalf("suggestions %+v", got)
	}

	g := got[0]
	gm, _ := tl.Find("Galio", "mid")
	metaWR := gm.WinRate()
	// 6-2, blended with 10 games at my 50% shifted by Galio's meta rate.
	if want := (6 + 10*metaWR) / 18; g.Games != 8 || math.Abs(g.Personal-want) > 1e-12 {
		t.Errorf("Galio personal %v (%d games), want %v", g.Personal, g.Games, want)
	}
	// Katarina is mid at ~100%; Galio is 2191 of 4213 into her.
	kat := g.Opponents[0]
	if want := (2191 + MatchupWeight*metaWR) / (4213 + MatchupWeight); kat.Champion != "Katarina" || kat.Odds < 0.99 || math.Abs(kat.WinRate-want) > 1e-12 || kat.Games != 4213 {
		t.Errorf("Katarina %+v, want win rate %v", kat, want)
	}
	if g.Matchup <= 0 || g.WinProb <= g.Personal {
		t.Errorf("a favourable matchup should raise the score: %+v", g)
	}
	if opp, ok := g.LikelyOpponent(0.5); !ok || opp != "Katarina" {
		t.Errorf("likely opponent %q %v", opp, ok)
	}

	// Vladimir has no matchup table: scored on record and meta only.
	v := got[1]
	if v.Matchup != 0 || !math.IsNaN(v.Opponents[0].WinRate) || !v.TeammateHovering {
		t.Errorf("Vladimir %+v", v)
	}

	reason := g.Reason("mid", nil)
	for _, want := range []string{"Galio ", "you're 6-2 on it Mid", "tier 4, 49% in Emerald+", "into Katarina (mid 100%)"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason missing %q: %s", want, reason)
		}
	}
	if r := v.Reason("mid", nil); !strings.Contains(r, "no matchup data vs Katarina") || !strings.Contains(r, "a teammate is hovering it") {
		t.Errorf("Vladimir reason: %s", r)
	}
}

func TestSuggestBeforeEnemiesPick(t *testing.T) {
	tl := tierList(t)
	in := Input{
		Position: "mid", Games: games("Galio", "MIDDLE", 3, 3), Base: 0.5, MinGames: 2, TierList: tl,
		Odds:     Roles(tl, []string{"", "", "", "", ""}, nil),
		Matchups: map[string][]meta.Counter{"Galio": galioTable(t)},
	}
	got := Suggest(in)
	if len(got) != 1 || got[0].Matchup != 0 || len(got[0].Opponents) != 0 || got[0].WinProb != got[0].Personal {
		t.Fatalf("%+v", got)
	}
	if r := got[0].Reason("mid", nil); !strings.Contains(r, "lane opponent not picked yet") {
		t.Errorf("reason %s", r)
	}
	if _, ok := got[0].LikelyOpponent(0.5); ok {
		t.Error("no likely opponent before anyone picks")
	}
}

func TestSuggestFlexOpponentIsWeighted(t *testing.T) {
	tl := tierList(t)
	// Swain could be mid or elsewhere: his matchup counts only as much as
	// he's likely to be my laner.
	table := []meta.Counter{{Key: "Swain", Play: 1000, Win: 700}}
	in := Input{
		Position: "mid", Games: games("Galio", "MIDDLE", 3, 3), Base: 0.5, MinGames: 2, TierList: tl,
		Matchups: map[string][]meta.Counter{"Galio": table},
	}
	in.Odds = Roles(tl, []string{"Swain", "", "", "", ""}, nil)
	flex := Suggest(in)[0]
	in.Odds = Roles(tl, []string{"Swain", "Seraphine", "Xayah", "Rengar", "Renekton"}, nil) // only mid left for Swain
	sure := Suggest(in)[0]
	if !(flex.Matchup > 0 && sure.Matchup > flex.Matchup*2) {
		t.Errorf("flex %v (odds %v), sure %v (odds %v)", flex.Matchup, flex.Opponents[0].Odds, sure.Matchup, sure.Opponents[0].Odds)
	}
}
