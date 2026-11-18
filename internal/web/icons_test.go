package web

import (
	"strings"
	"testing"
)

func TestIcons(t *testing.T) {
	// Trees, keystones, minor runes and stat shards all resolve.
	for id, name := range map[int]string{8100: "Domination", 8112: "Electrocute", 8139: "Taste of Blood", 5008: "Adaptive Force"} {
		if got := runeName(id); got != name {
			t.Errorf("runeName(%d) = %q, want %q", id, got, name)
		}
		if !strings.HasPrefix(runeImg(id), "https://ddragon.leagueoflegends.com/cdn/img/perk-images/") {
			t.Errorf("runeImg(%d) = %q", id, runeImg(id))
		}
	}
	if runeImg(1) != "" || runeName(1) != "Rune 1" {
		t.Error("unknown rune should have no icon")
	}
	if got := spellImg(4, "16.22"); got != "https://ddragon.leagueoflegends.com/cdn/16.22.1/img/spell/SummonerFlash.png" {
		t.Errorf("Flash = %q", got)
	}
	if spellName(11) != "Smite" || spellImg(9999, "16.22") != "" {
		t.Error("spell lookup")
	}

	if got := rankCrest("EMERALD"); !strings.HasSuffix(got, "/ranked-mini-crests/emerald.svg") {
		t.Errorf("crest = %q", got)
	}
	if got := rankEmblem(""); !strings.HasSuffix(got, "/unranked.svg") {
		t.Errorf("unranked emblem = %q", got)
	}
	if rankLabel("EMERALD", "III") != "Emerald III" || rankLabel("MASTER", "I") != "Master" {
		t.Error("rank labels")
	}
	for in, want := range map[string]string{"MIDDLE": "mid", "Mid": "mid", "adc": "bot", "UTILITY": "support", "Support": "support", "Invalid": ""} {
		if got := roleKey(in); got != want {
			t.Errorf("roleKey(%q) = %q, want %q", in, got, want)
		}
	}
	if got := splashImg("FiddleSticks"); !strings.HasSuffix(got, "/splash/Fiddlesticks_0.jpg") {
		t.Errorf("splash = %q", got)
	}
}
