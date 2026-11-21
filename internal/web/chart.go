package web

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
)

// Charts are rendered server side as inline SVG: no chart library, nothing
// to load, and handlers can be tested by looking at the HTML. Colors come
// from CSS classes so they follow the theme.

// Point is one value; Label becomes the hover tooltip.
type Point struct {
	X, Y  float64
	Label string
}

// Series is one line. Class picks its color (see style.css: s1, s2, s3).
type Series struct {
	Name   string
	Class  string
	Points []Point
	// Dots draws a marker on every point (useful for sparse series).
	Dots bool
}

// LineChart is a simple multi-series line chart.
type LineChart struct {
	Title  string // accessible name
	Series []Series
	// YRange fixes the y axis; nil fits it to the data.
	YRange *[2]float64
	// RefY draws dashed horizontal guide lines (e.g. 50% win rate, 0 diff).
	RefY    []float64
	YFormat func(float64) string
	XFormat func(float64) string
	XTicks  []float64 // nil picks about five ticks
	YTicks  int       // number of y ticks, default 4
	// Width is the drawing's width in user units (default chartW); the
	// height is always chartH, so a wider chart is flatter on the page.
	Width int
}

const (
	chartW, chartH           = 640, 220
	padR, padT, padB float64 = 12, 12, 26
	plotH                    = chartH - padT - padB
)

// niceRange widens [lo, hi] to multiples of a round step (1, 2, 2.5 or 5
// times a power of ten) giving about n intervals, and returns the number
// of intervals actually used.
func niceRange(lo, hi float64, n int) (float64, float64, int) {
	if hi <= lo || n <= 0 {
		return lo, hi, max(n, 1)
	}
	raw := (hi - lo) / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag * 10
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	lo = math.Floor(lo/step) * step
	hi = math.Ceil(hi/step) * step
	return lo, hi, int(math.Round((hi - lo) / step))
}

// SVG renders the chart. An empty chart renders nothing.
func (c LineChart) SVG() template.HTML {
	xmin, xmax := math.Inf(1), math.Inf(-1)
	ymin, ymax := math.Inf(1), math.Inf(-1)
	n := 0
	for _, s := range c.Series {
		for _, p := range s.Points {
			n++
			xmin, xmax = math.Min(xmin, p.X), math.Max(xmax, p.X)
			ymin, ymax = math.Min(ymin, p.Y), math.Max(ymax, p.Y)
		}
	}
	if n == 0 {
		return ""
	}
	for _, r := range c.RefY {
		ymin, ymax = math.Min(ymin, r), math.Max(ymax, r)
	}
	if xmax == xmin {
		xmin, xmax = xmin-1, xmax+1
	}
	yt := c.YTicks
	if yt <= 0 {
		yt = 4
	}
	if c.YRange != nil {
		ymin, ymax = c.YRange[0], c.YRange[1]
	} else {
		ymin, ymax, yt = niceRange(ymin, ymax, yt)
	}
	if ymax == ymin {
		ymin, ymax = ymin-1, ymax+1
	}
	yf := c.YFormat
	if yf == nil {
		yf = func(v float64) string { return fmt.Sprintf("%g", v) }
	}
	xf := c.XFormat
	if xf == nil {
		xf = func(v float64) string { return fmt.Sprintf("%.0f", v) }
	}

	// Size the left margin to the widest y label. Ticks are 11px, but 18px
	// on phones (style.css), where the chart is scaled down; ~10px/char
	// leaves room for both.
	labels := make([]string, yt+1)
	widest := 0
	for i := range labels {
		labels[i] = yf(ymin + (ymax-ymin)*float64(i)/float64(yt))
		widest = max(widest, len([]rune(labels[i])))
	}
	padL := math.Max(28, 10*float64(widest)+12)
	w := c.Width
	if w <= 0 {
		w = chartW
	}
	plotW := float64(w) - padL - padR
	sx := func(x float64) float64 { return padL + (x-xmin)/(xmax-xmin)*plotW }
	sy := func(y float64) float64 { return padT + (1-(y-ymin)/(ymax-ymin))*plotH }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" style="aspect-ratio: %d / %d" role="img" aria-label="%s">`,
		w, chartH, w, chartH, html.EscapeString(c.Title))

	// Grid and y labels.
	for i, label := range labels {
		y := sy(ymin + (ymax-ymin)*float64(i)/float64(yt))
		fmt.Fprintf(&b, `<line class="gridline" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, padL+plotW, y, y)
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" dy=".35em" text-anchor="end">%s</text>`, padL-6, y, html.EscapeString(label))
	}
	for _, r := range c.RefY {
		y := sy(r)
		fmt.Fprintf(&b, `<line class="ref" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, padL+plotW, y, y)
	}

	// X labels.
	xt := c.XTicks
	if xt == nil {
		for i := 0; i <= 4; i++ {
			xt = append(xt, xmin+(xmax-xmin)*float64(i)/4)
		}
	}
	for _, v := range xt {
		if v < xmin || v > xmax {
			continue
		}
		anchor := "middle"
		if v == xmin {
			anchor = "start"
		} else if v == xmax {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`,
			sx(v), float64(chartH)-6, anchor, html.EscapeString(xf(v)))
	}

	for _, s := range c.Series {
		if len(s.Points) == 0 {
			continue
		}
		fmt.Fprintf(&b, `<g class="series %s"><title>%s</title>`, s.Class, html.EscapeString(s.Name))
		if len(s.Points) > 1 {
			var pts []string
			for _, p := range s.Points {
				pts = append(pts, fmt.Sprintf("%.1f,%.1f", sx(p.X), sy(p.Y)))
			}
			fmt.Fprintf(&b, `<polyline points="%s"/>`, strings.Join(pts, " "))
		}
		dots := s.Dots || len(s.Points) == 1
		for _, p := range s.Points {
			label := html.EscapeString(p.Label)
			if dots {
				fmt.Fprintf(&b, `<circle class="dot" cx="%.1f" cy="%.1f" r="3.5"><title>%s</title></circle>`, sx(p.X), sy(p.Y), label)
			} else {
				// An invisible, larger hover target keeps tooltips usable on lines.
				fmt.Fprintf(&b, `<circle class="hit" cx="%.1f" cy="%.1f" r="6"><title>%s</title></circle>`, sx(p.X), sy(p.Y), label)
			}
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// DeathMap plots death positions on a square Summoner's Rift outline. Blue
// base is bottom left, as in game.
func DeathMap(points []Point, title string) template.HTML {
	const size, edge = 240.0, 15000.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="deathmap" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, size, size, html.EscapeString(title))
	fmt.Fprintf(&b, `<rect class="rift" x="0" y="0" width="%.0f" height="%.0f" rx="6"/>`, size, size)
	// River (the x+y = edge diagonal) and the three lanes.
	fmt.Fprintf(&b, `<line class="river" x1="6" y1="6" x2="%.0f" y2="%.0f"/>`, size-6, size-6)
	fmt.Fprintf(&b, `<polyline class="lane" points="18,%.0f 18,18 %.0f,18"/>`, size-18, size-18)
	fmt.Fprintf(&b, `<polyline class="lane" points="18,%.0f %.0f,%.0f %.0f,18"/>`, size-18, size-18, size-18, size-18)
	fmt.Fprintf(&b, `<line class="lane" x1="18" y1="%.0f" x2="%.0f" y2="18"/>`, size-18, size-18)
	for _, p := range points {
		x := p.X / edge * size
		y := size - p.Y/edge*size
		fmt.Fprintf(&b, `<circle class="death" cx="%.1f" cy="%.1f" r="5"><title>%s</title></circle>`, x, y, html.EscapeString(p.Label))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
