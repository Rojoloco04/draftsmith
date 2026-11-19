package meta

import (
	"fmt"
	"strings"
)

// Choice is one option OP.GG lists for a champion: a summoner spell pair
// or an item set, with how often it's taken and won.
type Choice struct {
	IDs      []int   `json:"ids"`
	Play     int     `json:"play"`
	Win      int     `json:"win"`
	PickRate float64 `json:"pick_rate"`
}

// WinRate is Win/Play (0 without games).
func (c Choice) WinRate() float64 {
	if c.Play == 0 {
		return 0
	}
	return float64(c.Win) / float64(c.Play)
}

// Build is how a champion is set up: summoner spells and items, each
// most played first. Champion analysis lists one option of each (the
// most played); a matchup guide lists several, for that matchup.
type Build struct {
	Spells  []Choice `json:"summoner_spells"`
	Starter []Choice `json:"starter_items"`
	Boots   []Choice `json:"boots"`
	Core    []Choice `json:"core_items"`
}

// TopSpells is the most played summoner spell pair, if any.
func (b Build) TopSpells() (Choice, bool) {
	for _, c := range b.Spells {
		if len(c.IDs) > 0 {
			return c, true
		}
	}
	return Choice{}, false
}

// buildFields are the analysis fields that fill a Build. In the analysis
// each is a single object, not a list.
var buildFields = []string{
	"data.summoner_spells.{ids[],play,win,pick_rate}",
	"data.starter_items.{ids[],play,win,pick_rate}",
	"data.boots.{ids[],play,win,pick_rate}",
	"data.core_items.{ids[],play,win,pick_rate}",
}

// analysisBuild is the analysis's single-option shape of a Build.
type analysisBuild struct {
	Spells  Choice `json:"summoner_spells"`
	Starter Choice `json:"starter_items"`
	Boots   Choice `json:"boots"`
	Core    Choice `json:"core_items"`
}

func (a analysisBuild) build() Build {
	one := func(c Choice) []Choice {
		if len(c.IDs) == 0 {
			return nil
		}
		return []Choice{c}
	}
	return Build{Spells: one(a.Spells), Starter: one(a.Starter), Boots: one(a.Boots), Core: one(a.Core)}
}

// spellNames are the summoner spells by id. The set rarely changes; an
// unknown id shows as a number.
var spellNames = map[int]string{
	1: "Cleanse", 3: "Exhaust", 4: "Flash", 6: "Ghost", 7: "Heal", 11: "Smite",
	12: "Teleport", 13: "Clarity", 14: "Ignite", 21: "Barrier", 32: "Mark",
}

// SpellName names a summoner spell.
func SpellName(id int) string {
	if n, ok := spellNames[id]; ok {
		return n
	}
	return fmt.Sprint(id)
}

// SpellNames describes a spell pair, e.g. "Flash + Teleport".
func SpellNames(ids []int) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = SpellName(id)
	}
	return strings.Join(names, " + ")
}
