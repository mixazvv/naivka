package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

const appName = "Naivka"

type App struct {
	dir   string
	core  *Core
	token string
	port  int

	mu       sync.Mutex
	settings *Settings
	applied  string // fingerprint настроек, с которыми запущено ядро

	geo       serverGeoCache
	mon       *Monitor
	zapret    *Zapret
	tgws      *TgWs
	wl        *WL
	xray      *Xray
	lists     *listCache
	traffic   *trafficStore
	installer *serverInstaller

	restarter  serialRunner  // перезапуски ядра — по одному
	life       lifeState     // включение и выключение — по одному, и чего хочет пользователь
	fo         failoverState // временный переход на другой сервер
	dnsg       dnsGuard      // запасной DNS, если прямой перестал отвечать
	ytdns      youtubeDNS    // DNS Google для адресов YouTube
	smart      *Smart        // автоопределение: что открывается из России напрямую
	proxyGuard proxyGuard
	zr         zapretRouteState // куда сейчас идут сайты zapret    // чтобы чужая программа не перехватила системный прокси

	probeMu sync.Mutex
	probes  map[string]int // служебные входы теста скорости: тег → порт
	ui      uiHost         // окно и значок в трее (зависит от платформы)
	sleep   sleepState     // сон и пробуждение компьютера
	lan     *lanShare

	quitOnce sync.Once
}

type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func main() {
	args := map[string]bool{}
	for _, a := range os.Args[1:] {
		args[a] = true
	}

	dir, err := appDataDir()
	if err != nil {
		fatal("Не удалось открыть папку данных: " + err.Error())
	}
	// Запуск из RunOnce после сбоя или выключения: только вернуть системный прокси и выйти.
	if args["--restore-proxy"] {
		if inst, ok := readInstance(dir); !ok || !pingInstance(inst) {
			restoreSystemProxy(dir)
		}
		return
	}

	encryptSecrets = runtime.GOOS == "windows" && !portable
	s, serr := LoadSettings(dir)

	// TUN и zapret требуют прав администратора — перезапускаемся сразу, до всего остального.
	if runtime.GOOS == "windows" && s.needsAdmin() && !isAdmin() && !args["--no-elevate"] {
		if err := relaunchElevated(args["--replace"], args["--connect"]); err == nil {
			return
		}
		// Пользователь отказался в UAC — продолжаем без прав, интерфейс подскажет.
	}

	// Перезапуск с правами ждёт, пока прежний экземпляр закроется.
	if args["--replace"] {
		waitOldInstance(dir)
	}
	// Уже запущены? Тогда просто показываем окно существующего экземпляра.
	if !args["--replace"] {
		if inst, ok := readInstance(dir); ok && pingInstance(inst) {
			_ = postInstance(inst, "/api/show")
			return
		}
	}

	initLog(dir)
	if serr != nil {
		log.Println(serr)
	}

	// Если в прошлый раз программа упала с включённым системным прокси — вернём как было.
	restoreSystemProxy(dir)

	a := &App{dir: dir, settings: s, token: randHex(24)}
	a.core = NewCore(dir)
	a.mon = newMonitor(a)
	a.core.OnCrash = a.onCrash
	a.zapret = newZapret(a)
	a.tgws = newTgWs(a)
	a.wl = newWL(a)
	a.smart = newSmart(a)
	a.xray = newXray(a)
	a.lists = newListCache(a.dir)
	a.traffic = newTrafficStore(a.dir)
	a.installer = &serverInstaller{}
	a.core.OnStop = a.xray.Stop
	a.core.ExtraEnv = a.fillEnv
	go a.mon.run()
	go a.zapret.healthLoop()
	go a.zapret.failWatch()
	go a.dnsWatch()
	go a.youtubeDNSWatch()
	go a.smart.run()
	go a.updatesLoop()
	go a.wl.loop()
	if serr != nil {
		a.core.logs.Add("[naivka] " + serr.Error())
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal("Не удалось открыть локальный порт для интерфейса: " + err.Error())
	}
	a.port = ln.Addr().(*net.TCPAddr).Port
	if err := writeInstance(dir, instanceInfo{Port: a.port, Token: a.token, PID: os.Getpid()}); err != nil {
		log.Println("instance file:", err)
	}

	srv := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Println("http:", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; a.Quit() }()

	autostart := args["--autostart"]
	a.ui = newUI(a)
	a.watchPower()
	go a.startAddons()
	// --connect: перезапуск с правами из подключённого состояния — подключаемся снова сами.
	if (s.AutoConnect || args["--connect"]) && len(s.Servers) > 0 && a.core.Present() {
		go func() {
			if err := a.connect(); err != nil {
				a.core.logs.Add("[naivka] автоподключение: " + err.Error())
			}
		}()
	}

	// При автозапуске окно не открываем — Naivka тихо ждёт в трее.
	a.ui.Run(!autostart)
}

// waitOldInstance: перезапущенная с правами Naivka ждёт, пока прежняя закроется и отпустит порты.
func waitOldInstance(dir string) {
	inst, ok := readInstance(dir)
	if !ok {
		return
	}
	for i := 0; i < 50 && pingInstance(inst); i++ {
		time.Sleep(200 * time.Millisecond)
	}
}

func (a *App) url() string {
	return fmt.Sprintf("http://127.0.0.1:%d/?k=%s", a.port, a.token)
}

// connectLocked — подключение; вызывать только под a.life.mu (см. lifecycle.go).
func (a *App) connectLocked() error {
	a.mu.Lock()
	s := a.settings.clone()
	a.mu.Unlock()
	a.core.SetKeepProxyOnCrash(s.KillSwitch)
	// zapret поднимаем до ядра: от него зависит, пускать ли его сайты напрямую.
	if s.Zapret.Enabled && !a.zapret.Running() && a.zapret.root() != "" && !a.zapret.busy() {
		if err := a.zapret.Start(s); err != nil {
			a.mon.Event("warn", "zapret не запустился: "+firstLine(err.Error())+" — его сайты пойдут через сервер", false)
		}
	}
	if err := a.core.Start(s); err != nil {
		a.mon.Event("error", "Не удалось подключиться: "+firstLine(err.Error()), false)
		return err
	}
	mode := "системный прокси"
	if s.Mode == "tun" {
		mode = "весь трафик (TUN)"
	}
	a.mon.Event("ok", "Подключено, "+mode, false)
	a.proxyGuard.reset()
	go a.warnConflicts()
	a.mu.Lock()
	a.applied = s.fingerprint()
	a.mu.Unlock()
	// sing-box помнит прошлый выбор сервера в cache.db и берёт его вместо default —
	// выставляем выбор из настроек Naivka явно.
	a.applySelectionNow()
	return nil
}

func (a *App) Quit() {
	a.quitOnce.Do(func() {
		a.life.want.Store(false)
		a.life.gen.Add(1)
		// Если сейчас идёт подключение, ждём его (недолго): иначе оно могло бы включить
		// системный прокси уже после нашей остановки ядра — и интернет пропал бы до следующего запуска.
		locked := false
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if locked = a.life.mu.TryLock(); locked {
				break
			}
		}
		a.core.Stop()
		a.traffic.save(true)
		restoreSystemProxy(a.dir)
		a.zapret.Stop()
		a.tgws.Stop()
		a.xray.Stop()
		a.stopShare()
		if a.ui != nil {
			a.ui.Close()
		}
		removeInstance(a.dir, a.token)
		a.core.Stop()
		restoreSystemProxy(a.dir) // последним шагом: что бы ни успело случиться за время остановки
		if locked {
			a.life.mu.Unlock()
		}
		log.Println("exit")
		os.Exit(0)
	})
}

// ---------- служебное ----------

// Портативный режим: если рядом с exe лежит portable.txt, все данные хранятся
// в папке Naivka-data рядом с exe, а в %APPDATA% ничего не пишется.
var portable bool

func appDataDir() (string, error) {
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		if fileExists(filepath.Join(base, "portable.txt")) {
			portable = true
			dir := filepath.Join(base, "Naivka-data")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", fmt.Errorf("портативный режим: нет доступа на запись в %s (%v). Перенесите папку Naivka туда, где можно писать, например на рабочий стол или флешку", base, err)
			}
			return dir, nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appName)
	return dir, os.MkdirAll(dir, 0o700)
}

func initLog(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "naivka.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err == nil {
		log.SetOutput(f)
	}
}

func fatal(msg string) {
	log.Println(msg)
	messageBox(appName, msg)
	os.Exit(1)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func instancePath(dir string) string { return filepath.Join(dir, "instance.json") }

func readInstance(dir string) (instanceInfo, bool) {
	var inst instanceInfo
	b, err := os.ReadFile(instancePath(dir))
	if err != nil || json.Unmarshal(b, &inst) != nil || inst.Port == 0 {
		return inst, false
	}
	return inst, true
}

func writeInstance(dir string, inst instanceInfo) error {
	b, _ := json.Marshal(inst)
	return os.WriteFile(instancePath(dir), b, 0o600)
}

func removeInstance(dir, token string) {
	if inst, ok := readInstance(dir); ok && inst.Token == token {
		_ = os.Remove(instancePath(dir))
	}
}

var localHC = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}

func instanceReq(inst instanceInfo, method, path string) error {
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", inst.Port, path), nil)
	req.Header.Set("X-Naivka-Token", inst.Token)
	resp, err := localHC.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func pingInstance(inst instanceInfo) bool            { return instanceReq(inst, "GET", "/api/ping") == nil }
func postInstance(inst instanceInfo, p string) error { return instanceReq(inst, "POST", p) }
