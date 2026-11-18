package analysis

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Rojoloco04/lolstats/internal/fixture"
)

func TestExtractLaneDiffsAndDeaths(t *testing.T) {
	spec := fixture.Spec{
		ID: "NA1_1", StartMs: time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC).UnixMilli(),
		Champion: "Ahri", Opponent: "Syndra", Position: "MIDDLE", Win: true,
		Kills: 7, Deaths: 3, Assists: 5, GoldLead: 800, CSLead: 12, EarlyDeathsEnemySide: 2,
	}
	m := fixture.Match(spec)
	tl := fixture.Timeline(spec)

	g, ok := Extract(&m, &tl, fixture.MePUUID)
	if !ok {
		t.Fatal("player not found")
	}
	if g.Champion != "Ahri" || g.Opponent != "Syndra" || g.Role != "MIDDLE" || !g.Win {
		t.Fatalf("basic fields wrong: %+v", g)
	}
	if g.Patch != "15.22" {
		t.Errorf("patch = %q", g.Patch)
	}
	if g.CS != 210 || g.Minutes != 30 {
		t.Errorf("cs=%d minutes=%v", g.CS, g.Minutes)
	}
	// team kills: me 7 + 4 teammates * 2 = 15; KP = (7+5)/15
	if want := 12.0 / 15.0; abs(g.KillParticipation-want) > 1e-9 {
		t.Errorf("kp = %v want %v", g.KillParticipation, want)
	}
	if want := 20000.0 / 80000.0; abs(g.DamageShare-want) > 1e-9 {
		t.Errorf("damage share = %v want %v", g.DamageShare, want)
	}
	if g.At10 == nil || g.At15 == nil {
		t.Fatal("missing lane diffs")
	}
	if g.At15.GoldDiff != 800 || g.At10.CSDiff != 12 || !g.At10.HasOpp {
		t.Errorf("lane diffs wrong: at10=%+v at15=%+v", g.At10, g.At15)
	}
	if g.DeathsBefore15 != 2 || g.DeathsEnemySide != 2 || len(g.DeathSpots) != 3 {
		t.Errorf("deaths: before15=%d enemySide=%d spots=%d", g.DeathsBefore15, g.DeathsEnemySide, len(g.DeathSpots))
	}
}

func TestExtractWithoutTimelineAndRemake(t *testing.T) {
	spec := fixture.Spec{ID: "NA1_2", StartMs: 1, Seconds: 200, Champion: "Ahri"}
	m := fixture.Match(spec)
	g, ok := Extract(&m, nil, fixture.MePUUID)
	if !ok || !g.Remake || g.HasTimeline || g.At10 != nil {
		t.Fatalf("unexpected: ok=%v %+v", ok, g)
	}
	if _, ok := Extract(&m, nil, "someone-else"); ok {
		t.Fatal("found a player who is not in the match")
	}
}

func TestShortGameHasNo15MinuteMark(t *testing.T) {
	spec := fixture.Spec{ID: "NA1_3", StartMs: 1, Seconds: 13 * 60, Champion: "Ahri"}
	m, tl := fixture.Match(spec), fixture.Timeline(spec)
	g, _ := Extract(&m, &tl, fixture.MePUUID)
	if g.At10 == nil || g.At15 != nil {
		t.Fatalf("at10=%v at15=%v", g.At10, g.At15)
	}
}

func TestReport(t *testing.T) {
	var games []Game
	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		champ := []string{"Ahri", "Syndra", "Orianna"}[i%3]
		// Ahri wins a lot, Orianna loses a lot, leads at 15 get thrown sometimes.
		win := (champ == "Ahri" && i%4 != 0) || (champ == "Syndra" && i%2 == 0) || (champ == "Orianna" && i%5 == 0)
		lead := 1500
		if i%2 == 1 {
			lead = -1200
		}
		deaths := 2
		if !win {
			deaths = 5
		}
		spec := fixture.Spec{
			ID: fmt.Sprintf("NA1_%d", 100+i), StartMs: base.Add(time.Duration(i) * 240 * time.Hour).UnixMilli(),
			Champion: champ, Opponent: "Zed", Win: win, Kills: 5, Deaths: deaths, Assists: 6,
			GoldLead: lead, CSLead: lead / 100, EarlyDeathsEnemySide: deaths - 1,
		}
		m, tl := fixture.Match(spec), fixture.Timeline(spec)
		g, _ := Extract(&m, &tl, fixture.MePUUID)
		games = append(games, g)
	}
	var buf bytes.Buffer
	WriteReport(&buf, ReportInput{RiotID: "Test#NA1", Rank: "EMERALD II 45 LP", RankDate: "2026-11-17", Games: games})
	out := buf.String()
	for _, want := range []string{
		"# Ranked report: Test#NA1", "## By season", "| 2025 |", "| 2026 |", "## By role", "| Mid |",
		"## Champion pool", "| Ahri |", "strength", "drain", "## Early game vs result", "## Toughest matchups (Mid)",
		"| Zed |", "## Last 20 games", "## What stands out", "deaths before 15 minutes in losses",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q", want)
		}
	}
	if t.Failed() {
		t.Log(out)
	}
}

func TestReportEmpty(t *testing.T) {
	var buf bytes.Buffer
	WriteReport(&buf, ReportInput{RiotID: "X#1"})
	if !strings.Contains(buf.String(), "No ranked games") {
		t.Fatal(buf.String())
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
