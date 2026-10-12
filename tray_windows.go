//go:build windows

package main

import (
	_ "embed"
	"encoding/binary"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

//go:embed assets/naivka.ico
var icoOn []byte

//go:embed assets/naivka-off.ico
var icoOff []byte

var (
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procCreateIconFromRes   = user32.NewProc("CreateIconFromResourceEx")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procRegisterWindowMsgW  = user32.NewProc("RegisterWindowMessageW")
	procSetProcessDpiCtx    = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmCommand  = 0x0111
	wmNull     = 0x0000
	wmApp      = 0x8000
	wmTray     = wmApp + 1
	wmRun      = wmApp + 2
	wmLButtonU = 0x0202
	wmRButtonU = 0x0205
	wmContext  = 0x007B

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 1
	nifIcon   = 2
	nifTip    = 4
	nifInfo   = 0x10
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type menuItem struct {
	Text     string
	ID       int
	Checked  bool
	Disabled bool
	Sep      bool
	Sub      []menuItem
}

// tray — значок в области уведомлений и поток интерфейса (все окна живут в нём).
type tray struct {
	hwnd       uintptr
	nid        notifyIconData
	iconOn     uintptr
	iconOff    uintptr
	taskbarMsg uint32
	onOpen     func()
	onMenu     func() []menuItem
	onCommand  func(id int)
	mu         sync.Mutex
	queue      []func()
}

var theTray *tray

func enableDPIAwareness() {
	if procSetProcessDpiCtx.Find() == nil {
		procSetProcessDpiCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	} else if procSetProcessDPIAware.Find() == nil {
		procSetProcessDPIAware.Call()
	}
}

// iconFromICO выбирает из .ico картинку нужного размера и создаёт HICON.
func iconFromICO(ico []byte, want int) uintptr {
	if len(ico) < 6 {
		return 0
	}
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	best, bestSize := -1, 0
	for i := 0; i < n; i++ {
		e := ico[6+16*i:]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		if best < 0 || (w >= want && (bestSize < want || w < bestSize)) || (bestSize < want && w > bestSize) {
			best, bestSize = i, w
		}
	}
	e := ico[6+16*best:]
	size := binary.LittleEndian.Uint32(e[8:])
	off := binary.LittleEndian.Uint32(e[12:])
	data := ico[off : off+size]
	h, _, _ := procCreateIconFromRes.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(size), 1, 0x00030000, uintptr(want), uintptr(want), 0)
	return h
}

func setTip(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)-1]
		u = append(u, 0)
	}
	copy(dst, u)
}

func newTray(onOpen func(), onMenu func() []menuItem, onCommand func(int)) *tray {
	runtime.LockOSThread()
	t := &tray{onOpen: onOpen, onMenu: onMenu, onCommand: onCommand}
	theTray = t
	hinst, _, _ := procGetModuleHandleW.Call(0)
	cls := u16("NaivkaTray")
	wc := wndClassEx{LpfnWndProc: syscall.NewCallback(trayProc), HInstance: hinst, LpszClassName: cls}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	// HWND_MESSAGE = -3: невидимое окно только для сообщений
	t.hwnd, _, _ = procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, hinst, 0)
	t.taskbarMsg = uint32(must(procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))))
	sm, _, _ := procGetSystemMetrics.Call(49) // SM_CXSMICON — с учётом масштаба экрана
	t.iconOn = iconFromICO(icoOn, int(sm))
	t.iconOff = iconFromICO(icoOff, int(sm))
	t.nid = notifyIconData{HWnd: t.hwnd, UID: 1, UFlags: nifMsg | nifIcon | nifTip, UCallbackMessage: wmTray, HIcon: t.iconOff}
	t.nid.CbSize = uint32(unsafe.Sizeof(t.nid))
	setTip(t.nid.SzTip[:], displayName)
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid)))
	return t
}

func must(r, _ uintptr, _ error) uintptr { return r }

// Run — цикл сообщений потока интерфейса. Возвращается после PostQuitMessage.
func (t *tray) Run() {
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// Do выполняет f в потоке интерфейса (окна Windows можно трогать только оттуда).
func (t *tray) Do(f func()) {
	t.mu.Lock()
	t.queue = append(t.queue, f)
	t.mu.Unlock()
	procPostMessageW.Call(t.hwnd, wmRun, 0, 0)
}

func (t *tray) SetState(connected bool, tip string) {
	t.Do(func() {
		t.nid.UFlags = nifIcon | nifTip
		if connected {
			t.nid.HIcon = t.iconOn
		} else {
			t.nid.HIcon = t.iconOff
		}
		t.nid.SzTip = [128]uint16{}
		setTip(t.nid.SzTip[:], tip)
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&t.nid)))
	})
}

func (t *tray) Balloon(text string, warn bool) {
	t.Do(func() {
		n := t.nid
		n.UFlags = nifInfo
		n.SzInfo = [256]uint16{}
		n.SzInfoTitle = [64]uint16{}
		setTip(n.SzInfo[:], text)
		setTip(n.SzInfoTitle[:], displayName)
		n.DwInfoFlags = 1 // NIIF_INFO
		if warn {
			n.DwInfoFlags = 2 // NIIF_WARNING
		}
		procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&n)))
	})
}

func (t *tray) Remove() {
	n := t.nid
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&n)))
}

func buildMenu(items []menuItem) uintptr {
	m, _, _ := procCreatePopupMenu.Call()
	for _, it := range items {
		switch {
		case it.Sep:
			procAppendMenuW.Call(m, 0x800, 0, 0) // MF_SEPARATOR
		case it.Sub != nil:
			sub := buildMenu(it.Sub)
			procAppendMenuW.Call(m, 0x10, sub, uintptr(unsafe.Pointer(u16(it.Text)))) // MF_POPUP
		default:
			var flags uintptr
			if it.Checked {
				flags |= 0x8 // MF_CHECKED
			}
			if it.Disabled {
				flags |= 0x1 // MF_GRAYED
			}
			procAppendMenuW.Call(m, flags, uintptr(it.ID), uintptr(unsafe.Pointer(u16(it.Text))))
		}
	}
	return m
}

func (t *tray) showMenu() {
	m := buildMenu(t.onMenu())
	defer procDestroyMenu.Call(m)
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(t.hwnd)
	// TPM_RETURNCMD | TPM_RIGHTBUTTON | TPM_NONOTIFY
	id, _, _ := procTrackPopupMenu.Call(m, 0x100|0x2|0x80, uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	if id != 0 {
		go t.onCommand(int(id))
	}
}

func trayProc(hwnd, msg, wp, lp uintptr) uintptr {
	t := theTray
	if t == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	switch {
	case msg == wmTray:
		switch lp & 0xFFFF {
		case wmLButtonU:
			t.onOpen()
		case wmRButtonU, wmContext:
			t.showMenu()
		}
		return 0
	case msg == wmRun:
		t.mu.Lock()
		q := t.queue
		t.queue = nil
		t.mu.Unlock()
		for _, f := range q {
			f()
		}
		return 0
	case t.taskbarMsg != 0 && uint32(msg) == t.taskbarMsg:
		// Проводник перезапустился — значок нужно добавить заново.
		n := t.nid
		n.UFlags = nifMsg | nifIcon | nifTip
		procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&n)))
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}
