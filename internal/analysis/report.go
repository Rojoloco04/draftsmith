package analysis

import (
	"fmt"
	"io"
)

type ReportInput struct {
	RiotID   string
	Rank     string // e.g. "EMERALD II 45 LP (120W 110L)"
	RankDate string
	Games    []Game
	// MinGames hides champions and matchups with fewer games.
	MinGames int
}

// WriteReport renders a markdown report.
func WriteReport(w io.Writer, in ReportInput) {
	RenderMarkdown(w, in, Summarize(in.Games, in.MinGames))
}

// RenderMarkdown writes the report for a summary computed from in.Games.
func RenderMarkdown(w io.Writer, in ReportInput, s Summary) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }

	p("# Ranked report: %s", in.RiotID)
	p("")
	if len(s.Games) == 0 {
		p("No ranked games archived yet. Run `draftsmith sync` first.")
		return
	}
	first, last := s.span()
	p("%d games from %s to %s (%d remakes skipped, %d with timelines).",
		len(s.Games), first.Format("Jan 2 2006"), last.Format("Jan 2 2006"), s.Remakes, s.Timelines)
	if in.Rank != "" {
		p("Current rank (as of %s): **%s**", in.RankDate, in.Rank)
	}
	p("")

	p("## By season")
	p("")
	p("| Season | Games | Win rate | KDA | CS/min | Vision/min |")
	p("|---|---:|---:|---:|---:|---:|")
	for _, g := range s.Seasons {
		p("| %s | %d | %s | %.2f | %.1f | %.2f |", g.Key, g.Games, Pct(g.WinRate()), g.KDA(), g.CSPerMin(), g.VisionPerMin())
	}
	p("")
	p("Seasons are calendar years, which matches how Riot splits seasons since 2025. Riot deletes match data after about two years, so older seasons only show up if they were archived in time.")
	p("")

	p("## By role")
	p("")
	p("| Role | Games | Win rate | CS/min | Vision/min | Ctrl wards | KP | Dmg share | CS diff @10 | Gold diff @15 | Deaths <15m |")
	p("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, g := range s.Roles {
		p("| %s | %d | %s | %.1f | %.2f | %.1f | %s | %s | %s | %s | %s |",
			g.Key, g.Games, Pct(g.WinRate()), g.CSPerMin(), g.VisionPerMin(), g.ControlWards(), Pct(g.KillPart()), Pct(g.DamageShare()),
			Signed(g.CSDiff10(), 1), Signed(g.GoldDiff15(), 0), Num(g.DeathsBefore15(), 2))
	}
	p("")

	p("## Champion pool")
	p("")
	p("Adjusted win rate blends each champion's record with %d games at your overall win rate (%s), so small samples don't look better or worse than they are. Champions with fewer than %d games are hidden.", int(PriorWeight), Pct(s.Base), s.MinGames)
	p("")
	p("| Champion | Games | Win rate | Adjusted | KDA | CS/min | Gold diff @15 | Read |")
	p("|---|---:|---:|---:|---:|---:|---:|---|")
	for _, c := range s.Champions {
		p("| %s | %d | %s | %s | %.2f | %.1f | %s | %s |", c.Key, c.Games, Pct(c.WinRate()), Pct(c.Adjusted), c.KDA(), c.CSPerMin(),
			Signed(c.GoldDiff15(), 0), c.Read)
	}
	p("")

	p("## Early game vs result")
	p("")
	p("Win rate by your gold lead over your lane opponent at 15 minutes.")
	p("")
	p("| Gold diff @15 | Games | Win rate |")
	p("|---|---:|---:|")
	for _, b := range s.Buckets {
		p("| %s | %d | %s |", b.Label, b.Games, Pct(b.WinRate()))
	}
	p("")

	if len(s.Matchups) > 0 {
		p("## Toughest matchups (%s)", s.MainRole)
		p("")
		p("Lane opponents you've faced at least %d times, worst adjusted win rate first.", MatchupMinGames)
		p("")
		p("| Opponent | Games | Win rate | CS diff @10 | Gold diff @15 |")
		p("|---|---:|---:|---:|---:|")
		for i, o := range s.Matchups {
			if i == 8 {
				break
			}
			p("| %s | %d | %s | %s | %s |", o.Key, o.Games, Pct(o.WinRate()), Signed(o.CSDiff10(), 1), Signed(o.GoldDiff15(), 0))
		}
		p("")
	}

	if s.Recent != nil {
		p("## Last 20 games vs everything before")
		p("")
		p("| | Win rate | KDA | CS/min | CS diff @10 | Gold diff @15 | Deaths <15m |")
		p("|---|---:|---:|---:|---:|---:|---:|")
		for _, row := range []struct {
			l string
			a *Stats
		}{{"Last 20", s.Recent}, {"Before", s.Before}} {
			a := row.a
			p("| %s | %s | %.2f | %.1f | %s | %s | %s |", row.l, Pct(a.WinRate()), a.KDA(), a.CSPerMin(),
				Signed(a.CSDiff10(), 1), Signed(a.GoldDiff15(), 0), Num(a.DeathsBefore15(), 2))
		}
		p("")
	}

	p("## What stands out")
	p("")
	if len(s.Notes) == 0 {
		p("Nothing clear yet. More games, or games with timelines, will help.")
	}
	for _, n := range s.Notes {
		p("- %s", n)
	}
	p("")
	p("These compare you against yourself. Benchmarks against other players in your elo need the `sample` crawler (not built yet).")
}
