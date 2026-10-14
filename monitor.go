package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Event struct {
	T    int64  `json:"t"`
	Kind string `json:"kind"` // ok | info | warn | error
	Text string `json:"text"`
}

type Sample struct {
	T          int64   `json:"t"`
	ProxyDown  float64 `json:"pd"`
	ProxyUp    float64 `json:"pu"`
	DirectDown float64 `json:"dd"`
	DirectUp   float64 `json:"du"`
	ZapretDown float64 `json:"zd"`
	ZapretUp   float64 `json:"zu"`
	TgDown     float64 `json:"td"`
	TgUp       float64 `json:"tu"`
}

type TopItem struct {
	Host  string  `json:"host"`
	Proc  string  `json:"proc"`
	Route string  `json:"route"` // тег outbound или direct
	Speed float64 `json:"speed"` // байт/с за последнюю секунду
	Total int64   `json:"total"`
	Conns int     `json:"conns"`
}

type DelayPoint struct {
	T  int64 `json:"t"`
	Ms int   `json:"ms"` // 0 — нет ответа
}

type connSnap struct {
	down, up int64
	direct   bool
}

type Monitor struct {
	a *App

	mu       sync.Mutex
	events   []Event
	samples  []Sample
	last     map[string]connSnap
	totals   [8]int64 // сервер ↓↑, напрямую ↓↑, zapret ↓↑, Telegram ↓↑
	zset     map[string]bool
	procSeen map[string]string // программы, выходившие в сеть: имя → путь
	zsetAt   time.Time
	tgLast   [2]int64
	tgInit   bool
	top      []TopItem
	ruleHits map[string]int // строка правила sing-box → число соединений
	userHits map[string]int // kind|value своего правила → число соединений
	delays   map[string][]DelayPoint
	session  int64
	wakeAt   time.Time // когда компьютер последний раз проснулся
	lastAuto string
	health   string // ok | fail | ""
	fails    int
	lastNet  uint64
	restarts []time.Time
}

func newMonitor(a *App) *Monitor {
	return &Monitor{a: a, last: map[string]connSnap{}, ruleHits: map[string]int{}, userHits: map[string]int{}, delays: map[string][]DelayPoint{}}
}

// Event добавляет запись в ленту; notify — показать уведомление Windows.
func (m *Monitor) Event(kind, text string, notify bool) {
	m.mu.Lock()
	m.events = append(m.events, Event{T: time.Now().Unix(), Kind: kind, Text: text})
	if len(m.events) > 200 {
		m.events = m.events[len(m.events)-200:]
	}
	m.mu.Unlock()
	m.a.core.logs.Add("[naivka] " + text)
	if notify {
		m.a.notify(text, kind == "error" || kind == "warn")
	}
}

func (m *Monitor) resetSession() {
	m.mu.Lock()
	m.samples = nil
	m.last = map[string]connSnap{}
	m.totals = [8]int64{}
	m.tgInit = false
	m.top = nil
	m.ruleHits = map[string]int{}
	m.userHits = map[string]int{}
	m.session = time.Now().Unix()
	m.lastAuto = ""
	m.health = ""
	m.fails = 0
	m.mu.Unlock()
}

func (m *Monitor) run() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var n int
	lastTick := time.Now()
	wasRunning := false
	for range tick.C {
		n++
		now := time.Now()
		gap := now.Sub(lastTick)
		lastTick = now
		if n%60 == 0 && m.a.traffic != nil {
			m.a.traffic.checkLimit(now, m.a.settingsCopy(), func(kind, text string) { m.Event(kind, text, true) })
			m.a.traffic.save(false)
		}
		running := m.a.core.Running()
		if running && !wasRunning {
			m.resetSession()
		}
		wasRunning = running
		if !running {
			m.sample(nil) // только Telegram: tg-ws-proxy работает и без сервера
			continue
		}
		cl := m.a.core.Clash()
		if cl == nil {
			continue
		}
		m.sample(cl)
		if n%5 == 0 {
			m.checkAuto(cl)
			m.checkNetwork(cl)
			m.a.guardSystemProxy()
		}
		if n%60 == 0 {
			m.recordDelays(cl)
			go m.a.failback(cl)
		}
		if n%120 == 0 {
			go m.a.wl.checkOwn(cl)
		}
		s := m.a.settingsCopy()
		// Сон: часы прыгнули вперёд — старые соединения мертвы.
		if gap > 20*time.Second {
			m.mu.Lock()
			m.wakeAt, m.fails = now, 0
			m.mu.Unlock()
			m.Event("info", fmt.Sprintf("Компьютер проснулся (пауза %s) — обновляю соединения", gap.Round(time.Second)), false)
			_ = cl.CloseAll()
			continue
		}
		if s.Watchdog && n%20 == 0 {
			go m.healthCheck(s, false)
		}
	}
}

func classify(chains []string) (route string, direct bool) {
	for _, c := range chains {
		if strings.HasPrefix(c, "s-") {
			return c, false
		}
	}
	for _, c := range chains {
		if c == tagDirect {
			return tagDirect, true
		}
		if c == tagTwitch {
			return tagTwitch, false
		}
	}
	if len(chains) > 0 {
		return chains[0], false
	}
	return "", false
}

// zapretMatch: идёт ли соединение к сайту, который обходит zapret.
func (m *Monitor) zapretMatch(host string) bool {
	if len(m.zset) == 0 || host == "" {
		return false
	}
	host = strings.ToLower(host)
	for {
		if m.zset[host] {
			return true
		}
		i := strings.IndexByte(host, '.')
		if i < 0 {
			return false
		}
		host = host[i+1:]
	}
}

func (m *Monitor) refreshZapretSet(s *Settings) {
	if time.Since(m.zsetAt) < 10*time.Second {
		return
	}
	m.zsetAt = time.Now()
	m.zset = nil
	if m.a.zapret == nil || !m.a.zapret.active(s) {
		return
	}
	set := map[string]bool{}
	if s.Zapret.RouteDirect {
		for _, d := range m.a.zapret.Domains(s) {
			set[d] = true
		}
	}
	for _, r := range s.Rules {
		if r.Action == "zapret" && r.Kind == "domain" {
			set[strings.ToLower(r.Value)] = true
		}
	}
	m.zset = set
}

// trafficServerKey — под каким ключом истории считать трафик через сервер.
func trafficServerKey(route string) string {
	switch {
	case strings.HasPrefix(route, "s-"), route == tagTwitch:
		return route
	case route == "wl", strings.HasPrefix(route, "w-"):
		return "wl"
	}
	return ""
}

func isTgWsProc(proc string) bool {
	return strings.HasPrefix(strings.ToLower(proc), "tgwsproxy")
}

func (m *Monitor) sample(cl *Clash) {
	v := &ConnsView{}
	if cl != nil {
		var err error
		if v, err = cl.ConnectionsRaw(); err != nil {
			return
		}
	}
	s := m.a.settingsCopy()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshZapretSet(s)
	var d [8]int64
	now := time.Now()
	seen := make(map[string]bool, len(v.Connections))
	agg := map[string]*TopItem{}
	for _, c := range v.Connections {
		seen[c.ID] = true
		route, direct := classify(c.Chains)
		prev, existed := m.last[c.ID]
		dd, du := c.Download-prev.down, c.Upload-prev.up
		if dd < 0 || du < 0 {
			dd, du = c.Download, c.Upload
		}
		m.last[c.ID] = connSnap{down: c.Download, up: c.Upload, direct: direct}
		if !existed {
			m.countHit(c, s)
		}
		proc := filepath.Base(strings.ReplaceAll(c.Metadata.ProcessPath, `\`, "/"))
		if proc == "." || proc == "/" {
			proc = ""
		}
		if proc != "" {
			if m.procSeen == nil {
				m.procSeen = map[string]string{}
			}
			if len(m.procSeen) < 500 {
				m.procSeen[proc] = c.Metadata.ProcessPath
			}
		}
		if isTgWsProc(proc) {
			continue // трафик Telegram считает посредник — не учитываем дважды
		}
		host := c.Metadata.Host
		switch {
		case direct && m.zapretMatch(host):
			route = "zapret"
			d[4] += dd
			d[5] += du
		case direct:
			d[2] += dd
			d[3] += du
		default:
			d[0] += dd
			d[1] += du
			if k := trafficServerKey(route); k != "" && m.a.traffic != nil {
				m.a.traffic.add(now, k, dd, du)
			}
		}
		if host == "" {
			host = c.Metadata.DestIP
		}
		key := host + "|" + proc + "|" + route
		it := agg[key]
		if it == nil {
			it = &TopItem{Host: host, Proc: proc, Route: route}
			agg[key] = it
		}
		it.Speed += float64(dd + du)
		it.Total += c.Download + c.Upload
		it.Conns++
	}
	for id := range m.last {
		if !seen[id] {
			delete(m.last, id)
		}
	}
	// Telegram — по счётчикам посредника.
	if m.a.tgws != nil {
		tdn, tup, tconns := m.a.tgws.Traffic()
		if m.tgInit {
			d[6], d[7] = tdn-m.tgLast[0], tup-m.tgLast[1]
			if d[6] < 0 || d[7] < 0 {
				d[6], d[7] = 0, 0
			}
		}
		m.tgLast, m.tgInit = [2]int64{tdn, tup}, true
		if tconns > 0 || d[6]+d[7] > 0 {
			agg["telegram"] = &TopItem{Host: "Telegram", Proc: "tg-ws-proxy", Route: "telegram",
				Speed: float64(d[6] + d[7]), Total: m.totals[6] + m.totals[7] + d[6] + d[7], Conns: int(tconns)}
		}
	}
	for i := range d {
		m.totals[i] += d[i]
	}
	if m.a.traffic != nil {
		for i, k := range []string{"proxy", "direct", "zapret", "telegram"} {
			m.a.traffic.add(now, k, d[2*i], d[2*i+1])
		}
	}
	m.samples = append(m.samples, Sample{T: time.Now().Unix(),
		ProxyDown: float64(d[0]), ProxyUp: float64(d[1]), DirectDown: float64(d[2]), DirectUp: float64(d[3]),
		ZapretDown: float64(d[4]), ZapretUp: float64(d[5]), TgDown: float64(d[6]), TgUp: float64(d[7])})
	if len(m.samples) > 300 {
		m.samples = m.samples[len(m.samples)-300:]
	}
	top := make([]TopItem, 0, len(agg))
	for _, it := range agg {
		top = append(top, *it)
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Speed != top[j].Speed {
			return top[i].Speed > top[j].Speed
		}
		return top[i].Total > top[j].Total
	})
	if len(top) > 8 {
		top = top[:8]
	}
	m.top = top
}

// countHit: какое правило сработало для нового соединения.
func (m *Monitor) countHit(c Conn, s *Settings) {
	if c.Rule != "" {
		m.ruleHits[c.Rule]++
	} else {
		m.ruleHits["final"]++
	}
	host := strings.ToLower(c.Metadata.Host)
	ip := net.ParseIP(c.Metadata.DestIP)
	proc := strings.ToLower(filepath.Base(strings.ReplaceAll(c.Metadata.ProcessPath, `\`, "/")))
	for _, r := range s.Rules {
		v := strings.ToLower(r.Value)
		if !strings.Contains(strings.ToLower(c.Rule), v) {
			continue
		}
		hit := false
		switch r.Kind {
		case "domain":
			hit = host == v || strings.HasSuffix(host, "."+v)
		case "process":
			hit = proc == v
		case "ip":
			if ip != nil {
				if _, n, err := net.ParseCIDR(r.Value); err == nil {
					hit = n.Contains(ip)
				} else {
					hit = ip.Equal(net.ParseIP(r.Value))
				}
			}
		}
		if hit {
			m.userHits[r.Kind+"|"+v]++
			return
		}
	}
}

func (m *Monitor) checkAuto(cl *Clash) {
	px, err := cl.Proxies()
	if err != nil || px.AutoNow == "" {
		return
	}
	m.mu.Lock()
	prev := m.lastAuto
	m.lastAuto = px.AutoNow
	m.mu.Unlock()
	if prev != "" && prev != px.AutoNow && px.Selected == tagAuto {
		m.Event("info", fmt.Sprintf("Автовыбор переключился: %s → %s", m.a.serverName(prev), m.a.serverName(px.AutoNow)), true)
	}
}

func (m *Monitor) recordDelays(cl *Clash) {
	px, err := cl.Proxies()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	for tag, ms := range px.Delays {
		id := strings.TrimPrefix(tag, "s-")
		pts := append(m.delays[id], DelayPoint{T: now, Ms: ms})
		if len(pts) > 60 {
			pts = pts[len(pts)-60:]
		}
		m.delays[id] = pts
	}
}

// checkNetwork: сменилась сеть (Wi-Fi ↔ кабель ↔ телефон) — старые соединения висят, закрываем.
func (m *Monitor) checkNetwork(cl *Clash) {
	ifs, err := net.Interfaces()
	if err != nil {
		return
	}
	h := fnv.New64a()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || strings.Contains(strings.ToLower(ifc.Name), "naivka") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			h.Write([]byte(ifc.Name + a.String()))
		}
	}
	sum := h.Sum64()
	m.mu.Lock()
	prev := m.lastNet
	m.lastNet = sum
	m.mu.Unlock()
	if prev != 0 && prev != sum {
		m.Event("info", "Сеть изменилась — переподключаю соединения", false)
		_ = cl.CloseAll()
	}
}

// healthCheck: запрос к адресу проверки строго через сервер.
// Две неудачи подряд — закрываем зависшие соединения, четыре — перезапускаем ядро.
// spareTestURL — второй адрес для проверки связи: сбоем считаем, только когда не открылись оба,
// чтобы недоступность одного сайта не рвала соединения и не перезапускала ядро зря.
func spareTestURL(main string) string {
	if strings.Contains(main, "cloudflare") {
		return "https://www.gstatic.com/generate_204"
	}
	return "https://cp.cloudflare.com/generate_204"
}

func probeVia(port int, u string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return false
	}
	resp, err := geoClient(port).Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	return err == nil && resp.StatusCode < 500
}

func (m *Monitor) healthCheck(s *Settings, force bool) {
	_, proxyPort := m.a.core.ProbePorts()
	if proxyPort == 0 {
		return
	}
	ok := probeVia(proxyPort, s.TestURL) || probeVia(proxyPort, spareTestURL(s.TestURL))
	m.mu.Lock()
	// Первую минуту-полторы после сна сеть ещё поднимается — сбои не считаем, чтобы не дёргать ядро зря.
	if !ok && time.Since(m.wakeAt) < wakeGrace {
		m.mu.Unlock()
		return
	}
	prev := m.health
	if ok {
		m.health, m.fails = "ok", 0
	} else {
		m.health = "fail"
		m.fails++
	}
	fails := m.fails
	m.mu.Unlock()

	switch {
	case ok && prev == "fail":
		m.Event("ok", "Связь через сервер восстановлена", true)
	case !ok && fails == 2:
		// Сначала — другой свой сервер, если выбран конкретный; нет живых — подписка «белые списки».
		if m.a.failover(s) {
			m.mu.Lock()
			m.fails = 0
			m.mu.Unlock()
			return
		}
		m.Event("warn", "Нет связи через сервер — переподключаю соединения", true)
		if cl := m.a.core.Clash(); cl != nil {
			_ = cl.CloseAll()
		}
		go m.a.wl.engage()
	case !ok && fails == 4 && m.allowRestart():
		m.Event("warn", "Связь не вернулась — перезапускаю ядро", false)
		go m.a.restartCore()
	}
}

func (m *Monitor) allowRestart() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	keep := m.restarts[:0]
	for _, t := range m.restarts {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	m.restarts = keep
	if len(m.restarts) >= 5 {
		return false
	}
	m.restarts = append(m.restarts, time.Now())
	return true
}

type StatsView struct {
	Session  int64                   `json:"session"`
	Samples  []Sample                `json:"samples"`
	Totals   [8]int64                `json:"totals"`
	Top      []TopItem               `json:"top"`
	Events   []Event                 `json:"events"`
	RuleHits map[string]int          `json:"ruleHits"`
	UserHits map[string]int          `json:"userHits"`
	Delays   map[string][]DelayPoint `json:"delays"`
	Health   string                  `json:"health"`
}

func (m *Monitor) Stats(nSamples int) StatsView {
	m.mu.Lock()
	defer m.mu.Unlock()
	smp := m.samples
	if len(smp) > nSamples {
		smp = smp[len(smp)-nSamples:]
	}
	ev := m.events
	if len(ev) > 60 {
		ev = ev[len(ev)-60:]
	}
	cp := func(src map[string]int) map[string]int {
		out := make(map[string]int, len(src))
		for k, v := range src {
			out[k] = v
		}
		return out
	}
	dl := make(map[string][]DelayPoint, len(m.delays))
	for k, v := range m.delays {
		dl[k] = append([]DelayPoint(nil), v...)
	}
	return StatsView{
		Session: m.session, Samples: append([]Sample{}, smp...), Totals: m.totals,
		Top: append([]TopItem{}, m.top...), Events: append([]Event{}, ev...),
		RuleHits: cp(m.ruleHits), UserHits: cp(m.userHits), Delays: dl, Health: m.health,
	}
}

// wakeGrace — сколько после пробуждения не верить проверкам связи.
const wakeGrace = 90 * time.Second

// SinceWake — сколько прошло с последнего пробуждения (очень много, если не спал).
func (m *Monitor) SinceWake() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wakeAt.IsZero() {
		return 24 * time.Hour
	}
	return time.Since(m.wakeAt)
}
