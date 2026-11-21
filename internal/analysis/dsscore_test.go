package analysis

import (
	"testing"

	"github.com/rojoloco04/draftsmith/internal/fixture"
)

// lobby is ten even players (blue wins), positions in order.
func lobby() []PlayerLine {
	pos := []string{"TOP", "JUNGLE", "MIDDLE", "BOTTOM", "UTILITY"}
	var ps []PlayerLine
	for t, team := range []int{100, 200} {
		for i, p := range pos {
			l := PlayerLine{ParticipantID: t*5 + i + 1, TeamID: team, Position: p, Win: team == 100,
				Kills: 4, Deaths: 4, Assists: 6, CS: 200, Gold: 11000, Damage: 18000, Vision: 25}
			if p == "UTILITY" {
				l.CS, l.Vision, l.Damage, l.Gold = 30, 70, 8000, 8000
			}
			ps = append(ps, l)
		}
	}
	return ps
}

func TestScorePlayersAwards(t *testing.T) {
	ps := lobby()
	ps[2].Kills, ps[2].Deaths, ps[2].Damage, ps[2].Gold = 14, 1, 40000, 16000 // blue mid pops off
	ps[8].Kills, ps[8].Deaths, ps[8].Damage = 10, 2, 32000                    // red bot does well in a loss
	ps[5].Kills, ps[5].Deaths, ps[5].Assists = 0, 11, 1                       // red top has a rough game
	ScorePlayers(ps, 30, false)

	if ps[2].DSRank != 1 || ps[2].Award != AwardMVP {
		t.Errorf("blue mid: rank %d award %q score %.1f", ps[2].DSRank, ps[2].Award, ps[2].DSScore)
	}
	if ps[8].Award != AwardSVP {
		t.Errorf("red bot should be SVP: rank %d award %q", ps[8].DSRank, ps[8].Award)
	}
	if ps[5].DSRank != 10 {
		t.Errorf("red top should be last, got rank %d (%.1f)", ps[5].DSRank, ps[5].DSScore)
	}
	ranks := map[int]bool{}
	awards := map[string]int{}
	for _, p := range ps {
		if p.DSScore < 0 || p.DSScore > 10 {
			t.Errorf("score %.1f out of range", p.DSScore)
		}
		ranks[p.DSRank] = true
		awards[p.Award]++
	}
	if len(ranks) != 10 || ranks[0] {
		t.Errorf("ranks should be 1..10: %v", ranks)
	}
	if awards[AwardMVP] != 1 || awards[AwardSVP] != 1 {
		t.Errorf("awards: %v", awards)
	}
}

func TestScorePlayersSupportNotJudgedOnCS(t *testing.T) {
	// The two supports differ only in CS: it must not change their scores.
	a := lobby()
	ScorePlayers(a, 30, false)
	b := lobby()
	b[4].CS = 0
	ScorePlayers(b, 30, false)
	if a[4].DSScore != b[4].DSScore {
		t.Errorf("support CS changed its score: %.1f vs %.1f", a[4].DSScore, b[4].DSScore)
	}
	// A mid laner with less CS does score lower.
	c := lobby()
	c[2].CS = 100
	ScorePlayers(c, 30, false)
	if c[2].DSScore >= a[2].DSScore {
		t.Errorf("mid CS should count: %.1f vs %.1f", c[2].DSScore, a[2].DSScore)
	}
}

func TestScorePlayersRemake(t *testing.T) {
	ps := lobby()
	ScorePlayers(ps, 3, true)
	for _, p := range ps {
		if p.DSScore != 0 || p.DSRank != 0 || p.Award != "" {
			t.Fatalf("remake scored: %+v", p)
		}
	}
}

func TestScorePlayersNoKills(t *testing.T) {
	// A team with no kills or damage must not produce NaN.
	ps := lobby()
	for i := 5; i < 10; i++ {
		ps[i].Kills, ps[i].Assists, ps[i].Damage = 0, 0, 0
	}
	ScorePlayers(ps, 20, false)
	for _, p := range ps {
		if p.DSScore != p.DSScore || p.DSRank == 0 {
			t.Fatalf("bad score %+v", p)
		}
	}
}

func TestRealGameScored(t *testing.T) {
	m, tl := fixture.Real()
	d, ok := ExtractDetail(&m, &tl, fixture.MePUUID)
	if !ok {
		t.Fatal("me not in the real game")
	}
	awards := map[string]int{}
	ranks := map[int]bool{}
	for _, p := range d.Players {
		awards[p.Award]++
		ranks[p.DSRank] = true
		if p.Award == AwardMVP && !p.Win || p.Award == AwardSVP && p.Win {
			t.Errorf("%s award on the wrong team: %+v", p.Award, p)
		}
	}
	if awards[AwardMVP] != 1 || awards[AwardSVP] != 1 || len(ranks) != 10 {
		t.Errorf("awards %v ranks %v", awards, ranks)
	}
}
