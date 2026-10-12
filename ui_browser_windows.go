//go:build windows && legacy

package main

// Сборка для Windows 7: окно открывается в браузере (WebView2 там нет), значок в трее — свой.

type browserUI struct {
	a *App
	t *tray
}

func newUI(a *App) uiHost {
	enableDPIAwareness()
	u := &browserUI{a: a}
	u.t = newTray(func() { u.Show() }, a.trayMenu, a.trayCommand)
	go a.trayLoop(u.t)
	return u
}

func (u *browserUI) Run(show bool) {
	if show {
		u.Show()
	}
	u.t.Run()
}

func (u *browserUI) Show()                         { go openWindow(u.a.url(), browserProfile(u.a.dir)) }
func (u *browserUI) Notify(text string, warn bool) { u.t.Balloon(text, warn) }
func (u *browserUI) Refresh()                      { on, tip := u.a.trayTip(); u.t.SetState(on, tip) }
func (u *browserUI) Close()                        { u.t.Remove() }
func (u *browserUI) SetTheme(bool)                 {}
