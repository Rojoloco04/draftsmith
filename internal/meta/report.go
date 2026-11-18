package meta

import (
	"fmt"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
)

// PoolMarkdown renders the report's "pool vs the meta" section: every
// champion in fits with at least PoolMinGames games, against the tier list
// tl. now dates the tier list's age. It returns "" when no champion
// qualifies.
func PoolMarkdown(fits []Fit, tl TierList, base float64, now time.Time) string {
	var rows []Fit
	for _, f := range fits {
		if f.Games >= PoolMinGames {
			rows = append(rows, f)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	p("## Your pool vs the meta")
	p("")
	age := "OP.GG's Emerald+ tier list as of " + tl.FetchedAt.Format("Jan 2 2006")
	if now.Sub(tl.FetchedAt) >= TierListMaxAge {
		age += " (cached; `draftsmith serve` refreshes it)"
	}
	p("%s. Champions with %d+ games, compared in the role you play each one most. \"Working\" and \"losing\" mean 3+ points above or below your overall %s after adjusting for sample size.",
		age, PoolMinGames, analysis.Pct(base))
	p("")
	p("| Champion | Role | Games | Adjusted | Meta tier | Read |")
	p("|---|---|---:|---:|---|---|")
	for _, f := range rows {
		tier := "unlisted"
		if f.InMeta {
			tier = fmt.Sprintf("%d", f.Meta.Tier)
		}
		p("| %s | %s | %d | %s | %s | %s |", f.Champion, PositionName(f.Position), f.Games, analysis.Pct(f.Adjusted), tier, f.Note)
	}
	return b.String()
}
