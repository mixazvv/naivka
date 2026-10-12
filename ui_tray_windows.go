//go:build windows

package main

import (
	"fmt"
	"time"
)

// Общее для обеих сборок Windows: меню и подсказка значка в трее.

const (
	cmdOpen    = 1
	cmdToggle  = 2
	cmdQuit    = 3
	cmdZapret  = 4
	cmdTg      = 5
	cmdWL      = 99
	cmdAuto    = 100
	cmdServer0 = 101
)

func (a *App) trayMenu() []menuItem {
	s := a.settingsCopy()
	st := a.core.Status()
	items := []menuItem{{Text: "Открыть MZ", ID: cmdOpen}, {Sep: true}}
	switch {
	case st.Starting:
		items = append(items, menuItem{Text: "Подключение…", ID: cmdToggle, Disabled: true})
	case st.Running || st.Blocked:
		items = append(items, menuItem{Text: "Отключить", ID: cmdToggle})
	default:
		items = append(items, menuItem{Text: "Подключить", ID: cmdToggle, Disabled: len(s.Servers) == 0})
	}
	if len(s.Servers) > 0 {
		sub := []menuItem{{Text: "Автовыбор", ID: cmdAuto, Checked: s.Selected == "auto"}, {Sep: true}}
		for i, sv := range s.Servers {
			sub = append(sub, menuItem{Text: fmt.Sprintf("%s (%s)", sv.Name, protoTitle(sv)), ID: cmdServer0 + i, Checked: s.Selected == sv.ID})
		}
		if s.WL.Enabled {
			sub = append(sub, menuItem{Sep: true}, menuItem{Text: "Белые списки (подписка)", ID: cmdWL, Checked: s.Selected == "wl"})
		}
		items = append(items, menuItem{Text: "Сервер", Sub: sub})
	}
	if a.zapret.root() != "" {
		items = append(items, menuItem{Text: "Обход zapret", ID: cmdZapret, Checked: s.Zapret.Enabled && a.zapret.Running()})
	}
	if a.tgws.snapshot().Installed != "" {
		items = append(items, menuItem{Text: "Telegram без сервера", ID: cmdTg, Checked: s.Telegram.Enabled && a.tgws.Running()})
	}
	return append(items, menuItem{Sep: true}, menuItem{Text: "Выход", ID: cmdQuit})
}

func (a *App) trayCommand(id int) {
	switch {
	case id == cmdOpen:
		a.ui.Show()
	case id == cmdQuit:
		a.Quit()
	case id == cmdZapret || id == cmdTg:
		a.mu.Lock()
		prev := a.settings.clone()
		if id == cmdZapret {
			a.settings.Zapret.Enabled = !a.settings.Zapret.Enabled
		} else {
			a.settings.Telegram.Enabled = !a.settings.Telegram.Enabled
		}
		ns := a.settings.clone()
		_ = SaveSettings(a.dir, a.settings)
		a.mu.Unlock()
		a.applyAddons(prev, ns)
		if a.core.Running() && prev.Zapret.Enabled != ns.Zapret.Enabled {
			a.restartCore() // сайты zapret: напрямую или через сервер
		}
		a.ui.Refresh()
	case id == cmdToggle:
		if st := a.core.Status(); st.Running || st.Blocked {
			a.core.Stop()
			a.mon.Event("info", "Отключено", false)
		} else if err := a.connect(); err != nil {
			a.notify("Не удалось подключиться: "+firstLine(err.Error()), true)
		}
		a.ui.Refresh()
	case id == cmdWL:
		a.mu.Lock()
		a.settings.Selected = "wl"
		_ = SaveSettings(a.dir, a.settings)
		a.mu.Unlock()
		a.applySelection()
		a.ui.Refresh()
	case id >= cmdAuto:
		s := a.settingsCopy()
		sel := "auto"
		if i := id - cmdServer0; id != cmdAuto && i >= 0 && i < len(s.Servers) {
			sel = s.Servers[i].ID
		}
		a.mu.Lock()
		a.settings.Selected = sel
		_ = SaveSettings(a.dir, a.settings)
		a.mu.Unlock()
		a.applySelection()
		a.ui.Refresh()
	}
}

func (a *App) trayTip() (bool, string) {
	st := a.core.Status()
	switch {
	case st.Blocked:
		return false, "MZ: ядро упало, трафик заблокирован"
	case st.Starting:
		return false, "MZ: подключение…"
	case st.Running:
		name := ""
		if cl := a.core.Clash(); cl != nil {
			if px, err := cl.Proxies(); err == nil {
				tag := px.Selected
				if tag == tagAuto {
					tag = px.AutoNow
				}
				name = ", " + a.serverName(tag)
			}
		}
		return true, "MZ: подключено" + name
	}
	return false, "MZ: отключено"
}

func (a *App) trayLoop(t *tray) {
	for {
		on, tip := a.trayTip()
		t.SetState(on, tip)
		time.Sleep(3 * time.Second)
	}
}
