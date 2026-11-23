package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/liveclient"
	"github.com/rojoloco04/draftsmith/internal/meta"
	"github.com/rojoloco04/draftsmith/internal/opgg"
	"github.com/rojoloco04/draftsmith/internal/overlay"
	"github.com/rojoloco04/draftsmith/internal/riot"
	"github.com/rojoloco04/draftsmith/internal/store"
	"github.com/rojoloco04/draftsmith/internal/web"
)

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var c common
	c.register(fs)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address; use your Tailscale IP to reach it from other devices")
	every := fs.Duration("sync-every", 25*time.Minute, "how often to sync in the background")
	noSync := fs.Bool("no-sync", false, "serve the archive as is, without background sync")
	rate := fs.String("rate", "20/1s,100/2m", "rate limits, comma separated N/duration")
	noTimelines := fs.Bool("no-timelines", false, "skip timelines when syncing")
	noMeta := fs.Bool("no-meta", false, "don't fetch OP.GG meta data (tier list, champion analysis)")
	opggEndpoint := fs.String("opgg-endpoint", opgg.DefaultEndpoint, "OP.GG MCP endpoint")
	noWatch := fs.Bool("no-watch", false, "don't follow the League client (no champ select page, overlay or in-game build)")
	leagueDir := fs.String("league-dir", env("DRAFTSMITH_LEAGUE_DIR", ""), "League install directory (default: the standard install location)")
	minGames := fs.Int("min-games", 2, "champ select: suggest only champions with at least this many games in your lane")
	noOverlay := fs.Bool("no-overlay", strings.EqualFold(env("DRAFTSMITH_OVERLAY", "on"), "off"), "don't dock the champ select window beside the League client")
	open := fs.Bool("open", false, "open the dashboard in your browser once it's up")
	app := fs.Bool("app", false, "show the dashboard in its own window (closing it stops serve); the browser if that fails")
	fs.Parse(args)
	watchClient := !*noWatch
	showOverlay := watchClient && !*noOverlay

	if err := checkListenAddr(*addr); err != nil {
		return err
	}
	logger := log.New(logOut, "", log.LstdFlags)
	ctx, quit := context.WithCancel(ctx)
	defer quit()

	s, err := store.Open(c.data)
	if err != nil {
		return err
	}
	st, err := s.LoadState()
	if err != nil {
		return err
	}

	ctl := newSyncControl()
	disabled := ""
	switch {
	case *noSync:
		disabled, ctl.off = "started with --no-sync", true
	case *every <= 0:
		disabled, ctl.off = "--sync-every is 0", true
	default:
		sy, err := newSyncer(c, *rate)
		switch {
		case err == nil:
			sy.timelines = !*noTimelines
			ctl.set(sy)
		case os.Getenv("RIOT_API_KEY") == "":
			disabled = "no Riot API key yet; add one in Settings"
		default:
			disabled = err.Error()
			logger.Printf("background sync off: %v", err)
		}
	}
	if len(st.Accounts) == 0 && ctl.off {
		return errors.New("no archive yet; run `draftsmith sync --riot-id Name#TAG` first")
	}

	d, err := db.OpenDir(ctx, c.data)
	if err != nil {
		return err
	}
	defer d.Close()
	if d.Rebuilt {
		logger.Printf("building the index at %s from the archive", d.Path())
	}
	if res, err := d.Index(ctx, s, st.PUUIDs()); err != nil {
		return fmt.Errorf("indexing: %w", err)
	} else if res.Added+res.Updated > 0 {
		logger.Printf("indexed %d new games (%d updated)", res.Added, res.Updated)
	}

	srv, err := web.New(d, webAccounts(st), logger)
	if err != nil {
		return err
	}
	srv.SetStatus(func(s *web.Status) {
		s.LastSync = st.LastSync()
		s.SyncDisabled = disabled
	})
	srv.SetTheme(os.Getenv("DRAFTSMITH_THEME"))
	srv.SetSettings(&settingsBackend{
		envPath: envFile(), c: c, rate: *rate, timelines: !*noTimelines, overlay: !*noOverlay,
		store: s, db: d, srv: srv, ctl: ctl, logger: logger,
	})
	if len(st.Accounts) > 0 {
		ctl.markReady()
	}
	var m *meta.Service
	if !*noMeta {
		m = meta.New(d, *opggEndpoint)
		srv.SetMeta(m)
		// Warm the cache so the first visit to the tier list doesn't wait.
		go func() {
			if tl, err := m.TierList(ctx); err != nil {
				logger.Printf("OP.GG tier list: %v", err)
			} else if tl.Stale() {
				logger.Printf("OP.GG tier list: serving the copy from %s: %v", tl.FetchedAt.Format(time.DateTime), tl.RefreshErr)
			}
		}()
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		if *app && isDraftsmith(*addr) {
			// Already running (a second double-click): open another
			// window on it rather than failing.
			return appWindow(ctx, "http://"+*addr, logger)
		}
		return err
	}
	if watchClient {
		srv.WatchStarted("")
		var ov *overlay.Overlay
		if showOverlay {
			ov, err = overlay.Start(ctx, overlay.Options{URL: "http://" + ln.Addr().String() + "/overlay", Log: logger})
			if err != nil {
				logger.Printf("overlay off: %v", err)
			}
		}
		dirs := lcu.DefaultDirs()
		if *leagueDir != "" {
			dirs = []string{*leagueDir}
		}
		// Import puts a rune page in the client; it runs only when I press
		// the button on a rune page.
		srv.SetRuneImporter(func(ctx context.Context, page lcu.RunePage) error {
			creds, err := lcu.Discover(ctx, dirs)
			if err != nil {
				return err
			}
			return lcu.New(creds).ImportRunes(ctx, page)
		})
		go func() {
			// Suggestions come from the archive; with none yet, wait for
			// the first sync to bring some.
			select {
			case <-ctl.ready:
			default:
				srv.WatchStarted("waiting for your first sync to finish")
				select {
				case <-ctl.ready:
				case <-ctx.Done():
					return
				}
				srv.WatchStarted("")
			}
			o := watchOpts{leagueDir: *leagueDir, minGames: *minGames, suggest: true, onBoard: srv.WatchBoard}
			w, note := newWatcher(ctx, d, m, o, logger)
			if note != "" {
				logger.Printf("champ select suggestions off: %s", note)
				srv.WatchStarted(note)
			}
			// During a game, read my champion's level, skill points and
			// items from the game itself for the in-game build card.
			game := liveclient.New()
			var stopGame context.CancelFunc
			onPhase := w.OnPhase
			w.OnPhase = func(p string) {
				if onPhase != nil {
					onPhase(p)
				}
				srv.WatchPhase(p)
				switch {
				case inGame(p) && stopGame == nil:
					var gctx context.Context
					gctx, stopGame = context.WithCancel(ctx)
					go game.Follow(gctx, 2*time.Second, srv.WatchLive)
				case !inGame(p) && stopGame != nil:
					stopGame()
					stopGame = nil
				}
				if ov != nil {
					ov.SetActive(p == lcu.PhaseChampSelect)
				}
			}
			if err := w.Run(ctx); err != nil {
				logger.Printf("watch: %v", err)
			}
		}()
	}

	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          logger,
		// Requests end with serve, so champ select streams don't hold up
		// shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	url := "http://" + ln.Addr().String()
	if len(st.Accounts) > 0 {
		logger.Printf("dashboard for %s at %s", st.RiotIDs(), url)
	} else {
		logger.Printf("dashboard at %s; add your account in Settings", url)
	}

	if !ctl.off {
		ctl.progress = func(stage string, done, total int) {
			srv.SetStatus(func(s *web.Status) {
				if s.Stage != stage || done == 0 {
					s.StageStart = time.Now()
				}
				s.Stage, s.Done, s.Total = stage, done, total
			})
		}
		if sy := ctl.get(); sy != nil {
			ctl.set(sy) // picks up progress
			srv.SetRefresh(ctl.request)
		}
		go syncLoop(ctx, ctl, s, d, srv, *every, logger)
	}

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	switch {
	case *app:
		go func() {
			if err := appWindow(ctx, url, logger); err != nil {
				logger.Printf("app window: %v; opening the browser instead", err)
				if err := openBrowser(url); err != nil {
					logger.Printf("couldn't open a browser: %v", err)
				}
				return
			}
			quit() // the window closed: so does Draftsmith
		}()
	case *open:
		if err := openBrowser(url); err != nil {
			logger.Printf("couldn't open a browser: %v", err)
		}
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Printf("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hs.Shutdown(shutCtx)
}

// syncLoop syncs right away, then every interval and whenever ctl.now
// receives (the Refresh button, the settings page), reindexing after each
// run. A sync from now restarts the interval. While there's no syncer (no
// key yet) or nothing to sync, it waits. Errors are logged and shown in
// the dashboard; the next run tries again.
func syncLoop(ctx context.Context, ctl *syncControl, s *store.Store, d *db.DB, srv *web.Server, every time.Duration, logger *log.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if sy := ctl.get(); sy != nil {
			syncOnce(ctx, ctl, sy, s, d, srv, logger)
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ctl.now:
			logger.Printf("sync requested from the dashboard")
			t.Reset(every)
		}
	}
}

// syncOnce runs one sync and reindex, keeping the dashboard's status, its
// accounts and the first-sync page up to date.
func syncOnce(ctx context.Context, ctl *syncControl, sy *syncer, s *store.Store, d *db.DB, srv *web.Server, logger *log.Logger) {
	before, err := s.LoadState()
	if err != nil {
		logger.Printf("sync: %v", err)
		return
	}
	if len(before.Accounts) == 0 && sy.riotID == "" {
		return // nothing to sync until an account is added
	}
	first := true
	for _, a := range before.Accounts {
		if !a.LastSync.IsZero() {
			first = false
		}
	}
	srv.SetStatus(func(s *web.Status) {
		s.Syncing, s.FirstSync = true, first
		s.Stage, s.Done, s.Total = "Starting", 0, 0
	})
	st, err := sy.run(ctx, logWriter{logger})
	if ctx.Err() != nil {
		return
	}
	added := 0
	if len(st.Accounts) > 0 {
		res, ierr := ctl.index(ctx, d, s, st)
		if ierr != nil {
			err = errors.Join(err, fmt.Errorf("indexing: %w", ierr))
		} else if res.Added+res.Updated > 0 {
			added = res.Added
			logger.Printf("indexed %d new games (%d updated)", res.Added, res.Updated)
		}
		srv.SetAccounts(webAccounts(st))
		if ierr == nil {
			ctl.markReady()
		}
	}
	if err != nil {
		logger.Printf("sync failed: %v", err)
	}
	rejected := errors.Is(err, riot.ErrAuth)
	srv.SetStatus(func(s *web.Status) {
		s.Syncing, s.Stage, s.Done, s.Total = false, "", 0, 0
		// A first sync that failed before bringing any games keeps its
		// page up, with the error and a way to settings.
		s.FirstSync = first && err != nil && added == 0
		s.LastSync = time.Now()
		s.KeyRejected = rejected
		s.LastError = ""
		switch {
		case rejected:
			s.LastError = "Riot rejected the API key (development keys expire after 24 hours). Paste a new one in Settings."
		case err != nil:
			s.LastError = err.Error()
		}
	})
}

// openBrowser opens url in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// webAccounts lists the tracked accounts for the dashboard.
func webAccounts(st store.State) []web.Account {
	out := make([]web.Account, len(st.Accounts))
	for i, a := range st.Accounts {
		out[i] = web.Account{PUUID: a.PUUID, RiotID: a.RiotID}
	}
	return out
}

// checkListenAddr refuses wildcard addresses. There is no auth, so the
// dashboard must only listen on loopback or a private interface such as
// the Tailscale IP.
func checkListenAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("--addr %q listens on every interface; give a host, e.g. 127.0.0.1:8080 or your Tailscale IP", addr)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return fmt.Errorf("--addr %q listens on every interface; the dashboard has no auth, so bind 127.0.0.1 or your Tailscale IP", addr)
	}
	return nil
}

// logWriter turns sync progress lines into timestamped log lines.
type logWriter struct{ l *log.Logger }

func (w logWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if s := strings.TrimSpace(string(line)); s != "" {
			w.l.Print(s)
		}
	}
	return len(p), nil
}
