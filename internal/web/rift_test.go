package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/db"
)

func TestRiftZone(t *testing.T) {
	cases := []struct {
		x, y float64
		want string
	}{
		{1000, 1000, "Own base"},
		{14000, 14000, "Enemy base"},
		{1200, 8000, "Top lane"},
		{8000, 14000, "Top lane, their side"},
		{8000, 1200, "Bot lane"},
		{14000, 8000, "Bot lane, their side"},
		{6000, 6200, "Mid lane"},
		{8500, 8600, "Mid lane, their side"},
		{5500, 9300, "Top river"},
		{9800, 5000, "Bot river"},
		{4000, 7000, "Own jungle, top side"},
		{7200, 4000, "Own jungle, bot side"},
		{7500, 11000, "Enemy jungle, top side"},
		{11000, 7500, "Enemy jungle, bot side"},
	}
	for _, c := range cases {
		if got := riftZone(c.x, c.y); got != c.want {
			t.Errorf("riftZone(%.0f, %.0f) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
}

func TestMySideTurnsRedAround(t *testing.T) {
	// A red side death in red's own base is in "my" base from my side.
	x, y := mySide(db.DeathSpot{X: 14000, Y: 14200, Blue: false})
	if got := riftZone(x, y); got != "Own base" {
		t.Errorf("red base from red side: %q at (%.0f, %.0f)", got, x, y)
	}
	x, y = mySide(db.DeathSpot{X: 14000, Y: 14200, Blue: true})
	if got := riftZone(x, y); got != "Enemy base" {
		t.Errorf("red base from blue side: %q", got)
	}
}

func TestDeathHeat(t *testing.T) {
	spots := []db.DeathSpot{
		{Minute: 3, X: 1200, Y: 8000, Blue: true, Win: false},  // top lane, laning
		{Minute: 5, X: 1250, Y: 8050, Blue: true, Win: false},  // same hex
		{Minute: 9, X: 13670, Y: 6870, Blue: false, Win: true}, // red side: top lane from my side
		{Minute: 18, X: 6000, Y: 6200, Blue: true, Win: true},  // mid lane, mid game
		{Minute: 31, X: 9800, Y: 5000, Blue: true, Win: false}, // bot river, late
	}
	h := newDeathHeat(spots, 2)
	if h.Total != 5 || len(h.Maps) != 3 {
		t.Fatalf("total %d, maps %d", h.Total, len(h.Maps))
	}
	if h.Maps[0].Deaths != 3 || h.Maps[1].Deaths != 1 || h.Maps[2].Deaths != 1 {
		t.Errorf("per phase: %d %d %d", h.Maps[0].Deaths, h.Maps[1].Deaths, h.Maps[2].Deaths)
	}
	if len(h.Zones) != 2 || h.Zones[0].Name != "Top lane" || h.Zones[0].Deaths != 3 || h.Zones[0].Losses != 2 {
		t.Fatalf("zones: %+v", h.Zones)
	}
	if got := h.Zones[0].AvgMinute(); got < 5.66 || got > 5.67 {
		t.Errorf("avg minute %.3f", got)
	}
	svg := string(h.Maps[0].SVG)
	if strings.Count(svg, `class="hex"`) != 2 || !strings.Contains(svg, `data-tip="2 deaths`) {
		t.Errorf("laning map should have two hexes, one with 2 deaths:\n%s", svg)
	}
}

func TestPoolPlot(t *testing.T) {
	var games []analysis.Game
	add := func(champ string, wins, losses int) {
		for i := 0; i < wins+losses; i++ {
			games = append(games, analysis.Game{Champion: champ, Win: i < wins, Kills: 5, Deaths: 3, Assists: 6})
		}
	}
	add("Ahri", 3, 1)
	add("MonkeyKing", 1, 0)
	add("Zed", 1, 0)
	add("Syndra", 0, 1)
	rows := champRows(games, .5)
	svg := string(poolPlot(rows, .5, "16.22", "solo"))
	for _, want := range []string{
		`href="/champions/MonkeyKing?queue=solo"`, `aria-label="Wukong"`, "Wukong\n1 game · 1W 0L",
		"Signature picks", "your average, 50%", "16.22.1/img/champion/Ahri.png",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Wukong and Zed land on the same spot (one win each): no two icons
	// may overlap.
	re := regexp.MustCompile(`<clipPath id="pp\d+"><circle cx="([\d.]+)" cy="([\d.]+)" r="([\d.]+)"/>`)
	var circles [][3]float64
	for _, m := range re.FindAllStringSubmatch(svg, -1) {
		var c [3]float64
		for i := range c {
			c[i], _ = strconv.ParseFloat(m[i+1], 64)
		}
		circles = append(circles, c)
	}
	if len(circles) != 4 {
		t.Fatalf("want 4 icons, got %d", len(circles))
	}
	for i := range circles {
		for j := i + 1; j < len(circles); j++ {
			a, b := circles[i], circles[j]
			if math.Hypot(a[0]-b[0], a[1]-b[1]) < a[2]+b[2] {
				t.Errorf("icons %d and %d overlap: %v %v", i, j, a, b)
			}
		}
	}
	if poolPlot(nil, .5, "", "solo") != "" {
		t.Error("empty pool should draw nothing")
	}
}
