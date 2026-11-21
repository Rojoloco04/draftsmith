package web

import (
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
