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
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/meta"
	"github.com/rojoloco04/draftsmith/internal/watch"
)

// cmdWatch follows the local League client. For now it logs gameflow phase
// changes and champ select as it happens; suggestions come next.
func cmdWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	data := fs.String("data", env("DRAFTSMITH_DATA", "data"), "archive directory (for champion names)")
	dir := fs.String("league-dir", env("DRAFTSMITH_LEAGUE_DIR", ""), "League install directory (default: the standard install location)")
	poll := fs.Duration("poll", 2*time.Second, "how often to check the client's state")
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
		Name:     championNames(ctx, *data, logger),
		Poll:     *poll,
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

// championNames maps champion ids to names through OP.GG's champion table
// (cached for a week in the index). Without it, logs show ids.
func championNames(ctx context.Context, data string, logger *log.Logger) func(int) string {
	path := filepath.Join(data, db.FileName)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	d, err := db.Open(ctx, path)
	if err != nil {
		logger.Printf("champion names: %v", err)
		return nil
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	champs, err := meta.New(d, "").Champions(ctx)
	if err != nil {
		logger.Printf("champion names unavailable, showing ids: %v", err)
		return nil
	}
	names := map[int]string{}
	for _, c := range champs.List {
		names[c.ID] = c.Name
	}
	return func(id int) string { return names[id] }
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
