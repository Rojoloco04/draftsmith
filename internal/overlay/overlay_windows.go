//go:build windows

package overlay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	ole32  = windows.NewLazySystemDLL("ole32.dll")

	procSetWindowLongPtrW            = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW            = user32.NewProc("GetWindowLongPtrW")
	procSetWindowPos                 = user32.NewProc("SetWindowPos")
	procShowWindow                   = user32.NewProc("ShowWindow")
	procGetWindowRect                = user32.NewProc("GetWindowRect")
	procIsIconic                     = user32.NewProc("IsIconic")
	procMonitorFromWindow            = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW              = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow              = user32.NewProc("GetDpiForWindow")
	procSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	procCoInitializeEx               = ole32.NewProc("CoInitializeEx")
)

const (
	gwlStyle   = -16
	gwlExStyle = -20

	wsPopup        = 0x80000000
	wsClipChildren = 0x02000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080 // no taskbar button or Alt+Tab entry

	hwndTopmost   = -1
	hwndNoTopmost = -2

	swHide = 0

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040

	monitorDefaultToNearest = 2
	dpiPerMonitorAwareV2    = -4
	coinitApartmentThreaded = 2

	// clientExe owns the League client's window.
	clientExe = "LeagueClientUx.exe"
	// follow is how often the overlay checks the client's window while
	// champ select is on.
	follow = 250 * time.Millisecond
)

// i passes a possibly negative Win32 constant as a uintptr argument.
func i(n int) uintptr { return uintptr(n) }

// Overlay is a borderless WebView2 window showing Options.URL beside the
// League client while active. It never takes focus by itself: it's shown
// without activating, kept above the client while the client (or the
// overlay) is in front, and dropped behind whatever else is.
type Overlay struct {
	wv     webview2.WebView
	hwnd   uintptr
	log    *log.Logger
	closed atomic.Bool // the window is gone (closed, or serve is stopping)

	mu        sync.Mutex
	active    bool // in champ select
	dismissed bool // hidden from the page until the next champ select

	// The rest belongs to the window's thread.
	client windows.HWND // the client's window, as last found
	shown  bool
	top    bool // topmost
	at     Rect
}

// Start opens the (hidden) overlay window on its own thread and loads
// o.URL in it. The window closes when ctx ends. It fails if the WebView2
// runtime isn't installed.
func Start(ctx context.Context, o Options) (*Overlay, error) {
	ov := &Overlay{log: o.Log}
	if ov.log == nil {
		ov.log = log.New(io.Discard, "", 0)
	}
	if o.DataDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		o.DataDir = filepath.Join(dir, "draftsmith", "webview2")
	}
	ready := make(chan error, 1)
	go ov.run(ctx, o, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	go ov.tick(ctx)
	return ov, nil
}

// run owns the window: it creates it, then pumps its messages until it
// closes. A window belongs to the thread that made it, and WebView2 needs
// that thread to be a COM single-threaded apartment, so the goroutine
// stays locked to one fresh thread (which exits with it).
func (ov *Overlay) run(ctx context.Context, o Options, ready chan<- error) {
	runtime.LockOSThread()
	defer ov.closed.Store(true)
	if r, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded); int32(r) < 0 {
		ready <- fmt.Errorf("overlay: CoInitializeEx: %#x", uint32(r))
		return
	}
	// Physical pixels everywhere, so the overlay lines up with the client
	// and stays sharp on scaled displays. Older Windows just scales it.
	procSetThreadDpiAwarenessContext.Call(i(dpiPerMonitorAwareV2))

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:      o.DataDir,
		WindowOptions: webview2.WindowOptions{Title: "Draftsmith overlay", Width: Width, Height: 600},
	})
	if wv == nil {
		ready <- errors.New("overlay: couldn't start WebView2 (is the Microsoft Edge WebView2 Runtime installed?)")
		return
	}
	ov.wv, ov.hwnd = wv, uintptr(wv.Window())
	// The library shows a normal framed window; make it a hidden popup
	// with no taskbar button until champ select.
	procShowWindow.Call(ov.hwnd, swHide)
	procSetWindowLongPtrW.Call(ov.hwnd, i(gwlStyle), wsPopup|wsClipChildren)
	procSetWindowLongPtrW.Call(ov.hwnd, i(gwlExStyle), wsExToolWindow)
	procSetWindowPos.Call(ov.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)

	if err := wv.Bind("draftsmithHide", ov.dismiss); err != nil {
		ov.log.Printf("overlay: %v", err)
	}
	wv.Navigate(o.URL)
	ready <- nil

	stop := context.AfterFunc(ctx, wv.Destroy)
	defer stop()
	wv.Run()
	if ctx.Err() == nil {
		ov.log.Printf("overlay window closed; restart serve to get it back")
	}
}

// SetActive shows the overlay (on) or hides it. Turning it on also undoes
// the page's hide button, so each champ select starts with it showing.
func (ov *Overlay) SetActive(on bool) {
	ov.mu.Lock()
	if on && !ov.active {
		ov.dismissed = false
	}
	ov.active = on
	ov.mu.Unlock()
	ov.dispatch(ov.follow)
}

// dismiss is the page's hide button (window.draftsmithHide). Bound calls
// run on the window's thread.
func (ov *Overlay) dismiss() {
	ov.mu.Lock()
	ov.dismissed = true
	ov.mu.Unlock()
	ov.follow()
}

func (ov *Overlay) dispatch(f func()) {
	if !ov.closed.Load() {
		ov.wv.Dispatch(f)
	}
}

// tick keeps the overlay docked while active: the client can move,
// minimize, or lose the foreground at any time.
func (ov *Overlay) tick(ctx context.Context) {
	t := time.NewTicker(follow)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if ov.closed.Load() {
			return
		}
		ov.mu.Lock()
		on := ov.active && !ov.dismissed
		ov.mu.Unlock()
		if on {
			ov.dispatch(ov.follow)
		}
	}
}

// follow shows, moves or hides the window to match the client. It runs on
// the window's thread.
func (ov *Overlay) follow() {
	ov.mu.Lock()
	want := ov.active && !ov.dismissed
	ov.mu.Unlock()
	var client windows.HWND
	if want {
		client = ov.findClient()
		want = client != 0 && !isIconic(client)
	}
	if !want {
		if ov.shown {
			procShowWindow.Call(ov.hwnd, swHide)
			ov.shown, ov.top = false, false
		}
		return
	}

	dpi, _, _ := procGetDpiForWindow.Call(uintptr(client))
	if dpi == 0 {
		dpi = 96
	}
	r := Dock(windowRect(client), workArea(client), int32(Width*dpi/96))

	fg := windows.GetForegroundWindow()
	front := fg == client || uintptr(fg) == ov.hwnd || sameProcess(fg, client)
	moved := !ov.shown || r != ov.at
	switch {
	case front && (!ov.top || moved):
		setPos(ov.hwnd, i(hwndTopmost), r, swpNoActivate|swpShowWindow)
		ov.top = true
	case !front && (ov.top || !ov.shown):
		// Behind whatever is in front, still above the client.
		setPos(ov.hwnd, i(hwndNoTopmost), r, swpNoActivate|swpShowWindow)
		if fg != 0 && !isTopmost(fg) {
			setPos(ov.hwnd, uintptr(fg), Rect{}, swpNoActivate|swpNoMove|swpNoSize)
		}
		ov.top = false
	case moved:
		setPos(ov.hwnd, 0, r, swpNoActivate|swpNoZOrder|swpShowWindow)
	}
	ov.shown, ov.at = true, r
}

// findClient returns the League client's main window, or 0 if it isn't
// showing (closed, or hidden in the tray during a game).
func (ov *Overlay) findClient() windows.HWND {
	if c := ov.client; c != 0 && windows.IsWindow(c) && windows.IsWindowVisible(c) {
		return c
	}
	ov.client = scanForClient()
	return ov.client
}

// scan is scanForClient's state. EnumWindows calls back on the calling
// thread, and only the window's thread scans.
var scan struct {
	cb     uintptr
	exe    map[uint32]bool
	best   windows.HWND
	bestSz int64
}

// scanForClient finds the largest visible top-level window owned by the
// client's process. It goes by process, not window class or title, so it
// can't pick up the Riot Client or a browser tab named League of Legends.
func scanForClient() windows.HWND {
	if scan.cb == 0 {
		scan.cb = windows.NewCallback(func(hwnd, _ uintptr) uintptr {
			h := windows.HWND(hwnd)
			if !windows.IsWindowVisible(h) {
				return 1
			}
			var pid uint32
			if _, err := windows.GetWindowThreadProcessId(h, &pid); err != nil || pid == 0 {
				return 1
			}
			is, seen := scan.exe[pid]
			if !seen {
				is = isClientProcess(pid)
				scan.exe[pid] = is
			}
			if is {
				r := windowRect(h)
				if sz := int64(r.Width()) * int64(r.Height()); sz > scan.bestSz {
					scan.best, scan.bestSz = h, sz
				}
			}
			return 1
		})
	}
	scan.exe, scan.best, scan.bestSz = map[uint32]bool{}, 0, 0
	windows.EnumWindows(scan.cb, nil)
	return scan.best
}

func isClientProcess(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(buf[:n])), clientExe)
}

// sameProcess reports whether a and b belong to one process (the client's
// own dialogs count as the client being in front).
func sameProcess(a, b windows.HWND) bool {
	if a == 0 || b == 0 {
		return false
	}
	var pa, pb uint32
	windows.GetWindowThreadProcessId(a, &pa)
	windows.GetWindowThreadProcessId(b, &pb)
	return pa != 0 && pa == pb
}

func windowRect(h windows.HWND) Rect {
	var r Rect
	procGetWindowRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
	return r
}

// workArea is the usable part (less the taskbar) of h's monitor.
func workArea(h windows.HWND) Rect {
	var mi struct {
		size          uint32
		monitor, work Rect
		flags         uint32
	}
	mi.size = uint32(unsafe.Sizeof(mi))
	m, _, _ := procMonitorFromWindow.Call(uintptr(h), monitorDefaultToNearest)
	if r, _, _ := procGetMonitorInfoW.Call(m, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return windowRect(h)
	}
	return mi.work
}

func isIconic(h windows.HWND) bool {
	r, _, _ := procIsIconic.Call(uintptr(h))
	return r != 0
}

func isTopmost(h windows.HWND) bool {
	ex, _, _ := procGetWindowLongPtrW.Call(uintptr(h), i(gwlExStyle))
	return ex&wsExTopmost != 0
}

func setPos(hwnd, after uintptr, r Rect, flags uintptr) {
	procSetWindowPos.Call(hwnd, after, i(int(r.Left)), i(int(r.Top)), i(int(r.Width())), i(int(r.Height())), flags)
}
