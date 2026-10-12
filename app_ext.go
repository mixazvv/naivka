package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// uiHost — окно и значок в трее. Реализации: своё окно (Windows) или браузер.
type uiHost interface {
	Run(showWindow bool) // цикл интерфейса, блокирует
	Show()
	SetTheme(dark bool)
	SetLogo(c logoColors) // перекрасить значок под интерфейс
	Notify(text string, warn bool)
	Refresh() // обновить подсказку и меню в трее
	Close()
}

func (a *App) settingsCopy() *Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.clone()
}

func (a *App) serverName(tag string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if tag == tagAuto {
		return "Автовыбор"
	}
	for _, sv := range a.settings.Servers {
		if serverTag(sv) == tag {
			return sv.Name
		}
	}
	return tag
}

func (a *App) notify(text string, warn bool) {
	a.mu.Lock()
	on := a.settings.Notify
	a.mu.Unlock()
	if on && a.ui != nil {
		a.ui.Notify(text, warn)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// onCrash: ядро упало. Защита от утечек уже удерживает трафик (если включена),
// а сторож пробует поднять ядро снова.
func (a *App) onCrash(msg string) {
	s := a.settingsCopy()
	if s.KillSwitch && s.Mode == "proxy" {
		a.mon.Event("error", "Ядро упало. Трафик заблокирован защитой от утечек, пока соединение не восстановится", true)
	} else {
		a.mon.Event("error", "Ядро упало: "+msg, true)
	}
	if a.ui != nil {
		a.ui.Refresh()
	}
	if !s.Watchdog {
		return
	}
	for i := 0; i < 3; i++ {
		time.Sleep(time.Duration(2+i*3) * time.Second)
		if a.core.Running() || !a.mon.allowRestart() {
			return
		}
		if err := a.connect(); err == nil {
			a.mon.Event("ok", "Ядро перезапущено, соединение восстановлено", true)
			if a.ui != nil {
				a.ui.Refresh()
			}
			return
		}
	}
	a.mon.Event("error", "Не удалось восстановить соединение автоматически", true)
}

// restartCore перезапускает ядро. Если перезапуск уже идёт, второй не начинается —
// идущий просто повторится один раз в конце с самыми свежими настройками.
func (a *App) restartCore() {
	a.restarter.Run(func() {
		a.core.Stop()
		if err := a.connect(); err != nil {
			a.mon.Event("error", "Перезапуск не удался: "+firstLine(err.Error()), true)
		}
	})
	if a.ui != nil {
		a.ui.Refresh()
	}
}

func (a *App) isRestarting() bool { return a.restarter.Busy() }

// serialRunner выполняет задачу по одной. Просьбы, пришедшие во время выполнения,
// сливаются в один повтор после него.
type serialRunner struct {
	mu      sync.Mutex
	running bool
	again   bool
}

func (r *serialRunner) Run(f func()) {
	r.mu.Lock()
	if r.running {
		r.again = true
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	for {
		f()
		r.mu.Lock()
		if r.again {
			r.again = false
			r.mu.Unlock()
			continue
		}
		r.running = false
		r.mu.Unlock()
		return
	}
}

func (r *serialRunner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (a *App) fillEnv(s *Settings, env *ConfigEnv) {
	env.ZapretActive = a.zapret.active(s)
	if env.ZapretActive && s.Zapret.RouteDirect {
		env.ZapretDomains = a.zapret.Domains(s)
	}
	env.TgWsProxy = s.Telegram.Enabled
	a.fillLists(s, env, env.APIPort > 0)
	env.WLServers = a.wl.Servers(s)
	a.wl.markApplied(env.WLServers)
	// серверы xHTTP (свои и из подписки) работают через Xray
	xs := map[string]Server{}
	for _, sv := range s.Servers {
		if sv.Type == "vless" && sv.Transport == "xhttp" {
			xs[serverTag(sv)] = sv
		}
	}
	for _, sv := range env.WLServers {
		if sv.Transport == "xhttp" {
			xs["w-"+sv.ID] = sv
		}
	}
	if env.APIPort > 0 {
		env.XrayPorts = a.xray.Sync(xs)
	} else if a.xray.Present() { // превью конфига: Xray не запускаем, порты условные
		env.XrayPorts = map[string]int{}
		i := 0
		for t := range xs {
			env.XrayPorts[t] = 20000 + i
			i++
		}
	}
	if env.APIPort > 0 { // в превью конфига и в профиле телефона служебные входы не нужны
		probes := map[string]int{}
		tags := []string{}
		for _, sv := range s.Servers {
			if sv.Type == "naive" && env.NoNaive {
				continue
			}
			tags = append(tags, serverTag(sv))
		}
		if len(env.WLServers) > 0 {
			tags = append(tags, "wl")
		}
		for _, t := range tags {
			if p, err := freePort(); err == nil {
				probes[t] = p
			}
		}
		env.ServerProbes = probes
		a.probeMu.Lock()
		a.probes = probes
		a.probeMu.Unlock()
	}
}

func (a *App) serverProbes() map[string]int {
	if !a.core.Running() {
		return nil
	}
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	out := map[string]int{}
	for k, v := range a.probes {
		out[k] = v
	}
	return out
}

// startAddons — при запуске Naivka поднимаем включённые дополнения.
func (a *App) startAddons() {
	s := a.settingsCopy()
	if s.Zapret.Enabled && a.zapret.root() != "" && !a.zapret.Running() {
		if err := a.zapret.Start(s); err != nil {
			a.mon.Event("warn", "zapret не запустился: "+firstLine(err.Error()), false)
		}
	}
	if s.Telegram.Enabled && a.tgws.snapshot().Installed != "" && !a.tgws.Running() {
		if err := a.tgws.Start(s); err != nil {
			a.mon.Event("warn", "tg-ws-proxy не запустился: "+firstLine(err.Error()), false)
		}
	}
}

func zapretRules(s *Settings) string {
	var out []string
	for _, r := range s.Rules {
		if r.Action == "zapret" {
			out = append(out, r.Value)
		}
	}
	return strings.Join(out, ",")
}

// applyAddons — реакция на изменение настроек: включить, выключить или перезапустить.
func (a *App) applyAddons(prev, ns *Settings) {
	pz, nz := prev.Zapret, ns.Zapret
	switch {
	case a.zapret.TrialActive():
		// во время ручной проверки zapret управляет она
	case nz.Enabled && !pz.Enabled:
		if err := a.zapret.Start(ns); err != nil {
			a.mon.Event("error", "zapret: "+firstLine(err.Error()), false)
		} else {
			a.mon.Event("ok", "zapret включён: стратегия «"+nz.Strategy+"»", false)
		}
	case !nz.Enabled && pz.Enabled:
		a.zapret.Stop()
		a.mon.Event("info", "zapret выключен", false)
	case nz.Enabled && a.zapret.Running() && (nz.Strategy != pz.Strategy || nz.GameFilter != pz.GameFilter ||
		nz.IPSet != pz.IPSet || zapretRules(prev) != zapretRules(ns)):
		if err := a.zapret.Start(ns); err != nil {
			a.mon.Event("error", "zapret: "+firstLine(err.Error()), false)
		}
	}
	pt, nt := prev.Telegram, ns.Telegram
	switch {
	case nt.Enabled && (!pt.Enabled || (a.tgws.Running() && (nt.Port != pt.Port || nt.Secret != pt.Secret))):
		if err := a.tgws.Start(ns); err != nil {
			a.mon.Event("error", "tg-ws-proxy: "+firstLine(err.Error()), false)
		} else {
			a.mon.Event("ok", "tg-ws-proxy запущен на 127.0.0.1:"+fmt.Sprint(nt.Port), false)
		}
	case !nt.Enabled && pt.Enabled:
		a.tgws.Stop()
	}
}
