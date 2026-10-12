//go:build windows && !legacy

package main

import (
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
)

var (
	procShowWindow        = user32.NewProc("ShowWindow")
	procIsIconic          = user32.NewProc("IsIconic")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW   = user32.NewProc("CallWindowProcW")
	procGetWindowPlace    = user32.NewProc("GetWindowPlacement")
	procMonitorFromRect   = user32.NewProc("MonitorFromRect")
	procSendMessageW      = user32.NewProc("SendMessageW")
	procGetDpiForWindow   = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem   = user32.NewProc("GetDpiForSystem")
	dwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttr  = dwmapi.NewProc("DwmSetWindowAttribute")
)

type rect struct{ L, T, R, B int32 }

type windowPlacement struct {
	Length, Flags, ShowCmd uint32
	MinPos, MaxPos         struct{ X, Y int32 }
	Normal                 rect
}

// nativeUI — своё окно на WebView2 (движок Edge, встроенный в Windows 10/11) и значок в трее.
type nativeUI struct {
	a        *App
	t        *tray
	wv       webview2.WebView
	hwnd     uintptr
	oldProc  uintptr
	fallback bool
	hinted   bool
	dark     bool
	mu       sync.Mutex
}

func newUI(a *App) uiHost {
	enableDPIAwareness()
	u := &nativeUI{a: a, dark: a.settingsCopy().Theme != "light"}
	u.t = newTray(func() { u.Show() }, a.trayMenu, a.trayCommand)
	go a.trayLoop(u.t)
	return u
}

func (u *nativeUI) Run(show bool) {
	if show {
		u.Show()
	}
	u.t.Run()
}

func (u *nativeUI) Notify(text string, warn bool) { u.t.Balloon(text, warn) }
func (u *nativeUI) Refresh()                      { on, tip := u.a.trayTip(); u.t.SetState(on, tip) }
func (u *nativeUI) Close()                        { u.saveGeom(); u.t.Remove() }

func dpiOf(hwnd uintptr) int {
	if hwnd != 0 && procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(hwnd); d > 0 {
			return int(d)
		}
	}
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d > 0 {
			return int(d)
		}
	}
	return 96
}

func (u *nativeUI) Show() {
	u.t.Do(func() {
		if u.fallback {
			openWindow(u.a.url(), browserProfile(u.a.dir))
			return
		}
		if u.wv == nil {
			u.create()
			return
		}
		if r, _, _ := procIsIconic.Call(u.hwnd); r != 0 {
			procShowWindow.Call(u.hwnd, 9) // SW_RESTORE
		} else {
			procShowWindow.Call(u.hwnd, 5) // SW_SHOW
		}
		procSetForegroundWindow.Call(u.hwnd)
	})
}

func (u *nativeUI) create() {
	dpi := dpiOf(0)
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(u.a.dir, "webview"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "Naivka", Width: uint(1180 * dpi / 96), Height: uint(820 * dpi / 96), Center: true,
		},
	})
	if wv == nil {
		// WebView2 не установлен (редко на Windows 10) — открываем в браузере.
		u.fallback = true
		openWindow(u.a.url(), browserProfile(u.a.dir))
		return
	}
	u.wv = wv
	u.hwnd = uintptr(wv.Window())
	dpi = dpiOf(u.hwnd)
	procSendMessageW.Call(u.hwnd, 0x80, 1, iconFromICO(icoOn, 32*dpi/96)) // WM_SETICON, ICON_BIG
	procSendMessageW.Call(u.hwnd, 0x80, 0, iconFromICO(icoOn, 16*dpi/96)) // ICON_SMALL
	u.restoreGeom()
	u.oldProc, _, _ = procSetWindowLongPtrW.Call(u.hwnd, ^uintptr(3), syscall.NewCallback(u.proc)) // GWLP_WNDPROC = -4
	u.applyTheme()
	wv.Navigate(u.a.url())
}

func (u *nativeUI) proc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case 0x0010: // WM_CLOSE
		u.saveGeom()
		if u.a.settingsCopy().ExitWithWindow {
			go u.a.Quit()
			return 0
		}
		procShowWindow.Call(hwnd, 0) // SW_HIDE — Naivka остаётся в трее
		if !u.hinted {
			u.hinted = true
			u.t.Balloon("Naivka работает в трее. Щёлкните по значку, чтобы открыть окно.", false)
		}
		return 0
	case 0x0024: // WM_GETMINMAXINFO
		r, _, _ := procCallWindowProcW.Call(u.oldProc, hwnd, msg, wp, lp)
		dpi := dpiOf(hwnd)
		mm := (*[10]int32)(osPtr(lp))
		mm[6], mm[7] = int32(760*dpi/96), int32(560*dpi/96) // ptMinTrackSize
		return r
	case 0x02E0: // WM_DPICHANGED — переносим окно на монитор с другим масштабом
		rc := (*rect)(osPtr(lp))
		procSetWindowPos.Call(hwnd, 0, uintptr(rc.L), uintptr(rc.T), uintptr(rc.R-rc.L), uintptr(rc.B-rc.T), 0x4|0x10)
		return 0
	}
	r, _, _ := procCallWindowProcW.Call(u.oldProc, hwnd, msg, wp, lp)
	return r
}

func (u *nativeUI) saveGeom() {
	if u.hwnd == 0 {
		return
	}
	var wp windowPlacement
	wp.Length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlace.Call(u.hwnd, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return
	}
	g := &WindowGeom{X: int(wp.Normal.L), Y: int(wp.Normal.T), W: int(wp.Normal.R - wp.Normal.L), H: int(wp.Normal.B - wp.Normal.T), Max: wp.ShowCmd == 3}
	a := u.a
	a.mu.Lock()
	a.settings.Window = g
	_ = SaveSettings(a.dir, a.settings)
	a.mu.Unlock()
}

func (u *nativeUI) restoreGeom() {
	g := u.a.settingsCopy().Window
	if g == nil || g.W < 300 || g.H < 300 {
		return
	}
	rc := rect{int32(g.X), int32(g.Y), int32(g.X + g.W), int32(g.Y + g.H)}
	// Монитор, на котором окно было в прошлый раз, может быть уже отключён.
	if m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rc)), 0); m == 0 {
		return
	}
	procSetWindowPos.Call(u.hwnd, 0, uintptr(g.X), uintptr(g.Y), uintptr(g.W), uintptr(g.H), 0x4)
	if g.Max {
		procShowWindow.Call(u.hwnd, 3) // SW_MAXIMIZE
	}
}

// SetTheme красит системный заголовок окна под тему интерфейса.
func (u *nativeUI) SetTheme(dark bool) {
	u.mu.Lock()
	u.dark = dark
	u.mu.Unlock()
	u.t.Do(u.applyTheme)
}

func (u *nativeUI) applyTheme() {
	if u.hwnd == 0 {
		return
	}
	u.mu.Lock()
	dark := u.dark
	u.mu.Unlock()
	set := func(attr, val uint32) {
		procDwmSetWindowAttr.Call(u.hwnd, uintptr(attr), uintptr(unsafe.Pointer(&val)), 4)
	}
	var on uint32
	if dark {
		on = 1
	}
	set(20, on) // DWMWA_USE_IMMERSIVE_DARK_MODE (Windows 10 20H1+)
	set(19, on) // то же для Windows 10 1809–1909
	// Windows 11: цвет заголовка и рамки — как фон интерфейса.
	caption, text := uint32(0x00F1EDE9), uint32(0x002A2017)
	if dark {
		caption, text = 0x001A140F, 0x00EFE9E4
	}
	set(35, caption) // DWMWA_CAPTION_COLOR
	set(34, caption) // DWMWA_BORDER_COLOR
	set(36, text)    // DWMWA_TEXT_COLOR
}

// osPtr превращает указатель, который Windows передала в оконную процедуру, в unsafe.Pointer.
// Память принадлежит системе, а не сборщику мусора Go, поэтому так можно.
func osPtr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
