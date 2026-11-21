package main

import (
	"context"
	_ "embed"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/appwin"
)

//go:embed winres/icon.ico
var appIcon []byte

// logOut is where serve logs: the console, or a log file once a
// double-clicked app has hidden its console.
var logOut io.Writer = os.Stdout

// appWindow shows url in Draftsmith's own window and returns when the
// window closes (or ctx ends).
func appWindow(ctx context.Context, url string, logger *log.Logger) error {
	return appwin.Run(ctx, appwin.Options{
		URL: url, Title: "Draftsmith", Width: 1280, Height: 860, Icon: appIcon, Log: logger,
	})
}

// isDraftsmith reports whether addr already serves Draftsmith's dashboard
// (another copy is running).
func isDraftsmith(addr string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/settings")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return strings.Contains(string(b), "· Draftsmith</title>")
}

// openLogFile sends serve's log to draftsmith.log in the data directory,
// starting it afresh when it passes 5 MB. Falls back to discarding it.
func openLogFile(dataDir string) io.Writer {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return io.Discard
	}
	path := filepath.Join(dataDir, "draftsmith.log")
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}
