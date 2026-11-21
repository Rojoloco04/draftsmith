package web

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"net/url"
	"sort"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/analysis"
)

// poolPlot draws my champions as icons: games played across, adjusted
// win rate up. Champions that land on the same spot (one-game picks only
// take two values) spread out sideways, then up and down. Lines at my
// overall win rate and between occasional and regular picks split it
// into four quadrants. Each icon links to its champion page; data-tip
// carries the details shown on hover.
func poolPlot(rows []analysis.Group, base float64, patch, queue string) template.HTML {
	if len(rows) == 0 {
		return ""
	}
	const (
		w, h               = 900.0, 460.0
		left, right        = 56.0, 24.0
		top, bottom        = 24.0, 44.0
		plotW, plotBottomY = w - left - right, h - bottom
	)
	gmax := 1
	lo, hi := base-.05, base+.05
	for _, r := range rows {
		gmax = max(gmax, r.Games)
		lo, hi = math.Min(lo, r.Adjusted), math.Max(hi, r.Adjusted)
	}
	lo, hi = lo-.01, hi+.01

	// Few games per champion: one column per count. Otherwise log scale.
	linear := gmax <= 12
	colW := plotW / (float64(gmax) + .2)
	xOf := func(g float64) float64 {
		if linear {
			return left + (g-.4)*colW
		}
		return left + 14 + math.Log(g)/math.Log(float64(gmax)*1.15)*(plotW-28)
	}
	yOf := func(a float64) float64 { return top + (1-(a-lo)/(hi-lo))*(plotBottomY-top) }
	radius := func(games int) float64 { return 7.5 + math.Sqrt(float64(games))*2.2 }
	spread := 40.0
	if linear {
		spread = colW * .45
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="pool" viewBox="0 0 %.0f %.0f" role="img" aria-label="Champion pool: games played against adjusted win rate">`, w, h)

	// Quadrants, split at regular picks and my average.
	yb := yOf(base)
	if gmax >= 2 {
		split := 5.0
		xs := xOf(split)
		if linear {
			split = math.Max(2, math.Ceil(float64(gmax)*.6))
			xs = xOf(split - .5)
		}
		fmt.Fprintf(&b, `<rect class="q-good" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, xs, top, w-right-xs, yb-top)
		fmt.Fprintf(&b, `<line class="ref" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, xs, top, xs, plotBottomY)
		for _, q := range []struct {
			text   string
			x, y   float64
			anchor string
		}{
			{"Signature picks", w - right - 8, top + 18, "end"},
			{"Hidden weapons", left + 8, top + 18, "start"},
			{"Comfort traps", w - right - 8, plotBottomY - 10, "end"},
			{"Experiments", left + 8, plotBottomY - 10, "start"},
		} {
			fmt.Fprintf(&b, `<text class="q-label" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, q.x, q.y, q.anchor, q.text)
		}
	}
	fmt.Fprintf(&b, `<line class="ref" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, left, yb, w-right, yb)
	fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f">your average, %s</text>`, left+4, yb-6, analysis.Pct(base))

	// Axes.
	var ticks []int
	if linear {
		for g := 1; g <= gmax; g++ {
			ticks = append(ticks, g)
		}
	} else {
		for _, g := range []int{1, 2, 5, 10, 20, 50, 100, 200, 500} {
			if float64(g) <= float64(gmax)*1.15 {
				ticks = append(ticks, g)
			}
		}
	}
	for _, g := range ticks {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%d</text>`, xOf(float64(g)), plotBottomY+20, g)
	}
	fmt.Fprintf(&b, `<text class="axis title" x="%.1f" y="%.1f" text-anchor="end">games played →</text>`, w-right, h-6)
	for a := math.Ceil(lo*20) / 20; a <= hi; a += .05 {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-10, yOf(a)+4, analysis.Pct(a))
	}

	// Place the most played first so they keep their exact spot.
	order := make([]analysis.Group, len(rows))
	copy(order, rows)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].Games != order[j].Games {
			return order[i].Games > order[j].Games
		}
		return order[i].Key < order[j].Key
	})
	type placed struct{ x, y, r float64 }
	var done []placed
	free := func(x, y, r float64) bool {
		for _, p := range done {
			if math.Hypot(p.x-x, p.y-y) < p.r+r+1.5 {
				return false
			}
		}
		return true
	}
	b.WriteString(`<defs>`)
	var icons strings.Builder
	for i, c := range order {
		r := radius(c.Games)
		x0, y0 := xOf(float64(c.Games)), yOf(c.Adjusted)
		x, y := x0, y0
		step := 2*r + 1.5
		best := math.Inf(1)
		for j := 0; j <= 16; j++ {
			dy := float64((j+1)/2) * step
			if j%2 == 1 {
				dy = -dy
			}
			for k := 0; k <= 40; k++ {
				dx := float64((k+1)/2) * step
				if k%2 == 1 {
					dx = -dx
				}
				if math.Abs(dx) > spread && k > 0 {
					break
				}
				cost := dx*dx + 2.5*dy*dy
				if cost < best && free(x0+dx, y0+dy, r) {
					best, x, y = cost, x0+dx, y0+dy
				}
			}
		}
		done = append(done, placed{x, y, r})
		fmt.Fprintf(&b, `<clipPath id="pp%d"><circle cx="%.1f" cy="%.1f" r="%.1f"/></clipPath>`, i, x, y, r)

		ring := "down"
		if c.Adjusted >= base {
			ring = "up"
		}
		tip := fmt.Sprintf("%s\n%s · %dW %dL\nAdjusted %s · raw %s\nKDA %s · %s CS/min",
			champName(c.Key), plural(c.Games, "game"), c.Wins, c.Losses(), analysis.Pct(c.Adjusted), analysis.Pct(c.WinRate()),
			analysis.Num(c.KDA(), 2), analysis.Num(c.CSPerMin(), 1))
		fmt.Fprintf(&icons, `<a class="pp %s" href="/champions/%s?queue=%s" data-tip="%s" aria-label="%s">`,
			ring, url.PathEscape(c.Key), url.QueryEscape(queue), html.EscapeString(tip), html.EscapeString(champName(c.Key)))
		fmt.Fprintf(&icons, `<circle class="pp-ring" cx="%.1f" cy="%.1f" r="%.1f"/>`, x, y, r+2)
		fmt.Fprintf(&icons, `<image href="%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" clip-path="url(#pp%d)"/>`,
			html.EscapeString(champImg(c.Key, patch)), x-r, y-r, 2*r, 2*r, i)
		icons.WriteString(`</a>`)
	}
	b.WriteString(`</defs>`)
	b.WriteString(icons.String())
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
