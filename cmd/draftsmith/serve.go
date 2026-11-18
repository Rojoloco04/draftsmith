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
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/db"
	"github.com/rojoloco04/draftsmith/internal/meta"
	"github.com/rojoloco04/draftsmith/internal/opgg"
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
	fs.Parse(args)

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
			if st.PUUID == "" {
				return err
			}
			disabled = err.Error()
			logger.Printf("background sync off: %v", err)
		} else {
			sy.timelines = !*noTimelines
		}
	}

	// Without an archive there is nothing to show, so sync once up front.
	if st.PUUID == "" {
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
	if res, err := d.Index(ctx, s, st.PUUID); err != nil {
		return fmt.Errorf("indexing: %w", err)
	} else if res.Added+res.Updated > 0 {
		logger.Printf("indexed %d new games (%d updated)", res.Added, res.Updated)
	}

	srv, err := web.New(d, st.RiotID, logger)
	if err != nil {
		return err
	}
	srv.SetStatus(func(s *web.Status) {
		s.LastSync = st.LastSync
		s.SyncDisabled = disabled
	})
	if !*noMeta {
		m := meta.New(d, *opggEndpoint)
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
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          logger,
	}
	logger.Printf("dashboard for %s at http://%s", st.RiotID, ln.Addr())

	if sy != nil {
		go syncLoop(ctx, sy, d, srv, *every, logger)
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

// syncLoop syncs right away and then every interval, reindexing after each
// run. Errors are logged and shown in the dashboard footer; the next tick
// tries again.
func syncLoop(ctx context.Context, sy *syncer, d *db.DB, srv *web.Server, every time.Duration, logger *log.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		srv.SetStatus(func(s *web.Status) { s.Syncing = true })
		st, err := sy.run(ctx, logWriter{logger})
		if ctx.Err() != nil {
			return
		}
		if st.PUUID != "" {
			res, ierr := d.Index(ctx, sy.store, st.PUUID)
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
		}
	}
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
