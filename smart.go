package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Автоопределение (режим «Россия напрямую»). Через сервер скорость упирается в сервер,
// поэтому сайт, который из России и так открывается нормально, выгоднее пускать напрямую.
// Как это работает:
//   - первое соединение с незнакомым сайтом идёт как раньше — через сервер, ничего не ломается;
//   - в фоне Naivka открывает этот сайт и напрямую, и через сервер и сравнивает: открылся ли
//     напрямую, не отдал ли «недоступно в вашей стране» (403/451), не сильно ли медленнее;
//   - решение попадает в два списка sing-box (smart-direct и smart-proxy). Ядро само
//     перечитывает их при изменении — без перезапуска, уже открытые соединения не рвутся,
//     по-новому идут только новые;
//   - если сайт, пущенный напрямую, начинает обрываться (3 раза за 5 минут, при живом
//     интернете), он переходит на сервер; через полсуток проверяется снова.
// Заблокированное в России (ru-blocked), сервисы, которые сами закрыты для России (geoblock),
// сайты zapret и ваши правила стоят в правилах раньше — автоопределение их не трогает.

const (
	tagSmartDirect = "smart-direct"
	tagSmartProxy  = "smart-proxy"
	smartDirectTTL = 3 * 24 * time.Hour // напрямую — перепроверяем раз в 3 дня
	smartProxyTTL  = 12 * time.Hour     // через сервер — пробуем напрямую снова через полсуток
	smartMax       = 3000               // сколько сайтов помнить в каждом списке
)

// smartNever — сервисы, которые часто открываются напрямую, но работают из России плохо или
// не полностью: ИИ-сервисы проверяют страну уже после входа, видео YouTube замедляют.
// Им автоопределение не нужно — они идут через сервер (или через zapret, если он включён).
var smartNever = []string{
	"youtube.com", "googlevideo.com", "ytimg.com", "youtu.be", "ggpht.com", "youtube-nocookie.com", "youtubei.googleapis.com",
	"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com", "anthropic.com", "claude.ai", "claude.com",
	"gemini.google.com", "aistudio.google.com", "generativelanguage.googleapis.com", "notebooklm.google.com",
	"x.ai", "grok.com", "perplexity.ai", "copilot.microsoft.com", "deepl.com",
	"discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media",
	"instagram.com", "cdninstagram.com", "facebook.com", "fbcdn.net", "x.com", "twitter.com", "twimg.com",
	"spotify.com", "scdn.co", "netflix.com", "nflxvideo.net",
}

type smartEntry struct {
	Direct bool  `json:"d"`
	At     int64 `json:"at"`
}

type smartJob struct {
	host  string
	https bool
}

type Smart struct {
	a       *App
	mu      sync.Mutex
	hosts   map[string]smartEntry
	pending map[string]bool
	skip    map[string]time.Time // не получилось решить (оба пути не открылись) — не дёргаем полчаса
	fails   map[string][]time.Time
	dirty   bool
	queue   chan smartJob
}

func newSmart(a *App) *Smart {
	sm := &Smart{a: a, hosts: map[string]smartEntry{}, pending: map[string]bool{}, skip: map[string]time.Time{},
		fails: map[string][]time.Time{}, queue: make(chan smartJob, 256)}
	if b, err := os.ReadFile(sm.statePath()); err == nil {
		_ = json.Unmarshal(b, &sm.hosts)
	}
	return sm
}

func (sm *Smart) statePath() string { return filepath.Join(sm.a.dir, "smart.json") }

// smartOn — автоопределение работает: включено и режим «Россия напрямую».
func smartOn(s *Settings) bool { return s.SmartDirect && s.Routing == "ru-direct" }

func smartNeverMatch(host string) bool {
	for _, d := range smartNever {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// observe — новое соединение (вызывается из Monitor.sample под его замком: только очередь, без ожиданий).
func (sm *Smart) observe(c Conn, s *Settings) {
	if !smartOn(s) || c.Metadata.Network != "tcp" {
		return
	}
	if c.Rule != "final" && !strings.Contains(c.Rule, "rule_set="+tagSmartProxy) {
		return // сайт попал под правило: ваше, список, zapret, Россия, заблокированное
	}
	host := strings.ToLower(strings.TrimSuffix(c.Metadata.Host, "."))
	if host == "" || isIP(host) || !strings.Contains(host, ".") {
		return
	}
	var https bool
	switch c.Metadata.DestPort {
	case "443":
		https = true
	case "80":
	default:
		return
	}
	select {
	case sm.queue <- smartJob{host, https}:
	default: // очередь полна — увидим сайт ещё раз
	}
}

// due — сайт пора проверить (новый или решение устарело).
func (sm *Smart) due(host string, now time.Time) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.pending[host] || now.Before(sm.skip[host]) {
		return false
	}
	e, ok := sm.hosts[host]
	if !ok {
		return true
	}
	ttl := smartProxyTTL
	if e.Direct {
		ttl = smartDirectTTL
	}
	return now.Sub(time.Unix(e.At, 0)) > ttl
}

// run — разбор очереди (не больше трёх проверок сразу), запись списков, перепроверка и слежка за обрывами.
func (sm *Smart) run() {
	sem := make(chan struct{}, 3)
	go sm.writer()
	go sm.failWatch()
	go sm.sweep()
	for j := range sm.queue {
		if !sm.due(j.host, time.Now()) {
			continue
		}
		sm.mu.Lock()
		sm.pending[j.host] = true
		sm.mu.Unlock()
		sem <- struct{}{}
		go func(j smartJob) {
			defer func() { <-sem }()
			sm.check(j)
		}(j)
	}
}

func (sm *Smart) check(j smartJob) {
	defer func() {
		sm.mu.Lock()
		delete(sm.pending, j.host)
		sm.mu.Unlock()
	}()
	s := sm.a.settingsCopy()
	if !smartOn(s) || smartNeverMatch(j.host) || sm.zapretHost(s, j.host) {
		sm.mu.Lock()
		sm.skip[j.host] = time.Now().Add(6 * time.Hour)
		sm.mu.Unlock()
		return
	}
	direct, decided := sm.probe(j)
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !decided {
		sm.skip[j.host] = time.Now().Add(30 * time.Minute)
		return
	}
	prev, had := sm.hosts[j.host]
	sm.hosts[j.host] = smartEntry{Direct: direct, At: time.Now().Unix()}
	if !had || prev.Direct != direct {
		sm.dirty = true
		where := "через сервер"
		if direct {
			where = "напрямую"
		}
		sm.a.core.logs.Add("[naivka] автоопределение: " + j.host + " — " + where)
	}
}

// zapretHost — сайт из списков zapret: им занимается zapret (или сервер, если zapret выключен).
func (sm *Smart) zapretHost(s *Settings, host string) bool {
	for _, d := range sm.a.zapret.Domains(s) {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

type smartProbe struct {
	ok       bool
	status   int
	location string
	ttfb     time.Duration
}

func smartGet(port int, u string) smartProbe {
	hc := geoClient(port)
	hc.Timeout = 8 * time.Second
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return smartProbe{}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36")
	start := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		return smartProbe{}
	}
	resp.Body.Close()
	return smartProbe{ok: true, status: resp.StatusCode, location: resp.Header.Get("Location"), ttfb: time.Since(start)}
}

// smartDecide — по результатам «напрямую» и «через сервер»: пускать ли напрямую.
// decided=false — понять нельзя (не открылось ни так, ни так).
func smartDecide(d, p smartProbe, https bool) (direct, decided bool) {
	switch {
	case !d.ok && !p.ok:
		return false, false
	case !d.ok:
		return false, true // напрямую не открывается — заблокирован
	case !p.ok:
		return d.status < 400, true // сервер сейчас не отвечает — судим только по прямому
	}
	if (d.status == 403 || d.status == 451) && d.status != p.status {
		return false, true // «недоступно в вашей стране»
	}
	if !https && (d.status != p.status || hostOf(d.location) != hostOf(p.location)) {
		// По обычному HTTP провайдер может подменить ответ своей страницей «доступ ограничен».
		return false, true
	}
	if d.status >= 500 && p.status < 500 {
		return false, true
	}
	// Сильно медленнее напрямую (замедление) — через сервер.
	if d.ttfb > 2*p.ttfb+700*time.Millisecond {
		return false, true
	}
	return true, true
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	return strings.ToLower(u)
}

func (sm *Smart) probe(j smartJob) (direct, decided bool) {
	directPort, proxyPort := sm.a.core.ProbePorts()
	if directPort == 0 || proxyPort == 0 {
		return false, false
	}
	u := "http://" + j.host + "/"
	if j.https {
		u = "https://" + j.host + "/"
	}
	var d, p smartProbe
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); d = smartGet(directPort, u) }()
	go func() { defer wg.Done(); p = smartGet(proxyPort, u) }()
	wg.Wait()
	if !d.ok && !sm.a.internetUp() {
		return false, false // пропал интернет — сайт тут ни при чём
	}
	return smartDecide(d, p, j.https)
}

// failRe — строка журнала ядра про сорвавшееся прямое соединение с сайтом.
var smartFailRe = regexp.MustCompile(`open connection to ([^\s:\[\]]+):\d+ using outbound/direct\[direct\]: .*(i/o timeout|connection reset|forcibly closed|connection refused|unexpected EOF)`)

// failWatch: сайт, пущенный напрямую, обрывается 3 раза за 5 минут (при живом интернете) —
// переводим его на сервер.
func (sm *Smart) failWatch() {
	var seq int64
	for {
		time.Sleep(10 * time.Second)
		lines := sm.a.core.logs.After(seq)
		if len(lines) > 0 {
			seq = lines[len(lines)-1].Seq
		}
		if !smartOn(sm.a.settingsCopy()) {
			continue
		}
		now := time.Now()
		var hit []string
		sm.mu.Lock()
		for _, l := range lines {
			m := smartFailRe.FindStringSubmatch(l.Text)
			if m == nil {
				continue
			}
			host := strings.ToLower(m[1])
			if e, ok := sm.hosts[host]; !ok || !e.Direct {
				continue
			}
			keep := []time.Time{now}
			for _, t := range sm.fails[host] {
				if now.Sub(t) < 5*time.Minute {
					keep = append(keep, t)
				}
			}
			sm.fails[host] = keep
			if len(keep) >= 3 {
				hit = append(hit, host)
			}
		}
		sm.mu.Unlock()
		if len(hit) == 0 || !sm.a.internetUp() {
			continue
		}
		sm.mu.Lock()
		for _, host := range hit {
			sm.hosts[host] = smartEntry{Direct: false, At: now.Unix()}
			delete(sm.fails, host)
			sm.dirty = true
			sm.a.core.logs.Add("[naivka] автоопределение: " + host + " обрывается напрямую — теперь через сервер")
		}
		sm.mu.Unlock()
	}
}

// sweep раз в 10 минут: устаревшие решения «через сервер» забываем (сайт проверится заново,
// когда его откроют), устаревшие «напрямую» перепроверяем в фоне.
func (sm *Smart) sweep() {
	for {
		time.Sleep(10 * time.Minute)
		if !smartOn(sm.a.settingsCopy()) || !sm.a.core.Running() {
			continue
		}
		now := time.Now()
		var recheck []string
		sm.mu.Lock()
		for host, e := range sm.hosts {
			age := now.Sub(time.Unix(e.At, 0))
			if !e.Direct && age > smartProxyTTL {
				delete(sm.hosts, host)
				sm.dirty = true
			} else if e.Direct && age > smartDirectTTL && len(recheck) < 30 {
				recheck = append(recheck, host)
			}
		}
		for host, t := range sm.skip {
			if now.After(t) {
				delete(sm.skip, host)
			}
		}
		sm.mu.Unlock()
		for _, host := range recheck {
			select {
			case sm.queue <- smartJob{host, true}:
			default:
			}
		}
	}
}

// writer раз в 5 секунд записывает изменения: списки для ядра и память на диск.
func (sm *Smart) writer() {
	for {
		time.Sleep(5 * time.Second)
		sm.mu.Lock()
		dirty := sm.dirty
		sm.dirty = false
		sm.mu.Unlock()
		if !dirty {
			continue
		}
		if err := sm.writeRuleSets(); err != nil {
			sm.a.core.logs.Add("[naivka] автоопределение: не удалось записать список: " + err.Error())
		}
		sm.saveState()
	}
}

// lists — домены «напрямую» и «через сервер», самые свежие решения, не больше smartMax в каждом.
func (sm *Smart) lists() (direct, proxy []string) {
	sm.mu.Lock()
	type kv struct {
		host string
		e    smartEntry
	}
	all := make([]kv, 0, len(sm.hosts))
	for h, e := range sm.hosts {
		all = append(all, kv{h, e})
	}
	sm.mu.Unlock()
	sort.Slice(all, func(i, j int) bool {
		if all[i].e.At != all[j].e.At {
			return all[i].e.At > all[j].e.At
		}
		return all[i].host < all[j].host
	})
	for _, x := range all {
		if x.e.Direct && len(direct) < smartMax {
			direct = append(direct, x.host)
		} else if !x.e.Direct && len(proxy) < smartMax {
			proxy = append(proxy, x.host)
		}
	}
	sort.Strings(direct)
	sort.Strings(proxy)
	return direct, proxy
}

func smartRuleSetJSON(domains []string) []byte {
	rules := []any{}
	if len(domains) > 0 {
		rules = append(rules, M{"domain": strs(domains)})
	}
	b, _ := json.Marshal(M{"version": 2, "rules": rules})
	return b
}

func (sm *Smart) dir() string { return sm.a.core.runDir() }

func smartRuleSetPath(dir, tag string) string { return filepath.Join(dir, tag+".json") }

// writeRuleSets — записать оба списка. Через временный файл: ядро не прочитает половину.
func (sm *Smart) writeRuleSets() error {
	direct, proxy := sm.lists()
	if err := os.MkdirAll(sm.dir(), 0o755); err != nil {
		return err
	}
	for tag, d := range map[string][]string{tagSmartDirect: direct, tagSmartProxy: proxy} {
		p := smartRuleSetPath(sm.dir(), tag)
		b := smartRuleSetJSON(d)
		if old, err := os.ReadFile(p); err == nil && string(old) == string(b) {
			continue
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, p); err != nil {
			return err
		}
	}
	return nil
}

func (sm *Smart) saveState() {
	sm.mu.Lock()
	b, err := json.Marshal(sm.hosts)
	sm.mu.Unlock()
	if err == nil {
		_ = os.WriteFile(sm.statePath(), b, 0o600)
	}
}

// SmartView — для окна: сколько сайтов в каждом списке и последние решения.
type SmartView struct {
	Direct int         `json:"direct"`
	Proxy  int         `json:"proxy"`
	Recent []SmartHost `json:"recent"`
}

type SmartHost struct {
	Host   string `json:"host"`
	Direct bool   `json:"direct"`
	At     int64  `json:"at"`
}

func (sm *Smart) View() SmartView {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	v := SmartView{Recent: []SmartHost{}}
	for h, e := range sm.hosts {
		if e.Direct {
			v.Direct++
		} else {
			v.Proxy++
		}
		v.Recent = append(v.Recent, SmartHost{h, e.Direct, e.At})
	}
	sort.Slice(v.Recent, func(i, j int) bool {
		if v.Recent[i].At != v.Recent[j].At {
			return v.Recent[i].At > v.Recent[j].At
		}
		return v.Recent[i].Host < v.Recent[j].Host
	})
	if len(v.Recent) > 100 {
		v.Recent = v.Recent[:100]
	}
	return v
}

// Forget — забыть решение по сайту (host="" — забыть всё).
func (sm *Smart) Forget(host string) {
	sm.mu.Lock()
	if host == "" {
		sm.hosts = map[string]smartEntry{}
		sm.skip = map[string]time.Time{}
	} else {
		delete(sm.hosts, strings.ToLower(host))
		delete(sm.skip, strings.ToLower(host))
	}
	sm.dirty = true
	sm.mu.Unlock()
}
