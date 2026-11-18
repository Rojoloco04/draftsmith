// Package ingest pulls ranked match history from Riot into the local store.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

type Options struct {
	Queues    []int
	Timelines bool
	// Max caps how many new matches to fetch this run (0 = no cap).
	Max int
	Log io.Writer
}

type Result struct {
	NewMatches       int
	NewTimelines     int
	MissingTimelines int
	Errors           int
}

// Sync lists match ids for each queue, then archives any match and timeline
// not already stored. It is safe to interrupt and rerun.
func Sync(ctx context.Context, c *riot.Client, s *store.Store, st *store.State, opt Options) (Result, error) {
	var res Result
	logf := func(format string, a ...any) {
		if opt.Log != nil {
			fmt.Fprintf(opt.Log, format+"\n", a...)
		}
	}

	var todo []string
	seen := map[string]bool{}
	listed := map[int][]string{}
	listComplete := map[int]bool{}
	// A queue's backfill only counts as done once every listed game is
	// archived; otherwise an interrupted or capped run would leave old games
	// behind a page of already-stored ones and never come back for them.
	defer func() {
		for q, ids := range listed {
			if listComplete[q] && allStored(s, ids) {
				st.BackfillDone[q] = true
			}
		}
	}()
	for _, q := range opt.Queues {
		ids, complete, err := listQueue(ctx, c, s, st.PUUID, q, st.BackfillDone[q])
		if err != nil {
			return res, fmt.Errorf("listing queue %d: %w", q, err)
		}
		logf("queue %d: %d match ids listed", q, len(ids))
		listed[q] = ids
		listComplete[q] = complete
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if !s.HasMatch(id) || (opt.Timelines && !s.HasTimeline(id)) {
				todo = append(todo, id)
			}
		}
	}

	if opt.Max > 0 && len(todo) > opt.Max {
		todo = todo[:opt.Max]
	}
	calls := 0
	for _, id := range todo {
		if !s.HasMatch(id) {
			calls++
		}
		if opt.Timelines && !s.HasTimeline(id) {
			calls++
		}
	}
	if len(todo) == 0 {
		logf("nothing new to fetch")
		return res, nil
	}
	// 100 calls per 2 minutes on a personal key is ~1.2s per call.
	logf("fetching %d games (%d API calls, roughly %s on a personal key)", len(todo), calls,
		(time.Duration(calls) * 1200 * time.Millisecond).Round(time.Minute))

	start := time.Now()
	for i, id := range todo {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !s.HasMatch(id) {
			raw, err := c.MatchRaw(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				if isAuth(err) {
					return res, err
				}
				res.Errors++
				logf("  %s: match: %v", id, err)
				continue
			}
			if err := s.PutMatch(id, raw); err != nil {
				return res, err
			}
			res.NewMatches++
		}
		if opt.Timelines && !s.HasTimeline(id) {
			raw, err := c.TimelineRaw(ctx, id)
			switch {
			case errors.Is(err, riot.ErrNotFound):
				if err := s.MarkTimelineMissing(id); err != nil {
					return res, err
				}
				res.MissingTimelines++
			case err != nil:
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				if isAuth(err) {
					return res, err
				}
				res.Errors++
				logf("  %s: timeline: %v", id, err)
			default:
				if err := s.PutTimeline(id, raw); err != nil {
					return res, err
				}
				res.NewTimelines++
			}
		}
		if (i+1)%25 == 0 || i+1 == len(todo) {
			logf("  %d/%d games (%s elapsed)", i+1, len(todo), time.Since(start).Round(time.Second))
		}
	}
	return res, nil
}

// listQueue pages through match ids newest first. Once a backfill has
// completed, it stops at the first page where every id is already stored.
// complete reports whether it reached the end of Riot's list.
func listQueue(ctx context.Context, c *riot.Client, s *store.Store, puuid string, queue int, backfillDone bool) ([]string, bool, error) {
	const page = 100
	var all []string
	for start := 0; ; start += page {
		ids, err := c.MatchIDs(ctx, puuid, queue, start, page)
		if err != nil {
			return all, false, err
		}
		all = append(all, ids...)
		if len(ids) < page {
			return all, true, nil
		}
		if backfillDone && allStored(s, ids) {
			return all, true, nil
		}
	}
}

func allStored(s *store.Store, ids []string) bool {
	for _, id := range ids {
		if !s.HasMatch(id) {
			return false
		}
	}
	return true
}

func isAuth(err error) bool { return errors.Is(err, riot.ErrAuth) }
