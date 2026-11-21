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
	noOverlay := fs.Bool("no-overlay", false, "don't dock the champ select window beside the League client")
	open := fs.Bool("open", false, "open the dashboard in your browser once it's up")
	fs.Parse(args)
	watchClient := !*noWatch
	showOverlay := watchClient && !*noOverlay

	if err := checkListenAddr(*addr); err != nil {
		return err
	}
	logger := log.New(os.Stdout, "", log.LstdFlags)

	s, err := store.Open(c.data)
	if err != nil {
		return err
	}
	st, err := s.LoadState()
	if err != nil {
		return err
	}

	var sy *syncer
	disabled := ""
	switch {
	case *noSync:
		disabled = "started with --no-sync"
	case *every <= 0:
		disabled = "--sync-every is 0"
	default:
		sy, err = newSyncer(c, *rate)
		if err != nil {
			if len(st.Accounts) == 0 {
				return err
			}
			disabled = err.Error()
			logger.Printf("background sync off: %v", err)
		} else {
			sy.timelines = !*noTimelines
		}
	}

	// Without an archive there is nothing to show, so sync once up front.
	if len(st.Accounts) == 0 {
		if sy == nil {
			return errors.New("no archive yet; run `draftsmith sync --riot-id Name#TAG` first")
		}
		logger.Printf("no archive yet, running a first sync")
		if st, err = sy.run(ctx, logWriter{logger}); err != nil {
			return err
		}
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
	logger.Printf("dashboard for %s at http://%s", st.RiotIDs(), ln.Addr())
	if *open {
		if err := openBrowser("http://" + ln.Addr().String()); err != nil {
			logger.Printf("couldn't open a browser: %v", err)
		}
	}

	if sy != nil {
		// The Refresh button asks for a sync now. One request can wait;
		// more while it does are dropped.
		now := make(chan struct{}, 1)
		srv.SetRefresh(func() bool {
			select {
			case now <- struct{}{}:
				return true
			default:
				return false
			}
		})
		go syncLoop(ctx, sy, d, srv, *every, now, logger)
	}

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
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

// syncLoop syncs right away, then every interval and whenever now
// receives (the Refresh button), reindexing after each run. A sync from
// now restarts the interval. Errors are logged and shown in the dashboard
// footer; the next run tries again.
func syncLoop(ctx context.Context, sy *syncer, d *db.DB, srv *web.Server, every time.Duration, now <-chan struct{}, logger *log.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		srv.SetStatus(func(s *web.Status) { s.Syncing = true })
		st, err := sy.run(ctx, logWriter{logger})
		if ctx.Err() != nil {
			return
		}
		if len(st.Accounts) > 0 {
			res, ierr := d.Index(ctx, sy.store, st.PUUIDs())
			if ierr != nil {
				err = errors.Join(err, fmt.Errorf("indexing: %w", ierr))
			} else if res.Added+res.Updated > 0 {
				logger.Printf("indexed %d new games (%d updated)", res.Added, res.Updated)
			}
		}
		if err != nil {
			logger.Printf("sync failed: %v", err)
		}
		srv.SetStatus(func(s *web.Status) {
			s.Syncing = false
			s.LastSync = time.Now()
			s.LastError = ""
			if err != nil {
				s.LastError = err.Error()
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-now:
			logger.Printf("sync requested from the dashboard")
			t.Reset(every)
		}
	}
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
