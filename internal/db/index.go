package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rojoloco04/draftsmith/internal/analysis"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

// IndexResult counts what one Index call changed.
type IndexResult struct {
	Added   int // matches indexed for the first time
	Updated int // matches reindexed because their timeline arrived later
	Skipped int // archived matches the tracked player is not in
	Ranks   int // rank snapshot rows written
}

// Index brings the database up to date with the archive: it indexes every
// archived match not yet in games, reindexes games whose timeline has been
// archived since, and imports rank snapshots. It is cheap when nothing
// changed. If puuid differs from the one the index was built for, the index
// is cleared first.
func (d *DB) Index(ctx context.Context, s *store.Store, puuid string) (IndexResult, error) {
	var res IndexResult
	if puuid == "" {
		return res, errors.New("index: no tracked player")
	}
	prev, err := d.setting(ctx, "puuid")
	if err != nil {
		return res, err
	}
	if prev != puuid {
		if err := d.clear(ctx); err != nil {
			return res, err
		}
		if err := d.setSetting(ctx, "puuid", puuid); err != nil {
			return res, err
		}
	}

	indexed := map[string]bool{} // match id -> has timeline
	rows, err := d.sql.QueryContext(ctx, `SELECT match_id, has_timeline FROM games`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		var tl bool
		if err := rows.Scan(&id, &tl); err != nil {
			rows.Close()
			return res, err
		}
		indexed[id] = tl
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	ids, err := s.MatchIDs()
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		hasTL, seen := indexed[id]
		if seen && (hasTL || !s.TimelineArchived(id)) {
			continue
		}
		ok, err := d.indexMatch(ctx, s, id, puuid)
		if err != nil {
			return res, fmt.Errorf("indexing %s: %w", id, err)
		}
		switch {
		case !ok:
			res.Skipped++
		case seen:
			res.Updated++
		default:
			res.Added++
		}
	}

	n, err := d.importRanks(ctx, s)
	res.Ranks = n
	return res, err
}

// Rebuild clears every derived table and indexes the whole archive again.
func (d *DB) Rebuild(ctx context.Context, s *store.Store, puuid string) (IndexResult, error) {
	if err := d.clear(ctx); err != nil {
		return IndexResult{}, err
	}
	return d.Index(ctx, s, puuid)
}

func (d *DB) clear(ctx context.Context) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"deaths", "participants", "frames", "games", "rank_snapshots"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// indexMatch (re)writes one match. ok is false if puuid is not in it.
func (d *DB) indexMatch(ctx context.Context, s *store.Store, id, puuid string) (ok bool, err error) {
	raw, err := s.LoadMatch(id)
	if err != nil {
		return false, err
	}
	var m riot.Match
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, err
	}
	var tl *riot.Timeline
	if traw, err := s.LoadTimeline(id); err == nil {
		tl = &riot.Timeline{}
		if err := json.Unmarshal(traw, tl); err != nil {
			return false, fmt.Errorf("timeline: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("timeline: %w", err)
	}
	g, ok := analysis.Extract(&m, tl, puuid)
	if !ok {
		return false, nil
	}
	det, _ := analysis.ExtractDetail(&m, tl, puuid)

	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Deleting the game cascades to deaths, participants and frames.
	if _, err := tx.ExecContext(ctx, `DELETE FROM games WHERE match_id = ?`, id); err != nil {
		return false, err
	}
	if err := insertGame(ctx, tx, g); err != nil {
		return false, err
	}
	for i, dth := range g.DeathSpots {
		if _, err := tx.ExecContext(ctx, `INSERT INTO deaths (match_id, seq, minute, x, y) VALUES (?, ?, ?, ?, ?)`,
			id, i, dth.Minute, dth.X, dth.Y); err != nil {
			return false, err
		}
	}
	for _, p := range det.Players {
		if _, err := tx.ExecContext(ctx, `INSERT INTO participants (match_id, participant_id, team_id, position, champion,
			me, win, level, kills, deaths, assists, cs, gold, damage, vision, items)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, p.ParticipantID, p.TeamID, p.Position, p.Champion, p.Me, p.Win, p.Level,
			p.Kills, p.Deaths, p.Assists, p.CS, p.Gold, p.Damage, p.Vision, joinItems(p.Items)); err != nil {
			return false, err
		}
	}
	for _, f := range det.Lane {
		if _, err := tx.ExecContext(ctx, `INSERT INTO frames (match_id, minute, gold, cs, xp, has_opp, opp_gold, opp_cs, opp_xp)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, f.Minute, f.Gold, f.CS, f.XP, f.HasOpp, f.OppGold, f.OppCS, f.OppXP); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func insertGame(ctx context.Context, tx *sql.Tx, g analysis.Game) error {
	at := func(l *analysis.LaneDiff) []any {
		if l == nil {
			return []any{nil, nil, nil, nil, nil, nil, nil}
		}
		return []any{l.Gold, l.CS, l.XP, l.GoldDiff, l.CSDiff, l.XPDiff, l.HasOpp}
	}
	args := []any{
		g.MatchID, g.Start.UnixMilli(), g.Patch, g.Queue, g.Remake, g.Minutes, g.Champion, g.Role, g.Opponent, g.Win,
		g.Kills, g.Deaths, g.Assists, g.CS, g.CSPerMin, g.GoldPerMin, g.VisionPerMin, g.ControlWards,
		g.KillParticipation, g.DamageShare, g.HasTimeline,
	}
	args = append(args, at(g.At10)...)
	args = append(args, at(g.At15)...)
	args = append(args, g.DeathsBefore15, g.DeathsEnemySide)
	_, err := tx.ExecContext(ctx, `INSERT INTO games (`+gameColumns+`) VALUES (`+placeholders(len(args))+`)`, args...)
	return err
}

func (d *DB) importRanks(ctx context.Context, s *store.Store) (int, error) {
	days, err := s.RankSnapshotDays()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, day := range days {
		raw, err := s.LoadRankSnapshot(day)
		if err != nil {
			return n, err
		}
		var entries []riot.LeagueEntry
		if err := json.Unmarshal(raw, &entries); err != nil {
			return n, fmt.Errorf("rank snapshot %s: %w", day, err)
		}
		for _, e := range entries {
			if _, err := d.sql.ExecContext(ctx, `INSERT INTO rank_snapshots (day, queue, tier, division, lp, wins, losses)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (day, queue) DO UPDATE SET tier = excluded.tier, division = excluded.division,
					lp = excluded.lp, wins = excluded.wins, losses = excluded.losses`,
				day, e.QueueType, e.Tier, e.Rank, e.LeaguePoints, e.Wins, e.Losses); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

const gameColumns = `match_id, start_ms, patch, queue, remake, minutes, champion, role, opponent, win,
	kills, deaths, assists, cs, cs_per_min, gold_per_min, vision_per_min, control_wards,
	kill_participation, damage_share, has_timeline,
	at10_gold, at10_cs, at10_xp, at10_gold_diff, at10_cs_diff, at10_xp_diff, at10_has_opp,
	at15_gold, at15_cs, at15_xp, at15_gold_diff, at15_cs_diff, at15_xp_diff, at15_has_opp,
	deaths_before_15, deaths_enemy_side`

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func joinItems(items [7]int) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = strconv.Itoa(it)
	}
	return strings.Join(parts, ",")
}

func splitItems(s string) [7]int {
	var out [7]int
	for i, p := range strings.SplitN(s, ",", 7) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
