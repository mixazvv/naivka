package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	applied  string      // fingerprint настроек, с которыми запущено ядро
	applyT   *time.Timer // отложенное переподключение после изменения настроек (AutoApply)

	geo       serverGeoCache
	mon       *Monitor
	zapret    *Zapret
	tgws      *TgWs
	wl        *WL
	xray      *Xray
	lists     *listCache
	traffic   *trafficStore
	ipcc      *ipCountry // страна сайта по IP для глобуса (ipcountry.go)
	installer *serverInstaller
	friends   friendsSessions // SSH к своим серверам для «Друзей» (friends.go)

	restarter    serialRunner  // перезапуски ядра — по одному
	life         lifeState     // включение и выключение — по одному, и чего хочет пользователь
	fo           failoverState // временный переход на другой сервер
	scores       serverScores
	selectionMu  sync.Mutex
	selectionRev uint64      // guarded by selectionMu; invalidates pending automatic choices
	dnsg         dnsGuard    // запасной DNS, если прямой перестал отвечать
	ytdns        youtubeDNS  // DNS Google для адресов YouTube
	smart        *Smart      // автоопределение: что открывается из России напрямую
	self         selfUpdater // обновление самой Naivka
	proxyGuard   proxyGuard
	zr           zapretRouteState // куда сейчас идут сайты zapret    // чтобы чужая программа не перехватила системный прокси

	probeMu sync.Mutex
	probes  map[string]int // служебные входы теста скорости: тег → порт
	ui      uiHost         // окно и значок в трее (зависит от платформы)
	sleep   sleepState     // сон и пробуждение компьютера

	candidate *updateHandshake
	transfer  *updateTransfer // guarded by life.intentMu
	repair    repairState
	quitOnce  sync.Once
}

type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--update-supervisor" {
		if runUpdateSupervisor(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	args := map[string]bool{}
	for _, a := range os.Args[1:] {
		args[a] = true
	}

	var handshake updateHandshake
	candidate := args["--update-candidate"]
	if candidate {
		var err error
		handshake, err = readUpdateHandshake()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Не удалось подтвердить обновление")
			os.Exit(1)
		}
	} else {
		_ = os.Unsetenv(updateHandshakeEnv)
	}
	dir, err := appDataDir()
	if candidate && !sameUpdateFile(filepath.Dir(handshake.Intent), dir) {
		os.Exit(1)
	}
	if err != nil {
		fatal("Не удалось открыть папку данных: " + err.Error())
	}
	// Запуск из RunOnce, записанного версиями до 2.16.1: только вернуть системный прокси и выйти.
	if args["--restore-proxy"] {
		if inst, ok := readInstance(dir); !ok || !pingInstance(inst) {
			restoreSystemProxy(dir)
		}
		return
	}

	// Создание задачи автозапуска: этот экземпляр запущен с правами только ради неё (см. setAutostart).
	if args["--create-autostart-task"] {
		if err := createAutostartTask(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Пароли шифруются всегда, и в портативном режиме тоже: на другом компьютере их придётся
	// ввести заново (или перенести настройки резервной копией), зато папку с ними не прочитать.
	encryptSecrets = runtime.GOOS == "windows"
	s, serr := LoadSettings(dir)
	// Пароли, сохранённые прежними версиями открытым текстом, сразу шифруем.
	if serr == nil && encryptSecrets && !candidate {
		_ = SaveSettings(dir, s)
	}

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
	if candidate {
		a.candidate = &handshake
	}
	a.life.updating = candidate
	updateGeneration := a.life.gen.Load()
	usePAC = func() bool { return a.settingsCopy().ProxyPAC }
	a.core = NewCore(dir)
	a.mon = newMonitor(a)
	if code := takeUpdateResult(dir); code != "" && code != "UPDATED" {
		a.core.logs.Add("[naivka] " + code)
		a.mon.Event("warn", updateResultMessage(code), false)
	}
	a.core.OnCrash = a.onCrash
	a.zapret = newZapret(a)
	a.tgws = newTgWs(a)
	a.wl = newWL(a)
	a.smart = newSmart(a)
	a.xray = newXray(a)
	a.lists = newListCache(a.dir)
	a.traffic = newTrafficStore(a.dir)
	a.ipcc = newIPCountry(a.dir)
	a.installer = &serverInstaller{}
	a.core.OnStop = a.xray.Stop
	a.core.ExtraEnv = a.fillEnv
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
	if !candidate {
		go func() {
			if a.startupReady() {
				cleanupUpdate(dir)
			}
		}()
	}
	updateResume := args["--connect"]
	if candidate {
		if !a.startupReady() {
			os.Exit(1)
		}
		var err error
		updateResume, err = reportUpdateReady(handshake)
		if err != nil {
			os.Exit(1)
		}
		go cleanupCommittedUpdate(dir)
		if serr == nil && encryptSecrets {
			_ = SaveSettings(dir, s)
		}
	}
	go a.mon.run()
	go a.zapret.healthLoop()
	go a.zapret.failWatch()
	go a.dnsWatch()
	go a.youtubeDNSWatch()
	go a.smart.run()
	go a.updatesLoop()
	go a.wl.loop()
	go fixUnsafeAutostart(s.clone())
	a.watchPower()
	go a.startAddons()
	// --connect: перезапуск с правами из подключённого состояния — подключаемся снова сами.
	if candidate || args["--update-rollback"] {
		a.resumeAfterUpdate(updateResume, updateGeneration)
	} else if (s.AutoConnect || args["--connect"]) && len(s.Servers) > 0 && a.core.Present() {
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
func (a *App) connectLocked(gen int64) error {
	ctx, cancel, err := a.life.operation(gen, true)
	if err != nil {
		return err
	}
	defer cancel()
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.core.StartContext(ctx, s); err != nil {
		if !errors.Is(err, context.Canceled) {
			a.mon.Event("error", "Не удалось подключиться: "+firstLine(err.Error()), false)
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		a.core.Stop()
		return err
	}
	a.scores.reset()
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

func (a *App) Quit() { a.cancelUpdateExit(); a.quit(false) }

func (a *App) quit(forUpdate bool) {
	a.quitOnce.Do(func() {
		a.prepareUpdateExit(forUpdate)
		a.life.stopIntent(true)
		_ = a.repair.stop("")
		// Не чистим системное состояние параллельно с запуском. Отмена выше
		// прерывает ожидание проверки конфига и готовности ядра.
		a.life.mu.Lock()
		a.core.Stop()
		a.traffic.save(true)
		restoreSystemProxy(a.dir)
		a.zapret.Stop()
		a.tgws.Stop()
		a.xray.Stop()
		if a.ui != nil {
			a.ui.Close()
		}
		removeInstance(a.dir, a.token)
		a.core.Stop()
		restoreSystemProxy(a.dir) // последним шагом: что бы ни успело случиться за время остановки
		a.life.mu.Unlock()
		a.flushUpdateIntent()
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
