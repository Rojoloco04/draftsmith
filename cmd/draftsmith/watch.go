package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/rojoloco04/draftsmith/internal/lcu"
	"github.com/rojoloco04/draftsmith/internal/watch"
)

// cmdWatch follows the local League client. For now it logs gameflow phase
// changes; champ select suggestions come next.
func cmdWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	dir := fs.String("league-dir", env("DRAFTSMITH_LEAGUE_DIR", ""), "League install directory (default: the standard install location)")
	poll := fs.Duration("poll", 2*time.Second, "how often to check the client's state")
	fs.Parse(args)

	dirs := lcu.DefaultDirs()
	if *dir != "" {
		dirs = []string{*dir}
	}
	w := &watch.Watcher{
		Discover: func(ctx context.Context) (lcu.Credentials, error) { return lcu.Discover(ctx, dirs) },
		Connect:  func(c lcu.Credentials) watch.Client { return lcu.New(c) },
		Log:      log.New(os.Stdout, "", log.LstdFlags),
		Poll:     *poll,
	}
	return w.Run(ctx)
}
