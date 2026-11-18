package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/fixture"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
)

// fakeRiot serves n solo queue games, newest first. Games older than
// timelineCutoff return 404 for their timeline, like Riot does after a year.
type fakeRiot struct {
	ids            []string
	timelineCutoff int
	listCalls      atomic.Int32
	matchCalls     atomic.Int32
}

func newFake(n, timelineCutoff int) *fakeRiot {
	f := &fakeRiot{timelineCutoff: timelineCutoff}
	for i := n; i >= 1; i-- {
		f.ids = append(f.ids, fmt.Sprintf("NA1_%d", i))
	}
	return f
}

func (f *fakeRiot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/ids"):
		f.listCalls.Add(1)
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		end := min(start+count, len(f.ids))
		if start > len(f.ids) {
			start = len(f.ids)
		}
		json.NewEncoder(w).Encode(f.ids[start:end])
	case strings.HasSuffix(p, "/timeline"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/lol/match/v5/matches/"), "/timeline")
		n, _ := strconv.Atoi(strings.TrimPrefix(id, "NA1_"))
		if n <= f.timelineCutoff {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(fixture.MustJSON(fixture.Timeline(fixture.Spec{ID: id, Champion: "Ahri"})))
	case strings.HasPrefix(p, "/lol/match/v5/matches/"):
		f.matchCalls.Add(1)
		id := strings.TrimPrefix(p, "/lol/match/v5/matches/")
		w.Write(fixture.MustJSON(fixture.Match(fixture.Spec{ID: id, Champion: "Ahri", StartMs: time.Now().UnixMilli()})))
	default:
		http.NotFound(w, r)
	}
}

func setup(t *testing.T, f *fakeRiot) (*riot.Client, *store.Store) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := riot.NewClient("k", "na1", []riot.Window{{Limit: 10000, Per: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = func(string) string { return srv.URL }
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestBackfillThenIncremental(t *testing.T) {
	f := newFake(230, 30)
	c, s := setup(t, f)
	st := store.State{PUUID: fixture.MePUUID, BackfillDone: map[int]bool{}}
	opt := Options{Queues: []int{riot.QueueSoloDuo}, Timelines: true}

	res, err := Sync(context.Background(), c, s, &st, opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewMatches != 230 || res.NewTimelines != 200 || res.MissingTimelines != 30 || res.Errors != 0 {
		t.Fatalf("first sync: %+v", res)
	}
	if !st.BackfillDone[riot.QueueSoloDuo] {
		t.Fatal("backfill not marked done")
	}
	if got := f.listCalls.Load(); got != 3 {
		t.Fatalf("list calls = %d, want 3 pages", got)
	}

	// Two new games arrive. The second run should only read the first page.
	f.ids = append([]string{"NA1_232", "NA1_231"}, f.ids...)
	f.listCalls.Store(0)
	f.matchCalls.Store(0)
	res, err = Sync(context.Background(), c, s, &st, opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewMatches != 2 || res.NewTimelines != 2 || res.MissingTimelines != 0 {
		t.Fatalf("second sync: %+v", res)
	}
	if f.listCalls.Load() != 2 || f.matchCalls.Load() != 2 {
		// page 1 has new ids so it continues to page 2, which is all known.
		t.Fatalf("list=%d match=%d", f.listCalls.Load(), f.matchCalls.Load())
	}
}

func TestInterruptedBackfillResumes(t *testing.T) {
	f := newFake(150, 0)
	c, s := setup(t, f)
	st := store.State{PUUID: fixture.MePUUID, BackfillDone: map[int]bool{}}

	if _, err := Sync(context.Background(), c, s, &st, Options{Queues: []int{riot.QueueSoloDuo}, Max: 120}); err != nil {
		t.Fatal(err)
	}
	ids, _ := s.MatchIDs()
	if len(ids) != 120 {
		t.Fatalf("stored %d", len(ids))
	}
	// The listing finished, but not every game was fetched. A rerun must
	// still pick up the 30 oldest games.
	res, err := Sync(context.Background(), c, s, &st, Options{Queues: []int{riot.QueueSoloDuo}})
	if err != nil {
		t.Fatal(err)
	}
	if res.NewMatches != 30 {
		t.Fatalf("resume fetched %d", res.NewMatches)
	}
}
