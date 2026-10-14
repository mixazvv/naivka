//go:build windows && !legacy

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

var (
	procShowWindow       = user32.NewProc("ShowWindow")
	procIsIconic         = user32.NewProc("IsIconic")
	procIsZoomed         = user32.NewProc("IsZoomed")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetWindowPlace   = user32.NewProc("GetWindowPlacement")
	procMonitorFromRect  = user32.NewProc("MonitorFromRect")
	procGetMonitorInfoW  = user32.NewProc("GetMonitorInfoW")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
	procGetSysMetricsDpi = user32.NewProc("GetSystemMetricsForDpi")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procReleaseCapture   = user32.NewProc("ReleaseCapture")
	procGetSystemMenu    = user32.NewProc("GetSystemMenu")
	procEnableMenuItem   = user32.NewProc("EnableMenuItem")
	procSetMenuDefault   = user32.NewProc("SetMenuDefaultItem")
	dwmapi               = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttr = dwmapi.NewProc("DwmSetWindowAttribute")
	procDwmExtendFrame   = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	gdi32                = syscall.NewLazyDLL("gdi32.dll")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procGetStockObject   = gdi32.NewProc("GetStockObject")
	shcore               = syscall.NewLazyDLL("shcore.dll")
	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
)

const (
	wmSize          = 0x0005
	wmMove          = 0x0003
	wmMoving        = 0x0216
	wmActivate      = 0x0006
	wmClose         = 0x0010
	wmGetMinMaxInfo = 0x0024
	wmNCCalcSize    = 0x0083
	wmNCLButtonDown = 0x00A1
	wmSysCommand    = 0x0112
	wmDPIChanged    = 0x02E0
	wmSetIcon       = 0x0080

	wsOverlappedWindow = 0x00CF0000
	wsSysMenu          = 0x00080000
	wsPopup            = 0x80000000
	wsExToolWindow     = 0x00000080
	wsExTopmost        = 0x00000008

	swHide     = 0
	swShow     = 5
	swMinimize = 6
	swMaximize = 3
	swRestore  = 9

	htCaption     = 2
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	csDropShadow  = 0x00020000
	monitorNear   = 2
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	swpFrameChg   = 0x0020
	swpShowWindow = 0x0040
)

type rect struct{ L, T, R, B int32 }

type windowPlacement struct {
	Length, Flags, ShowCmd uint32
	MinPos, MaxPos         struct{ X, Y int32 }
	Normal                 rect
}

type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}

// webWindow — окно Windows с WebView2 внутри.
type webWindow struct {
	hwnd  uintptr
	wv    *edge.Chromium
	frame bool // своя строка заголовка (Windows 11): системная убрана, кнопки рисует страница
	mica  bool // фон Mica (Windows 11 22H2+): страница прозрачная, сквозь неё виден фон окна
	mini  bool // мини-окно из трея
}

// nativeUI — своё окно на WebView2 (движок Edge, встроенный в Windows 10/11), мини-окно и значок в трее.
type nativeUI struct {
	a        *App
	t        *tray
	main     *webWindow
	mini     *webWindow
	fallback bool
	creating bool
	hinted   bool
	dark     bool
	logo     logoColors
	winIcons [2]uintptr // значки главного окна (большой и маленький)
	build    uint32     // номер сборки Windows
	miniH    int        // высота мини-окна в пикселях CSS (её сообщает страница)
	miniHid  time.Time
	mu       sync.Mutex
}

func newUI(a *App) uiHost {
	enableDPIAwareness()
	u := &nativeUI{a: a, dark: true, logo: defaultLogoColors(a.settingsCopy()), miniH: 460}
	if v := windows.RtlGetVersion(); v != nil {
		u.build = v.BuildNumber
	}
	u.t = newTray(u.trayClick, a.trayMenu, a.trayCommand, u.logo)
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

func dpiOfMonitor(hmon uintptr) int {
	if hmon != 0 && procGetDpiForMonitor.Find() == nil {
		var x, y uint32
		if r, _, _ := procGetDpiForMonitor.Call(hmon, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); r == 0 && x > 0 {
			return int(x)
		}
	}
	return dpiOf(0)
}

func sysMetricDpi(idx, dpi int) int {
	if procGetSysMetricsDpi.Find() == nil {
		r, _, _ := procGetSysMetricsDpi.Call(uintptr(idx), uintptr(dpi))
		return int(r)
	}
	r, _, _ := procGetSystemMetrics.Call(uintptr(idx))
	return int(r)
}

func isVisible(hwnd uintptr) bool { r, _, _ := procIsWindowVisible.Call(hwnd); return r != 0 }
func isZoomed(hwnd uintptr) bool  { r, _, _ := procIsZoomed.Call(hwnd); return r != 0 }

func dwmSet(hwnd uintptr, attr, val uint32) bool {
	if procDwmSetWindowAttr.Find() != nil {
		return false
	}
	r, _, _ := procDwmSetWindowAttr.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&val)), 4)
	return r == 0 // S_OK
}

// registerClass регистрирует класс окна один раз за запуск.
var classes = map[string]bool{}

func registerClass(name string, proc func(hwnd, msg, wp, lp uintptr) uintptr, style uint32, brush uintptr) *uint16 {
	cls := u16(name)
	if classes[name] {
		return cls
	}
	hinst, _, _ := procGetModuleHandleW.Call(0)
	cur, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
	wc := wndClassEx{Style: style, LpfnWndProc: syscall.NewCallback(proc), HInstance: hinst, HCursor: cur, HbrBackground: brush, LpszClassName: cls}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	classes[name] = true
	return cls
}

// Цвета фона страницы до её загрузки (как --bg в web/app.css), чтобы окно не вспыхивало белым.
func pageBackground(dark bool) (edge.COREWEBVIEW2_COLOR, uint32) {
	if dark {
		return edge.COREWEBVIEW2_COLOR{A: 255, R: 0x0F, G: 0x14, B: 0x1A}, 0x001A140F
	}
	return edge.COREWEBVIEW2_COLOR{A: 255, R: 0xE9, G: 0xED, B: 0xF1}, 0x00F1EDE9
}

// embed создаёт WebView2 в окне. false — WebView2 недоступен.
func (u *nativeUI) embed(w *webWindow, url string) bool {
	wv := edge.NewChromium()
	wv.DataPath = filepath.Join(u.a.dir, "webview")
	wv.MessageCallback = func(m string) { u.onMessage(w, m) }
	wv.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)
	if !wv.Embed(w.hwnd) {
		return false
	}
	w.wv = wv
	if st, err := wv.GetSettings(); err == nil {
		_ = st.PutAreDefaultContextMenusEnabled(false)
		_ = st.PutAreDevToolsEnabled(false)
		_ = st.PutIsStatusBarEnabled(false)
	}
	u.setPageBackground(w)
	wv.Resize()
	host, _ := json.Marshal(map[string]bool{"frame": w.frame, "mica": w.mica, "mini": w.mini, "max": !w.mini && isZoomed(w.hwnd)})
	wv.Init("window.NAIVKA_HOST=" + string(host) + ";")
	wv.Navigate(url + "&host=" + w.hostFlags())
	return true
}

func (u *nativeUI) setPageBackground(w *webWindow) {
	if w == nil || w.wv == nil || w.wv.GetController() == nil {
		return
	}
	c2 := w.wv.GetController().GetICoreWebView2Controller2()
	if c2 == nil {
		return
	}
	u.mu.Lock()
	col, _ := pageBackground(u.dark)
	u.mu.Unlock()
	if w.mica {
		col = edge.COREWEBVIEW2_COLOR{} // прозрачный: под страницей фон Mica
	}
	_ = c2.PutDefaultBackgroundColor(col)
}

// hostFlags — признаки окна для страницы (дублируют NAIVKA_HOST в адресе).
func (w *webWindow) hostFlags() string {
	var f []string
	for _, x := range []struct {
		on   bool
		name string
	}{{w.frame, "frame"}, {w.mica, "mica"}, {w.mini, "mini"}} {
		if x.on {
			f = append(f, x.name)
		}
	}
	return strings.Join(f, ",")
}

// push сообщает странице о состоянии окна (развёрнуто, активно, показано).
func (w *webWindow) push(state map[string]bool) {
	if w == nil || w.wv == nil {
		return
	}
	b, _ := json.Marshal(state)
	w.wv.Eval("window.naivkaHost&&naivkaHost.update(" + string(b) + ")")
}

// ---------- главное окно ----------

func (u *nativeUI) Show() {
	u.t.Do(func() {
		if u.fallback {
			openWindow(u.a.url(), browserProfile(u.a.dir))
			return
		}
		if u.main == nil {
			if !u.creating {
				u.createMain()
			}
			return
		}
		u.hideMini()
		hwnd := u.main.hwnd
		if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
			procShowWindow.Call(hwnd, swRestore)
		} else {
			procShowWindow.Call(hwnd, swShow)
		}
		if u.main.wv != nil {
			_ = u.main.wv.Show()
			u.main.wv.Resize()
		}
		procSetForegroundWindow.Call(hwnd)
	})
}

func (u *nativeUI) createMain() {
	u.creating = true
	defer func() { u.creating = false }()
	s := u.a.settingsCopy()
	classic := s.NativeFrame || os.Getenv("NAIVKA_CLASSIC_WINDOW") != ""
	w := &webWindow{frame: !classic && u.build >= 22000}
	u.main = w // оконная процедура нужна уже во время CreateWindowEx

	brush, _, _ := procGetStockObject.Call(4) // BLACK_BRUSH: под Mica чёрное = прозрачное
	if classic || u.build < 22621 {
		u.mu.Lock()
		_, cref := pageBackground(u.dark)
		u.mu.Unlock()
		brush, _, _ = procCreateSolidBrush.Call(uintptr(cref))
	}
	cls := registerClass("NaivkaWindow", u.mainProc, 0x3, brush) // CS_HREDRAW | CS_VREDRAW
	dpi := dpiOf(0)
	width, height := 1180*dpi/96, 820*dpi/96
	sw, _, _ := procGetSystemMetrics.Call(0)
	sh, _, _ := procGetSystemMetrics.Call(1)
	x, y := (int(sw)-width)/2, (int(sh)-height)/2
	hinst, _, _ := procGetModuleHandleW.Call(0)
	style := uintptr(wsOverlappedWindow)
	if w.frame {
		// Без WS_SYSMENU система не рисует свои кнопки «свернуть/развернуть/закрыть» поверх страницы
		// (под Mica рамка продлена на всё окно). Меню окна страница открывает сама (Alt+Пробел, правый щелчок).
		style &^= wsSysMenu
	}
	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Naivka"))),
		style, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, hinst, 0)
	if hwnd == 0 {
		u.main = nil
		u.fallback = true
		openWindow(u.a.url(), browserProfile(u.a.dir))
		return
	}
	w.hwnd = hwnd
	dpi = dpiOf(hwnd)
	u.setWindowIcons()
	if !classic && u.build >= 22621 && dwmSet(hwnd, 38, 2) { // DWMWA_SYSTEMBACKDROP_TYPE = DWMSBT_MAINWINDOW (Mica)
		m := [4]int32{-1, -1, -1, -1}
		if r, _, _ := procDwmExtendFrame.Call(hwnd, uintptr(unsafe.Pointer(&m))); r == 0 {
			w.mica = true
		} else {
			dwmSet(hwnd, 38, 1) // DWMSBT_NONE
		}
	}
	u.applyTheme()
	if w.frame {
		procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpFrameChg|swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate)
	}
	maxed := u.restoreGeom()
	if !u.embed(w, u.a.url()) {
		// WebView2 не установлен (редко на Windows 10) — открываем в браузере.
		procDestroyWindow.Call(hwnd)
		u.main = nil
		u.fallback = true
		openWindow(u.a.url(), browserProfile(u.a.dir))
		return
	}
	if maxed {
		procShowWindow.Call(hwnd, swMaximize)
	} else {
		procShowWindow.Call(hwnd, swShow)
	}
	procSetForegroundWindow.Call(hwnd)
	w.wv.Focus()
}

func (u *nativeUI) mainProc(hwnd, msg, wp, lp uintptr) uintptr {
	w := u.main
	if w == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	switch msg {
	case wmNCCalcSize:
		if wp != 0 && w.frame {
			// Своя строка заголовка: рамку слева, справа и снизу (за неё тянут размер) оставляет система,
			// заголовок сверху убираем — его место занимает страница. Как в Windows Terminal.
			rc := (*rect)(osPtr(lp)) // NCCALCSIZE_PARAMS.rgrc[0]
			top := rc.T
			procDefWindowProcW.Call(hwnd, msg, wp, lp)
			rc.T = top
			if isZoomed(hwnd) {
				// Развёрнутое окно выходит за край экрана на толщину рамки.
				dpi := dpiOf(hwnd)
				rc.T += int32(sysMetricDpi(33, dpi) + sysMetricDpi(92, dpi)) // SM_CYFRAME + SM_CXPADDEDBORDER
			}
			return 0
		}
	case wmSize:
		if w.wv != nil {
			if wp == 1 { // SIZE_MINIMIZED — странице незачем рисовать анимацию
				_ = w.wv.Hide()
			} else {
				_ = w.wv.Show()
				w.wv.Resize()
			}
			w.push(map[string]bool{"max": wp == 2})
		}
		return 0
	case wmMove, wmMoving:
		if w.wv != nil {
			_ = w.wv.NotifyParentWindowPositionChanged()
		}
	case wmActivate:
		active := wp&0xFFFF != 0
		if active && w.wv != nil {
			w.wv.Focus()
		}
		w.push(map[string]bool{"active": active})
	case wmClose:
		u.saveGeom()
		if u.a.settingsCopy().ExitWithWindow {
			go u.a.Quit()
			return 0
		}
		procShowWindow.Call(hwnd, swHide) // Naivka остаётся в трее
		if w.wv != nil {
			_ = w.wv.Hide()
		}
		if !u.hinted {
			u.hinted = true
			u.t.Balloon(u.a.tr("Naivka работает в трее. Щёлкните по значку, чтобы открыть окно."), false)
		}
		return 0
	case wmGetMinMaxInfo:
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
		dpi := dpiOf(hwnd)
		mm := (*[10]int32)(osPtr(lp))
		mm[6], mm[7] = int32(760*dpi/96), int32(560*dpi/96) // ptMinTrackSize
		return r
	case wmDPIChanged: // переносим окно на монитор с другим масштабом
		rc := (*rect)(osPtr(lp))
		procSetWindowPos.Call(hwnd, 0, uintptr(rc.L), uintptr(rc.T), uintptr(rc.R-rc.L), uintptr(rc.B-rc.T), swpNoZOrder|swpNoActivate)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

// onMessage — команды страницы (window.chrome.webview.postMessage). Приходят в потоке интерфейса.
func (u *nativeUI) onMessage(w *webWindow, raw string) {
	var m struct {
		Cmd  string `json:"cmd"`
		Edge string `json:"edge"`
		H    int    `json:"h"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	switch m.Cmd {
	case "drag":
		if w.frame {
			startNCDrag(w.hwnd, htCaption)
		}
	case "resize":
		hit := map[string]uintptr{"n": htTop, "nw": htTopLeft, "ne": htTopRight}[m.Edge]
		if w.frame && hit != 0 && !isZoomed(w.hwnd) {
			startNCDrag(w.hwnd, hit)
		}
	case "min":
		procShowWindow.Call(w.hwnd, swMinimize)
	case "max":
		if isZoomed(w.hwnd) {
			procShowWindow.Call(w.hwnd, swRestore)
		} else {
			procShowWindow.Call(w.hwnd, swMaximize)
		}
	case "close":
		procPostMessageW.Call(w.hwnd, wmClose, 0, 0)
	case "menu":
		if w.frame {
			showSystemMenu(w.hwnd)
		}
	case "open": // из мини-окна: «Открыть Naivka»
		u.hideMini()
		u.Show()
	case "hide":
		if w.mini {
			u.hideMini()
		}
	case "size":
		if w.mini && m.H > 0 {
			u.miniH = min(max(m.H, 200), 760)
			if isVisible(w.hwnd) {
				u.placeMini()
			}
		}
	}
}

// startNCDrag передаёт перетаскивание окну так, будто мышь нажата на его рамке или заголовке.
func startNCDrag(hwnd, hit uintptr) {
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procReleaseCapture.Call()
	lp := uintptr(uint16(int16(pt.X))) | uintptr(uint16(int16(pt.Y)))<<16
	procPostMessageW.Call(hwnd, wmNCLButtonDown, hit, lp)
}

// showSystemMenu — меню окна (Свернуть, Развернуть, Закрыть…) по правому щелчку на строке заголовка.
func showSystemMenu(hwnd uintptr) {
	m, _, _ := procGetSystemMenu.Call(hwnd, 0)
	if m == 0 {
		return
	}
	zoomed := isZoomed(hwnd)
	state := func(on bool) uintptr {
		if on {
			return 0 // MF_ENABLED
		}
		return 1 // MF_GRAYED
	}
	procEnableMenuItem.Call(m, 0xF120, state(zoomed))  // SC_RESTORE
	procEnableMenuItem.Call(m, 0xF010, state(!zoomed)) // SC_MOVE
	procEnableMenuItem.Call(m, 0xF000, state(!zoomed)) // SC_SIZE
	procEnableMenuItem.Call(m, 0xF030, state(!zoomed)) // SC_MAXIMIZE
	procSetMenuDefault.Call(m, 0xF060, 0)              // SC_CLOSE
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	cmd, _, _ := procTrackPopupMenu.Call(m, 0x100|0x2, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0) // TPM_RETURNCMD | TPM_RIGHTBUTTON
	if cmd != 0 {
		procPostMessageW.Call(hwnd, wmSysCommand, cmd, 0)
	}
}

func (u *nativeUI) saveGeom() {
	if u.main == nil || u.main.hwnd == 0 {
		return
	}
	var wp windowPlacement
	wp.Length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlace.Call(u.main.hwnd, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return
	}
	g := &WindowGeom{X: int(wp.Normal.L), Y: int(wp.Normal.T), W: int(wp.Normal.R - wp.Normal.L), H: int(wp.Normal.B - wp.Normal.T), Max: wp.ShowCmd == 3}
	a := u.a
	a.mu.Lock()
	a.settings.Window = g
	_ = SaveSettings(a.dir, a.settings)
	a.mu.Unlock()
}

// restoreGeom ставит окно туда, где оно было в прошлый раз. true — оно было развёрнуто.
func (u *nativeUI) restoreGeom() bool {
	g := u.a.settingsCopy().Window
	if g == nil || g.W < 300 || g.H < 300 {
		return false
	}
	rc := rect{int32(g.X), int32(g.Y), int32(g.X + g.W), int32(g.Y + g.H)}
	// Монитор, на котором окно было в прошлый раз, может быть уже отключён.
	if m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rc)), 0); m == 0 {
		return false
	}
	procSetWindowPos.Call(u.main.hwnd, 0, uintptr(g.X), uintptr(g.Y), uintptr(g.W), uintptr(g.H), swpNoZOrder|swpNoActivate)
	return g.Max
}

// SetLogo перекрашивает значок в трее и у окна (заголовок, панель задач, Alt+Tab).
func (u *nativeUI) SetLogo(c logoColors) {
	u.mu.Lock()
	same := u.logo == c
	u.logo = c
	u.mu.Unlock()
	if same {
		return
	}
	u.t.SetLogo(c)
	u.t.Do(u.setWindowIcons)
}

// setWindowIcons ставит главному окну значки в текущих цветах. Вызывается в потоке интерфейса.
func (u *nativeUI) setWindowIcons() {
	if u.main == nil || u.main.hwnd == 0 {
		return
	}
	u.mu.Lock()
	c := u.logo
	u.mu.Unlock()
	dpi := dpiOf(u.main.hwnd)
	old := u.winIcons
	u.winIcons = [2]uintptr{logoIcon(32*dpi/96, c, icoOn), logoIcon(16*dpi/96, c, icoOn)}
	procSendMessageW.Call(u.main.hwnd, wmSetIcon, 1, u.winIcons[0])
	procSendMessageW.Call(u.main.hwnd, wmSetIcon, 0, u.winIcons[1])
	for _, h := range old {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
}

// SetTheme красит рамку окна под тему интерфейса.
func (u *nativeUI) SetTheme(dark bool) {
	u.mu.Lock()
	u.dark = dark
	u.mu.Unlock()
	u.t.Do(func() {
		u.applyTheme()
		u.setPageBackground(u.main)
		u.setPageBackground(u.mini)
	})
}

func (u *nativeUI) applyTheme() {
	u.mu.Lock()
	dark := u.dark
	u.mu.Unlock()
	var on uint32
	if dark {
		on = 1
	}
	_, caption := pageBackground(dark)
	text := uint32(0x002A2017)
	border := uint32(0x00DED5CD)
	if dark {
		text, border = 0x00EFE9E4, 0x003D3126
	}
	for _, w := range []*webWindow{u.main, u.mini} {
		if w == nil || w.hwnd == 0 {
			continue
		}
		dwmSet(w.hwnd, 20, on) // DWMWA_USE_IMMERSIVE_DARK_MODE (Windows 10 20H1+); у Mica от него зависит оттенок
		dwmSet(w.hwnd, 19, on) // то же для Windows 10 1809–1909
		if w.mini {
			dwmSet(w.hwnd, 34, border) // DWMWA_BORDER_COLOR
			continue
		}
		if !w.mica && !w.frame {
			// Windows 11: цвет заголовка и рамки — как фон интерфейса.
			dwmSet(w.hwnd, 35, caption) // DWMWA_CAPTION_COLOR
			dwmSet(w.hwnd, 34, caption) // DWMWA_BORDER_COLOR
			dwmSet(w.hwnd, 36, text)    // DWMWA_TEXT_COLOR
		}
	}
}

// ---------- мини-окно из трея ----------

func (u *nativeUI) trayClick() {
	if u.fallback || !u.a.settingsCopy().TrayMini {
		u.Show()
		return
	}
	u.toggleMini()
}

func (u *nativeUI) toggleMini() {
	if u.mini != nil && isVisible(u.mini.hwnd) {
		u.hideMini()
		return
	}
	// Щелчок по значку сначала снимает фокус с мини-окна (и оно прячется), потом приходит сам щелчок.
	if time.Since(u.miniHid) < 400*time.Millisecond {
		return
	}
	if u.mini == nil {
		if u.creating {
			return
		}
		u.createMini()
		if u.mini == nil {
			u.Show()
			return
		}
	}
	u.placeMini()
	w := u.mini
	if w.wv != nil {
		_ = w.wv.Show()
		w.wv.Resize()
	}
	procSetForegroundWindow.Call(w.hwnd)
	if w.wv != nil {
		w.wv.Focus()
	}
	w.push(map[string]bool{"shown": true})
}

func (u *nativeUI) createMini() {
	u.creating = true
	defer func() { u.creating = false }()
	w := &webWindow{mini: true}
	u.mini = w
	u.mu.Lock()
	_, cref := pageBackground(u.dark)
	u.mu.Unlock()
	brush, _, _ := procCreateSolidBrush.Call(uintptr(cref))
	cls := registerClass("NaivkaMini", u.miniProc, csDropShadow, brush)
	hinst, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, _ := procCreateWindowExW.Call(wsExToolWindow|wsExTopmost, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Naivka"))),
		wsPopup, 0, 0, 360, uintptr(u.miniH), 0, 0, hinst, 0)
	if hwnd == 0 {
		u.mini = nil
		return
	}
	w.hwnd = hwnd
	dwmSet(hwnd, 33, 2) // DWMWA_WINDOW_CORNER_PREFERENCE = DWMWCP_ROUND (Windows 11)
	u.applyTheme()
	if !u.embed(w, u.a.url()) {
		procDestroyWindow.Call(hwnd)
		u.mini = nil
		u.fallback = true
	}
}

// placeMini ставит мини-окно в угол экрана у панели задач (как всплывающие окна самой Windows).
func (u *nativeUI) placeMini() {
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	at := rect{pt.X, pt.Y, pt.X + 1, pt.Y + 1}
	hmon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&at)), monitorNear)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	dpi := dpiOfMonitor(hmon)
	width, height := int32(360*dpi/96), int32(u.miniH*dpi/96)
	gap := int32(12 * dpi / 96)
	work := mi.Work
	if work.R-work.L == 0 {
		return
	}
	height = min(height, work.B-work.T-2*gap)
	x, y := work.R-width-gap, work.B-height-gap
	if work.L > mi.Monitor.L { // панель задач слева
		x = work.L + gap
	}
	if work.T > mi.Monitor.T { // панель задач сверху
		y = work.T + gap
	}
	procSetWindowPos.Call(u.mini.hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(width), uintptr(height), swpShowWindow) // HWND_TOPMOST
}

func (u *nativeUI) hideMini() {
	w := u.mini
	if w == nil || !isVisible(w.hwnd) {
		return
	}
	procShowWindow.Call(w.hwnd, swHide)
	if w.wv != nil {
		_ = w.wv.Hide() // скрытая страница не опрашивает Naivka и не рисует анимацию
	}
	u.miniHid = time.Now()
}

func (u *nativeUI) miniProc(hwnd, msg, wp, lp uintptr) uintptr {
	w := u.mini
	if w != nil {
		switch msg {
		case wmActivate:
			if wp&0xFFFF == 0 { // WA_INACTIVE: щёлкнули мимо — прячемся, как всплывающие окна Windows
				u.hideMini()
			} else if w.wv != nil {
				w.wv.Focus()
			}
			return 0
		case wmSize:
			if w.wv != nil {
				w.wv.Resize()
			}
			return 0
		case wmMove, wmMoving:
			if w.wv != nil {
				_ = w.wv.NotifyParentWindowPositionChanged()
			}
		case wmClose:
			u.hideMini()
			return 0
		case wmDPIChanged:
			u.placeMini()
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

// osPtr превращает указатель, который Windows передала в оконную процедуру, в unsafe.Pointer.
// Память принадлежит системе, а не сборщику мусора Go, поэтому так можно.
func osPtr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }
