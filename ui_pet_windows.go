//go:build windows && !legacy

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"
)

// Котёнок на панели задач: прозрачное окно поверх всех во всю ширину монитора, лежащее на верхнем
// краю панели задач. Страница web/pet.html рисует в нём котёнка. Окно пропускает щелчки насквозь
// (WS_EX_TRANSPARENT), кроме места, где сейчас котёнок: туда курсор попадает, и кота можно гладить
// и носить. Пока кота держат, окно растягивается на весь монитор, а монитор — тот, где курсор: так
// кота переносят на другой экран. Пока на экране полноэкранное окно (игра, видео), котёнок прячется.
//
// Два режима:
//   - «живёт на панели» (настройка «Котёнок на панели задач»): кот всё время там, в окне Naivka его нет;
//   - «спрыгивает из окна» (по умолчанию): кот живёт в окне Naivka и, пока оно открыто, иногда
//     спрыгивает с его низа на панель задач, бегает там под окном и запрыгивает обратно. Переход
//     бесшовный: страница окна передаёт кота (место и скорость) сюда, здесь он продолжает тот же полёт.
//     Свернули или закрыли окно — кот сразу дома, в окне.

var (
	procSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	procSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procSetTimer              = user32.NewProc("SetTimer")
	procKillTimer             = user32.NewProc("KillTimer")
	procFindWindowW           = user32.NewProc("FindWindowW")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procGetClientRect         = user32.NewProc("GetClientRect")
	procClientToScreen        = user32.NewProc("ClientToScreen")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procMonitorFromWindow     = user32.NewProc("MonitorFromWindow")
	procMonitorFromPoint      = user32.NewProc("MonitorFromPoint")
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
	petModeLive     = 1   // живёт на панели задач
	petModeHop      = 2   // живёт в окне, спрыгивает на панель
)

type petState struct {
	mode      int
	rect      rect // где кот, в пикселях окна; пустой — кота здесь нет
	held      bool
	clickable bool
	shown     bool
	out       bool    // режим «спрыгивает»: кот сейчас на панели задач, а не в окне
	mon       uintptr // монитор, на панели которого кот (0 — ещё не выбран)
	expanded  bool    // кота держат — окно на весь монитор
	shrinkIn  int     // через сколько тиков после того, как отпустили, вернуть полосу (кот долетит до низа)
	placed    rect
	zoneMain  string // что последний раз сообщили странице окна Naivka
	zoneHost  string // и странице котёнка
}

// SyncPet включает или выключает котёнка на панели задач по настройкам. Можно звать из любого потока.
func (u *nativeUI) SyncPet() {
	s := u.a.settingsCopy()
	mode := 0
	if s.Mascot && s.PetTaskbar {
		mode = petModeLive
	} else if s.Mascot && !s.PetHopOff {
		mode = petModeHop
	}
	u.t.Do(func() {
		if u.fallback {
			return
		}
		if u.pet != nil && u.petSt.mode != mode {
			u.destroyPet()
		}
		if mode != 0 && u.pet == nil {
			u.createPet(mode)
		} else if u.pet != nil && u.pet.wv != nil {
			u.pet.wv.Eval("window.petHost&&petHost.reload()") // сменили котёнка (бета) — перечитать настройки
		}
	})
}

func (u *nativeUI) destroyPet() {
	if u.petSt.out {
		u.evalMain("window.naivkaPetBack&&naivkaPetBack(null)")
	}
	u.evalMain("window.naivkaPetZone=null")
	procKillTimer.Call(u.pet.hwnd, petTimerID)
	if u.pet.wv != nil {
		u.pet.wv.Hide()
	}
	procDestroyWindow.Call(u.pet.hwnd)
	u.pet = nil
	u.petSt = petState{}
}

func (u *nativeUI) evalMain(js string) {
	if u.main != nil && u.main.wv != nil {
		u.main.wv.Eval(js)
	}
}

func (u *nativeUI) evalPet(js string) {
	if u.pet != nil && u.pet.wv != nil {
		u.pet.wv.Eval(js)
	}
}

func (u *nativeUI) createPet(mode int) {
	w := &webWindow{pet: true}
	u.pet = w
	u.petSt = petState{mode: mode}
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
	u.pickPetMonitor()
	u.placePet(true)
	url := strings.Replace(u.a.url(), "/?", "/pet.html?", 1)
	if mode == petModeHop {
		url += "&hop=1"
	}
	if !u.embed(w, url) {
		procDestroyWindow.Call(hwnd)
		u.pet = nil
		return
	}
	_ = w.wv.Show()
	w.wv.Resize()
	procSetTimer.Call(hwnd, petTimerID, 50, 0)
	u.petTick()
}

// taskbarRect — где панель задач и её монитор. ok=false — панели нет (её скрыли сторонней программой).
func taskbarRect() (tb rect, mon uintptr, ok bool) {
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(u16("Shell_TrayWnd"))), 0)
	if h == 0 {
		return tb, 0, false
	}
	procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&tb)))
	mon, _, _ = procMonitorFromWindow.Call(h, monitorNear)
	return tb, mon, mon != 0
}

func monitorOf(hmon uintptr) (mi monitorInfo, ok bool) {
	mi.Size = uint32(unsafe.Sizeof(mi))
	r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	return mi, r != 0 && mi.Monitor.R > mi.Monitor.L
}

// mainOpen — окно Naivka на экране (не свёрнуто и не спрятано в трей).
func (u *nativeUI) mainOpen() bool {
	if u.main == nil || u.main.hwnd == 0 || !isVisible(u.main.hwnd) {
		return false
	}
	r, _, _ := procIsIconic.Call(u.main.hwnd)
	return r == 0
}

func (u *nativeUI) mainMonitor() uintptr {
	if u.main == nil {
		return 0
	}
	m, _, _ := procMonitorFromWindow.Call(u.main.hwnd, monitorNear)
	return m
}

// mainClient — клиентская область окна Naivka в пикселях экрана (там страница).
func (u *nativeUI) mainClient() (r rect) {
	var cr rect
	procGetClientRect.Call(u.main.hwnd, uintptr(unsafe.Pointer(&cr)))
	var pt struct{ X, Y int32 }
	procClientToScreen.Call(u.main.hwnd, uintptr(unsafe.Pointer(&pt)))
	return rect{pt.X, pt.Y, pt.X + cr.R, pt.Y + cr.B}
}

// pickPetMonitor — где кот, пока его не носят: живёт на панели — на мониторе панели задач (или там,
// куда его унесли); спрыгивает — под окном Naivka, а если его унесли на другой экран — там.
func (u *nativeUI) pickPetMonitor() {
	st := &u.petSt
	if st.held {
		return
	}
	if st.mode == petModeHop && !st.out {
		if m := u.mainMonitor(); m != 0 {
			st.mon = m
		}
	}
	if _, ok := monitorOf(st.mon); !ok { // монитор отключили
		st.mon = 0
	}
	if st.mon == 0 {
		if st.mode == petModeHop {
			st.mon = u.mainMonitor()
		}
		if st.mon == 0 {
			_, st.mon, _ = taskbarRect()
		}
	}
}

// petRect — где должна быть полоса: на всю ширину монитора, низом на панели задач (или на низу рабочей
// области), высотой petHeightCSS; пока кота держат — на весь монитор над панелью.
func (u *nativeUI) petRect() (r rect, ok bool) {
	st := &u.petSt
	mi, ok := monitorOf(st.mon)
	if !ok {
		return r, false
	}
	floor := mi.Work.B
	if tb, tm, ok := taskbarRect(); ok && tm == st.mon && tb.T > mi.Monitor.T && tb.B >= mi.Monitor.B-2 && tb.R-tb.L > (mi.Monitor.R-mi.Monitor.L)/2 {
		floor = tb.T // панель внизу (в том числе автоскрываемая)
	}
	top := floor - int32(petHeightCSS*dpiOfMonitor(st.mon)/96)
	if st.expanded {
		top = mi.Work.T
	}
	return rect{mi.Monitor.L, top, mi.Monitor.R, floor}, true
}

// placePet ставит полосу на место (force — и поверх всех заново, даже если место то же).
func (u *nativeUI) placePet(force bool) {
	w := u.pet
	if w == nil {
		return
	}
	r, ok := u.petRect()
	if !ok || (!force && r == u.petSt.placed) {
		return
	}
	u.petSt.placed = r
	procSetWindowPos.Call(w.hwnd, ^uintptr(0), uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoActivate) // HWND_TOPMOST
	if w.wv != nil {
		w.wv.Resize()
	}
}

// fullscreenApp — на мониторе открыто окно во весь экран (игра, видео, презентация).
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

// petTick — раз в 50 мс: где быть полосе, видна ли она, ловит ли мышь (только над котом), и что
// сообщить страницам о соседнем окне для перехода кота.
func (u *nativeUI) petTick() {
	w := u.pet
	if w == nil {
		return
	}
	st := &u.petSt
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	open := u.mainOpen()

	// Окно Naivka свернули или закрыли, пока кот на панели, — он сразу дома.
	if st.mode == petModeHop && st.out && !open && !st.held {
		st.out = false
		u.evalPet("window.petHost&&petHost.recall()")
		u.evalMain("window.naivkaPetBack&&naivkaPetBack(null)")
	}

	// Держат — окно на весь монитор под курсором; отпустили — через 2 с (долетит до низа) снова полоса.
	if st.held {
		st.expanded, st.shrinkIn = true, 40
		if m, _, _ := procMonitorFromPoint.Call(uintptr(uint32(pt.X))|uintptr(uint32(pt.Y))<<32, monitorNear); m != 0 {
			st.mon = m
		}
	} else if st.expanded {
		if st.shrinkIn--; st.shrinkIn <= 0 {
			st.expanded = false
		}
	}
	if !st.expanded {
		u.pickPetMonitor()
	}
	if u.petTicks++; u.petTicks%60 == 0 { // раз в 3 с: панель задач могла переехать или перекрыть
		u.placePet(true)
	} else {
		u.placePet(false)
	}

	mi, _ := monitorOf(st.mon)
	full := !st.held && fullscreenApp(mi.Monitor)
	show := !full && (st.mode == petModeLive || open || st.held)
	if show != st.shown {
		st.shown = show
		if show {
			procShowWindow.Call(w.hwnd, 4) // SW_SHOWNOACTIVATE
			u.placePet(true)
		} else {
			procShowWindow.Call(w.hwnd, swHide)
		}
	}
	u.petZones(show)
	if !show {
		return
	}

	var wr rect
	procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&wr)))
	dpi := int32(dpiOf(w.hwnd))
	r := st.rect
	x, y := (pt.X-wr.L)*96/dpi, (pt.Y-wr.T)*96/dpi
	over := r.R > r.L && x >= r.L && x <= r.R && y >= r.T && y <= r.B
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

// petZones сообщает страницам, где соседнее окно (в их CSS-пикселях): окну Naivka — где начинается
// полоса котёнка (туда кот спрыгивает), полосе — где окно Naivka (туда кот запрыгивает и где его
// можно отпустить «домой»). Кот на другом мониторе — окна над ним нет.
func (u *nativeUI) petZones(show bool) {
	st := &u.petSt
	zm, zh := "null", "null"
	if st.mode == petModeHop && show && u.mainOpen() && st.mon == u.mainMonitor() {
		pr, _ := u.petRect()
		if st.expanded { // полоса растянута — граница всё равно по обычной полосе
			pr.T = pr.B - int32(petHeightCSS*dpiOfMonitor(st.mon)/96)
		}
		mc := u.mainClient()
		dm, dp := int32(dpiOf(u.main.hwnd)), int32(dpiOfMonitor(st.mon))
		zm = fmt.Sprintf(`{"top":%d}`, (pr.T-mc.T)*96/dm)
		var wr rect
		procGetWindowRect.Call(u.pet.hwnd, uintptr(unsafe.Pointer(&wr)))
		cx := func(v int32) int32 { return (v - wr.L) * 96 / dp }
		cy := func(v int32) int32 { return (v - wr.T) * 96 / dp }
		zh = fmt.Sprintf(`{"win":[%d,%d,%d,%d]}`, cx(mc.L), cy(mc.T), cx(mc.R), cy(mc.B))
	}
	if zm != st.zoneMain {
		st.zoneMain = zm
		u.evalMain("window.naivkaPetZone=" + zm)
	}
	if zh != st.zoneHost {
		st.zoneHost = zh
		u.evalPet("window.petHost&&petHost.zone(" + zh + ")")
	}
}

// onMainPet — кот из окна Naivka уходит на панель задач (спрыгнул или его вынесли за низ окна):
// {cmd:"petout", x, y, vx, vy} — левый верхний угол и скорость в CSS-пикселях окна.
func (u *nativeUI) onMainPet(raw string) {
	var m struct{ X, Y, VX, VY float64 }
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	x, y, vx, vy := m.X, m.Y, m.VX, m.VY
	st := &u.petSt
	if u.pet == nil || st.mode != petModeHop || !st.shown || u.main == nil {
		u.evalMain("window.naivkaPetBack&&naivkaPetBack(null)") // некуда — домой
		return
	}
	mc := u.mainClient()
	var wr rect
	procGetWindowRect.Call(u.pet.hwnd, uintptr(unsafe.Pointer(&wr)))
	dm, dp := float64(dpiOf(u.main.hwnd)), float64(dpiOfMonitor(st.mon))
	k := dm / dp
	px := (float64(mc.L) + x*dm/96 - float64(wr.L)) * 96 / dp
	py := (float64(mc.T) + y*dm/96 - float64(wr.T)) * 96 / dp
	st.out = true
	u.evalPet(fmt.Sprintf("window.petHost&&petHost.arrive(%.1f,%.1f,%.1f,%.1f)", px, py, vx*k, vy*k))
}

func (u *nativeUI) onPetMessage(raw string) {
	var m struct {
		Cmd  string  `json:"cmd"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		W    float64 `json:"w"`
		H    float64 `json:"h"`
		VX   float64 `json:"vx"`
		VY   float64 `json:"vy"`
		Held bool    `json:"held"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	switch m.Cmd {
	case "petrect":
		u.petSt.rect = rect{int32(m.X), int32(m.Y), int32(m.X + m.W), int32(m.Y + m.H)}
		u.petSt.held = m.Held
	case "petback": // кот запрыгнул (или его отнесли) в окно Naivka
		st := &u.petSt
		if st.mode != petModeHop || !st.out {
			return
		}
		st.out = false
		if !u.mainOpen() {
			u.evalMain("window.naivkaPetBack&&naivkaPetBack(null)")
			return
		}
		if st.mon != u.mainMonitor() { // с другого монитора — выпрыгивает снизу окна
			u.evalMain("window.naivkaPetBack&&naivkaPetBack('jump')")
			return
		}
		mc := u.mainClient()
		var wr rect
		procGetWindowRect.Call(u.pet.hwnd, uintptr(unsafe.Pointer(&wr)))
		dm, dp := float64(dpiOf(u.main.hwnd)), float64(dpiOfMonitor(st.mon))
		k := dp / dm
		mx := (float64(wr.L) + m.X*dp/96 - float64(mc.L)) * 96 / dm
		my := (float64(wr.T) + m.Y*dp/96 - float64(mc.T)) * 96 / dm
		u.evalMain(fmt.Sprintf("window.naivkaPetBack&&naivkaPetBack(%.1f,%.1f,%.1f,%.1f)", mx, my, m.VX*k, m.VY*k))
	}
}

func (u *nativeUI) petProc(hwnd, msg, wp, lp uintptr) uintptr {
	w := u.pet
	if w != nil {
		switch msg {
		case wmTimer:
			if wp == petTimerID {
				u.petTick()
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
			u.placePet(true)
			return 0
		case wmClose:
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}
