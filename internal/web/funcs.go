package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/meta"
	"github.com/rojoloco04/draftsmith/internal/riot"
)

var funcs = template.FuncMap{
	"pct": analysis.Pct,
	"pct1": func(x float64) string {
		if math.IsNaN(x) {
			return "-"
		}
		return fmt.Sprintf("%.1f%%", x*100)
	},
	"pos":         meta.PositionName,
	"clientPhase": clientPhase,
	"csPhase":     champSelectPhase,
	"metaOff":     func(err error) bool { return errors.Is(err, errMetaOff) },
	"signed":      analysis.Signed,
	"num":         analysis.Num,
	"role":        analysis.RoleName,
	"champ":       champName,
	"champImg":    champImg,
	"itemImg":     itemImg,
	"nameAt": func(names []string, i int) string {
		if i < len(names) {
			return names[i]
		}
		return ""
	},
	"runeImg":    runeImg,
	"runeName":   runeName,
	"spellImg":   spellImg,
	"spellName":  spellName,
	"splashImg":  splashImg,
	"rankCrest":  rankCrest,
	"rankEmblem": rankEmblem,
	"rankLabel":  rankLabel,
	"roleKey":    roleKey,
	"roleLabel":  roleLabel,
	"dict":       dict,
	"list":       func(xs ...any) []any { return xs },
	"opggRunes":  opggRunes,
	"asset":      asset,
	"recordPct": func(wins, losses int) float64 {
		if wins+losses == 0 {
			return math.NaN()
		}
		return float64(wins) / float64(wins+losses)
	},
	"queue":   queueName,
	"kda":     kda,
	"dur":     duration,
	"date":    func(t time.Time) string { return t.Format("Jan 2, 2006") },
	"ago":     ago,
	"wrClass": wrClass,
	"diffClass": func(x float64) string {
		switch {
		case math.IsNaN(x) || x == 0:
			return ""
		case x > 0:
			return "pos"
		}
		return "neg"
	},
	"diffInt": func(l *analysis.LaneDiff, what string) string {
		if l == nil || !l.HasOpp {
			return "-"
		}
		switch what {
		case "gold":
			return fmt.Sprintf("%+d", l.GoldDiff)
		case "cs":
			return fmt.Sprintf("%+d", l.CSDiff)
		case "xp":
			return fmt.Sprintf("%+d", l.XPDiff)
		}
		return "-"
	},
	"laneClass": func(l *analysis.LaneDiff, what string) string {
		if l == nil || !l.HasOpp {
			return ""
		}
		v := map[string]int{"gold": l.GoldDiff, "cs": l.CSDiff, "xp": l.XPDiff}[what]
		switch {
		case v > 0:
			return "pos"
		case v < 0:
			return "neg"
		}
		return ""
	},
	"thousands": func(n int) string {
		if n >= 1000 || n <= -1000 {
			return fmt.Sprintf("%.1fk", float64(n)/1000)
		}
		return fmt.Sprint(n)
	},
	"widthPct": func(v, max int) string {
		if max <= 0 {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(v)/float64(max))
	},
	// lpWidth is LP as a bar width, full at 100 (Master+ can go past).
	"lpWidth": func(lp int) string { return fmt.Sprintf("%d%%", max(0, min(lp, 100))) },
	"lower": strings.ToLower,
	// ordinal is 1st, 2nd, 3rd, 4th... (places in a lobby of ten).
	"ordinal": func(n int) string {
		suffix := "th"
		if n%100 < 11 || n%100 > 13 {
			switch n % 10 {
			case 1:
				suffix = "st"
			case 2:
				suffix = "nd"
			case 3:
				suffix = "rd"
			}
		}
		return fmt.Sprintf("%d%s", n, suffix)
	},
	"add":  func(a, b int) int { return a + b },
	"sub":  func(a, b int) int { return a - b },
	"q":    url.QueryEscape,
	"path": func(s string) string { return url.PathEscape(s) },
	"initials": func(name string) string {
		r := []rune(champName(name))
		if len(r) > 2 {
			r = r[:2]
		}
		return string(r)
	},
	"pctBar": func(x float64) string {
		if math.IsNaN(x) {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", 100*math.Max(0, math.Min(1, x)))
	},
}

// dict builds a map from key, value pairs so a template can pass more
// than one value to another: {{template "x" (dict "A" 1 "B" 2)}}.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("dict needs key, value pairs")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict key %v is not a string", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// displayNames covers champions whose Data Dragon id isn't their name.
var displayNames = map[string]string{
	"AurelionSol": "Aurelion Sol", "Belveth": "Bel'Veth", "Chogath": "Cho'Gath", "DrMundo": "Dr. Mundo",
	"FiddleSticks": "Fiddlesticks", "JarvanIV": "Jarvan IV", "Kaisa": "Kai'Sa", "Khazix": "Kha'Zix",
	"KogMaw": "Kog'Maw", "KSante": "K'Sante", "Leblanc": "LeBlanc", "LeeSin": "Lee Sin", "MasterYi": "Master Yi",
	"MissFortune": "Miss Fortune", "MonkeyKing": "Wukong", "Nunu": "Nunu & Willump", "RekSai": "Rek'Sai",
	"Renata": "Renata Glasc", "TahmKench": "Tahm Kench", "TwistedFate": "Twisted Fate", "Velkoz": "Vel'Koz",
	"XinZhao": "Xin Zhao",
}

// champName turns match-v5's championName into a display name.
func champName(id string) string {
	if n, ok := displayNames[id]; ok {
		return n
	}
	return id
}

// ddVersion maps a game patch ("16.22") to its Data Dragon version.
func ddVersion(patch string) string {
	if patch == "" {
		return "latest"
	}
	return patch + ".1"
}

func champImg(id, patch string) string {
	if id == "FiddleSticks" { // match-v5 and Data Dragon disagree
		id = "Fiddlesticks"
	}
	return "https://ddragon.leagueoflegends.com/cdn/" + ddVersion(patch) + "/img/champion/" + url.PathEscape(id) + ".png"
}

func itemImg(id int, patch string) string {
	return fmt.Sprintf("https://ddragon.leagueoflegends.com/cdn/%s/img/item/%d.png", ddVersion(patch), id)
}

func queueName(id int) string {
	switch id {
	case riot.QueueSoloDuo:
		return "Ranked Solo"
	case riot.QueueFlex:
		return "Ranked Flex"
	}
	return fmt.Sprintf("Queue %d", id)
}

func kda(k, d, a int) string {
	if d == 0 {
		return "Perfect"
	}
	return fmt.Sprintf("%.2f", float64(k+a)/float64(d))
}

func duration(minutes float64) string {
	secs := int(math.Round(minutes * 60))
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	}
	return t.Format("Jan 2, 2006")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// wrClass colors a win rate: good, bad, or neutral.
func wrClass(x float64) string {
	switch {
	case math.IsNaN(x):
		return ""
	case x >= 0.55:
		return "pos"
	case x < 0.45:
		return "neg"
	}
	return ""
}

// tierOrder ranks tiers for the LP chart; apex tiers share one ladder.
var tierOrder = []string{"IRON", "BRONZE", "SILVER", "GOLD", "PLATINUM", "EMERALD", "DIAMOND", "MASTER", "GRANDMASTER", "CHALLENGER"}

var divisionOrder = map[string]int{"IV": 0, "III": 1, "II": 2, "I": 3}

// ladderPoints puts a rank on one axis: 400 per tier, 100 per division.
// Master and above have no divisions and share Master's base.
func ladderPoints(tier, division string, lp int) float64 {
	for i, t := range tierOrder {
		if t == tier {
			if i >= 7 {
				return float64(7*400 + lp)
			}
			return float64(i*400 + divisionOrder[division]*100 + lp)
		}
	}
	return math.NaN()
}

// ladderLabel is the inverse of ladderPoints for axis ticks.
func ladderLabel(v float64) string {
	if v >= 7*400 {
		return "Master+"
	}
	if v < 0 {
		return ""
	}
	i := int(v) / 400
	div := (int(v) % 400) / 100
	names := []string{"IV", "III", "II", "I"}
	t := tierOrder[i]
	return t[:1] + strings.ToLower(t[1:]) + " " + names[div]
}

// assetVersion changes whenever an embedded static file does. Static
// files are cached for an hour, so their URLs carry it: a new build
// doesn't run new pages against an old stylesheet or script.
var assetVersion = func() string {
	h := sha256.New()
	fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := staticFS.ReadFile(p)
			h.Write([]byte(p))
			h.Write(b)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

// asset is a static file's versioned URL.
func asset(name string) string { return "/static/" + name + "?v=" + assetVersion }
