package meta

import (
	"fmt"
	"strings"
)

// Choice is one option OP.GG lists for a champion: a summoner spell pair
// or an item set, with how often it's taken and won.
type Choice struct {
	IDs []int `json:"ids"`
	// Names are the items' names (empty for spells; see SpellName).
	Names    []string `json:"ids_names"`
	Play     int      `json:"play"`
	Win      int      `json:"win"`
	PickRate float64  `json:"pick_rate"`
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
	// Slots are the options for each item by when it's finished (Depth 1
	// is the first item). A matchup guide lists every depth; champion
	// analysis lists 4 to 6, after the core.
	Slots []ItemSlot `json:"single_items"`
	// Late are items that end up in finished builds, most built first.
	Late []Choice `json:"last_items"`
	// Skills are level-by-level skill orders, Max the order abilities are
	// maxed in (e.g. Q, W, E).
	Skills []SkillOrder `json:"skills"`
	Max    []SkillMax   `json:"skill_masteries"`
}

// ItemSlot is the options for one item in build order.
type ItemSlot struct {
	Depth int      `json:"depth"`
	Items []Choice `json:"items"`
}

// Slot returns the options for the depth-th item, if listed.
func (b Build) Slot(depth int) []Choice {
	for _, s := range b.Slots {
		if s.Depth == depth {
			return s.Items
		}
	}
	return nil
}

// SkillOrder is which ability to level at each level, "Q", "W", "E" or "R".
type SkillOrder struct {
	Order    []string `json:"order"`
	Play     int      `json:"play"`
	Win      int      `json:"win"`
	PickRate float64  `json:"pick_rate"`
}

// SkillMax is the order abilities are maxed in.
type SkillMax struct {
	IDs      []string `json:"ids"`
	Play     int      `json:"play"`
	Win      int      `json:"win"`
	PickRate float64  `json:"pick_rate"`
}

// Empty reports whether OP.GG listed nothing to build.
func (b Build) Empty() bool {
	return len(b.Starter)+len(b.Boots)+len(b.Core)+len(b.Slots)+len(b.Late)+len(b.Skills)+len(b.Max) == 0
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
	"data.summoner_spells.{ids[],play,win,pick_rate}", // ids_names are ids here
	"data.starter_items.{ids[],ids_names[],play,win,pick_rate}",
	"data.boots.{ids[],ids_names[],play,win,pick_rate}",
	"data.core_items.{ids[],ids_names[],play,win,pick_rate}",
	"data.skills.{order[],play,win,pick_rate}",
	"data.skill_masteries.{ids[],play,win,pick_rate}",
	"data.fourth_items[].{ids[],ids_names[],play,win,pick_rate}",
	"data.fifth_items[].{ids[],ids_names[],play,win,pick_rate}",
	"data.sixth_items[].{ids[],ids_names[],play,win,pick_rate}",
	"data.last_items[].{ids[],ids_names[],play,win,pick_rate}",
}

// analysisBuild is the analysis's single-option shape of a Build.
type analysisBuild struct {
	Spells  Choice     `json:"summoner_spells"`
	Starter Choice     `json:"starter_items"`
	Boots   Choice     `json:"boots"`
	Core    Choice     `json:"core_items"`
	Skills  SkillOrder `json:"skills"`
	Max     SkillMax   `json:"skill_masteries"`
	Fourth  []Choice   `json:"fourth_items"`
	Fifth   []Choice   `json:"fifth_items"`
	Sixth   []Choice   `json:"sixth_items"`
	Late    []Choice   `json:"last_items"`
}

func (a analysisBuild) build() Build {
	one := func(c Choice) []Choice {
		if len(c.IDs) == 0 {
			return nil
		}
		return []Choice{c}
	}
	b := Build{Spells: one(a.Spells), Starter: one(a.Starter), Boots: one(a.Boots), Core: one(a.Core), Late: a.Late}
	for i, items := range [][]Choice{a.Fourth, a.Fifth, a.Sixth} {
		if len(items) > 0 {
			b.Slots = append(b.Slots, ItemSlot{Depth: 4 + i, Items: items})
		}
	}
	if len(a.Skills.Order) > 0 {
		b.Skills = []SkillOrder{a.Skills}
	}
	if len(a.Max.IDs) > 0 {
		b.Max = []SkillMax{a.Max}
	}
	return b
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

// String describes the build in one line: "Doran's Ring + Health Potion x2
// > Mercury's Treads > Hextech Rocketbelt, Imperial Mandate, Zhonya's
// Hourglass; max Q > W > E". Missing parts are left out.
func (b Build) String() string {
	var parts []string
	for i, cs := range [][]Choice{b.Starter, b.Boots, b.Core} {
		if len(cs) > 0 && len(cs[0].IDs) > 0 {
			sep := ", "
			if i == 0 { // starting items go together
				sep = " + "
			}
			parts = append(parts, strings.Join(itemNames(cs[0]), sep))
		}
	}
	s := strings.Join(parts, " > ")
	if len(b.Max) > 0 && len(b.Max[0].IDs) > 0 {
		if s != "" {
			s += "; "
		}
		s += "max " + strings.Join(b.Max[0].IDs, " > ")
	}
	return s
}

// itemNames names a choice's items, counting repeats ("Health Potion x2").
// Items without a name show their id.
func itemNames(c Choice) []string {
	var out []string
	count := map[string]int{}
	for i, id := range c.IDs {
		n := fmt.Sprint(id)
		if i < len(c.Names) && c.Names[i] != "" {
			n = c.Names[i]
		}
		if count[n] == 0 {
			out = append(out, n)
		}
		count[n]++
	}
	for i, n := range out {
		if count[n] > 1 {
			out[i] = fmt.Sprintf("%s x%d", n, count[n])
		}
	}
	return out
}
