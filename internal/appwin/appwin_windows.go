//go:build windows

package appwin

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSendMessageW                 = user32.NewProc("SendMessageW")
	procShowWindow                   = user32.NewProc("ShowWindow")
	procSetForegroundWindow          = user32.NewProc("SetForegroundWindow")
	procMessageBoxW                  = user32.NewProc("MessageBoxW")
	procCreateIconFromResourceEx     = user32.NewProc("CreateIconFromResourceEx")
	procGetDpiForSystem              = user32.NewProc("GetDpiForSystem")
	procSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	procCoInitializeEx               = ole32.NewProc("CoInitializeEx")
	procGetConsoleWindow             = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList        = kernel32.NewProc("GetConsoleProcessList")
)

const (
	wmSetIcon               = 0x0080
	iconSmall               = 0
	iconBig                 = 1
	swHide                  = 0
	swShow                  = 5
	mbIconError             = 0x10
	dpiPerMonitorAwareV2    = -4
	coinitApartmentThreaded = 2
)

// Run opens the window, loads o.URL in it, and blocks until the window is
// closed or ctx ends. It fails at once if the WebView2 runtime is missing.
func Run(ctx context.Context, o Options) error {
	logger := o.Log
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if o.DataDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		o.DataDir = filepath.Join(dir, "draftsmith", "webview2-app")
	}
	// A window belongs to the thread that made it, and WebView2 needs that
	// thread to be a COM single-threaded apartment.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded); int32(r) < 0 {
		return fmt.Errorf("app window: CoInitializeEx: %#x", uint32(r))
	}
	// Sharp text on scaled displays.
	procSetThreadDpiAwarenessContext.Call(negative(dpiPerMonitorAwareV2))
	scale := func(n int) uint {
		dpi, _, _ := procGetDpiForSystem.Call()
		if dpi == 0 {
			dpi = 96
		}
		return uint(n) * uint(dpi) / 96
	}

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  o.DataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: o.Title, Width: scale(o.Width), Height: scale(o.Height), Center: true,
		},
	})
	if wv == nil {
		return errors.New("couldn't start WebView2 (is the Microsoft Edge WebView2 Runtime installed?)")
	}
	hwnd := uintptr(wv.Window())
	if len(o.Icon) > 0 {
		if small := iconFrom(o.Icon, 32); small != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
		}
		if big := iconFrom(o.Icon, 256); big != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
		}
	}
	wv.Navigate(o.URL)
	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)

	stop := context.AfterFunc(ctx, func() { wv.Dispatch(wv.Terminate) })
	wv.Run()
	stop()
	wv.Destroy()
	logger.Printf("app window closed")
	return nil
}

// negative passes a negative Win32 constant as a uintptr argument.
func negative(n int) uintptr { return uintptr(n) }

// iconFrom makes an icon from the .ico entry closest to size (PNG or
// BMP data both work with CreateIconFromResourceEx). 0 if it can't.
func iconFrom(ico []byte, size int) uintptr {
	if len(ico) < 6 {
		return 0
	}
	n := int(binary.LittleEndian.Uint16(ico[4:6]))
	best, bestDiff := -1, 1<<30
	for i := 0; i < n; i++ {
		e := 6 + 16*i
		if e+16 > len(ico) {
			return 0
		}
		w := int(ico[e])
		if w == 0 {
			w = 256
		}
		d := w - size
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			best, bestDiff = e, d
		}
	}
	if best < 0 {
		return 0
	}
	sz := binary.LittleEndian.Uint32(ico[best+8:])
	off := binary.LittleEndian.Uint32(ico[best+12:])
	if uint64(off)+uint64(sz) > uint64(len(ico)) || sz == 0 {
		return 0
	}
	data := ico[off : off+sz]
	h, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&data[0])), uintptr(sz), 1, 0x00030000, uintptr(size), uintptr(size), 0)
	return h
}

// Alert shows a message box: how the app reports a startup error once its
// console is hidden.
func Alert(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError)
}

// HideOwnConsole hides the console window if this process is the only one
// using it, which is what a double-click gives a console program. Started
// from a terminal, the console is shared and stays. It reports whether
// the console was hidden.
func HideOwnConsole() bool {
	h, _, _ := procGetConsoleWindow.Call()
	if h == 0 {
		return false
	}
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 && os.Getenv(ownConsoleEnv) == "" {
		return false
	}
	procShowWindow.Call(h, swHide)
	return true
}

// ownConsoleEnv tells a rebuilt child (sharing its parent's console, so
// two processes on it) that the console was the parent's own.
const ownConsoleEnv = "DRAFTSMITH_OWN_CONSOLE"

// MarkOwnConsole records, before the exe restarts itself after a rebuild,
// whether the console is this process's own, so the new one can hide it.
func MarkOwnConsole() {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n == 1 {
		os.Setenv(ownConsoleEnv, "1")
	}
}
