package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/fixture"
	"github.com/rojoloco04/draftsmith/internal/store"
	"github.com/rojoloco04/draftsmith/internal/web"
)

func TestSetDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# my settings\r\nRIOT_API_KEY=old\r\nDRAFTSMITH_RIOT_ID=Me#NA1\r\nOTHER=keep\r\n"), 0o600)
	if err := setDotEnv(path, map[string]string{"RIOT_API_KEY": "RGAPI-new", "DRAFTSMITH_RIOT_ID": "", "DRAFTSMITH_PLATFORM": "euw1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "# my settings\nRIOT_API_KEY=RGAPI-new\nOTHER=keep\nDRAFTSMITH_PLATFORM=euw1\n"
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
	// A missing file is created.
	fresh := filepath.Join(t.TempDir(), ".env")
	if err := setDotEnv(fresh, map[string]string{"RIOT_API_KEY": "RGAPI-x"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); string(b) != "RIOT_API_KEY=RGAPI-x\n" {
		t.Errorf("fresh: %q", b)
	}
}

// TestSettingsFirstRun is a friend's first start: no key, no account. The
// settings page saves a key (checked with Riot), adds an account, and the
// background loop syncs and indexes it.
func TestSettingsFirstRun(t *testing.T) {
	spec := func(i int) fixture.Spec {
		return fixture.Spec{
			ID: fmt.Sprintf("NA1_%d", i), StartMs: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour).UnixMilli(),
			Champion: "Ahri", Opponent: "Zed", Win: true,
		}
	}
	riotSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if r.Header.Get("X-Riot-Token") != "RGAPI-good" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch {
		case p == "/lol/status/v4/platform-data":
			w.Write([]byte(`{"id":"NA1"}`))
		case p == "/riot/account/v1/accounts/by-riot-id/jack/na1":
			w.Write([]byte(`{"puuid":"` + fixture.MePUUID + `","gameName":"Jack","tagLine":"NA1"}`))
		case strings.HasPrefix(p, "/lol/league/v4/entries/by-puuid/"):
			w.Write([]byte(`[]`))
		case strings.HasSuffix(p, "/ids"):
			ids := []string{"NA1_3", "NA1_2", "NA1_1"}
			if r.URL.Query().Get("start") != "0" {
				ids = nil
			}
			json.NewEncoder(w).Encode(ids)
		case strings.HasSuffix(p, "/timeline"):
			var i int
			fmt.Sscanf(p, "/lol/match/v5/matches/NA1_%d/timeline", &i)
			w.Write(fixture.MustJSON(fixture.Timeline(spec(i))))
		case strings.HasPrefix(p, "/lol/match/v5/matches/"):
			var i int
			fmt.Sscanf(p, "/lol/match/v5/matches/NA1_%d", &i)
			w.Write(fixture.MustJSON(fixture.Match(spec(i))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer riotSrv.Close()

	dir := t.TempDir()
	t.Setenv("DRAFTSMITH_RIOT_BASE_URL", riotSrv.URL)
	t.Setenv("RIOT_API_KEY", "")
	t.Setenv("DRAFTSMITH_RIOT_ID", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	data := filepath.Join(dir, "data")
	s, _ := store.Open(data)
	d, err := db.OpenDir(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	srv, err := web.New(d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	ctl := newSyncControl()
	ctl.progress = func(string, int, int) {}
	b := &settingsBackend{
		envPath: filepath.Join(dir, ".env"), c: common{data: data, platform: "na1", queues: "solo"}, rate: "1000/1s", timelines: true,
		store: s, db: d, srv: srv, ctl: ctl, logger: logger,
	}
	go syncLoop(ctx, ctl, s, d, srv, time.Hour, logger)

	if _, err := b.AddAccount(ctx, "Jack#NA1"); err == nil || !strings.Contains(err.Error(), "key first") {
		t.Errorf("an account before a key: %v", err)
	}
	if err := b.SetKey(ctx, "RGAPI-bad"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("bad key: %v", err)
	}
	if err := b.SetKey(ctx, "not a key"); err == nil {
		t.Error("a key without RGAPI- should be refused before asking Riot")
	}
	if err := b.SetKey(ctx, "RGAPI-good"); err != nil {
		t.Fatal(err)
	}
	if env, _ := os.ReadFile(b.envPath); !strings.Contains(string(env), "RIOT_API_KEY=RGAPI-good") {
		t.Errorf(".env after saving the key: %q", env)
	}
	got := b.Settings()
	if !got.KeySet || got.KeyHint != "good" {
		t.Errorf("settings after the key: %+v", got)
	}

	id, err := b.AddAccount(ctx, " jack#na1 ")
	if err != nil || id != "Jack#NA1" {
		t.Fatalf("add: %q, %v", id, err)
	}
	if _, err := b.AddAccount(ctx, "Jack#NA1"); err == nil {
		t.Error("adding the same account twice should say so")
	}

	// The loop picks the request up and the first sync indexes the games.
	deadline := time.Now().Add(10 * time.Second)
	for {
		games, _ := d.Games(ctx, db.GameFilter{})
		st := srv.Status()
		if len(games) == 3 && !st.FirstSync && !st.Syncing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first sync didn't finish: %d games, status %+v", len(games), st)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := b.SetOptions(ctx, "euw1", "both", "dark", false); err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(b.envPath)
	for _, want := range []string{"DRAFTSMITH_PLATFORM=euw1", "DRAFTSMITH_QUEUES=both", "DRAFTSMITH_OVERLAY=off", "DRAFTSMITH_THEME=dark"} {
		if !strings.Contains(string(env), want) {
			t.Errorf(".env missing %s: %q", want, env)
		}
	}
	if err := b.SetOptions(ctx, "mars1", "solo", "", true); err == nil {
		t.Error("unknown region accepted")
	}

	if err := b.RemoveAccount(ctx, fixture.MePUUID); err != nil {
		t.Fatal(err)
	}
	if games, _ := d.Games(ctx, db.GameFilter{}); len(games) != 0 {
		t.Errorf("removed account's games still indexed: %d", len(games))
	}
	if st, _ := s.LoadState(); len(st.Accounts) != 0 {
		t.Errorf("state after remove: %+v", st.Accounts)
	}
}
