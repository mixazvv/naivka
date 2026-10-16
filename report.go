package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
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
	"congestion": true, "method": true, "stack": true, "accent": true, "format": true,
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

// Отчёт отправляют автору, поэтому из журнала и событий убирается всё, что говорит
// о человеке: какие сайты он открывал, IP-адреса, адреса его серверов, имя пользователя Windows.
// Остаются только домены служб, по которым разбирают сбои (YouTube, Discord, Telegram, проверки связи),
// и их поддомены сводятся к «*.домен».

// reportKeepDomains — службы, которые нужны для разбора сбоев.
var reportKeepDomains = map[string]bool{
	"googlevideo.com": true, "youtube.com": true, "ytimg.com": true, "ggpht.com": true, "youtu.be": true,
	"google.com": true, "gstatic.com": true, "googleapis.com": true, "googleusercontent.com": true,
	"discord.com": true, "discord.gg": true, "discordapp.com": true, "discordapp.net": true, "discord.media": true,
	"telegram.org": true, "t.me": true, "github.com": true, "githubusercontent.com": true,
	"cloudflare.com": true, "cloudflare-dns.com": true, "one.one.one.one": true, "dns.google": true,
	"yandex.net": true, "yandex.ru": true, "ipwho.is": true, "ipinfo.io": true,
}

// reportKeepIPs — общеизвестные адреса (публичные DNS и проверки связи): по ним видно, что сломалось.
var reportKeepIPs = map[string]bool{
	"1.1.1.1": true, "1.0.0.1": true, "8.8.8.8": true, "8.8.4.4": true, "77.88.8.8": true, "77.88.8.1": true,
	"77.88.55.242": true, "5.255.255.242": true, "9.9.9.9": true, "127.0.0.1": true, "0.0.0.0": true, "172.19.0.1": true,
}

var (
	reDomain = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:xn--[a-z0-9-]{1,59}|[a-z]{2,24})\b`)
	reIPv4   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reIPv6   = regexp.MustCompile(`(?i)\[?[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}\]?`)
	// Не домены, а имена файлов: «config.json», «sing-box.exe».
	reportFileExt = map[string]bool{"exe": true, "json": true, "log": true, "bat": true, "dll": true, "txt": true, "zip": true,
		"db": true, "srs": true, "go": true, "sys": true, "dat": true, "bin": true, "cmd": true, "xml": true, "yaml": true, "js": true}
)

// baseOf — основной домен: «rr5---sn-x.googlevideo.com» → «googlevideo.com», «a.b.co.uk» → «b.co.uk».
func baseOf(host string) string {
	parts := strings.Split(strings.ToLower(host), ".")
	n := 2
	if len(parts) >= 3 && len(parts[len(parts)-1]) == 2 {
		switch parts[len(parts)-2] {
		case "co", "com", "org", "net", "ac", "gov", "edu":
			n = 3
		}
	}
	if len(parts) <= n {
		return strings.Join(parts, ".")
	}
	return strings.Join(parts[len(parts)-n:], ".")
}

// redactReport убирает из текста личное. labels — точные строки (адреса серверов и их IP) → «сервер-N».
func redactReport(text string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if len(k) >= 4 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		text = strings.ReplaceAll(text, k, labels[k])
	}
	// Имя пользователя Windows в путях к файлам.
	if home, err := os.UserHomeDir(); err == nil && len(home) > 3 {
		text = replaceFold(text, home, "%USERPROFILE%")
	}
	if u := os.Getenv("USERNAME"); len(u) >= 3 {
		text = replaceFold(text, `\Users\`+u+`\`, `\Users\%USERNAME%\`)
	}
	text = reIPv4.ReplaceAllStringFunc(text, func(ip string) string {
		p := net.ParseIP(ip)
		switch {
		case p == nil || reportKeepIPs[ip]:
			return ip
		case p.IsPrivate() || p.IsLoopback():
			return "локальный-IP"
		}
		return "IP"
	})
	text = reIPv6.ReplaceAllStringFunc(text, func(m string) string {
		ip := strings.Trim(m, "[]")
		if strings.Count(ip, ":") < 2 || net.ParseIP(ip) == nil || ip == "::1" || ip == "::" {
			return m // время «18:53:59» и прочее, что не адрес
		}
		return "IPv6"
	})
	text = reDomain.ReplaceAllStringFunc(text, func(d string) string {
		low := strings.ToLower(d)
		tld := low[strings.LastIndexByte(low, '.')+1:]
		if reportFileExt[tld] {
			return d
		}
		base := baseOf(low)
		if reportKeepDomains[base] {
			if base == low {
				return low
			}
			return "*." + base
		}
		return "•••." + tld
	})
	return text
}

func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(old))
	return re.ReplaceAllLiteralString(s, repl)
}

// reportLogLines — журнал без строк автоопределения: в них по сайту на строку, то есть история посещений.
func reportLogLines(text string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(text, "\n") {
		if strings.Contains(l, "] автоопределение: ") && !strings.Contains(l, "не удалось") {
			continue
		}
		b.WriteString(l)
	}
	return b.String()
}

// serverLabels — адреса серверов (и их IP, как их видит DNS) → «сервер-N».
func serverLabels(servers []Server) map[string]string {
	labels := map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i, sv := range servers {
		l := fmt.Sprintf("сервер-%d", i+1)
		for _, h := range []string{sv.Host, sv.SNI} {
			if h == "" {
				continue
			}
			labels[h] = l
			if net.ParseIP(h) == nil {
				if ips, err := net.DefaultResolver.LookupHost(ctx, h); err == nil {
					for _, ip := range ips {
						labels[ip] = l
					}
				}
			}
		}
	}
	return labels
}

// dnsLabel — DNS из настроек показываем, только если это известный общий сервис: свой адрес DoH
// (например, личный NextDNS) может указывать на человека.
func dnsLabel(v string) string {
	low := strings.ToLower(v)
	for _, known := range []string{"yandex", "cloudflare", "1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "dns.google", "adguard", "quad9", "9.9.9.9", "77.88.8."} {
		if strings.Contains(low, known) {
			return v
		}
	}
	if v == "" {
		return ""
	}
	return "свой (•••)"
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
		line("Последняя ошибка: %s", redactReport(firstLine(st.Error), serverLabels(s.Servers)))
	}
	line("Ядро sing-box: %s", st.Info.Version)
	zv := a.zapret.View()
	line("zapret: включён %v, работает %v, стратегия %q, не справляется %v", s.Zapret.Enabled, zv.Running, zv.Strategy, zv.Degraded)
	line("Telegram (tg-ws-proxy): включён %v, работает %v", s.Telegram.Enabled, a.tgws.Running())
	line("Xray (xHTTP): работает %v", a.xray.Running())
	line("Запасной DNS вместо прямого: %v", a.dnsg.fallback.Load())
	line("DNS Google для YouTube: %v", a.ytdns.active.Load() && !a.ytdns.off.Load())
	if sv := a.smart.View(); smartOn(s) {
		line("Автоопределение: напрямую %d сайтов, через сервер %d", sv.Direct, sv.Proxy)
	}
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
	red := redactSettings(raw, "")
	if m, ok := red.(map[string]any); ok {
		m["dnsDirect"], m["dnsRemote"] = dnsLabel(s.DNSDirect), dnsLabel(s.DNSRemote)
	}
	pretty, _ := json.MarshalIndent(red, "", "  ")
	b.Write(pretty)
	line("")
	line("")

	labels := serverLabels(append(append([]Server(nil), s.Servers...), a.wl.Servers(s)...))
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
	b.WriteString(redactReport(reportLogLines(ev.String()), labels))
	line("")
	line("== Журнал (последние 400 строк, без адресов сайтов и IP) ==")
	b.WriteString(redactReport(reportLogLines(a.core.logs.Tail(400)), labels))
	return b.String()
}
