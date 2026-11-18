package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/draft"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/meta"
	"github.com/rojoloco04/draftsmith/internal/opgg"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/watch"
)

// cmdWatch follows the local League client and, in champ select, logs the
// draft and suggestions from my pool with OP.GG rune pages. It only reads
// the client; picking is always up to me.
func cmdWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	data := fs.String("data", env("DRAFTSMITH_DATA", "data"), "archive directory (for your games and the OP.GG cache)")
	dir := fs.String("league-dir", env("DRAFTSMITH_LEAGUE_DIR", ""), "League install directory (default: the standard install location)")
	poll := fs.Duration("poll", 2*time.Second, "how often to check the client's state")
	minGames := fs.Int("min-games", 2, "suggest only champions with at least this many games in your lane")
	noSuggest := fs.Bool("no-suggest", false, "only log the draft, without suggestions")
	opggEndpoint := fs.String("opgg-endpoint", opgg.DefaultEndpoint, "OP.GG MCP endpoint")
	capture := fs.String("capture", "", "write each champ select update (allowed fields only) and the payload's key names to this directory")
	fs.Parse(args)

	logger := log.New(os.Stdout, "", log.LstdFlags)
	dirs := lcu.DefaultDirs()
	if *dir != "" {
		dirs = []string{*dir}
	}
	w := &watch.Watcher{
		Discover: func(ctx context.Context) (lcu.Credentials, error) { return lcu.Discover(ctx, dirs) },
		Connect:  func(c lcu.Credentials) watch.Client { return lcu.New(c) },
		Log:      logger,
		Poll:     *poll,
	}

	// The index holds my games and the OP.GG cache. Without one, watch
	// still follows the draft, with champion ids and no suggestions.
	path := filepath.Join(*data, db.FileName)
	if _, err := os.Stat(path); err != nil {
		logger.Printf("no index at %s (run `draftsmith sync` first); showing the draft without suggestions", path)
	} else {
		d, err := db.Open(ctx, path)
		if err != nil {
			return err
		}
		defer d.Close()
		svc := meta.New(d, *opggEndpoint)
		names := championTable(ctx, svc, logger)
		if names != nil {
			w.Name = func(id int) string { return names.byID[id].Name }
		}
		if !*noSuggest {
			if live, err := newLive(ctx, d, svc, names, *minGames, logger); err != nil {
				logger.Printf("suggestions off: %v", err)
			} else {
				w.OnSession = live.Update
				w.OnPhase = func(p string) {
					if p != lcu.PhaseChampSelect {
						live.Reset()
					}
				}
			}
		}
	}

	if *capture != "" {
		if err := os.MkdirAll(*capture, 0o755); err != nil {
			return err
		}
		cp := &capturer{dir: *capture, shape: map[string]bool{}, log: logger}
		w.Connect = func(c lcu.Credentials) watch.Client { return &capturingClient{Client: lcu.New(c), cp: cp} }
		logger.Printf("capturing champ select to %s", *capture)
	}
	return w.Run(ctx)
}

// champions indexes OP.GG's champion table by Riot id and key.
type champions struct {
	byID  map[int]meta.Champion
	byKey map[string]meta.Champion
}

// championTable loads OP.GG's champion table (cached for a week), or nil
// if it can't, in which case logs show champion ids.
func championTable(ctx context.Context, svc *meta.Service, logger *log.Logger) *champions {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	list, err := svc.Champions(ctx)
	if err != nil {
		logger.Printf("champion names unavailable, showing ids: %v", err)
		return nil
	}
	c := &champions{byID: map[int]meta.Champion{}, byKey: map[string]meta.Champion{}}
	for _, ch := range list.List {
		c.byID[ch.ID] = ch
		c.byKey[ch.Key] = ch
	}
	return c
}

// newLive sets up suggestions from my solo queue games, and starts
// fetching OP.GG matchups for my main lane in the background.
func newLive(ctx context.Context, d *db.DB, svc *meta.Service, champs *champions, minGames int, logger *log.Logger) (*draft.Live, error) {
	if champs == nil {
		return nil, fmt.Errorf("no champion table to read the draft with")
	}
	games, err := d.Games(ctx, db.GameFilter{Queue: riot.QueueSoloDuo, NoRemakes: true})
	if err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("no solo queue games indexed")
	}
	wins := 0
	for _, g := range games {
		if g.Win {
			wins++
		}
	}
	live := &draft.Live{
		Meta: svc, Games: games, Base: float64(wins) / float64(len(games)), MinGames: minGames,
		Key:  func(id int) string { return champs.byID[id].Key },
		Name: func(key string) string { return champs.byKey[key].Name },
		Log:  logger,
	}
	live.Start(ctx)
	live.Warm("")
	return live, nil
}

// capturingClient records every champ select read for checking field names
// against the real client.
type capturingClient struct {
	*lcu.Client
	cp *capturer
}

func (c *capturingClient) ChampSelectSession(ctx context.Context) (lcu.Session, error) {
	s, paths, err := c.Client.ChampSelectShape(ctx)
	if err == nil {
		c.cp.record(s, paths)
	}
	return s, err
}

// capturer writes sessions that differ from the last one, and the union of
// key names seen. Only the typed session is written, so no identity field
// reaches disk; the key list has names, no values.
type capturer struct {
	dir   string
	shape map[string]bool
	last  []byte
	n     int
	log   *log.Logger
}

func (cp *capturer) record(s lcu.Session, paths []string) {
	s.Timer.AdjustedTimeLeftInPhase, s.Timer.InternalNowInEpochMs = 0, 0 // ticks every read
	b, _ := json.MarshalIndent(s, "", "  ")
	grew := false
	for _, p := range paths {
		if !cp.shape[p] {
			cp.shape[p], grew = true, true
		}
	}
	if grew {
		all := make([]string, 0, len(cp.shape))
		for p := range cp.shape {
			all = append(all, p)
		}
		sort.Strings(all)
		if err := os.WriteFile(filepath.Join(cp.dir, "keys.txt"), []byte(strings.Join(all, "\n")+"\n"), 0o644); err != nil {
			cp.log.Printf("capture: %v", err)
		}
	}
	if string(b) == string(cp.last) {
		return
	}
	cp.last = b
	cp.n++
	name := filepath.Join(cp.dir, fmt.Sprintf("session-%03d.json", cp.n))
	if err := os.WriteFile(name, b, 0o644); err != nil {
		cp.log.Printf("capture: %v", err)
	}
}
