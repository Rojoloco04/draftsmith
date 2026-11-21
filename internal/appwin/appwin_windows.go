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
	"golang.org/x/sys/windows/registry"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	ole32  = windows.NewLazySystemDLL("ole32.dll")
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procSendMessageW                 = user32.NewProc("SendMessageW")
	procShowWindow                   = user32.NewProc("ShowWindow")
	procSetForegroundWindow          = user32.NewProc("SetForegroundWindow")
	procMessageBoxW                  = user32.NewProc("MessageBoxW")
	procCreateIconFromResourceEx     = user32.NewProc("CreateIconFromResourceEx")
	procGetDpiForSystem              = user32.NewProc("GetDpiForSystem")
	procSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	procCoInitializeEx               = ole32.NewProc("CoInitializeEx")
	procDwmSetWindowAttribute        = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wmSetIcon               = 0x0080
	iconSmall               = 0
	iconBig                 = 1
	swShow                  = 5
	mbIconError             = 0x10
	dpiPerMonitorAwareV2    = -4
	coinitApartmentThreaded = 2

	dwmwaUseImmersiveDarkMode = 20
	dwmwaBorderColor          = 34 // Windows 11
	dwmwaCaptionColor         = 35 // Windows 11
	dwmwaTextColor            = 36 // Windows 11
)

// titleBar is the window frame's colors for one theme, matching the top
// of the dashboard's header (--header-a, --heading, --gold-line in
// style.css), so the title bar reads as part of the app.
type titleBar struct{ caption, text, border uint32 }

var (
	darkBar  = titleBar{caption: rgb(0x0b, 0x13, 0x22), text: rgb(0xf0, 0xdc, 0xa6), border: rgb(0x78, 0x5a, 0x28)}
	lightBar = titleBar{caption: rgb(0xfb, 0xf8, 0xf1), text: rgb(0x6e, 0x4f, 0x17), border: rgb(0xc9, 0xa8, 0x65)}
)

// rgb is a Win32 COLORREF.
func rgb(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

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
	dark := o.Theme == "dark" || (o.Theme != "light" && systemDark())
	// What WebView2 paints before the page arrives: the theme's
	// background rather than a white flash.
	if dark {
		os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "FF060B14")
	} else {
		os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "FFF1EBDD")
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
	setTitleBar(hwnd, dark)
	if len(o.Icon) > 0 {
		if small := iconFrom(o.Icon, 32); small != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
		}
		if big := iconFrom(o.Icon, 256); big != 0 {
			procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
		}
	}
	// The page reports the theme it shows (app.js), on load and whenever
	// it changes, so the title bar follows a theme picked in settings.
	if err := wv.Bind("draftsmithTheme", func(dark bool) { setTitleBar(hwnd, dark) }); err != nil {
		logger.Printf("app window: %v", err)
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

// setTitleBar colors the window frame for a theme. Windows 10 only takes
// the dark/light switch; Windows 11 takes the colors too.
func setTitleBar(hwnd uintptr, dark bool) {
	on := uint32(0)
	bar := lightBar
	if dark {
		on, bar = 1, darkBar
	}
	dwmSet(hwnd, dwmwaUseImmersiveDarkMode, on)
	dwmSet(hwnd, dwmwaCaptionColor, bar.caption)
	dwmSet(hwnd, dwmwaTextColor, bar.text)
	dwmSet(hwnd, dwmwaBorderColor, bar.border)
}

func dwmSet(hwnd uintptr, attr uintptr, v uint32) {
	if procDwmSetWindowAttribute.Find() != nil {
		return
	}
	procDwmSetWindowAttribute.Call(hwnd, attr, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
}

// systemDark reports whether Windows is set to dark mode for apps.
func systemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
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

// Alert shows a message box: how the app reports a startup error when it
// runs in the background with no console.
func Alert(title, msg string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(msg)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError)
}
