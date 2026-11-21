package draft

import (
	"math"
	"strings"
	"testing"
)

func TestAPShareAndFrontline(t *testing.T) {
	for _, c := range []struct {
		key   string
		share float64
		front bool
	}{
		{"Ahri", 1, false}, {"Jinx", 0, false}, {"Zed", 0, false}, {"Ornn", 0, true},
		{"Galio", 1, true}, {"Belveth", 0, false}, {"Corki", 0.5, false}, {"MonkeyKing", 0, true},
		{"TwistedFate", 1, false}, {"Leona", 1, true},
	} {
		got, ok := APShare(c.key)
		if !ok || got != c.share {
			t.Errorf("APShare(%s) = %v, %v; want %v", c.key, got, ok, c.share)
		}
		if Frontline(c.key) != c.front {
			t.Errorf("Frontline(%s) = %v", c.key, !c.front)
		}
	}
	if _, ok := APShare("NotAChampion"); ok {
		t.Error("unknown champion should be !ok")
	}
}

func TestCompFit(t *testing.T) {
	// Four AD teammates, no tank: a mage helps, a marksman hurts, and a
	// tank mage helps most.
	ad := Comp([]string{"Zed", "Jinx", "Darius", ""})
	if ad.Known != 3 || ad.AP != 0 || ad.Frontline != 1 || ad.Lean() != "mostly AD" {
		t.Fatalf("comp: %+v", ad)
	}
	all := Comp([]string{"Zed", "Jinx", "Talon", "Graves"})
	mage, note := all.fit("Ahri")
	if mage <= 0 || !strings.Contains(note, "adds AP to a mostly AD team") {
		t.Errorf("Ahri into all AD: %v %q", mage, note)
	}
	mm, note := all.fit("Caitlyn")
	if mm >= 0 || !strings.Contains(note, "more one-sided") {
		t.Errorf("Caitlyn into all AD: %v %q", mm, note)
	}
	tank, note := all.fit("Galio")
	if tank <= mage || !strings.Contains(note, "no frontline") {
		t.Errorf("Galio into all AD, no tank: %v (mage %v) %q", tank, mage, note)
	}
	if math.Abs(mage-DamageWeight) > 1e-9 {
		t.Errorf("pure AP into a pure AD team of four should be the full weight: %v", mage)
	}

	// One known teammate is too little to go on.
	if s, n := Comp([]string{"Zed"}).fit("Ahri"); s != 0 || n != "" {
		t.Errorf("one teammate: %v %q", s, n)
	}
	// A balanced team leaves damage alone.
	if s, _ := Comp([]string{"Ahri", "Jinx", "Ornn", "Lux"}).fit("Zed"); math.Abs(s) > 0.03 {
		t.Errorf("balanced team moved Zed by %v", s)
	}
}

func TestSuggestUsesComp(t *testing.T) {
	var mine = append(games("Ahri", "MIDDLE", 5, 5), games("Zed", "MIDDLE", 5, 5)...)
	base := Suggest(Input{Position: "mid", Games: mine, Base: 0.5})
	if len(base) != 2 || base[0].WinProb != base[1].WinProb {
		t.Fatalf("without comp they should tie: %+v", base)
	}
	sug := Suggest(Input{Position: "mid", Games: mine, Base: 0.5, Allies: []string{"Jinx", "Talon", "Graves", "Darius"}})
	if sug[0].Champion != "Ahri" || sug[0].WinProb <= sug[1].WinProb || sug[0].CompNote == "" {
		t.Errorf("Ahri should lead an all-AD team: %+v", sug)
	}
	if r := sug[0].Reason("mid", nil); !strings.Contains(r, "adds AP") {
		t.Errorf("reason: %s", r)
	}
}
