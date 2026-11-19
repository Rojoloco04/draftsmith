package meta

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAnalysisBuild(t *testing.T) {
	b, err := os.ReadFile("../opgg/testdata/analysis_galio_mid_build.txt")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOPGG{t: t, body: map[string]string{"lol_get_champion_analysis": string(b)}}
	s := NewWith(memCache{}, func() Caller { return f }, func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) })
	a, err := s.Analysis(context.Background(), "Galio", "mid")
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := a.Build.TopSpells()
	if !ok || SpellNames(sp.IDs) != "Flash + Teleport" || sp.Play != 4657 || sp.PickRate != 0.74 {
		t.Errorf("spells %+v", sp)
	}
	if len(a.Build.Core) != 1 || len(a.Build.Core[0].IDs) != 3 || a.Build.Core[0].IDs[0] != 3152 {
		t.Errorf("core %+v", a.Build.Core)
	}
	if len(a.Build.Boots) != 1 || a.Build.Boots[0].IDs[0] != 3111 || len(a.Build.Starter) != 1 || a.Build.Starter[0].IDs[0] != 1056 {
		t.Errorf("boots %+v, starter %+v", a.Build.Boots, a.Build.Starter)
	}
	wantBuild := "Doran's Ring + Health Potion x2 > Mercury's Treads > Hextech Rocketbelt, Imperial Mandate, Zhonya's Hourglass; max Q > W > E"
	if got := a.Build.String(); got != wantBuild {
		t.Errorf("build\n got %s\nwant %s", got, wantBuild)
	}
	if len(a.Build.Skills) != 1 || len(a.Build.Skills[0].Order) != 15 || a.Build.Skills[0].Order[5] != "R" {
		t.Errorf("skills %+v", a.Build.Skills)
	}
	// The fields are asked for.
	var fields []string
	for i, c := range f.calls {
		if c == "lol_get_champion_analysis" {
			fields, _ = f.args[i]["desired_output_fields"].([]string)
		}
	}
	want := map[string]bool{}
	for _, fl := range buildFields {
		want[fl] = true
	}
	for _, fl := range fields {
		delete(want, fl)
	}
	if len(want) > 0 {
		t.Errorf("not requested: %v", want)
	}
}

func TestMatchupBuild(t *testing.T) {
	f := &matchupOPGG{fakeOPGG{t: t, body: map[string]string{}}}
	s := NewWith(memCache{}, func() Caller { return f }, func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) })
	m, err := s.Matchup(context.Background(), "Galio", "Katarina", "mid")
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := m.Build.TopSpells()
	if !ok || SpellNames(sp.IDs) != "Flash + Teleport" || sp.Play != 3441 || sp.Win != 1773 {
		t.Errorf("spells %+v", sp)
	}
	if len(m.Build.Spells) < 2 || SpellNames(m.Build.Spells[1].IDs) != "Flash + Ignite" {
		t.Errorf("second spell pair: %+v", m.Build.Spells)
	}
	if len(m.Build.Core) == 0 || len(m.Build.Boots) == 0 || len(m.Build.Starter) == 0 {
		t.Errorf("items: %d core, %d boots, %d starter", len(m.Build.Core), len(m.Build.Boots), len(m.Build.Starter))
	}
	if len(m.Build.Skills) == 0 || len(m.Build.Max) == 0 || len(m.Build.Core[0].Names) != len(m.Build.Core[0].IDs) {
		t.Errorf("skills %d, max %d, core %+v", len(m.Build.Skills), len(m.Build.Max), m.Build.Core[0])
	}
	if s := m.Build.String(); !strings.Contains(s, " > ") || !strings.Contains(s, "; max ") {
		t.Errorf("matchup build: %s", s)
	}
}

func TestSpellNames(t *testing.T) {
	if got := SpellNames([]int{4, 14}); got != "Flash + Ignite" {
		t.Errorf("got %q", got)
	}
	if got := SpellNames([]int{11, 99}); got != "Smite + 99" {
		t.Errorf("got %q", got)
	}
}

func TestBuildStringPartial(t *testing.T) {
	b := Build{Core: []Choice{{IDs: []int{3152, 9999}, Names: []string{"Hextech Rocketbelt"}}}}
	if got := b.String(); got != "Hextech Rocketbelt, 9999" {
		t.Errorf("got %q", got)
	}
	if !(Build{}).Empty() || b.Empty() {
		t.Error("Empty")
	}
	if got := (Build{}).String(); got != "" {
		t.Errorf("empty build: %q", got)
	}
}
