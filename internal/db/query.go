package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/analysis"
)

// ErrNotFound is returned for a match id that is not indexed.
var ErrNotFound = errors.New("not found")

// GameFilter selects games. Zero values match everything.
type GameFilter struct {
	// Account is one of my accounts' PUUID; "" means all of them.
	Account  string
	Queue    int
	Role     string // Riot position, e.g. MIDDLE
	Champion string
	Opponent string
	// NoRemakes drops remakes.
	NoRemakes bool
}

func (f GameFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.Account != "" {
		conds = append(conds, "account = ?")
		args = append(args, f.Account)
	}
	if f.Queue != 0 {
		conds = append(conds, "queue = ?")
		args = append(args, f.Queue)
	}
	if f.Role != "" {
		conds = append(conds, "role = ?")
		args = append(args, f.Role)
	}
	if f.Champion != "" {
		conds = append(conds, "champion = ?")
		args = append(args, f.Champion)
	}
	if f.Opponent != "" {
		conds = append(conds, "opponent = ?")
		args = append(args, f.Opponent)
	}
	if f.NoRemakes {
		conds = append(conds, "remake = 0")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Games returns matching games oldest first, without death spots. This is
// the input for analysis.Summarize.
func (d *DB) Games(ctx context.Context, f GameFilter) ([]analysis.Game, error) {
	where, args := f.where()
	return d.queryGames(ctx, `SELECT `+gameColumns+` FROM games`+where+` ORDER BY start_ms, match_id`, args...)
}

// GamePage returns one page of matching games, newest first, and the total
// number of matching games.
func (d *DB) GamePage(ctx context.Context, f GameFilter, offset, limit int) ([]analysis.Game, int, error) {
	where, args := f.where()
	var total int
	if err := d.sql.QueryRowContext(ctx, `SELECT count(*) FROM games`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	games, err := d.queryGames(ctx, `SELECT `+gameColumns+` FROM games`+where+
		` ORDER BY start_ms DESC, match_id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	return games, total, err
}

// Match returns one game with its death spots, and the detail rows.
func (d *DB) Match(ctx context.Context, id string) (analysis.Game, analysis.Detail, error) {
	var det analysis.Detail
	games, err := d.queryGames(ctx, `SELECT `+gameColumns+` FROM games WHERE match_id = ?`, id)
	if err != nil {
		return analysis.Game{}, det, err
	}
	if len(games) == 0 {
		return analysis.Game{}, det, ErrNotFound
	}
	g := games[0]

	rows, err := d.sql.QueryContext(ctx, `SELECT minute, x, y FROM deaths WHERE match_id = ? ORDER BY seq`, id)
	if err != nil {
		return g, det, err
	}
	for rows.Next() {
		var dth analysis.Death
		if err := rows.Scan(&dth.Minute, &dth.X, &dth.Y); err != nil {
			rows.Close()
			return g, det, err
		}
		g.DeathSpots = append(g.DeathSpots, dth)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return g, det, err
	}

	rows, err = d.sql.QueryContext(ctx, `SELECT `+playerColumns+`
		FROM participants WHERE match_id = ? ORDER BY participant_id`, id)
	if err != nil {
		return g, det, err
	}
	for rows.Next() {
		p, err := scanPlayer(rows)
		if err != nil {
			rows.Close()
			return g, det, err
		}
		det.Players = append(det.Players, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return g, det, err
	}

	rows, err = d.sql.QueryContext(ctx, `SELECT minute, gold, cs, xp, has_opp, opp_gold, opp_cs, opp_xp
		FROM frames WHERE match_id = ? ORDER BY minute`, id)
	if err != nil {
		return g, det, err
	}
	defer rows.Close()
	for rows.Next() {
		var f analysis.LanePoint
		if err := rows.Scan(&f.Minute, &f.Gold, &f.CS, &f.XP, &f.HasOpp, &f.OppGold, &f.OppCS, &f.OppXP); err != nil {
			return g, det, err
		}
		det.Lane = append(det.Lane, f)
	}
	return g, det, rows.Err()
}

func (d *DB) queryGames(ctx context.Context, query string, args ...any) ([]analysis.Game, error) {
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []analysis.Game
	for rows.Next() {
		var g analysis.Game
		var start int64
		var at10, at15 laneCols
		if err := rows.Scan(
			&g.MatchID, &g.Account, &start, &g.Patch, &g.Queue, &g.Remake, &g.Minutes, &g.Champion, &g.Role, &g.Opponent, &g.Win,
			&g.Kills, &g.Deaths, &g.Assists, &g.CS, &g.CSPerMin, &g.GoldPerMin, &g.VisionPerMin, &g.ControlWards,
			&g.KillParticipation, &g.DamageShare, &g.HasTimeline,
			&at10.gold, &at10.cs, &at10.xp, &at10.goldDiff, &at10.csDiff, &at10.xpDiff, &at10.hasOpp,
			&at15.gold, &at15.cs, &at15.xp, &at15.goldDiff, &at15.csDiff, &at15.xpDiff, &at15.hasOpp,
			&g.DeathsBefore15, &g.DeathsEnemySide,
		); err != nil {
			return nil, err
		}
		g.Start = time.UnixMilli(start)
		g.At10 = at10.diff()
		g.At15 = at15.diff()
		out = append(out, g)
	}
	return out, rows.Err()
}

type laneCols struct {
	gold, cs, xp, goldDiff, csDiff, xpDiff sql.NullInt64
	hasOpp                                 sql.NullBool
}

func (l laneCols) diff() *analysis.LaneDiff {
	if !l.gold.Valid {
		return nil
	}
	return &analysis.LaneDiff{
		Gold: int(l.gold.Int64), CS: int(l.cs.Int64), XP: int(l.xp.Int64),
		GoldDiff: int(l.goldDiff.Int64), CSDiff: int(l.csDiff.Int64), XPDiff: int(l.xpDiff.Int64),
		HasOpp: l.hasOpp.Bool,
	}
}

// RankSnapshot is one day's standing in one queue.
type RankSnapshot struct {
	Day      string // YYYY-MM-DD
	Queue    string // RANKED_SOLO_5x5, RANKED_FLEX_SR
	Tier     string
	Division string
	LP       int
	Wins     int
	Losses   int
}

// RankHistory returns an account's daily snapshots for a queue, oldest
// first.
func (d *DB) RankHistory(ctx context.Context, account, queue string) ([]RankSnapshot, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT day, queue, tier, division, lp, wins, losses
		FROM rank_snapshots WHERE account = ? AND queue = ? ORDER BY day`, account, queue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RankSnapshot
	for rows.Next() {
		var r RankSnapshot
		if err := rows.Scan(&r.Day, &r.Queue, &r.Tier, &r.Division, &r.LP, &r.Wins, &r.Losses); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestRank returns an account's newest snapshot for a queue, or nil if
// there is none.
func (d *DB) LatestRank(ctx context.Context, account, queue string) (*RankSnapshot, error) {
	var r RankSnapshot
	err := d.sql.QueryRowContext(ctx, `SELECT day, queue, tier, division, lp, wins, losses
		FROM rank_snapshots WHERE account = ? AND queue = ? ORDER BY day DESC LIMIT 1`, account, queue).
		Scan(&r.Day, &r.Queue, &r.Tier, &r.Division, &r.LP, &r.Wins, &r.Losses)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Queues lists the queue ids that have indexed games, most games first.
func (d *DB) Queues(ctx context.Context) ([]int, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT queue FROM games GROUP BY queue ORDER BY count(*) DESC, queue`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var q int
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

const playerColumns = `participant_id, team_id, position, champion, me, win, level,
	kills, deaths, assists, cs, gold, damage, vision, items, spells, runes`

// scanPlayer reads playerColumns, after any lead columns into lead.
func scanPlayer(rows *sql.Rows, lead ...any) (analysis.PlayerLine, error) {
	var p analysis.PlayerLine
	var items, spells, runes string
	dest := append(lead, &p.ParticipantID, &p.TeamID, &p.Position, &p.Champion, &p.Me, &p.Win, &p.Level,
		&p.Kills, &p.Deaths, &p.Assists, &p.CS, &p.Gold, &p.Damage, &p.Vision, &items, &spells, &runes)
	if err := rows.Scan(dest...); err != nil {
		return p, err
	}
	splitInts(items, p.Items[:])
	splitInts(spells, p.Spells[:])
	p.Runes = splitRunes(runes)
	return p, nil
}

// MyLines returns my end-of-game line (items, spells, runes) for each of
// the given matches, keyed by match id. Matches without one are left out.
func (d *DB) MyLines(ctx context.Context, ids []string) (map[string]analysis.PlayerLine, error) {
	out := map[string]analysis.PlayerLine{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT match_id, `+playerColumns+`
		FROM participants WHERE me = 1 AND match_id IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		p, err := scanPlayer(rows, &id)
		if err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}
