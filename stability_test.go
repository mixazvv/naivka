package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPickFailover(t *testing.T) {
	d := map[string]int{"s-a": 300, "s-b": 120, "s-c": 0, "s-d": 120}
	if got := pickFailover(d, "s-b"); got != "s-d" {
		t.Fatalf("got %q, want s-d (самый быстрый, кроме пропущенного)", got)
	}
	if got := pickFailover(d); got != "s-b" {
		t.Fatalf("got %q, want s-b (при равенстве — по имени)", got)
	}
	if got := pickFailover(map[string]int{"s-a": 0}, ""); got != "" {
		t.Fatalf("мёртвый сервер не годится, got %q", got)
	}
}

func TestSelectedServerTag(t *testing.T) {
	s := &Settings{Servers: []Server{{ID: "x"}}, Selected: "x"}
	if selectedServerTag(s) != "s-x" {
		t.Fatal("ожидался тег выбранного сервера")
	}
	s.Selected = "auto"
	if selectedServerTag(s) != "" {
		t.Fatal("автовыбор — не конкретный сервер")
	}
}

func TestFailoverState(t *testing.T) {
	var f failoverState
	f.set("s-a", "s-b")
	if from, to := f.get(); from != "s-a" || to != "s-b" {
		t.Fatal(from, to)
	}
	f.clear()
	if from, _ := f.get(); from != "" {
		t.Fatal("clear не сработал")
	}
}

func TestCrashBudget(t *testing.T) {
	var b crashBudget
	for i := 0; i < 3; i++ {
		if !b.allow(3, time.Minute) {
			t.Fatalf("попытка %d должна быть разрешена", i)
		}
	}
	if b.allow(3, time.Minute) {
		t.Fatal("четвёртая попытка за окно должна быть запрещена")
	}
	b.at = []time.Time{time.Now().Add(-2 * time.Minute)}
	if !b.allow(1, time.Minute) {
		t.Fatal("старые попытки за окном не считаются")
	}
}

func TestWatchdogDelay(t *testing.T) {
	if watchdogDelay(0) != 2*time.Second || watchdogDelay(100) != time.Minute {
		t.Fatal(watchdogDelay(0), watchdogDelay(100))
	}
	for i := 1; i < 10; i++ {
		if watchdogDelay(i) < watchdogDelay(i-1) {
			t.Fatal("паузы не должны уменьшаться")
		}
	}
}

func TestSpareTestURL(t *testing.T) {
	if u := spareTestURL("https://www.gstatic.com/generate_204"); !strings.Contains(u, "cloudflare") {
		t.Fatal(u)
	}
	if u := spareTestURL("https://cp.cloudflare.com/generate_204"); strings.Contains(u, "cloudflare") {
		t.Fatal("запасной адрес должен отличаться от основного:", u)
	}
}

func TestFindConflicts(t *testing.T) {
	procs := []procEntry{
		{Name: "v2rayN.exe"},
		{Name: "goodbyedpi.exe"},
		{Name: "sing-box.exe", Path: `/own/sing-box.exe`},
		{Name: "sing-box.exe", Path: `/other/app/sing-box.exe`},
		{Name: "sing-box.exe"}, // путь не прочитался — не тревожим
		{Name: "explorer.exe"},
	}
	got := findConflicts(procs, []string{"/own/sing-box.exe"}, false)
	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
	}
	j := strings.Join(names, ",")
	if !strings.Contains(j, "v2rayN") || !strings.Contains(j, "sing-box (не из Naivka)") {
		t.Fatal(j)
	}
	if strings.Contains(j, "GoodbyeDPI") {
		t.Fatal("GoodbyeDPI мешает только при включённом zapret:", j)
	}
	if len(got) != 2 {
		t.Fatal("лишние находки:", j)
	}
	if got := findConflicts(procs[1:2], nil, true); len(got) != 1 || got[0].Name != "GoodbyeDPI" {
		t.Fatal(got)
	}
}

func TestRedactSettings(t *testing.T) {
	s := DefaultSettings()
	s.Mode = "tun"
	s.Servers = []Server{{ID: "a", Host: "secret.example", Password: "hunter2"}}
	s.Rules = []Rule{{Kind: "domain", Value: "private-site.example", Action: "proxy"}}
	var raw any
	b, _ := json.Marshal(s)
	_ = json.Unmarshal(b, &raw)
	out, _ := json.Marshal(redactSettings(raw, ""))
	txt := string(out)
	for _, bad := range []string{"secret.example", "hunter2", "private-site.example"} {
		if strings.Contains(txt, bad) {
			t.Fatalf("в отчёт попало %q", bad)
		}
	}
	if !strings.Contains(txt, `"mode":"tun"`) || !strings.Contains(txt, `"action":"proxy"`) {
		t.Fatal("безопасные поля должны остаться:", txt)
	}
}

func TestMaskHosts(t *testing.T) {
	got := maskHosts("dial tcp 1.2.3.4:443 via vpn.example.com", []string{"vpn.example.com", "1.2.3.4", ""})
	if strings.Contains(got, "vpn.example.com") || strings.Contains(got, "1.2.3.4") {
		t.Fatal(got)
	}
}

func TestFailoverNotInFingerprint(t *testing.T) {
	a, b := DefaultSettings(), DefaultSettings()
	b.Failover = !a.Failover
	if a.fingerprint() != b.fingerprint() {
		t.Fatal("переход на рабочий сервер не должен требовать переподключения")
	}
	if !DefaultSettings().Failover {
		t.Fatal("по умолчанию включено")
	}
}

// Под -race: бюджет перезапусков безопасен при одновременных вызовах.
func TestCrashBudgetConcurrent(t *testing.T) {
	var b crashBudget
	done := make(chan bool)
	ok := make(chan bool, 40)
	for i := 0; i < 40; i++ {
		go func() { ok <- b.allow(5, time.Minute); done <- true }()
	}
	for i := 0; i < 40; i++ {
		<-done
	}
	close(ok)
	n := 0
	for v := range ok {
		if v {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("разрешено %d попыток, ожидалось 5", n)
	}
}

// Сайты zapret идут через переключатель zapret-out: его можно перевести на сервер и обратно
// без перезапуска ядра. Адреса этих сайтов, пока zapret справляется, — через прямой DNS.
func TestZapretRouteConfig(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Name: "A", Host: "a.example", Port: 443, Username: "u", Password: "p"}}
	s.Selected = "a"
	s.Routing = "ru-direct"
	s.Rules = []Rule{{Kind: "domain", Value: "rutracker.org", Action: "zapret"}}
	s.normalize()
	find := func(cfg map[string]any) (sel map[string]any, routeOut, dnsSrv, ruleOut string) {
		b, _ := json.Marshal(cfg)
		var c struct {
			Outbounds []map[string]any `json:"outbounds"`
			Route     struct {
				Rules []map[string]any `json:"rules"`
			} `json:"route"`
			DNS struct {
				Rules []map[string]any `json:"rules"`
			} `json:"dns"`
		}
		_ = json.Unmarshal(b, &c)
		for _, o := range c.Outbounds {
			if o["tag"] == tagZapretOut {
				sel = o
			}
		}
		has := func(r map[string]any, d string) bool {
			l, _ := r["domain_suffix"].([]any)
			for _, x := range l {
				if x == d {
					return true
				}
			}
			return false
		}
		for _, r := range c.Route.Rules {
			if has(r, "youtube.com") && routeOut == "" {
				routeOut, _ = r["outbound"].(string)
			}
			if has(r, "rutracker.org") && ruleOut == "" {
				ruleOut, _ = r["outbound"].(string)
			}
		}
		for _, r := range c.DNS.Rules {
			if has(r, "youtube.com") && dnsSrv == "" {
				dnsSrv, _ = r["server"].(string)
			}
		}
		return
	}
	cfg, err := BuildConfig(s, ConfigEnv{APIPort: 1, Secret: "x", CachePath: "c", ZapretActive: true, ZapretRoute: true, ZapretDomains: []string{"youtube.com"}})
	if err != nil {
		t.Fatal(err)
	}
	sel, route, dns, rule := find(cfg)
	if sel == nil || sel["default"] != tagDirect || sel["interrupt_exist_connections"] != true {
		t.Fatalf("нет переключателя zapret-out или он не напрямую: %v", sel)
	}
	if route != tagZapretOut || rule != tagZapretOut {
		t.Fatalf("сайты zapret должны идти через zapret-out: %q %q", route, rule)
	}
	if dns != tagDNSDirect {
		t.Fatalf("DNS сайтов zapret, пока он справляется, — напрямую, а не %q", dns)
	}

	cfg, _ = BuildConfig(s, ConfigEnv{APIPort: 1, Secret: "x", CachePath: "c", ZapretRoute: true, ZapretDomains: []string{"youtube.com"}})
	sel, route, dns, _ = find(cfg)
	if sel == nil || sel["default"] != tagProxy || route != tagZapretOut || dns != tagDNSRemote {
		t.Fatalf("zapret не работает — по умолчанию через сервер: %v %q %q", sel, route, dns)
	}

	cfg, _ = BuildConfig(s, ConfigEnv{Mobile: true, ZapretRoute: true, ZapretDomains: []string{"youtube.com"}})
	if sel, _, _, _ := find(cfg); sel != nil {
		t.Fatal("в профиле телефона zapret нет")
	}
}

func TestZapretExcludedServers(t *testing.T) {
	s := &Settings{Servers: []Server{{Host: "a.example"}, {Host: "1.2.3.4"}, {Host: "A.example"}}}
	got := zapretExcludedServers(s, []Server{{Host: "wl.example"}, {Host: "1.2.3.4"}, {Host: ""}})
	if len(got) != 3 {
		t.Fatalf("ожидалось 3 сервера без повторов, получено %v", got)
	}
	if zapretServersKey(s, nil) == zapretServersKey(&Settings{Servers: s.Servers[:1]}, nil) {
		t.Fatal("ключ должен меняться при смене серверов")
	}
	if zapretRouteTarget(true) != tagDirect || zapretRouteTarget(false) != tagProxy {
		t.Fatal("zapretRouteTarget")
	}
}

// Режимов маршрута два; прежний «Только заблокированное» становится «Россия напрямую».
func TestRoutingTwoModes(t *testing.T) {
	for in, want := range map[string]string{"blocked-only": "ru-direct", "global": "global", "ru-direct": "ru-direct", "": "ru-direct"} {
		s := DefaultSettings()
		s.Routing = in
		s.normalize()
		if s.Routing != want {
			t.Fatalf("%q → %q, ожидалось %q", in, s.Routing, want)
		}
	}
}

// Маскировка начала соединения: только для TLS поверх TCP, у Naive и QUIC-протоколов её нет.
func TestFragment(t *testing.T) {
	cases := []struct {
		sv           Server
		record, full bool
	}{
		{Server{Type: "vless", Security: "reality", Fragment: "record"}, true, false},
		{Server{Type: "vless", Security: "tls", Fragment: "full"}, true, true},
		{Server{Type: "trojan", Fragment: "full"}, true, true},
		{Server{Type: "anytls", Fragment: "record"}, true, false},
		{Server{Type: "vless", Security: "none", Fragment: "full"}, false, false},
		{Server{Type: "hysteria2", Fragment: "full"}, false, false},
		{Server{Type: "naive", Fragment: "full"}, false, false},
		{Server{Type: "trojan"}, false, false},
	}
	for _, c := range cases {
		tl := withFragment(M{}, c.sv)
		if (tl["record_fragment"] == true) != c.record || (tl["fragment"] == true) != c.full {
			t.Errorf("%s/%s %q: %v", c.sv.Type, c.sv.Security, c.sv.Fragment, tl)
		}
	}
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "trojan", Host: "h", Port: 443, Password: "p", Fragment: "weird"}}
	s.normalize()
	if s.Servers[0].Fragment != "" {
		t.Fatal("неизвестное значение маскировки сбрасывается")
	}
}

func TestClassifyResolvers(t *testing.T) {
	entries := []bashwsEntry{
		{IP: "5.6.7.8", CountryName: "Russia", ASN: "AS12389 Rostelecom", Type: "ip"},
		{IP: "1.1.1.1", CountryName: "Netherlands", ASN: "AS13335 Cloudflare", Type: "dns"},
		{IP: "95.167.0.1", CountryName: "Russia", ASN: "AS12389 Rostelecom", Type: "dns"},
		{Type: "conclusion"},
	}
	me := &GeoInfo{Country: "Russia", ASN: "AS12389"}
	rs, leak := classifyResolvers(entries, me, true)
	if len(rs) != 2 || rs[0].Leak || !rs[1].Leak || !leak {
		t.Fatalf("%+v %v", rs, leak)
	}
	rs, leak = classifyResolvers(entries[:2], me, true)
	if leak || len(rs) != 1 {
		t.Fatal("DNS через сервер за границей — не утечка")
	}
	// Без подключения DNS провайдера — это ожидаемо, но всё равно помечается (тот же AS).
	if _, leak := classifyResolvers(entries, me, false); !leak {
		t.Fatal("тот же AS — утечка и без подключения")
	}
}

func TestZapretFailLine(t *testing.T) {
	ok := []string{
		"ERROR [1] connection: open connection to 95.217.169.233:443 using outbound/selector[zapret-out]: dial tcp 95.217.169.233:443: i/o timeout",
		"ERROR [2] connection: open connection to x:443 using outbound/selector[zapret-out]: read: An existing connection was forcibly closed by the remote host.",
	}
	bad := []string{
		"ERROR [3] connection: open connection to 149.154.167.220:443 using outbound/direct[direct]: dial tcp: i/o timeout",
		"INFO [4] outbound/selector[zapret-out]: connected",
	}
	for _, l := range ok {
		if !zapretFailLine(l) {
			t.Error("должна считаться:", l)
		}
	}
	for _, l := range bad {
		if zapretFailLine(l) {
			t.Error("не должна считаться:", l)
		}
	}
}

// tg-ws-proxy, пока Naivka подключена, по умолчанию идёт через сервер.
func TestTgWsViaServer(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "trojan", Name: "A", Host: "a.example", Port: 443, Password: "p"}}
	s.normalize()
	route := func() string {
		cfg, err := BuildConfig(s, ConfigEnv{APIPort: 1, Secret: "x", CachePath: "c", TgWsProxy: true})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(cfg)
		var c struct {
			Route struct {
				Rules []map[string]any `json:"rules"`
			} `json:"route"`
		}
		_ = json.Unmarshal(b, &c)
		for _, r := range c.Route.Rules {
			if p, ok := r["process_name"].([]any); ok && len(p) > 0 && p[0] == "TgWsProxy.exe" {
				return r["outbound"].(string)
			}
		}
		return ""
	}
	if !s.Telegram.ViaServer || route() != tagProxy {
		t.Fatal("по умолчанию — через сервер")
	}
	s.Telegram.ViaServer = false
	if route() != tagDirect {
		t.Fatal("выключено — напрямую")
	}
	a, b := DefaultSettings(), DefaultSettings()
	b.Telegram.ViaServer = false
	if !addonsNeedRestart(a, b) {
		t.Fatal("смена маршрута tg-ws-proxy применяется перезапуском ядра")
	}
}

func TestDNSFailLine(t *testing.T) {
	yes := []string{
		"+0300 2026-10-16 00:50:31 ERROR [3855400064 123ms] router: process DNS packet: unexpected EOF",
		"+0300 2026-10-15 22:50:19 ERROR [1 44ms] connection: open connection to sun9-2.vkuserphoto.ru:443 using outbound/direct[direct]: lookup sun9-2.vkuserphoto.ru: unexpected EOF",
		"ERROR connection: open connection to a.ru:443 using outbound/direct[direct]: lookup a.ru: read tcp 192.168.0.200:52934->77.88.8.1:443: wsarecv: An existing connection was forcibly closed by the remote host.",
	}
	no := []string{
		"ERROR connection: open connection to mobile.yandex.net:443 using outbound/direct[direct]: dial tcp 77.88.21.110:443: i/o timeout",
		"ERROR connection: open connection to 65.109.41.220:443 using outbound/selector[zapret-out]: dial tcp 65.109.41.220:443: i/o timeout",
	}
	for _, l := range yes {
		if !dnsFailLine(l, "common.dot.dns.yandex.net") {
			t.Errorf("должна считаться: %s", l)
		}
	}
	for _, l := range no {
		if dnsFailLine(l, "common.dot.dns.yandex.net") {
			t.Errorf("не должна считаться: %s", l)
		}
	}
	if !dnsFailLine("read tcp 1.2.3.4:5->9.9.9.9:443: connection reset by peer", "9.9.9.9") {
		t.Error("обрыв до DoH-сервера по IP")
	}
}

func TestDNSFallbackConfig(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Name: "a", Type: "naive", Host: "example.com", Port: 443, Username: "u", Password: "p"}}
	s.Selected = "a"
	find := func(env ConfigEnv) M {
		cfg, err := BuildConfig(s, env)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range cfg["dns"].(M)["servers"].([]any) {
			if m := x.(M); m["tag"] == tagDNSDirect {
				return m
			}
		}
		t.Fatal("нет dns-direct")
		return nil
	}
	if d := find(ConfigEnv{}); d["type"] != "https" {
		t.Fatalf("по умолчанию DoH: %v", d)
	}
	d := find(ConfigEnv{DNSFallback: true})
	if d["type"] != "udp" || d["server"] != "77.88.8.8" || d["domain_resolver"] != nil {
		t.Fatalf("запасной DNS: %v", d)
	}
	if dnsFallbackAddr(directDNS{Kind: "https", Host: "1.1.1.1"}) != "1.1.1.1" {
		t.Fatal("DoH по IP — тот же адрес по UDP")
	}
}

func TestDNSQueryWire(t *testing.T) {
	b, err := base64.RawURLEncoding.DecodeString(dnsQuery("ya.ru"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 2, 'y', 'a', 2, 'r', 'u', 0, 0, 1, 0, 1}
	if !bytes.Equal(b, want) {
		t.Fatalf("запрос %v, нужно %v", b, want)
	}
}

func TestSmartConfig(t *testing.T) {
	dir := t.TempDir()
	for _, tag := range []string{tagSmartDirect, tagSmartProxy} {
		os.WriteFile(smartRuleSetPath(dir, tag), smartRuleSetJSON(nil), 0o644)
	}
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Name: "A", Host: "a.example", Port: 443, Username: "u", Password: "p"}}
	s.Selected = "a"
	cfg, err := BuildConfig(s, ConfigEnv{SmartDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	js := string(b)
	iBlocked := strings.Index(js, `"outbound":"proxy","rule_set":["ru-blocked"]`)
	iGeo := strings.Index(js, `"outbound":"proxy","rule_set":["geoblock"]`)
	iSmartP := strings.Index(js, `"outbound":"proxy","rule_set":["smart-proxy"]`)
	iSmartD := strings.Index(js, `"outbound":"direct","rule_set":["smart-direct"]`)
	iRu := strings.Index(js, `"outbound":"direct","rule_set":["geosite-category-ru"]`)
	iDl := strings.Index(js, `"outbound":"direct"},{"outbound":"proxy","rule_set":["smart-proxy"]`) // загрузки — прямо перед автоопределением
	if !strings.Contains(js[iGeo:iSmartP], `"steamcontent.com"`) {
		iDl = -1
	}
	if !(iBlocked >= 0 && iBlocked < iGeo && iGeo < iDl && iDl < iSmartP && iSmartP < iSmartD && iSmartD < iRu) {
		t.Fatalf("порядок правил: blocked %d geoblock %d smart-proxy %d smart-direct %d ru %d", iBlocked, iGeo, iSmartP, iSmartD, iRu)
	}
	if !strings.Contains(js, `"type":"local"`) {
		t.Fatal("нет локальных списков")
	}
	if bin := os.Getenv("SINGBOX"); bin != "" {
		p := filepath.Join(dir, "c.json")
		os.WriteFile(p, b, 0o600)
		if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
			t.Fatalf("sing-box check: %v %s", err, out)
		}
	}
	// В режиме «всё через сервер» автоопределения нет.
	s.Routing = "global"
	cfg, _ = BuildConfig(s, ConfigEnv{SmartDir: dir})
	b, _ = json.Marshal(cfg)
	if strings.Contains(string(b), "smart-direct") {
		t.Fatal("автоопределение в режиме «всё через сервер»")
	}
	if strings.Contains(string(b), "steamcontent.com") {
		t.Fatal("загрузки напрямую в режиме «всё через сервер»")
	}
	s.Routing, s.DownloadsDirect = "ru-direct", false
	cfg, _ = BuildConfig(s, ConfigEnv{SmartDir: dir})
	b, _ = json.Marshal(cfg)
	if strings.Contains(string(b), "steamcontent.com") {
		t.Fatal("загрузки напрямую выключены, а правило есть")
	}
}

func TestSmartDecide(t *testing.T) {
	ok := func(st int, ms int) smartProbe {
		return smartProbe{ok: true, status: st, ttfb: time.Duration(ms) * time.Millisecond}
	}
	cases := []struct {
		name          string
		d, p          smartProbe
		https         bool
		direct, known bool
	}{
		{"открывается напрямую", ok(200, 100), ok(200, 300), true, true, true},
		{"напрямую не открывается", smartProbe{}, ok(200, 300), true, false, true},
		{"ничего не открывается", smartProbe{}, smartProbe{}, true, false, false},
		{"недоступно в вашей стране", ok(403, 100), ok(200, 300), true, false, true},
		{"корень везде 403", ok(403, 100), ok(403, 300), true, true, true},
		{"сильно медленнее напрямую", ok(200, 3000), ok(200, 300), true, false, true},
		{"чуть медленнее напрямую", ok(200, 500), ok(200, 300), true, true, true},
		{"HTTP: подмена ответа", smartProbe{ok: true, status: 302, location: "http://blocked.isp.ru/"}, smartProbe{ok: true, status: 200}, false, false, true},
		{"HTTP: одинаково", smartProbe{ok: true, status: 301, location: "https://a.com/"}, smartProbe{ok: true, status: 301, location: "https://a.com/x"}, false, true, true},
		{"сервер не отвечает", ok(200, 100), smartProbe{}, true, true, true},
	}
	for _, c := range cases {
		d, k := smartDecide(c.d, c.p, c.https)
		if d != c.direct || k != c.known {
			t.Errorf("%s: напрямую %v решено %v", c.name, d, k)
		}
	}
	if !smartNeverMatch("rr3---sn-abc.googlevideo.com") || smartNeverMatch("github.com") {
		t.Error("smartNever")
	}
	m := smartFailRe.FindStringSubmatch("ERROR [1 5.0s] connection: open connection to mobile.yandex.net:443 using outbound/direct[direct]: dial tcp 77.88.21.110:443: i/o timeout")
	if m == nil || m[1] != "mobile.yandex.net" {
		t.Errorf("smartFailRe: %v", m)
	}
}

func TestSmartObserveAndLists(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: DefaultSettings()}
	a.core = NewCore(a.dir)
	sm := newSmart(a)
	s := DefaultSettings()
	c := Conn{Rule: "final"}
	c.Metadata.Network, c.Metadata.Host, c.Metadata.DestPort = "tcp", "Example.COM", "443"
	sm.observe(c, s)
	c.Rule = "rule_set=ru-blocked => route(proxy)"
	sm.observe(c, s)
	if len(sm.queue) != 1 {
		t.Fatalf("в очереди %d, нужно 1 (только то, что попало в final)", len(sm.queue))
	}
	if j := <-sm.queue; j.host != "example.com" || !j.https {
		t.Fatalf("job %+v", j)
	}
	s.Routing = "global"
	c.Rule = "final"
	sm.observe(c, s)
	if len(sm.queue) != 0 {
		t.Fatal("в режиме «всё через сервер» не проверяем")
	}
	now := time.Now()
	sm.hosts["a.com"] = smartEntry{Direct: true, At: now.Unix()}
	sm.hosts["b.com"] = smartEntry{Direct: false, At: now.Unix()}
	sm.hosts["old.com"] = smartEntry{Direct: false, At: now.Add(-13 * time.Hour).Unix()}
	if sm.due("a.com", now) || sm.due("b.com", now) || !sm.due("old.com", now) || !sm.due("new.com", now) {
		t.Fatal("due")
	}
	d, p := sm.lists()
	if strings.Join(d, ",") != "a.com" || strings.Join(p, ",") != "b.com,old.com" {
		t.Fatalf("списки %v %v", d, p)
	}
	if err := sm.writeRuleSets(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(smartRuleSetPath(sm.dir(), tagSmartDirect))
	if !strings.Contains(string(b), `"a.com"`) {
		t.Fatalf("smart-direct.json: %s", b)
	}
	sm.Forget("")
	if v := sm.View(); v.Direct+v.Proxy != 0 {
		t.Fatal("Forget")
	}
}

func TestTwitchFakeIPInTun(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Name: "A", Host: "a.example", Port: 443, Username: "u", Password: "p"}}
	s.Selected = "a"
	s.TwitchAdFree = true
	s.Mode = "tun"
	cfg, err := BuildConfig(s, ConfigEnv{})
	if err != nil {
		t.Fatal(err)
	}
	dns := cfg["dns"].(M)
	b, _ := json.Marshal(dns)
	if !strings.Contains(string(b), `"type":"fakeip"`) {
		t.Fatal("в TUN нет fakeip для плейлистов Twitch")
	}
	first, _ := json.Marshal(dns["rules"].([]any)[0])
	if !strings.Contains(string(first), "dns-twitch") || !strings.Contains(string(first), "playlist") {
		t.Fatalf("правило fakeip должно быть первым: %s", first)
	}
	s.Mode = "proxy"
	cfg, _ = BuildConfig(s, ConfigEnv{})
	b, _ = json.Marshal(cfg["dns"])
	if strings.Contains(string(b), "fakeip") {
		t.Fatal("в режиме прокси fakeip не нужен: имя сайта и так приходит")
	}
}
