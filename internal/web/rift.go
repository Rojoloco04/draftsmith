package web

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"sort"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/db"
)

// riftEdge is the far corner of Summoner's Rift in game units on both axes
// (analysis.mapEdge). Blue's base is bottom left, red's top right, and the
// diagonal x+y = riftEdge is the river.
const riftEdge = 14870.0

// mySide is a death's position seen from my side: red side games are
// turned around the map's center so my base is always bottom left.
func mySide(d db.DeathSpot) (x, y float64) {
	if d.Blue {
		return float64(d.X), float64(d.Y)
	}
	return riftEdge - float64(d.X), riftEdge - float64(d.Y)
}

// riftZone names a position seen from my side (see mySide). The lanes run
// along the edges (top: left and top, bot: bottom and right) and the
// x = y diagonal (mid); "their side" is past the river.
func riftZone(x, y float64) string {
	s := x + y
	mid := riftEdge
	switch {
	case x < 3600 && y < 3600:
		return "Own base"
	case x > riftEdge-3600 && y > riftEdge-3600:
		return "Enemy base"
	case (x < 2300 && y >= 3600) || (y > riftEdge-2300 && x <= riftEdge-3600):
		if y > riftEdge-2300 && x > 4500 {
			return "Top lane, their side"
		}
		return "Top lane"
	case (y < 2300 && x >= 3600) || (x > riftEdge-2300 && y <= riftEdge-3600):
		if x > riftEdge-2300 && y > 4500 {
			return "Bot lane, their side"
		}
		return "Bot lane"
	case math.Abs(x-y) < 1300:
		if s > mid {
			return "Mid lane, their side"
		}
		return "Mid lane"
	case math.Abs(s-mid) < 1500:
		if x < y {
			return "Top river"
		}
		return "Bot river"
	case s < mid:
		if x < y {
			return "Own jungle, top side"
		}
		return "Own jungle, bot side"
	}
	if x < y {
		return "Enemy jungle, top side"
	}
	return "Enemy jungle, bot side"
}

// gamePhase is a span of game time the heatmap splits deaths by.
type gamePhase struct {
	Name, Span string
	From, To   float64 // minutes, From <= m < To
}

var gamePhases = []gamePhase{
	{"Laning", "0–14 min", 0, 14},
	{"Mid game", "14–25 min", 14, 25},
	{"Late game", "25 min +", 25, math.Inf(1)},
}

// dangerZone is where I die most, with how often it was in a loss.
type dangerZone struct {
	Name   string
	Deaths int
	Losses int
	minSum float64
}

func (z dangerZone) LossShare() float64 { return float64(z.Losses) / float64(z.Deaths) }
func (z dangerZone) AvgMinute() float64 { return z.minSum / float64(z.Deaths) }

// phaseMap is one phase's hex-binned map.
type phaseMap struct {
	gamePhase
	Deaths int
	SVG    template.HTML
}

// deathHeat is the overview's death heatmap: one map per game phase and
// the zones with the most deaths across all of them.
type deathHeat struct {
	Total int
	Maps  []phaseMap
	Zones []dangerZone
}

// Hex bins on a size x size map: pointy-topped hexes of radius hexR.
const (
	heatSize = 300.0
	hexR     = 9.5
)

type hexBin struct {
	row, col int
	n        int
	zones    map[string]int
}

func (b *hexBin) center() (float64, float64) {
	w := math.Sqrt(3) * hexR
	x := float64(b.col) * w
	if b.row%2 != 0 {
		x += w / 2
	}
	return x, float64(b.row) * 1.5 * hexR
}

// newDeathHeat bins deaths by phase and names the top nZones zones.
func newDeathHeat(spots []db.DeathSpot, nZones int) deathHeat {
	h := deathHeat{Total: len(spots)}
	zones := map[string]*dangerZone{}
	bins := make([]map[[2]int]*hexBin, len(gamePhases))
	counts := make([]int, len(gamePhases))
	most := 1
	w := math.Sqrt(3) * hexR
	for i := range bins {
		bins[i] = map[[2]int]*hexBin{}
	}
	for _, d := range spots {
		x, y := mySide(d)
		name := riftZone(x, y)
		z := zones[name]
		if z == nil {
			z = &dangerZone{Name: name}
			zones[name] = z
		}
		z.Deaths++
		z.minSum += d.Minute
		if !d.Win {
			z.Losses++
		}
		for i, p := range gamePhases {
			if d.Minute < p.From || d.Minute >= p.To {
				continue
			}
			counts[i]++
			px, py := x/riftEdge*heatSize, (1-y/riftEdge)*heatSize
			row := int(math.Round(py / (1.5 * hexR)))
			off := 0.0
			if row%2 != 0 {
				off = w / 2
			}
			col := int(math.Round((px - off) / w))
			k := [2]int{row, col}
			b := bins[i][k]
			if b == nil {
				b = &hexBin{row: row, col: col, zones: map[string]int{}}
				bins[i][k] = b
			}
			b.n++
			b.zones[name]++
			most = max(most, b.n)
		}
	}
	for i, p := range gamePhases {
		h.Maps = append(h.Maps, phaseMap{gamePhase: p, Deaths: counts[i], SVG: heatSVG(p, bins[i], most)})
	}
	for _, z := range zones {
		h.Zones = append(h.Zones, *z)
	}
	sort.Slice(h.Zones, func(i, j int) bool {
		if h.Zones[i].Deaths != h.Zones[j].Deaths {
			return h.Zones[i].Deaths > h.Zones[j].Deaths
		}
		return h.Zones[i].Name < h.Zones[j].Name
	})
	if len(h.Zones) > nZones {
		h.Zones = h.Zones[:nZones]
	}
	return h
}

// heatSVG draws one phase: a line drawing of the Rift (my base bottom
// left) under hexes shaded by their share of the busiest hex.
func heatSVG(p gamePhase, bins map[[2]int]*hexBin, most int) template.HTML {
	const s = heatSize
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="heat" viewBox="0 0 %.0f %.0f" role="img" aria-label="Deaths, %s">`, s, s, html.EscapeString(strings.ToLower(p.Name)))
	fmt.Fprintf(&b, `<rect class="h-edge" x="1" y="1" width="%.0f" height="%.0f"/>`, s-2, s-2)
	fmt.Fprintf(&b, `<line class="h-river" x1="0" y1="0" x2="%.0f" y2="%.0f"/>`, s, s)
	fmt.Fprintf(&b, `<polyline class="h-lane" points="16,%.0f 16,16 %.0f,16"/>`, s-30, s-30)
	fmt.Fprintf(&b, `<polyline class="h-lane" points="30,%.0f %.0f,%.0f %.0f,30"/>`, s-16, s-16, s-16, s-16)
	fmt.Fprintf(&b, `<line class="h-lane" x1="40" y1="%.0f" x2="%.0f" y2="40"/>`, s-40, s-40)
	fmt.Fprintf(&b, `<path class="h-base own" d="M0 %.0f A62 62 0 0 1 62 %.0f L0 %.0f Z"/>`, s-62, s, s)
	fmt.Fprintf(&b, `<path class="h-base" d="M%.0f 62 A62 62 0 0 1 %.0f 0 L%.0f 0 Z"/>`, s, s-62, s)

	keys := make([][2]int, 0, len(bins))
	for k := range bins {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		h := bins[k]
		cx, cy := h.center()
		var pts []string
		for i := 0; i < 6; i++ {
			a := math.Pi / 180 * float64(60*i-30)
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", cx+hexR*.94*math.Cos(a), cy+hexR*.94*math.Sin(a)))
		}
		names := make([]string, 0, len(h.zones))
		for z := range h.zones {
			names = append(names, z)
		}
		sort.Strings(names)
		tip := fmt.Sprintf("%s\n%s", plural(h.n, "death"), strings.Join(names, ", "))
		fmt.Fprintf(&b, `<polygon class="hex" points="%s" style="fill-opacity:%.2f" data-tip="%s"/>`,
			strings.Join(pts, " "), .18+.82*float64(h.n)/float64(most), html.EscapeString(tip))
		if h.n >= 3 {
			fmt.Fprintf(&b, `<text class="hex-n" x="%.1f" y="%.1f">%d</text>`, cx, cy+3.5, h.n)
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
