package main

import (
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Запасной DNS. «DNS напрямую» по умолчанию — DoH Яндекса (77.88.8.1:443). Если провайдер
// или чужая программа начинает рвать эти соединения, перестают узнаваться адреса вообще всех
// сайтов (через этот DNS узнаётся и адрес самого сервера) — со стороны это выглядит как пропавший
// интернет. Тогда Naivka переходит на обычный DNS (UDP) и перезапускает ядро, а к выбранному
// в настройках возвращается при следующем подключении.

type dnsGuard struct {
	fallback atomic.Bool
}

// dnsFallbackAddr — обычный DNS вместо DoH/DoT: тот же адрес, если он задан IP, иначе Яндекс.
func dnsFallbackAddr(d directDNS) string {
	if isIP(d.Host) {
		return d.Host
	}
	return "77.88.8.8"
}

// dnsFailLine — строка журнала про сорвавшийся запрос к прямому DNS.
// host — адрес DoH/DoT-сервера из настроек (если задан IP).
func dnsFailLine(l, host string) bool {
	if strings.Contains(l, "process DNS packet: unexpected EOF") {
		return true
	}
	if strings.Contains(l, "outbound/direct[") && strings.Contains(l, ": lookup ") &&
		(strings.Contains(l, "unexpected EOF") || strings.Contains(l, "forcibly closed") || strings.Contains(l, "connection reset")) {
		return true
	}
	return host != "" && isIP(host) && strings.Contains(l, "->"+host+":") &&
		(strings.Contains(l, "forcibly closed") || strings.Contains(l, "connection reset"))
}

// dnsWatch раз в 5 секунд смотрит журнал ядра: 12 и больше сорвавшихся DNS-запросов за минуту —
// прямой DNS не работает, переходим на запасной.
func (a *App) dnsWatch() {
	const window, limit = time.Minute, 12
	var seq int64
	var fails []time.Time
	for {
		time.Sleep(5 * time.Second)
		lines := a.core.logs.After(seq)
		if len(lines) > 0 {
			seq = lines[len(lines)-1].Seq
		}
		s := a.settingsCopy()
		dd, err := parseDirectDNS(s.DNSDirect)
		// Во время подбора стратегии zapret перезапускает winws — обрывы ожидаемы, а перезапуск
		// ядра посреди подбора испортил бы его результат.
		if err != nil || !dd.Encrypted() || a.dnsg.fallback.Load() || !a.core.Running() ||
			a.mon.SinceWake() < wakeGrace || a.zapret.busy() {
			fails = nil
			continue
		}
		now := time.Now()
		for _, l := range lines {
			if dnsFailLine(l.Text, dd.Host) {
				fails = append(fails, now)
			}
		}
		keep := fails[:0]
		for _, t := range fails {
			if now.Sub(t) < window {
				keep = append(keep, t)
			}
		}
		fails = keep
		if len(fails) < limit {
			continue
		}
		fails = nil
		// Обрывы бывают и когда пропал сам интернет или DoH лишь на миг моргнул (сменилась сеть).
		// Переходим на запасной, только если интернет есть, а DoH из настроек правда не отвечает.
		if !a.internetUp() || a.dohWorks(dd) {
			continue
		}
		a.dnsg.fallback.Store(true)
		a.mon.Event("warn", "DNS напрямую ("+dd.Host+") перестал отвечать — временно перешёл на обычный DNS "+dnsFallbackAddr(dd)+". Вернусь к нему при следующем подключении", true)
		a.restartCore()
	}
}

// ---------- DNS Google для YouTube ----------
// DNS Яндекса на часть видеосерверов YouTube (rr…---sn-….googlevideo.com) отвечает «нет такого
// адреса» — плеер перебирает серверы, видео подвисает. Поэтому, пока YouTube идёт через zapret,
// его адреса узнаём у DNS Google (DoH 8.8.8.8, напрямую). Не ответил при подключении или начал
// сбоить — возвращаемся к DNS из настроек до следующего запуска Naivka.

type youtubeDNS struct {
	mu      sync.Mutex
	checked time.Time
	ok      bool
	off     atomic.Bool
	active  atomic.Bool
}

// usable — можно ли сейчас пользоваться DNS Google (проверка не чаще раза в 10 минут).
func (y *youtubeDNS) usable() bool {
	if y.off.Load() {
		y.active.Store(false)
		return false
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if time.Since(y.checked) > 10*time.Minute {
		y.ok = googleDoHWorks()
		y.checked = time.Now()
	}
	y.active.Store(y.ok)
	return y.ok
}

// googleDoHWorks — пробный запрос к DoH Google напрямую, мимо системного прокси.
func googleDoHWorks() bool {
	hc := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	req, err := http.NewRequest("GET", "https://"+googleDNSIP+"/dns-query?dns="+dnsQuery("www.youtube.com"), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/dns-message")
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

var ytLookupFail = regexp.MustCompile(`lookup [^:\s]*(googlevideo\.com|youtube\.com|ytimg\.com|ggpht\.com|youtu\.be)[^:\s]*: .*(unexpected EOF|timeout|forcibly closed|connection reset|connection refused|no such host)`)

// youtubeDNSWatch: 8 и больше сорвавшихся запросов адресов YouTube за минуту при живом интернете —
// DNS Google у этого провайдера не работает, возвращаемся к DNS из настроек.
func (a *App) youtubeDNSWatch() {
	var seq int64
	var fails []time.Time
	for {
		time.Sleep(10 * time.Second)
		lines := a.core.logs.After(seq)
		if len(lines) > 0 {
			seq = lines[len(lines)-1].Seq
		}
		if !a.ytdns.active.Load() || a.ytdns.off.Load() || !a.core.Running() {
			fails = nil
			continue
		}
		now := time.Now()
		for _, l := range lines {
			if ytLookupFail.MatchString(l.Text) {
				fails = append(fails, now)
			}
		}
		keep := fails[:0]
		for _, t := range fails {
			if now.Sub(t) < time.Minute {
				keep = append(keep, t)
			}
		}
		fails = keep
		if len(fails) < 8 || !a.internetUp() {
			continue
		}
		fails = nil
		a.ytdns.off.Store(true)
		a.mon.Event("warn", "DNS Google для YouTube не отвечает — адреса YouTube снова узнаю у DNS из настроек", false)
		a.restartCore()
	}
}
