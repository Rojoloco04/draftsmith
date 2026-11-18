package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rojoloco04/lolstats/internal/fixture"
)

// TestSyncReportExport runs the CLI commands end to end against a fake Riot.
func TestSyncReportExport(t *testing.T) {
	const n = 45
	champs := []string{"Ahri", "Syndra", "Viktor"}
	spec := func(i int) fixture.Spec {
		return fixture.Spec{
			ID: fmt.Sprintf("NA1_%d", i), StartMs: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * 24 * time.Hour).UnixMilli(),
			Champion: champs[i%3], Opponent: "Zed", Win: i%3 != 2, Kills: 4, Deaths: 3, Assists: 6, GoldLead: 400 * (i%3 - 1),
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/riot/account/v1/accounts/by-riot-id/"):
			w.Write([]byte(`{"puuid":"` + fixture.MePUUID + `","gameName":"Jack","tagLine":"NA1"}`))
		case strings.HasPrefix(p, "/lol/league/v4/entries/by-puuid/"):
			w.Write([]byte(`[{"queueType":"RANKED_SOLO_5x5","tier":"EMERALD","rank":"II","leaguePoints":45,"wins":30,"losses":15}]`))
		case strings.HasSuffix(p, "/ids"):
			start, _ := strconv.Atoi(r.URL.Query().Get("start"))
			var ids []string
			for i := n - start; i >= 1 && len(ids) < 100; i-- {
				ids = append(ids, fmt.Sprintf("NA1_%d", i))
			}
			json.NewEncoder(w).Encode(ids)
		case strings.HasSuffix(p, "/timeline"):
			id := strings.TrimSuffix(strings.TrimPrefix(p, "/lol/match/v5/matches/"), "/timeline")
			i, _ := strconv.Atoi(strings.TrimPrefix(id, "NA1_"))
			w.Write(fixture.MustJSON(fixture.Timeline(spec(i))))
		case strings.HasPrefix(p, "/lol/match/v5/matches/"):
			i, _ := strconv.Atoi(strings.TrimPrefix(p, "/lol/match/v5/matches/NA1_"))
			w.Write(fixture.MustJSON(fixture.Match(spec(i))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("RIOT_API_KEY", "RGAPI-test")
	t.Setenv("LOLSTATS_RIOT_BASE_URL", srv.URL)
	data := filepath.Join(dir, "data")

	if err := cmdSync(context.Background(), []string{"--data", data, "--riot-id", "Jack#NA1", "--rate", "1000/1s"}); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "report.md")
	if err := cmdReport([]string{"--data", data, "--out", report}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(report)
	out := string(b)
	for _, want := range []string{"# Ranked report: Jack#NA1", "45 games", "EMERALD II 45 LP", "| Ahri | 15 |", "| Viktor | 15 | 0% |"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n%s", want, out)
		}
	}

	export := filepath.Join(dir, "games.jsonl")
	if err := cmdExport([]string{"--data", data, "--out", export}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(export)
	if lines := strings.Count(string(b), "\n"); lines != n {
		t.Fatalf("export has %d lines", lines)
	}

	// Second sync is a no-op.
	if err := cmdSync(context.Background(), []string{"--data", data, "--rate", "1000/1s"}); err != nil {
		t.Fatal(err)
	}
}
