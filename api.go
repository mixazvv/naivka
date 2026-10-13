package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type apiFunc func(r *http.Request) (any, error)

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// Шрифты интерфейса: в старых Windows тип .woff2 может быть не зарегистрирован.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		static.ServeHTTP(w, r)
	}))

	// Один обработчик на адрес, внутри — выбор по методу (так работает и на Go 1.20).
	table := map[string]map[string]apiFunc{}
	var order []string
	api := func(method, path string, f apiFunc) {
		if table[path] == nil {
			table[path] = map[string]apiFunc{}
			order = append(order, path)
		}
		table[path][method] = f
	}
	serve := func(methods map[string]apiFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Naivka-Token")), []byte(a.token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			m := r.Method
			if m == "HEAD" {
				m = "GET"
			}
			f := methods[m]
			if f == nil {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			res, err := f(r)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			if res == nil {
				res = map[string]bool{"ok": true}
			}
			_ = json.NewEncoder(w).Encode(res)
		}
	}

	api("GET", "/api/ping", func(*http.Request) (any, error) { return nil, nil })
	api("POST", "/api/show", func(*http.Request) (any, error) { a.ui.Show(); return nil, nil })
	api("POST", "/api/window/theme", func(r *http.Request) (any, error) {
		var req struct {
			Dark bool `json:"dark"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		a.ui.SetTheme(req.Dark)
		return nil, nil
	})
	// Страница сообщает цвета логотипа, и значок в трее и у окна перекрашивается вместе с ней.
	api("POST", "/api/window/logo", func(r *http.Request) (any, error) {
		var c logoColors
		if err := decode(r, &c); err != nil {
			return nil, err
		}
		if !c.valid() {
			return nil, errors.New("цвета значка — в формате #RRGGBB")
		}
		a.ui.SetLogo(c)
		return nil, nil
	})
	api("GET", "/api/state", a.apiState)
	api("GET", "/api/settings", func(*http.Request) (any, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.settings.clone(), nil
	})
	api("POST", "/api/settings", a.apiSaveSettings)
	api("POST", "/api/import", a.apiImport)
	api("POST", "/api/connect", func(*http.Request) (any, error) { return nil, a.connect() })
	api("POST", "/api/disconnect", func(*http.Request) (any, error) {
		a.core.Stop()
		a.mon.Event("info", "Отключено", false)
		a.mu.Lock()
		a.applied = ""
		a.mu.Unlock()
		return nil, nil
	})
	api("POST", "/api/port/auto", func(*http.Request) (any, error) {
		a.mu.Lock()
		port := pickFreePort(a.settings.MixedPort, a.settings.Telegram.Port)
		a.settings.MixedPort = port
		err := SaveSettings(a.dir, a.settings)
		a.mu.Unlock()
		if err != nil {
			return nil, err
		}
		a.mon.Event("info", fmt.Sprintf("Порт локального прокси сменён на %d", port), false)
		if err := a.connect(); err != nil {
			return nil, err
		}
		return map[string]int{"port": port}, nil
	})
	api("GET", "/api/wl", func(*http.Request) (any, error) { return a.wl.View(), nil })
	api("POST", "/api/wl/refresh", func(*http.Request) (any, error) {
		if err := a.wl.Refresh(); err != nil {
			return nil, err
		}
		s := a.settingsCopy()
		if a.core.Running() && s.WL.Enabled && a.wl.stale(s) {
			go a.restartCore()
		}
		return a.wl.View(), nil
	})
	api("GET", "/api/self", func(*http.Request) (any, error) { return selfView(), nil })
	api("POST", "/api/self/check", func(*http.Request) (any, error) {
		if err := a.checkSelf(false); err != nil {
			return nil, err
		}
		return selfView(), nil
	})
	api("POST", "/api/self/apply", func(*http.Request) (any, error) { return nil, a.applySelf() })
	api("POST", "/api/wl/test", func(*http.Request) (any, error) {
		cl := a.core.Clash()
		if cl == nil {
			return nil, errors.New("Подключитесь, чтобы проверить серверы подписки")
		}
		if len(a.wl.Servers(a.settingsCopy())) == 0 {
			return nil, errors.New("Подписка выключена или пуста")
		}
		res, err := cl.GroupDelay("wl", a.settingsCopy().TestURL, 5000)
		if err != nil {
			return nil, fmt.Errorf("не удалось проверить: %v", err)
		}
		return map[string]any{"ok": len(res), "delays": res}, nil
	})
	api("POST", "/api/xray/install", func(*http.Request) (any, error) { return nil, a.xray.Install() })
	api("POST", "/api/speedtest", func(*http.Request) (any, error) { return nil, a.StartSpeedTest() })
	api("GET", "/api/speedtest", func(*http.Request) (any, error) { return speedView(), nil })
	api("GET", "/api/processes", func(*http.Request) (any, error) { return a.Processes(), nil })
	api("GET", "/api/share", func(*http.Request) (any, error) {
		s := a.settingsCopy()
		ips := lanIPs()
		if ips == nil {
			ips = []string{}
		}
		return map[string]any{"ips": ips, "port": s.Share.Port, "auth": s.Share.Auth, "username": s.Share.Username,
			"password": s.Share.Password, "running": a.core.Running()}, nil
	})
	api("POST", "/api/share/firewall", func(*http.Request) (any, error) { return nil, allowLANPort(a.settingsCopy().Share.Port) })
	api("POST", "/api/reconnect", func(*http.Request) (any, error) {
		a.core.Stop()
		return nil, a.connect()
	})
	api("GET", "/api/proxies", func(*http.Request) (any, error) {
		cl := a.core.Clash()
		if cl == nil {
			return map[string]any{"running": false}, nil
		}
		return cl.Proxies()
	})
	api("POST", "/api/select", a.apiSelect)
	api("POST", "/api/test", a.apiTest)
	api("GET", "/api/test/results", func(*http.Request) (any, error) { return serverTests.view(), nil })
	api("GET", "/api/conns", func(*http.Request) (any, error) {
		cl := a.core.Clash()
		if cl == nil {
			return &ConnsView{Connections: []Conn{}}, nil
		}
		return cl.Connections(300)
	})
	api("POST", "/api/conns/close", func(*http.Request) (any, error) {
		if cl := a.core.Clash(); cl != nil {
			return nil, cl.CloseAll()
		}
		return nil, nil
	})
	api("GET", "/api/logs", func(r *http.Request) (any, error) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		return a.core.logs.After(after), nil
	})
	api("GET", "/api/config", func(*http.Request) (any, error) {
		a.mu.Lock()
		s := a.settings.clone()
		a.mu.Unlock()
		env := ConfigEnv{APIPort: 0, Secret: "••••", CachePath: "cache.db"}
		a.fillEnv(s, &env)
		cfg, err := BuildConfig(s, env)
		if err != nil {
			return nil, err
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		return map[string]string{"config": string(b)}, nil
	})
	api("GET", "/api/ip", a.apiIP)
	api("GET", "/api/geo/servers", a.apiServerGeo)
	api("POST", "/api/rules/parse", func(r *http.Request) (any, error) {
		var req struct {
			Text   string `json:"text"`
			Action string `json:"action"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return ParseRules(req.Text, req.Action), nil
	})
	api("GET", "/api/rules/export", func(r *http.Request) (any, error) {
		a.mu.Lock()
		rules := append([]Rule(nil), a.settings.Rules...)
		a.mu.Unlock()
		if rules == nil {
			rules = []Rule{}
		}
		return map[string]any{
			"json": RulesFile{Format: "naivka-rules", Version: 1, Rules: rules},
			"text": RulesAsText(rules),
		}, nil
	})
	api("GET", "/api/stats", func(r *http.Request) (any, error) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 || n > 300 {
			n = 120
		}
		return a.mon.Stats(n), nil
	})
	api("POST", "/api/check", a.apiCheck)
	api("GET", "/api/link", func(r *http.Request) (any, error) {
		id := r.URL.Query().Get("id")
		for _, sv := range a.settingsCopy().Servers {
			if sv.ID == id {
				return map[string]string{"link": ShareLink(sv)}, nil
			}
		}
		return nil, errors.New("сервер не найден")
	})
	api("POST", "/api/mobile/share", a.apiMobileShare)
	api("POST", "/api/mobile/stop", func(*http.Request) (any, error) { a.stopShare(); return nil, nil })
	api("GET", "/api/mobile/config", func(*http.Request) (any, error) {
		b, err := a.mobileConfig()
		if err != nil {
			return nil, err
		}
		return map[string]string{"config": string(b)}, nil
	})
	api("POST", "/api/backup", func(r *http.Request) (any, error) {
		var req struct {
			Password string `json:"password"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		s := a.settingsCopy()
		s.Window = nil
		return makeBackup(s, req.Password)
	})
	api("POST", "/api/restore", a.apiRestore)
	a.serverInstallRoutes(api)
	api("GET", "/api/lists", func(*http.Request) (any, error) { return a.listsView(), nil })
	api("GET", "/api/traffic", func(*http.Request) (any, error) {
		return a.traffic.view(time.Now(), 31, a.settingsCopy()), nil
	})
	api("POST", "/api/lists/refresh", func(*http.Request) (any, error) {
		err := a.refreshLists()
		return a.listsView(), err
	})
	api("GET", "/api/addons", func(r *http.Request) (any, error) {
		full := r.URL.Query().Get("full") == "1" // проверка чужого zapret — только для открытого раздела
		return map[string]any{"updates": a.updatesView(), "zapret": a.zapret.View(), "telegram": a.tgws.View(), "conflict": func() string {
			if a.zapret.Running() || !full {
				return ""
			}
			return zapretConflictCached()
		}()}, nil
	})
	api("POST", "/api/addons/check", func(*http.Request) (any, error) { a.checkUpdates(false); return a.updatesView(), nil })
	api("POST", "/api/zapret/install", func(*http.Request) (any, error) { return nil, a.zapret.Install() })
	api("POST", "/api/zapret/pick", func(*http.Request) (any, error) { return nil, a.zapret.Pick() })
	api("POST", "/api/zapret/pick/cancel", func(*http.Request) (any, error) { a.zapret.CancelPick(); return nil, nil })
	api("POST", "/api/zapret/trial/start", func(r *http.Request) (any, error) {
		var req struct {
			Strategy string `json:"strategy"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return nil, a.zapret.TrialStart(req.Strategy)
	})
	api("POST", "/api/zapret/trial/probe", func(*http.Request) (any, error) { go a.zapret.TrialProbe(); return nil, nil })
	api("POST", "/api/zapret/trial/accept", func(*http.Request) (any, error) { return nil, a.zapret.TrialAccept() })
	api("POST", "/api/zapret/trial/cancel", func(*http.Request) (any, error) { a.zapret.TrialCancel(); return nil, nil })
	api("POST", "/api/zapret/restart", func(*http.Request) (any, error) { return nil, a.zapret.Start(a.settingsCopy()) })
	api("POST", "/api/tg/install", func(*http.Request) (any, error) { return nil, a.tgws.Install() })
	api("POST", "/api/tg/open", func(*http.Request) (any, error) {
		if !a.tgws.Running() {
			return nil, errors.New("Сначала включите tg-ws-proxy")
		}
		openURL(tgLink(a.settingsCopy()))
		return nil, nil
	})
	// Внешние ссылки открываются в браузере, а не в окне Naivka. Только из списка: страница не может
	// попросить открыть произвольный адрес или программу.
	api("POST", "/api/open", func(r *http.Request) (any, error) {
		var req struct {
			Link string `json:"link"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		u, ok := externalLinks[req.Link]
		if !ok {
			return nil, errors.New("неизвестная ссылка")
		}
		openURL(u)
		return nil, nil
	})
	api("POST", "/api/core/download", func(*http.Request) (any, error) { return nil, a.core.StartDownload() })
	api("POST", "/api/elevate", func(*http.Request) (any, error) {
		if isAdmin() {
			return nil, errors.New("Naivka уже запущена с правами администратора")
		}
		wasRunning := a.core.Running()
		a.core.Stop()
		if err := relaunchElevated(true, wasRunning); err != nil {
			if wasRunning {
				go a.connect()
			}
			return nil, fmt.Errorf("Не удалось перезапуститься: %v", err)
		}
		go func() { time.Sleep(400 * time.Millisecond); a.Quit() }()
		return nil, nil
	})
	api("POST", "/api/quit", func(*http.Request) (any, error) {
		go func() { time.Sleep(200 * time.Millisecond); a.Quit() }()
		return nil, nil
	})

	for _, path := range order {
		mux.HandleFunc(path, serve(table[path]))
	}
	return a.hostGuard(mux)
}

// hostGuard защищает от DNS rebinding: принимаем только запросы на наш адрес.
func (a *App) hostGuard(next http.Handler) http.Handler {
	ok1 := fmt.Sprintf("127.0.0.1:%d", a.port)
	ok2 := fmt.Sprintf("localhost:%d", a.port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != ok1 && r.Host != ok2 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type stateResp struct {
	CoreStatus
	Admin        bool   `json:"admin"`
	OS           string `json:"os"`
	NeedsRestart bool   `json:"needsRestart"`
	MixedPort    int    `json:"mixedPort"`
	Servers      int    `json:"servers"`
	DataDir      string `json:"dataDir"`
	Portable     bool   `json:"portable"`
	Secrets      bool   `json:"secrets"`   // пароли зашифрованы DPAPI
	Protected    bool   `json:"protected"` // ядро в Program Files
	Legacy       bool   `json:"legacy"`    // Windows 7/8
	Install      bool   `json:"install"`   // есть установка своего сервера по SSH
}

func (a *App) apiState(*http.Request) (any, error) {
	st := a.core.Status()
	a.mu.Lock()
	defer a.mu.Unlock()
	return stateResp{
		CoreStatus:   st,
		Admin:        isAdmin(),
		OS:           runtime.GOOS,
		NeedsRestart: st.Running && a.applied != "" && a.applied != a.settings.fingerprint(),
		MixedPort:    a.settings.MixedPort,
		Servers:      len(a.settings.Servers),
		DataDir:      a.dir,
		Portable:     portable,
		Secrets:      encryptSecrets,
		Protected:    a.core.Protected(),
		Legacy:       legacyWindows(),
		Install:      serverInstallSupported,
	}, nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("неверный запрос: %v", err)
	}
	return nil
}

func (a *App) apiSaveSettings(r *http.Request) (any, error) {
	ns := DefaultSettings()
	if err := decode(r, ns); err != nil {
		return nil, err
	}
	ns.normalize()
	if err := ns.Validate(); err != nil {
		return nil, err
	}
	if err := SaveSettings(a.dir, ns); err != nil {
		return nil, fmt.Errorf("не удалось сохранить настройки: %v", err)
	}
	a.mu.Lock()
	prev := a.settings.clone()
	prevSel := a.settings.Selected
	if ns.Window == nil {
		ns.Window = a.settings.Window
	}
	a.settings = ns
	a.mu.Unlock()
	if prevSel != ns.Selected {
		a.applySelection()
	}
	a.core.KeepProxyOnCrash = ns.KillSwitch // применяется сразу, без переподключения
	go func() {
		a.applyAddons(prev, ns)
		// zapret и tg-ws-proxy меняют маршруты ядра: применяем сразу, без кнопки «Переподключить».
		if a.core.Running() && addonsNeedRestart(prev, ns) {
			a.restartCore()
		}
	}()
	if err := a.applyAutostart(prev, ns); err != nil {
		return nil, err
	}
	return ns, nil
}

func (a *App) applySelection() {
	cl := a.core.Clash()
	// Ядро перезапускается — выбор и так возьмётся из настроек при старте.
	if cl == nil || a.isRestarting() || !a.core.Running() {
		return
	}
	a.mu.Lock()
	tag := tagAuto
	for _, sv := range a.settings.Servers {
		if sv.ID == a.settings.Selected {
			tag = serverTag(sv)
		}
	}
	if a.settings.Selected == "wl" || (a.wl.Active() && a.settings.WL.Enabled) || len(a.settings.Servers) == 0 {
		tag = "wl"
	}
	if strings.HasPrefix(a.settings.Selected, "w-") && !a.wl.Active() {
		tag = a.settings.Selected
	}
	a.mu.Unlock()
	if err := cl.Select(tag); err != nil {
		a.core.logs.Add("[naivka] не удалось переключить сервер: " + err.Error())
	}
}

func (a *App) apiImport(r *http.Request) (any, error) {
	var req struct {
		Text string `json:"text"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	servers, errs := ParseShareLinks(req.Text)
	if len(servers) == 0 && len(errs) == 0 {
		return nil, errors.New("Не найдено ни одной ссылки")
	}
	a.mu.Lock()
	ns := a.settings.clone()
	ns.Servers = append(ns.Servers, servers...)
	ns.normalize()
	if err := SaveSettings(a.dir, ns); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	a.settings = ns
	a.mu.Unlock()
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{"added": len(servers), "errors": errs, "settings": ns}, nil
}

func (a *App) apiSelect(r *http.Request) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	a.mu.Lock()
	ns := a.settings.clone()
	ns.Selected = req.ID
	ns.normalize()
	a.wl.setActive(false) // выбрал сам — автоматика подписки не мешает
	if err := SaveSettings(a.dir, ns); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	a.settings = ns
	a.mu.Unlock()
	a.applySelection()
	return nil, nil
}

type testResult struct {
	ID    string `json:"id"`
	Ms    int    `json:"ms"`
	Error string `json:"error,omitempty"`
	Kind  string `json:"kind"` // proxy — реальный запрос через сервер; tls — только рукопожатие
}

func (a *App) apiTest(r *http.Request) (any, error) {
	var req struct {
		ID    string `json:"id"`    // пусто = все
		Async bool   `json:"async"` // не ждать: результаты забирать из /api/test/results по мере готовности
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	a.mu.Lock()
	s := a.settings.clone()
	a.mu.Unlock()
	cl := a.core.Clash()

	var targets []Server
	for _, sv := range s.Servers {
		if req.ID == "" || sv.ID == req.ID {
			targets = append(targets, sv)
		}
	}
	// Все серверы проверяются одновременно; каждый результат сразу кладётся в serverTests.
	results := make([]testResult, len(targets))
	var wg sync.WaitGroup
	serverTests.begin(targets)
	for i, sv := range targets {
		wg.Add(1)
		go func(i int, sv Server) {
			defer wg.Done()
			res := testResult{ID: sv.ID}
			if cl != nil {
				res.Kind = "proxy"
				ms, err := cl.Delay(serverTag(sv), s.TestURL)
				res.Ms = ms
				if err != nil {
					res.Error = humanErr(err)
				}
			} else {
				res.Kind = "tls"
				ms, err := tlsProbe(sv)
				res.Ms = ms
				if err != nil {
					res.Error = humanErr(err)
				}
			}
			results[i] = res
			serverTests.put(res)
		}(i, sv)
	}
	if req.Async {
		return map[string]int{"started": len(targets)}, nil
	}
	wg.Wait()
	return results, nil
}

// serverTests — результаты идущей проверки серверов: страница забирает их по мере готовности,
// не дожидаясь самого медленного.
var serverTests = &testBoard{res: map[string]*testResult{}}

type testBoard struct {
	mu  sync.Mutex
	res map[string]*testResult // nil — ещё проверяется
}

func (b *testBoard) begin(list []Server) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sv := range list {
		b.res[sv.ID] = nil
	}
}

func (b *testBoard) put(r testResult) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.res[r.ID] = &r
}

// view — готовые результаты и список ещё проверяемых.
func (b *testBoard) view() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	done, pending := []testResult{}, []string{}
	for id, r := range b.res {
		if r == nil {
			pending = append(pending, id)
		} else {
			done = append(done, *r)
		}
	}
	return map[string]any{"done": done, "pending": pending}
}

// tlsProbe — проверка без подключения: сколько занимает TLS-рукопожатие с сервером.
func tlsProbe(sv Server) (int, error) {
	if sv.QUIC {
		return 0, errors.New("HTTP/3-сервер проверяется только при подключении")
	}
	if sv.Type == "hysteria2" || sv.Type == "tuic" {
		return 0, errors.New("UDP-сервер проверяется только при подключении")
	}
	sni := sv.SNI
	if sni == "" {
		sni = sv.Host
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	addr := net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port))
	start := time.Now()
	if sv.Type == "shadowsocks" || (sv.Type == "vless" && sv.Security == "none") { // без TLS — только TCP
		conn, err := d.Dial("tcp", addr)
		if err != nil {
			return 0, err
		}
		conn.Close()
		return int(time.Since(start) / time.Millisecond), nil
	}
	cfg := &tls.Config{ServerName: sni, NextProtos: []string{"h2", "http/1.1"}}
	if sv.PinSHA256 != "" { // свой сертификат: проверяем отпечаток ключа, а не удостоверяющий центр
		cfg.InsecureSkipVerify = true
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				if der, err := x509.MarshalPKIXPublicKey(cs.PeerCertificates[0].PublicKey); err == nil {
					h := sha256.Sum256(der)
					if base64.StdEncoding.EncodeToString(h[:]) == sv.PinSHA256 {
						return nil
					}
				}
			}
			return errors.New("сертификат сервера не совпал с отпечатком — возможно, подмена")
		}
	} else if sv.Insecure {
		cfg.InsecureSkipVerify = true
	}
	conn, err := tls.DialWithDialer(d, "tcp", addr, cfg)
	if err != nil {
		return 0, err
	}
	ms := int(time.Since(start) / time.Millisecond)
	conn.Close()
	return ms, nil
}

func humanErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "нет ответа"
	case strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused"):
		return "соединение отклонено"
	case strings.Contains(s, "no such host"):
		return "домен не найден"
	case strings.Contains(s, "certificate"):
		return "проблема с сертификатом"
	case strings.Contains(s, "reset"):
		return "соединение сброшено"
	}
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// applyAutostart включает/выключает автозапуск, когда меняется настройка или режим.
func (a *App) applyAutostart(prev, ns *Settings) error {
	if prev.AutoStart == ns.AutoStart && (prev.needsAdmin() == ns.needsAdmin() || !ns.AutoStart) {
		return nil
	}
	if err := setAutostart(ns.AutoStart, ns.needsAdmin()); err != nil {
		a.mu.Lock()
		a.settings.AutoStart = prev.AutoStart
		_ = SaveSettings(a.dir, a.settings)
		a.mu.Unlock()
		return fmt.Errorf("Автозапуск: %v", err)
	}
	return nil
}

func (a *App) apiRestore(r *http.Request) (any, error) {
	var req struct {
		Text     string `json:"text"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	var wrap struct {
		Format   string          `json:"format"`
		Settings json.RawMessage `json:"settings"`
	}
	raw, err := openBackup([]byte(req.Text), req.Password)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &wrap) == nil && wrap.Format == backupFormat {
		raw = wrap.Settings
	}
	// Принимаем только копию Naivka или её settings.json: в любом другом JSON
	// Unmarshal тихо дал бы пустые настройки и стёр бы все серверы.
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || keys["servers"] == nil || keys["rules"] == nil {
		return nil, errors.New("Файл не похож на резервную копию Naivka")
	}
	ns := DefaultSettings()
	if err := json.Unmarshal(raw, ns); err != nil {
		return nil, fmt.Errorf("файл не похож на резервную копию Naivka: %v", err)
	}
	if problems := openSettings(ns); len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	ns.normalize()
	if err := ns.Validate(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	ns.Window = a.settings.Window
	prev := a.settings.clone()
	a.settings = ns
	err = SaveSettings(a.dir, ns)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	_ = a.applyAutostart(prev, ns)
	a.mon.Event("info", fmt.Sprintf("Настройки восстановлены из копии: %d серверов, %d правил", len(ns.Servers), len(ns.Rules)), false)
	return ns, nil
}

var (
	conflictMu  sync.Mutex
	conflictMsg string
	conflictAt  time.Time
)

// zapretConflictCached — проверка чужого zapret раз в 10 секунд (она запускает sc и tasklist).
func zapretConflictCached() string {
	conflictMu.Lock()
	defer conflictMu.Unlock()
	if time.Since(conflictAt) > 10*time.Second {
		conflictMsg, conflictAt = zapretConflict(), time.Now()
	}
	return conflictMsg
}
