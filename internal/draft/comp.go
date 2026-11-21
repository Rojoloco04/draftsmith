package draft

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// champions.json is Data Dragon's champion table cut down to what comp
// needs: class tags and Riot's 1-10 attack, defense and magic ratings. To
// refresh it, take data/en_US/champion.json for the newest version from
// https://ddragon.leagueoflegends.com/api/versions.json and keep
// {version, champions: {key: {tags, attack, defense, magic}}}.
//
//go:embed champions.json
var championsJSON []byte

type champInfo struct {
	Tags    []string `json:"tags"`
	Attack  int      `json:"attack"`
	Defense int      `json:"defense"`
	Magic   int      `json:"magic"`
}

var champInfos = func() map[string]champInfo {
	var f struct {
		Champions map[string]champInfo `json:"champions"`
	}
	if err := json.Unmarshal(championsJSON, &f); err != nil {
		panic("draft: bad embedded champions.json: " + err.Error())
	}
	out := map[string]champInfo{}
	for k, c := range f.Champions {
		out[norm(k)] = c
	}
	return out
}()

// apOverride fixes champions whose Data Dragon ratings misread their
// damage: 0 is physical, 1 magic, 0.5 a real mix.
var apOverride = map[string]float64{
	"belveth": 0, "akshan": 0, "ezreal": 0, "vex": 1, "seraphine": 1, "twistedfate": 1,
	"corki": 0.5, "kaisa": 0.5, "kogmaw": 0.5, "varus": 0.5, "kayle": 0.5, "jax": 0.5, "shaco": 0.5,
	"shen": 0.5, "rell": 1, "leona": 1,
}

// APShare is how much of a champion's damage is magic: 1 for mages, 0
// for marksmen and most fighters, 0.5 for a mix. ok is false for a
// champion Data Dragon doesn't list.
func APShare(key string) (share float64, ok bool) {
	k := norm(key)
	if v, ok := apOverride[k]; ok {
		return v, true
	}
	c, ok := champInfos[k]
	if !ok {
		return 0, false
	}
	switch {
	case c.Magic > c.Attack:
		return 1, true
	case c.Attack > c.Magic:
		return 0, true
	}
	return 0.5, true
}

// Frontline reports whether a champion can stand in front: Riot tags it a
// tank.
func Frontline(key string) bool {
	c, ok := champInfos[norm(key)]
	if !ok {
		return false
	}
	for _, t := range c.Tags {
		if t == "Tank" {
			return true
		}
	}
	return false
}

// How much team comp moves a suggestion, in log odds, with four teammates
// known. Fewer known teammates scale it down. DamageWeight applies to a
// fully one-sided team and a pure AP or AD pick (about 6 points of win
// rate at 50%); FrontlineWeight to adding the team's first frontliner
// (about 4).
const (
	DamageWeight    = 0.25
	FrontlineWeight = 0.15
	// minComp is the fewest teammates with champions before comp counts.
	minComp = 2
)

// TeamComp is my team without me: what's picked or hovered so far.
type TeamComp struct {
	Known     int     // teammates with a champion (picked or hovered)
	AP        float64 // average magic share of their damage
	Frontline int     // how many of them are tanks
}

// Comp describes my teammates' champions (Riot keys; "" for none yet).
func Comp(allies []string) TeamComp {
	var t TeamComp
	sum := 0.0
	for _, a := range allies {
		if a == "" {
			continue
		}
		share, ok := APShare(a)
		if !ok {
			share = 0.5
		}
		t.Known++
		sum += share
		if Frontline(a) {
			t.Frontline++
		}
	}
	if t.Known > 0 {
		t.AP = sum / float64(t.Known)
	}
	return t
}

// Counts reports whether enough of the team is known for comp to matter.
func (t TeamComp) Counts() bool { return t.Known >= minComp }

// Lean describes the team's damage, e.g. "mostly AD" or "mixed damage".
func (t TeamComp) Lean() string {
	switch {
	case t.AP <= 0.25:
		return "mostly AD"
	case t.AP >= 0.75:
		return "mostly AP"
	}
	return "mixed damage"
}

// String is the team in a few words for the board: "3 known · mostly AD ·
// no frontline".
func (t TeamComp) String() string {
	if t.Known == 0 {
		return ""
	}
	front := "no frontline"
	switch {
	case t.Frontline == 1:
		front = "1 frontliner"
	case t.Frontline > 1:
		front = fmt.Sprintf("%d frontliners", t.Frontline)
	}
	return fmt.Sprintf("%s · %s", t.Lean(), front)
}

// fit is how much champ would help or hurt the team's comp, in log odds,
// and a short note saying why ("" when it doesn't matter).
func (t TeamComp) fit(champ string) (float64, string) {
	if !t.Counts() {
		return 0, ""
	}
	scale := math.Min(float64(t.Known), 4) / 4
	var shift float64
	var notes []string
	if share, ok := APShare(champ); ok {
		// need > 0 when the team lacks magic damage, lean > 0 for an AP
		// pick; their product is at most 0.25.
		need, lean := 0.5-t.AP, share-0.5
		d := DamageWeight * 4 * need * lean * scale
		shift += d
		switch {
		case d >= 0.03 && lean > 0:
			notes = append(notes, "adds AP to a "+t.Lean()+" team")
		case d >= 0.03:
			notes = append(notes, "adds AD to a "+t.Lean()+" team")
		case d <= -0.03:
			notes = append(notes, "makes a "+t.Lean()+" team more one-sided")
		}
	}
	if t.Frontline == 0 && Frontline(champ) {
		shift += FrontlineWeight * scale
		notes = append(notes, "gives a team with no frontline one")
	}
	return shift, strings.Join(notes, ", ")
}
