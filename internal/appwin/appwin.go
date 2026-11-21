// Package appwin shows the dashboard as a desktop app: its own window
// with a WebView2 inside, instead of a tab in the browser. The dashboard
// server still runs on localhost; the window is just its only viewer.
// Windows only; elsewhere Run returns ErrUnsupported and serve falls back
// to the browser.
package appwin

import (
	"errors"
	"log"
)

// ErrUnsupported is returned by Run on systems other than Windows.
var ErrUnsupported = errors.New("the app window needs Windows")

// Options configure the window.
type Options struct {
	URL   string
	Title string
	// Width and Height are the starting size in logical pixels (scaled by
	// the monitor's DPI).
	Width, Height int
	// Icon is an .ico file (PNG entries are fine) for the title bar and
	// taskbar. Optional.
	Icon []byte
	// DataDir holds WebView2's profile (cache, cookies).
	DataDir string
	Log     *log.Logger
}
