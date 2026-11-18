package riot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseLimits(t *testing.T) {
	w, err := ParseLimits("20/1s, 100/2m")
	if err != nil || len(w) != 2 || w[1].Limit != 100 || w[1].Per != 2*time.Minute {
		t.Fatalf("got %v %v", w, err)
	}
	for _, bad := range []string{"", "20", "x/1s", "20/forever", "0/1s"} {
		if _, err := ParseLimits(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestLimiterBlocksUntilWindowFrees(t *testing.T) {
	l := NewLimiter([]Window{{Limit: 3, Per: 150 * time.Millisecond}})
	l.pad = 0
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 140*time.Millisecond {
		t.Fatalf("4th request went out after %v; should have waited for the window", el)
	}
}

func TestLimiterRespectsContext(t *testing.T) {
	l := NewLimiter([]Window{{Limit: 1, Per: time.Hour}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	l.Wait(ctx)
	if err := l.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient("RGAPI-test", "na1", []Window{{Limit: 1000, Per: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	c.BaseURL = func(string) string { return srv.URL }
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Riot-Token") != "RGAPI-test" {
			t.Errorf("missing api key header")
		}
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"puuid":"abc","gameName":"Jack","tagLine":"NA1"}`))
	})
	a, err := c.AccountByRiotID(context.Background(), "Jack", "NA1")
	if err != nil || a.PUUID != "abc" || calls.Load() != 3 {
		t.Fatalf("a=%+v err=%v calls=%d", a, err, calls.Load())
	}
}

func TestNotFoundAndAuthErrors(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if _, err := c.TimelineRaw(context.Background(), "NA1_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if _, err := c.MatchRaw(context.Background(), "NA1_1"); !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v", err)
	}
}

func TestMatchIDsQuery(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/lol/match/v5/matches/by-puuid/p1/ids" || q.Get("queue") != "420" || q.Get("start") != "100" || q.Get("count") != "100" {
			t.Errorf("bad request %s", r.URL)
		}
		w.Write([]byte(`["NA1_2","NA1_1"]`))
	})
	ids, err := c.MatchIDs(context.Background(), "p1", QueueSoloDuo, 100, 100)
	if err != nil || len(ids) != 2 {
		t.Fatalf("%v %v", ids, err)
	}
}

func TestRegionFor(t *testing.T) {
	if r, _ := RegionFor("na1"); r != "americas" {
		t.Fatal(r)
	}
	if r, _ := RegionFor("kr"); r != "asia" {
		t.Fatal(r)
	}
	if _, ok := RegionFor("mars1"); ok {
		t.Fatal("unknown platform accepted")
	}
}
