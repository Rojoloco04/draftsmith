package web

import (
	"fmt"

	"github.com/rojoloco04/draftsmith/internal/liveclient"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// trinketSlot is the inventory slot the Live Client Data API gives the
// trinket.
const trinketSlot = 6

// How many options each part of the in-game build lists.
const (
	gameStarts = 2
	gameCores  = 3
	gameBoots  = 3
	gameSlot   = 4
	gameLate   = 6
)

// gameBuild is OP.GG's build for my champion laid out for the game in
// progress, with what I already own marked when the game's data is
// available.
type gameBuild struct {
	Patch       string
	Start       []buildOpt
	Core        []buildOpt
	Boots       []buildOpt
	Slots       []buildSlot // 4th to 6th item
	Situational []buildItem // other late items, most built first
	Max         *meta.SkillMax
	Order       []skillStep

	// Live is set while the game's data is readable.
	Live bool
	// Level is my champion level, Items what I hold (inventory order,
	// trinket last), NextSkill the ability the skill order levels next
	// when I have a point to spend ("" when I don't).
	Level     int
	Items     []buildItem
	NextSkill string
	// Next names the item to work toward: the first core item I don't
	// have, then the most built option for the next slot. Before I've
	// bought anything, NextStart is the starting set instead.
	Next      *buildItem
	NextStart *buildOpt
}

// buildOpt is one option: a set of items (a starting set, a core path or
// a single item) and how often it's built and won.
type buildOpt struct {
	Items    []buildItem
	PickRate float64
	WinRate  float64
	Play     int
	// Done is set when I own every item in it.
	Done bool
}

type buildItem struct {
	ID    int
	Name  string
	Count int  // repeats in a set ("Health Potion x2")
	Have  bool // in my inventory now
	Next  bool // the item to buy toward next
}

// buildSlot is the options for one item by build order.
type buildSlot struct {
	Depth   int
	Label   string // "4th item"
	Options []buildOpt
	Next    bool // the slot I'm buying for now
}

// skillStep is one level of the skill order.
type skillStep struct {
	Key  string // Q, W, E, R
	Done bool   // I've spent this point
	Next bool   // the point to spend now
}

// newGameBuild lays out b, marking what s (nil outside a readable game)
// says I own.
func newGameBuild(b meta.Build, patch string, s *liveclient.Snapshot) gameBuild {
	have := func(int) bool { return false }
	if s != nil {
		have = s.Has
	}
	item := func(c meta.Choice, i int) buildItem {
		n := fmt.Sprint(c.IDs[i])
		if i < len(c.Names) && c.Names[i] != "" {
			n = c.Names[i]
		}
		return buildItem{ID: c.IDs[i], Name: n, Count: 1, Have: have(c.IDs[i])}
	}
	opt := func(c meta.Choice, merge bool) buildOpt {
		o := buildOpt{PickRate: c.PickRate, WinRate: c.WinRate(), Play: c.Play, Done: true}
		for i := range c.IDs {
			it := item(c, i)
			if merge && len(o.Items) > 0 && o.Items[len(o.Items)-1].ID == it.ID {
				o.Items[len(o.Items)-1].Count++
				continue
			}
			o.Items = append(o.Items, it)
			o.Done = o.Done && it.Have
		}
		return o
	}
	opts := func(cs []meta.Choice, n int, merge bool) []buildOpt {
		var out []buildOpt
		for _, c := range cs {
			if len(out) == n {
				break
			}
			if len(c.IDs) > 0 {
				out = append(out, opt(c, merge))
			}
		}
		return out
	}

	g := gameBuild{Patch: patch, Start: opts(b.Starter, gameStarts, true), Core: opts(b.Core, gameCores, false), Boots: opts(b.Boots, gameBoots, false)}
	shown := map[int]bool{}
	for _, o := range g.Core {
		for _, it := range o.Items {
			shown[it.ID] = true
		}
	}
	for _, o := range g.Boots {
		for _, it := range o.Items {
			shown[it.ID] = true
		}
	}
	for d := 4; d <= 6; d++ {
		if os := opts(b.Slot(d), gameSlot, false); len(os) > 0 {
			g.Slots = append(g.Slots, buildSlot{Depth: d, Label: ordinal(d) + " item", Options: os})
			for _, o := range os {
				for _, it := range o.Items {
					shown[it.ID] = true
				}
			}
		}
	}
	for _, c := range b.Late {
		if len(g.Situational) == gameLate {
			break
		}
		if len(c.IDs) == 1 && !shown[c.IDs[0]] {
			shown[c.IDs[0]] = true
			g.Situational = append(g.Situational, item(c, 0))
		}
	}
	if len(b.Max) > 0 && len(b.Max[0].IDs) > 0 {
		g.Max = &b.Max[0]
	}
	var order []string
	if len(b.Skills) > 0 {
		order = fullOrder(b.Skills[0].Order, g.Max)
	}
	for _, k := range order {
		g.Order = append(g.Order, skillStep{Key: k})
	}

	if s == nil {
		return g
	}
	g.Live, g.Level = true, s.Level
	for _, it := range s.Items {
		g.Items = append(g.Items, buildItem{ID: it.ID, Name: it.Name, Count: it.Count, Have: true})
	}
	// A step is done when I've put a point in its ability for it, so
	// leveling in another order than OP.GG's still lines up: the next
	// step is the first one my points don't cover.
	left := map[string]int{}
	for k, n := range s.Skills {
		left[k] = n
	}
	for i := range g.Order {
		if k := g.Order[i].Key; left[k] > 0 {
			left[k]--
			g.Order[i].Done = true
		}
	}
	if s.Spent() < s.Level {
		for i := range g.Order {
			if !g.Order[i].Done {
				g.Order[i].Next = true
				g.NextSkill = g.Order[i].Key
				break
			}
		}
	}
	g.markNext(b, s)
	return g
}

// fullOrder extends OP.GG's skill order, which stops at level 15, to 18:
// R at 16, then the points the basic abilities still need to max (the
// last maxed first, when the max order is known). Orders of any other
// shape are left as they are.
func fullOrder(order []string, max *meta.SkillMax) []string {
	if len(order) != 15 {
		return order
	}
	n := map[string]int{}
	for _, k := range order {
		n[k]++
	}
	if n["R"] != 2 {
		return order
	}
	out := append(append([]string(nil), order...), "R")
	basics := []string{"Q", "W", "E"}
	if max != nil && len(max.IDs) == 3 {
		basics = []string{max.IDs[2], max.IDs[1], max.IDs[0]}
	}
	for _, k := range basics {
		for ; n[k] < 5 && len(out) < 18; n[k]++ {
			out = append(out, k)
		}
	}
	return out
}

// markNext picks the item to buy toward. Finished items I own that OP.GG
// lists in builds (core, slots or late) say how far along I am: before
// three, it's the first missing item of the most built core path; after,
// the most built option for the next slot that I don't own.
func (g *gameBuild) markNext(b meta.Build, s *liveclient.Snapshot) {
	// Nothing bought yet (the trinket doesn't count): the starting set.
	bought := false
	for _, it := range s.Items {
		if it.Slot != trinketSlot {
			bought = true
		}
	}
	if !bought && len(g.Start) > 0 {
		st := &g.Start[0]
		for i := range st.Items {
			st.Items[i].Next = true
		}
		g.NextStart = st
		return
	}
	finished := map[int]bool{}
	for _, cs := range [][]meta.Choice{b.Core, b.Late, b.Slot(4), b.Slot(5), b.Slot(6)} {
		for _, c := range cs {
			for _, id := range c.IDs {
				finished[id] = true
			}
		}
	}
	done := 0
	for _, it := range s.Items {
		if finished[it.ID] {
			done++
		}
	}
	if done < 3 && len(g.Core) > 0 {
		for i := range g.Core[0].Items {
			if it := &g.Core[0].Items[i]; !it.Have {
				it.Next = true
				g.Next = it
				return
			}
		}
	}
	for si := range g.Slots {
		sl := &g.Slots[si]
		if sl.Depth <= done {
			continue
		}
		for oi := range sl.Options {
			if it := &sl.Options[oi].Items[0]; !it.Have {
				sl.Next = true
				it.Next = true
				g.Next = it
				return
			}
		}
	}
}

func ordinal(n int) string {
	switch n {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	}
	return fmt.Sprintf("%dth", n)
}
