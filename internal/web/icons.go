package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/meta"
)

// Rune and summoner spell names and icon paths, as published by Data
// Dragon. Runes and spells rarely change; to refresh, download the files
// again from https://ddragon.leagueoflegends.com/cdn/<version>/data/en_US/
// (runesReforged.json and summoner.json). An id missing here shows as a
// blank slot with its number in the tooltip.
//
//go:embed ddragon/runesReforged.json ddragon/summoner.json
var ddragonFS embed.FS

const ddragonCDN = "https://ddragon.leagueoflegends.com/cdn/"

// cdragonAssets is CommunityDragon's mirror of the League client's static
// assets, the only public source for ranked emblems.
const cdragonAssets = "https://raw.communitydragon.org/latest/plugins/rcp-fe-lol-static-assets/global/default/images/"

type icon struct {
	Name string
	Path string // runes: under cdn/img/; spells: the file under cdn/<version>/img/spell/
}

var runeIcons, spellIcons = loadIcons()

// statShards are the stat rune ids, which runesReforged.json leaves out.
var statShards = map[int]icon{
	5001: {"Health Scaling", "perk-images/StatMods/StatModsHealthScalingIcon.png"},
	5005: {"Attack Speed", "perk-images/StatMods/StatModsAttackSpeedIcon.png"},
	5007: {"Ability Haste", "perk-images/StatMods/StatModsCDRScalingIcon.png"},
	5008: {"Adaptive Force", "perk-images/StatMods/StatModsAdaptiveForceIcon.png"},
	5010: {"Move Speed", "perk-images/StatMods/StatModsMovementSpeedIcon.png"},
	5011: {"Health", "perk-images/StatMods/StatModsHealthPlusIcon.png"},
	5013: {"Tenacity and Slow Resist", "perk-images/StatMods/StatModsTenacityIcon.png"},
}

func loadIcons() (runes, spells map[int]icon) {
	runes, spells = map[int]icon{}, map[int]icon{}
	for id, ic := range statShards {
		runes[id] = ic
	}

	raw, err := ddragonFS.ReadFile("ddragon/runesReforged.json")
	if err != nil {
		panic(err)
	}
	type entry struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	var trees []struct {
		entry
		Slots []struct {
			Runes []entry `json:"runes"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(raw, &trees); err != nil {
		panic(fmt.Errorf("runesReforged.json: %w", err))
	}
	for _, t := range trees {
		runes[t.ID] = icon{t.Name, t.Icon}
		for _, s := range t.Slots {
			for _, r := range s.Runes {
				runes[r.ID] = icon{r.Name, r.Icon}
			}
		}
	}

	raw, err = ddragonFS.ReadFile("ddragon/summoner.json")
	if err != nil {
		panic(err)
	}
	var sum struct {
		Data map[string]struct {
			Key   string `json:"key"`
			Name  string `json:"name"`
			Image struct {
				Full string `json:"full"`
			} `json:"image"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &sum); err != nil {
		panic(fmt.Errorf("summoner.json: %w", err))
	}
	for _, s := range sum.Data {
		if id, err := strconv.Atoi(s.Key); err == nil {
			spells[id] = icon{s.Name, s.Image.Full}
		}
	}
	return runes, spells
}

// runeImg is a rune, tree or stat shard icon, "" for an unknown id.
func runeImg(id int) string {
	if ic, ok := runeIcons[id]; ok {
		return ddragonCDN + "img/" + ic.Path
	}
	return ""
}

func runeName(id int) string {
	if ic, ok := runeIcons[id]; ok {
		return ic.Name
	}
	return fmt.Sprintf("Rune %d", id)
}

// spellImg is a summoner spell icon, "" for an unknown id.
func spellImg(id int, patch string) string {
	if ic, ok := spellIcons[id]; ok {
		return ddragonCDN + ddVersion(patch) + "/img/spell/" + url.PathEscape(ic.Path)
	}
	return ""
}

func spellName(id int) string {
	if ic, ok := spellIcons[id]; ok {
		return ic.Name
	}
	return fmt.Sprintf("Spell %d", id)
}

// splashImg is a champion's default skin splash art.
func splashImg(id string) string {
	if id == "FiddleSticks" {
		id = "Fiddlesticks"
	}
	return ddragonCDN + "img/champion/splash/" + url.PathEscape(id) + "_0.jpg"
}

var rankTiers = map[string]bool{
	"iron": true, "bronze": true, "silver": true, "gold": true, "platinum": true, "emerald": true,
	"diamond": true, "master": true, "grandmaster": true, "challenger": true,
}

func tierKey(tier string) string {
	t := strings.ToLower(tier)
	if rankTiers[t] {
		return t
	}
	return "unranked"
}

// rankCrest is the small single-color crest for a tier (IRON..CHALLENGER).
func rankCrest(tier string) string {
	return cdragonAssets + "ranked-mini-crests/" + tierKey(tier) + ".svg"
}

// rankEmblem is the full ranked emblem for a tier.
func rankEmblem(tier string) string {
	t := tierKey(tier)
	if t == "unranked" {
		return rankCrest(t)
	}
	return cdragonAssets + "ranked-emblem/emblem-" + t + ".png"
}

// tierName turns EMERALD into Emerald.
func tierName(tier string) string {
	if tier == "" {
		return "Unranked"
	}
	return strings.ToUpper(tier[:1]) + strings.ToLower(tier[1:])
}

// rankLabel is "Emerald III", or just the tier for apex tiers.
func rankLabel(tier, division string) string {
	switch tierKey(tier) {
	case "master", "grandmaster", "challenger", "unranked":
		return tierName(tier)
	}
	return tierName(tier) + " " + division
}

// roleKey folds Riot positions (MIDDLE), display names (Mid) and OP.GG
// positions (mid) into the role sprite's ids: top, jungle, mid, bot,
// support. Unknown roles give "".
func roleKey(r string) string {
	switch strings.ToLower(r) {
	case "top":
		return "top"
	case "jungle":
		return "jungle"
	case "middle", "mid":
		return "mid"
	case "bottom", "bot", "adc":
		return "bot"
	case "utility", "support":
		return "support"
	}
	return ""
}

// roleLabel names a roleKey.
func roleLabel(key string) string {
	switch key {
	case "mid":
		return "Mid"
	case "bot":
		return "Bot"
	case "":
		return ""
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

// opggRunes converts OP.GG's rune page into the shape the templates draw.
func opggRunes(p meta.RunePage) analysis.Runes {
	r := analysis.Runes{PrimaryStyle: p.PrimaryStyle, SubStyle: p.SubStyle}
	copy(r.Primary[:], p.Primary)
	copy(r.Sub[:], p.Sub)
	copy(r.Shards[:], p.StatMods)
	return r
}
