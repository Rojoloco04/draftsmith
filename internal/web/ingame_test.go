package web

import (
	"strings"
	"testing"

	"github.com/rojoloco04/draftsmith/internal/liveclient"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

func choice(play int, pick float64, ids ...int) meta.Choice {
	return meta.Choice{IDs: ids, Play: play, Win: play / 2, PickRate: pick}
}

var gameTestBuild = meta.Build{
	Starter: []meta.Choice{choice(100, .9, 1056, 2003, 2003)},
	Boots:   []meta.Choice{choice(80, .6, 3020), choice(40, .3, 3111)},
	Core:    []meta.Choice{choice(60, .4, 6655, 3157, 3089), choice(30, .2, 6655, 3089, 3157)},
	Slots: []meta.ItemSlot{
		{Depth: 1, Items: []meta.Choice{choice(9, .9, 6655)}}, // before the core: not shown as a slot
		{Depth: 4, Items: []meta.Choice{choice(20, .5, 3135), choice(10, .2, 3102)}},
		{Depth: 5, Items: []meta.Choice{choice(15, .4, 3102)}},
	},
	Late:   []meta.Choice{choice(50, .5, 3089), choice(20, .2, 3165), choice(10, .1, 3916)},
	Skills: []meta.SkillOrder{{Order: []string{"Q", "W", "E", "Q", "Q", "R"}}},
}

func TestGameBuildWithoutGame(t *testing.T) {
	g := newGameBuild(gameTestBuild, "16.23", nil)
	if g.Live || g.Next != nil || len(g.Start) != 1 || len(g.Core) != 2 || len(g.Boots) != 2 {
		t.Fatalf("build: %+v", g)
	}
	if s := g.Start[0].Items; len(s) != 2 || s[1].Count != 2 {
		t.Errorf("start should merge potions: %+v", s)
	}
	if len(g.Slots) != 2 || g.Slots[0].Label != "4th item" || g.Slots[1].Label != "5th item" {
		t.Errorf("slots: %+v", g.Slots)
	}
	// Late items already shown elsewhere are left out.
	if len(g.Situational) != 2 || g.Situational[0].ID != 3165 || g.Situational[1].ID != 3916 {
		t.Errorf("situational: %+v", g.Situational)
	}
}

func TestGameBuildTracksItems(t *testing.T) {
	snap := &liveclient.Snapshot{Level: 6, Skills: map[string]int{"Q": 2, "W": 1, "E": 1},
		Items: []liveclient.Item{{ID: 6655, Name: "Luden's", Count: 1}, {ID: 3020, Name: "Sorc Shoes", Count: 1}}}
	g := newGameBuild(gameTestBuild, "16.23", snap)
	if !g.Live || g.Level != 6 || len(g.Items) != 2 {
		t.Fatalf("live: %+v", g)
	}
	if g.Next == nil || g.Next.ID != 3157 || !g.Core[0].Items[1].Next || !g.Core[0].Items[0].Have {
		t.Errorf("next should be the second core item: %+v", g.Next)
	}
	if !g.Boots[0].Done || g.Boots[1].Done {
		t.Errorf("boots done: %+v", g.Boots)
	}
	if g.NextSkill != "Q" || !g.Order[3].Done || !g.Order[4].Next || g.Order[5].Next {
		t.Errorf("skills: next %q, order %+v", g.NextSkill, g.Order)
	}

	// Core done: the 4th item's top option is next.
	snap.Items = append(snap.Items, liveclient.Item{ID: 3157}, liveclient.Item{ID: 3089})
	snap.Skills["Q"] = 3
	snap.Skills["R"] = 1
	g = newGameBuild(gameTestBuild, "16.23", snap)
	if g.Next == nil || g.Next.ID != 3135 || !g.Slots[0].Next || g.Slots[1].Next {
		t.Errorf("next after core: %+v, slots %+v", g.Next, g.Slots)
	}
	if g.NextSkill != "" {
		t.Errorf("no points to spend, got %q", g.NextSkill)
	}
}

// Before anything is bought (only the trinket), the starting set is next.
func TestGameBuildStartingItems(t *testing.T) {
	snap := &liveclient.Snapshot{Level: 1, Skills: map[string]int{},
		Items: []liveclient.Item{{ID: 3340, Name: "Stealth Ward", Count: 1, Slot: 6}}}
	g := newGameBuild(gameTestBuild, "16.23", snap)
	if g.NextStart != &g.Start[0] || g.Next != nil {
		t.Fatalf("next should be the starting set: start %v, next %+v", g.NextStart, g.Next)
	}
	for _, it := range g.Start[0].Items {
		if !it.Next {
			t.Errorf("starting item %d not marked next", it.ID)
		}
	}
	if g.NextSkill != "Q" {
		t.Errorf("first point: %q", g.NextSkill)
	}

	// Once something's bought, it's the core again.
	snap.Items = append(snap.Items, liveclient.Item{ID: 1056, Name: "Doran's Ring", Count: 1})
	g = newGameBuild(gameTestBuild, "16.23", snap)
	if g.NextStart != nil || g.Next == nil || g.Next.ID != 6655 {
		t.Errorf("after buying: start %v, next %+v", g.NextStart, g.Next)
	}
}

// Points spent in another order than OP.GG's still line up: the next
// step is the first one they don't cover.
func TestGameBuildSkillsOffOrder(t *testing.T) {
	// The order is Q W E Q Q R; I took Q, Q, E.
	snap := &liveclient.Snapshot{Level: 4, Skills: map[string]int{"Q": 2, "E": 1}}
	g := newGameBuild(gameTestBuild, "16.23", snap)
	if g.NextSkill != "W" || !g.Order[1].Next {
		t.Errorf("next should be the W I skipped: %q, %+v", g.NextSkill, g.Order)
	}
	if !g.Order[0].Done || g.Order[1].Done || !g.Order[2].Done || !g.Order[3].Done || g.Order[4].Done {
		t.Errorf("done steps: %+v", g.Order)
	}
}

// OP.GG's 15 levels run on to 18: R at 16, then the basic ability that
// isn't maxed yet.
func TestFullOrder(t *testing.T) {
	order := strings.Split("Q E W Q Q R Q E Q E R E E W W", " ")
	max := &meta.SkillMax{IDs: []string{"Q", "E", "W"}}
	if got := strings.Join(fullOrder(order, max), " "); got != "Q E W Q Q R Q E Q E R E E W W R W W" {
		t.Errorf("full order: %s", got)
	}
	if got := fullOrder(order[:6], max); len(got) != 6 {
		t.Errorf("a short order should be left alone: %v", got)
	}
}
