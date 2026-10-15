package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Отчёт о работе — один текстовый файл, который пользователь может прислать, когда связь
// работает плохо: состояние, мешающие программы, настройки без секретов, события и журнал.
// Пароли, ключи, адреса серверов, ссылки и списки сайтов в него не попадают.

// reportKeepKeys — строковые настройки, которые ничего не раскрывают и нужны для разбора.
var reportKeepKeys = map[string]bool{
	"mode": true, "routing": true, "theme": true, "lang": true, "nav": true, "type": true, "transport": true,
	"security": true, "flow": true, "fingerprint": true, "xmode": true, "strategy": true, "testInterval": true,
	"logLevel": true, "action": true, "kind": true, "network": true, "gameFilter": true, "ipset": true, "selected": true,
	"congestion": true, "method": true, "stack": true, "accent": true, "format": true, "dnsDirect": true, "dnsRemote": true,
}

// redactSettings — настройки, где от строк остаётся только «есть/нет», кроме безопасных полей.
func redactSettings(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = redactSettings(val, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactSettings(val, key)
		}
		return out
	case string:
		if x == "" || reportKeepKeys[key] {
			return x
		}
		return "•••"
	}
	return v
}

// maskHosts заменяет адреса серверов в журнале на «сервер-N».
func maskHosts(text string, hosts []string) string {
	sort.Slice(hosts, func(i, j int) bool { return len(hosts[i]) > len(hosts[j]) })
	for i, h := range hosts {
		if len(h) >= 4 {
			text = strings.ReplaceAll(text, h, fmt.Sprintf("сервер-%d", i+1))
		}
	}
	return text
}

func (a *App) buildReport() string {
	s := a.settingsCopy()
	st := a.core.Status()
	var b strings.Builder
	line := func(f string, args ...any) { fmt.Fprintf(&b, f+"\n", args...) }

	line("Отчёт Naivka %s, %s", appVersion, time.Now().Format("2006-01-02 15:04:05 -0700"))
	line("Система: %s/%s, Windows 7/8: %v, права администратора: %v, портативная: %v", runtime.GOOS, runtime.GOARCH, legacyWindows(), isAdmin(), portable)
	line("")
	line("== Состояние ==")
	line("Ядро: работает %v, запускается %v, заблокировано защитой %v, режим %q, восстанавливаю %v", st.Running, st.Starting, st.Blocked, st.Mode, a.life.trying.Load() > 0)
	if st.Error != "" {
		line("Последняя ошибка: %s", firstLine(st.Error))
	}
	line("Ядро sing-box: %s", st.Info.Version)
	zv := a.zapret.View()
	line("zapret: включён %v, работает %v, стратегия %q, не справляется %v", s.Zapret.Enabled, zv.Running, zv.Strategy, zv.Degraded)
	line("Telegram (tg-ws-proxy): включён %v, работает %v", s.Telegram.Enabled, a.tgws.Running())
	line("Xray (xHTTP): работает %v", a.xray.Running())
	line("Запасной DNS вместо прямого: %v", a.dnsg.fallback.Load())
	if from, to := a.fo.get(); from != "" {
		line("Временная замена сервера: %s → %s", a.serverName(from), a.serverName(to))
	}
	if p := a.core.SystemProxyPort(); p > 0 {
		ours, _ := systemProxyOurs(p)
		line("Системный прокси указывает на Naivka: %v", ours)
	}
	line("")
	line("== Мешающие программы ==")
	if cs := a.scanConflicts(); len(cs) == 0 {
		line("не найдено")
	} else {
		for _, c := range cs {
			line("- %s", c.Name)
		}
	}
	line("")
	line("== Серверы ==")
	for _, sv := range s.Servers {
		if sv.Transport != "" {
			line("- %s (%s, %s)", protoTitle(sv), sv.Type, sv.Transport)
		} else {
			line("- %s (%s)", protoTitle(sv), sv.Type)
		}
	}
	line("")
	line("== Настройки (без паролей, адресов и списков сайтов) ==")
	var raw any
	sb, _ := json.Marshal(s)
	_ = json.Unmarshal(sb, &raw)
	if m, ok := raw.(map[string]any); ok {
		delete(m, "servers") // уже выше, без адресов
		delete(m, "window")
	}
	pretty, _ := json.MarshalIndent(redactSettings(raw, ""), "", "  ")
	b.Write(pretty)
	line("")
	line("")

	hosts := []string{}
	for _, sv := range s.Servers {
		hosts = append(hosts, sv.Host, sv.SNI)
	}
	for _, sv := range a.wl.Servers(s) {
		hosts = append(hosts, sv.Host, sv.SNI)
	}
	line("== События ==")
	a.mon.mu.Lock()
	events := append([]Event(nil), a.mon.events...)
	a.mon.mu.Unlock()
	if len(events) > 100 {
		events = events[len(events)-100:]
	}
	var ev strings.Builder
	for _, e := range events {
		fmt.Fprintf(&ev, "%s [%s] %s\n", time.Unix(e.T, 0).Format("01-02 15:04:05"), e.Kind, e.Text)
	}
	b.WriteString(maskHosts(ev.String(), hosts))
	line("")
	line("== Журнал (последние 400 строк) ==")
	b.WriteString(maskHosts(a.core.logs.Tail(400), hosts))
	return b.String()
}
