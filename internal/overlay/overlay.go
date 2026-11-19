// Package overlay shows the dashboard's champ select board in a borderless
// window docked beside the League client, during champ select only.
//
// It is a separate window that only looks at where the client is. It
// never touches the client's own UI, input or process (client plugins and
// injection are outside what Riot allows). Windows only.
package overlay

import (
	"errors"
	"log"
)

// ErrUnsupported is returned by Start on systems other than Windows.
var ErrUnsupported = errors.New("the overlay needs Windows")

// Width is the overlay's width in logical pixels (scaled by the client
// monitor's DPI). A 1280x720 client centered on a 1080p screen leaves
// exactly this much on each side.
const Width = 320

// Options configure the overlay window.
type Options struct {
	// URL is the page to show, normally the dashboard's /overlay.
	URL string
	// DataDir holds WebView2's profile (cache, cookies).
	DataDir string
	Log     *log.Logger
}

// Rect is a screen rectangle in physical pixels, right and bottom
// exclusive, like Win32's RECT.
type Rect struct{ Left, Top, Right, Bottom int32 }

func (r Rect) Width() int32  { return r.Right - r.Left }
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// Dock places a window width pixels wide beside the client: to its right
// if the monitor's work area has room, else to its left, else over the
// client's right edge (where the client shows the enemy team, which the
// overlay repeats). It is as tall as the client.
func Dock(client, work Rect, width int32) Rect {
	r := Rect{Top: client.Top, Bottom: client.Bottom}
	switch {
	case client.Right+width <= work.Right:
		r.Left = client.Right
	case client.Left-width >= work.Left:
		r.Left = client.Left - width
	default:
		r.Left = client.Right - width
	}
	r.Right = r.Left + width
	return r
}
