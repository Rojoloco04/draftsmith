package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
	"github.com/rojoloco04/draftsmith/internal/web"
)

// syncControl is serve's background sync: the syncer it runs (nil until
// there's a key; the settings page swaps it when the key, region or
// queues change), the Refresh button's queue, and a lock so only one
// reindex runs at a time.
type syncControl struct {
	mu  sync.Mutex
	sy  *syncer
	off bool // --no-sync: never sync, whatever the settings say

	now      chan struct{}
	progress func(stage string, done, total int)
	indexMu  sync.Mutex

	readyOnce sync.Once
	ready     chan struct{} // closed once the index has games to suggest from
}

func newSyncControl() *syncControl {
	return &syncControl{now: make(chan struct{}, 1), ready: make(chan struct{})}
}

func (c *syncControl) get() *syncer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sy
}

func (c *syncControl) set(sy *syncer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sy != nil {
		sy.progress = c.progress
	}
	c.sy = sy
}

// request asks for a sync now. One request can wait; more while it does
// are dropped.
func (c *syncControl) request() bool {
	select {
	case c.now <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *syncControl) markReady() { c.readyOnce.Do(func() { close(c.ready) }) }

// index brings the dashboard's index up to date with the tracked accounts.
func (c *syncControl) index(ctx context.Context, d *db.DB, s *store.Store, st store.State) (db.IndexResult, error) {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	return d.Index(ctx, s, st.PUUIDs())
}

// settingsBackend is the settings page's store: .env for the key, region,
// queues and overlay, the archive's state for the accounts. Each change
// also updates the running sync and the dashboard.
type settingsBackend struct {
	mu        sync.Mutex
	envPath   string
	c         common
	rate      string
	timelines bool
	overlay   bool

	store  *store.Store
	db     *db.DB
	srv    *web.Server
	ctl    *syncControl
	logger *log.Logger
}

func (b *settingsBackend) Settings() web.Settings {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := os.Getenv("RIOT_API_KEY")
	out := web.Settings{
		KeySet: key != "", Platform: b.c.platform, Queues: strings.ToLower(b.c.queues), Overlay: b.overlay, File: b.envPath,
		Theme: b.srv.Theme(),
	}
	switch out.Queues {
	case "":
		out.Queues = "solo"
	case "all":
		out.Queues = "both"
	}
	if len(key) > 4 {
		out.KeyHint = key[len(key)-4:]
	}
	st, err := b.store.LoadState()
	if err != nil {
		b.logger.Printf("settings: %v", err)
	}
	for _, a := range st.Accounts {
		out.Accounts = append(out.Accounts, web.SettingsAccount{PUUID: a.PUUID, RiotID: a.RiotID, Platform: a.Platform, LastSync: a.LastSync})
	}
	return out
}

func (b *settingsBackend) SetKey(ctx context.Context, key string) error {
	if !strings.HasPrefix(key, "RGAPI-") || strings.ContainsAny(key, " \t\r\n") {
		return errors.New("that doesn't look like a Riot API key; they start with RGAPI-")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sy, err := newSyncerKey(b.c, b.rate, key)
	if err != nil {
		return err
	}
	sy.timelines = b.timelines
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := sy.client.CheckKey(cctx); err != nil {
		if errors.Is(err, riot.ErrAuth) {
			return errors.New("Riot rejected that key. Copy it again from developer.riotgames.com (development keys expire after 24 hours)")
		}
		return fmt.Errorf("couldn't reach Riot to check the key: %w", err)
	}
	if err := b.saveEnv(map[string]string{"RIOT_API_KEY": key}); err != nil {
		return err
	}
	if b.ctl.off {
		return nil
	}
	b.ctl.set(sy)
	b.srv.SetStatus(func(s *web.Status) { s.SyncDisabled, s.KeyRejected = "", false })
	b.srv.SetRefresh(b.ctl.request)
	b.ctl.request()
	return nil
}

func (b *settingsBackend) SetOptions(ctx context.Context, platform, queues, theme string, overlay bool) error {
	if _, ok := riot.RegionFor(platform); !ok {
		return fmt.Errorf("unknown region %q", platform)
	}
	switch queues {
	case "solo", "flex", "both":
	default:
		return fmt.Errorf("unknown queues %q", queues)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	on := "on"
	if !overlay {
		on = "off"
	}
	switch theme {
	case "", "dark", "light":
	default:
		return fmt.Errorf("unknown theme %q", theme)
	}
	if err := b.saveEnv(map[string]string{
		"DRAFTSMITH_PLATFORM": platform, "DRAFTSMITH_QUEUES": queues, "DRAFTSMITH_OVERLAY": on, "DRAFTSMITH_THEME": theme,
	}); err != nil {
		return err
	}
	changed := platform != b.c.platform || queues != strings.ToLower(b.c.queues)
	b.c.platform, b.c.queues, b.overlay = platform, queues, overlay
	if key := os.Getenv("RIOT_API_KEY"); key != "" && !b.ctl.off && changed {
		sy, err := newSyncerKey(b.c, b.rate, key)
		if err != nil {
			return err
		}
		sy.timelines = b.timelines
		b.ctl.set(sy)
		b.ctl.request()
	}
	return nil
}

func (b *settingsBackend) AddAccount(ctx context.Context, riotID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sy := b.ctl.get()
	if sy == nil {
		return "", errors.New("save a Riot API key first")
	}
	before, err := b.store.LoadState()
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, canon, err := sy.addAccount(cctx, riotID, logWriter{b.logger})
	switch {
	case errors.Is(err, riot.ErrNotFound):
		return "", fmt.Errorf("Riot has no account named %s. Check the spelling and the #TAG", strings.TrimSpace(riotID))
	case errors.Is(err, riot.ErrAuth):
		b.srv.SetStatus(func(s *web.Status) { s.KeyRejected = true })
		return "", errors.New("Riot rejected your API key. Paste a new one above (development keys expire after 24 hours)")
	case err != nil:
		return "", err
	}
	if tracks(before, canon) {
		return "", fmt.Errorf("%s is already tracked", canon)
	}
	b.srv.SetAccounts(webAccounts(st))
	if len(before.Accounts) == 0 {
		b.srv.SetStatus(func(s *web.Status) { s.FirstSync, s.Stage, s.LastError = true, "Waiting to start", "" })
	}
	b.ctl.request()
	return canon, nil
}

func (b *settingsBackend) RemoveAccount(ctx context.Context, puuid string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var gone string
	st, err := b.store.UpdateState(func(st *store.State) error {
		kept := st.Accounts[:0:0]
		for _, a := range st.Accounts {
			if a.PUUID == puuid {
				gone = a.RiotID
				continue
			}
			kept = append(kept, a)
		}
		if gone == "" {
			return errors.New("that account isn't tracked")
		}
		st.Accounts = kept
		return nil
	})
	if err != nil {
		return err
	}
	// A Riot ID in .env would be added straight back on the next sync.
	if strings.EqualFold(os.Getenv("DRAFTSMITH_RIOT_ID"), gone) || strings.EqualFold(b.c.riotID, gone) {
		if err := b.saveEnv(map[string]string{"DRAFTSMITH_RIOT_ID": ""}); err != nil {
			return err
		}
		b.c.riotID = ""
		if sy := b.ctl.get(); sy != nil {
			cp := *sy
			cp.riotID = ""
			b.ctl.set(&cp)
		}
	}
	if _, err := b.ctl.index(ctx, b.db, b.store, st); err != nil {
		return fmt.Errorf("removed, but reindexing failed: %w", err)
	}
	b.srv.SetAccounts(webAccounts(st))
	return nil
}

// saveEnv writes kv into .env (an empty value removes the line) and into
// this process's environment.
func (b *settingsBackend) saveEnv(kv map[string]string) error {
	if err := setDotEnv(b.envPath, kv); err != nil {
		return fmt.Errorf("saving %s: %w", b.envPath, err)
	}
	for k, v := range kv {
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	return nil
}

// setDotEnv sets KEY=VALUE lines in a .env file, keeping its other lines
// and comments, adding keys it doesn't have yet, and dropping keys set to
// "". The file is created if missing.
func setDotEnv(path string, kv map[string]string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	done := map[string]bool{}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(old))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		k, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
		k = strings.TrimSpace(k)
		if v, set := kv[k]; ok && set && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			if !done[k] && v != "" {
				fmt.Fprintf(&out, "%s=%s\n", k, v)
			}
			done[k] = true
			continue
		}
		out.WriteString(line + "\n")
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(kv)) {
		if !done[k] && kv[k] != "" {
			fmt.Fprintf(&out, "%s=%s\n", k, kv[k])
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// envFile is the .env serve reads and the settings page writes, as an
// absolute path (shown on the page).
func envFile() string {
	p, err := filepath.Abs(".env")
	if err != nil {
		return ".env"
	}
	return p
}
