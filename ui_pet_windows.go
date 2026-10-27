//go:build windows && !legacy

package main

import (
	"encoding/json"
	"strings"
	"unsafe"
)

// Котёнок на панели задач: прозрачное окно поверх всех во всю ширину экрана, лежащее на верхнем краю
// панели задач. Страница web/pet.html рисует в нём котёнка, который гуляет вдоль панели. Окно
// пропускает щелчки насквозь (WS_EX_TRANSPARENT), кроме места, где сейчас котёнок: туда курсор
// попадает, и кота можно гладить и носить. Пока на экране полноэкранное окно (игра, видео),
// котёнок прячется.

var (
	procSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	procSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procSetTimer              = user32.NewProc("SetTimer")
	procKillTimer             = user32.NewProc("KillTimer")
	procFindWindowW           = user32.NewProc("FindWindowW")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procMonitorFromWindow     = user32.NewProc("MonitorFromWindow")
	procDwmEnableBlurBehind   = dwmapi.NewProc("DwmEnableBlurBehindWindow")
	procCreateRectRgn         = gdi32.NewProc("CreateRectRgn")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procGetClassNameW         = user32.NewProc("GetClassNameW")
	procGetDesktopWindow      = user32.NewProc("GetDesktopWindow")
	procGetShellWindow        = user32.NewProc("GetShellWindow")
)

const (
	gwlExStyle      = ^uintptr(19) // -20
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExNoActivate  = 0x08000000
	lwaAlpha        = 0x2
	wmTimer         = 0x0113
	wmMouseActivate = 0x0021
	maNoActivate    = 3
	petTimerID      = 7
	petHeightCSS    = 170 // высота полосы над панелью задач, CSS-пиксели: прыжки и надпись над котом
)

type petState struct {
	rect          rect // где кот, в пикселях окна
	held          bool
	clickable     bool
	hiddenForFull bool
}

// SyncPet включает или выключает котёнка на панели задач по настройкам. Можно звать из любого потока.
func (u *nativeUI) SyncPet() {
	s := u.a.settingsCopy()
	on := s.Mascot && s.PetTaskbar
	u.t.Do(func() {
		if u.fallback {
			return
		}
		if on && u.pet == nil {
			u.createPet()
		} else if !on && u.pet != nil {
			procKillTimer.Call(u.pet.hwnd, petTimerID)
			if u.pet.wv != nil {
				u.pet.wv.Hide()
			}
			procDestroyWindow.Call(u.pet.hwnd)
			u.pet = nil
		} else if on && u.pet != nil && u.pet.wv != nil {
			u.pet.wv.Eval("window.petHost&&petHost.reload()") // сменили котёнка (бета) — перечитать настройки
		}
	})
}

func (u *nativeUI) createPet() {
	w := &webWindow{pet: true}
	u.pet = w
	u.petSt = petState{}
	brush, _, _ := procGetStockObject.Call(4) // BLACK_BRUSH: при «размытии позади» чёрное — прозрачное
	cls := registerClass("NaivkaPet", u.petProc, 0, brush)
	hinst, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, _ := procCreateWindowExW.Call(wsExToolWindow|wsExTopmost|wsExNoActivate|wsExLayered|wsExTransparent,
		uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Naivka"))), wsPopup, 0, 0, 100, 100, 0, 0, hinst, 0)
	if hwnd == 0 {
		u.pet = nil
		return
	}
	w.hwnd = hwnd
	procSetLayeredWindowAttrs.Call(hwnd, 0, 255, lwaAlpha)
	// Прозрачность по альфа-каналу страницы: «размытие позади» с пустой областью (так делают Tauri и др.).
	rgn, _, _ := procCreateRectRgn.Call(0, 0, ^uintptr(0), ^uintptr(0))
	bb := struct {
		Flags      uint32
		Enable     int32
		Rgn        uintptr
		Transition int32
	}{Flags: 0x1 | 0x2, Enable: 1, Rgn: rgn} // DWM_BB_ENABLE | DWM_BB_BLURREGION
	procDwmEnableBlurBehind.Call(hwnd, uintptr(unsafe.Pointer(&bb)))
	procDeleteObject.Call(rgn)
	u.placePet()
	url := strings.Replace(u.a.url(), "/?", "/pet.html?", 1)
	if !u.embed(w, url) {
		procDestroyWindow.Call(hwnd)
		u.pet = nil
		return
	}
	procShowWindow.Call(hwnd, 4) // SW_SHOWNOACTIVATE
	_ = w.wv.Show()
	w.wv.Resize()
	procSetTimer.Call(hwnd, petTimerID, 50, 0)
}

// taskbarRect — где панель задач и её монитор. ok=false — панели нет (её скрыли сторонней программой).
func taskbarRect() (tb rect, mi monitorInfo, ok bool) {
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(u16("Shell_TrayWnd"))), 0)
	if h == 0 {
		return tb, mi, false
	}
	procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&tb)))
	hmon, _, _ := procMonitorFromWindow.Call(h, monitorNear)
	mi.Size = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	return tb, mi, mi.Monitor.R > mi.Monitor.L
}

// placePet кладёт полосу на верхний край панели задач (если панель внизу) или на низ рабочей области.
func (u *nativeUI) placePet() {
	w := u.pet
	if w == nil {
		return
	}
	tb, mi, ok := taskbarRect()
	if !ok {
		return
	}
	hmon, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&tb)), monitorNear)
	dpi := int32(dpiOfMonitor(hmon))
	floor := mi.Work.B
	if tb.T > mi.Monitor.T && tb.B >= mi.Monitor.B-2 && tb.R-tb.L > (mi.Monitor.R-mi.Monitor.L)/2 { // панель внизу
		floor = tb.T
	}
	h := petHeightCSS * dpi / 96
	x, width := mi.Monitor.L, mi.Monitor.R-mi.Monitor.L
	procSetWindowPos.Call(w.hwnd, ^uintptr(0), uintptr(x), uintptr(floor-h), uintptr(width), uintptr(h), swpNoActivate) // HWND_TOPMOST
	if w.wv != nil {
		w.wv.Resize()
	}
}

// fullscreenApp — на мониторе панели задач открыто окно во весь экран (игра, видео, презентация).
func fullscreenApp(mon rect) bool {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return false
	}
	if d, _, _ := procGetDesktopWindow.Call(); fg == d {
		return false
	}
	if s, _, _ := procGetShellWindow.Call(); fg == s {
		return false
	}
	var buf [64]uint16
	n, _, _ := procGetClassNameW.Call(fg, uintptr(unsafe.Pointer(&buf[0])), 64)
	switch cls := string(utf16Decode(buf[:n])); cls {
	case "WorkerW", "Progman", "Shell_TrayWnd", "Shell_SecondaryTrayWnd", "NaivkaPet":
		return false
	}
	var r rect
	procGetWindowRect.Call(fg, uintptr(unsafe.Pointer(&r)))
	return r.L <= mon.L && r.T <= mon.T && r.R >= mon.R && r.B >= mon.B
}

func utf16Decode(s []uint16) []rune {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		out = append(out, rune(c))
	}
	return out
}

// petTick — раз в 50 мс: курсор над котом — окно ловит мышь, иначе пропускает щелчки насквозь;
// полноэкранное окно — прячемся; раз в несколько секунд — снова поверх всех (панель задач могла перекрыть).
func (u *nativeUI) petTick() {
	w := u.pet
	if w == nil {
		return
	}
	st := &u.petSt
	_, mi, ok := taskbarRect()
	full := ok && fullscreenApp(mi.Monitor)
	if full != st.hiddenForFull {
		st.hiddenForFull = full
		if full {
			procShowWindow.Call(w.hwnd, swHide)
		} else {
			procShowWindow.Call(w.hwnd, 4)
			u.placePet()
		}
	}
	if full {
		return
	}
	var wr rect
	procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&wr)))
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	dpi := int32(dpiOf(w.hwnd))
	r := st.rect
	x, y := (pt.X-wr.L)*96/dpi, (pt.Y-wr.T)*96/dpi
	over := x >= r.L && x <= r.R && y >= r.T && y <= r.B
	want := over || st.held
	if want != st.clickable {
		st.clickable = want
		ex, _, _ := procGetWindowLongPtrW.Call(w.hwnd, gwlExStyle)
		if want {
			ex &^= wsExTransparent
		} else {
			ex |= wsExTransparent
		}
		procSetWindowLongPtrW.Call(w.hwnd, gwlExStyle, ex)
	}
}

func (u *nativeUI) onPetMessage(raw string) {
	var m struct {
		Cmd  string  `json:"cmd"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		W    float64 `json:"w"`
		H    float64 `json:"h"`
		Held bool    `json:"held"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil || m.Cmd != "petrect" {
		return
	}
	u.petSt.rect = rect{int32(m.X), int32(m.Y), int32(m.X + m.W), int32(m.Y + m.H)}
	u.petSt.held = m.Held
}

func (u *nativeUI) petProc(hwnd, msg, wp, lp uintptr) uintptr {
	w := u.pet
	if w != nil {
		switch msg {
		case wmTimer:
			if wp == petTimerID {
				u.petTick()
				if u.petTicks++; u.petTicks%60 == 0 { // раз в 3 с: панель задач могла переехать или перекрыть
					u.placePet()
				}
			}
			return 0
		case wmMouseActivate:
			return maNoActivate // щелчок по коту не забирает фокус у текущей программы
		case wmSize:
			if w.wv != nil {
				w.wv.Resize()
			}
			return 0
		case wmDPIChanged:
			u.placePet()
			return 0
		case wmClose:
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}
