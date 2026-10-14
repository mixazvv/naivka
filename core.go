package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---------- журнал ----------

type LogLine struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

type LogRing struct {
	mu    sync.Mutex
	lines []LogLine
	seq   int64
	max   int
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (r *LogRing) Add(text string) {
	text = strings.TrimRight(ansiRe.ReplaceAllString(text, ""), "\r\n")
	if text == "" {
		return
	}
	// Cronet (Chromium) проверяет доступность IPv6 пробным UDP-сокетом к 2001:4860:4860::8888.
	// Без IPv6 это пишется как ERROR, хотя ни на что не влияет.
	if strings.Contains(text, "[2001:4860:4860::8888]:443") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	r.lines = append(r.lines, LogLine{Seq: r.seq, Text: text})
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
}

func (r *LogRing) After(seq int64) []LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []LogLine{}
	for _, l := range r.lines {
		if l.Seq > seq {
			out = append(out, l)
		}
	}
	return out
}

func (r *LogRing) Tail(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := len(r.lines) - n
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for _, l := range r.lines[start:] {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// ---------- ядро ----------

type CoreInfo struct {
	Present bool   `json:"present"`
	Version string `json:"version"`
	Naive   bool   `json:"naive"`  // собран с with_naive_outbound
	Cronet  bool   `json:"cronet"` // рядом лежит libcronet
}

type DownloadState struct {
	Active  bool   `json:"active"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Error   string `json:"error"`
	Version string `json:"version"`
}

type CoreStatus struct {
	Running   bool          `json:"running"`
	Blocked   bool          `json:"blocked"` // ядро упало, трафик удерживается защитой от утечек
	PortIssue *PortIssue    `json:"portIssue,omitempty"`
	Starting  bool          `json:"starting"`
	Error     string        `json:"error"`
	StartedAt int64         `json:"startedAt"`
	Mode      string        `json:"mode"`
	Info      CoreInfo      `json:"core"`
	Download  DownloadState `json:"download"`
}

type Core struct {
	dir  string
	logs *LogRing

	mu        sync.Mutex
	cmd       *exec.Cmd
	done      chan struct{}
	running   bool
	starting  bool
	stopping  bool
	startedAt time.Time
	lastErr   string
	mode      string
	sysProxy  int // порт, на который указывает включённый нами системный прокси; 0 — не включали
	apiPort   int
	secret    string
	probeD    int
	probeP    int
	info      CoreInfo
	dl        DownloadState
	blocked   bool
	portIssue *PortIssue

	// KeepProxyOnCrash: при падении ядра не возвращать системный прокси —
	// трафик упрётся в закрытый порт и не уйдёт напрямую (защита от утечек).
	keepProxy bool // под c.mu: см. SetKeepProxyOnCrash
	// OnCrash вызывается при неожиданном завершении ядра.
	OnCrash func(msg string)
	// ExtraEnv дополняет параметры конфига (zapret, tg-ws-proxy).
	ExtraEnv func(s *Settings, env *ConfigEnv)
	// OnStop вызывается после остановки ядра (гасим Xray).
	OnStop func()
}

func NewCore(dir string) *Core {
	c := &Core{dir: dir, logs: &LogRing{max: 2000}}
	_ = os.MkdirAll(c.coreDir(), 0o755)
	_ = os.MkdirAll(c.runDir(), 0o700)
	c.refreshInfo()
	return c
}

// С правами администратора (режим TUN) ядро и его конфиг лежат в Program Files,
// куда обычный пользователь писать не может: вредонос не подменит sing-box.exe.
func (c *Core) coreDir() string {
	if pb := protectedBase(); pb != "" {
		return filepath.Join(pb, "core")
	}
	return filepath.Join(c.dir, "core")
}

func (c *Core) runDir() string {
	if pb := protectedBase(); pb != "" {
		return filepath.Join(pb, "run")
	}
	return filepath.Join(c.dir, "run")
}

func (c *Core) Protected() bool { return protectedBase() != "" }

func exeName() string {
	if runtime.GOOS == "windows" {
		return "sing-box.exe"
	}
	return "sing-box"
}

func cronetName() string {
	switch runtime.GOOS {
	case "windows":
		return "libcronet.dll"
	case "darwin":
		return "libcronet.dylib"
	}
	return "libcronet.so"
}

func (c *Core) exePath() string { return filepath.Join(c.coreDir(), exeName()) }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (c *Core) Present() bool { return fileExists(c.exePath()) }

func (c *Core) refreshInfo() {
	info := CoreInfo{Present: c.Present(), Cronet: fileExists(filepath.Join(c.coreDir(), cronetName()))}
	if info.Present {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, c.exePath(), "version")
		cmd.Dir = c.coreDir()
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "sing-box version ") {
					info.Version = strings.TrimPrefix(line, "sing-box version ")
				}
				if strings.HasPrefix(line, "Tags:") && strings.Contains(line, "with_naive_outbound") {
					info.Naive = true
				}
			}
		} else {
			c.logs.Add("[naivka] не удалось узнать версию ядра: " + err.Error())
		}
	}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
}

func (c *Core) Status() CoreStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := CoreStatus{
		Running:   c.running,
		Blocked:   c.blocked,
		PortIssue: c.portIssue,
		Starting:  c.starting,
		Error:     c.lastErr,
		Mode:      c.mode,
		Info:      c.info,
		Download:  c.dl,
	}
	if c.running {
		st.StartedAt = c.startedAt.Unix()
	}
	return st
}

// SetKeepProxyOnCrash — защита от утечек: при падении ядра не возвращать системный прокси.
func (c *Core) SetKeepProxyOnCrash(v bool) {
	c.mu.Lock()
	c.keepProxy = v
	c.mu.Unlock()
}

func (c *Core) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// Clash возвращает клиент API работающего ядра (или nil).
// SystemProxyPort — порт системного прокси, который сейчас держит Naivka; 0 — не держит
// (режим TUN, ядро остановлено или останавливается).
func (c *Core) SystemProxyPort() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running || c.stopping {
		return 0
	}
	return c.sysProxy
}

func (c *Core) Clash() *Clash {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil
	}
	return NewClash(c.apiPort, c.secret)
}

// ProbePorts — служебные входы для проверки IP (0, если ядро не запущено).
func (c *Core) ProbePorts() (direct, proxy int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return 0, 0
	}
	return c.probeD, c.probeP
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (c *Core) setErr(msg string) {
	c.mu.Lock()
	c.lastErr = msg
	c.mu.Unlock()
}

func (c *Core) Start(s *Settings) (err error) {
	c.mu.Lock()
	if c.running || c.starting {
		c.mu.Unlock()
		return nil
	}
	if c.dl.Active {
		c.mu.Unlock()
		return errors.New("Дождитесь окончания загрузки ядра")
	}
	c.starting = true
	c.lastErr = ""
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.starting = false
		c.portIssue = nil
		if err != nil {
			c.lastErr = err.Error()
			if pi, ok := err.(*PortIssue); ok {
				c.portIssue = pi
			}
		}
		c.mu.Unlock()
	}()

	if !c.Present() {
		return errors.New("Ядро sing-box не найдено. Скачайте его в «Настройках».")
	}
	// Порт локального прокси должен быть свободен — иначе sing-box упадёт с непонятной ошибкой.
	if err := c.freePortFor(s.MixedPort); err != nil {
		return err
	}
	if s.Share.Enabled {
		if err := c.freePortFor(s.Share.Port); err != nil {
			return err
		}
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	secret := randHex(16)
	probeD, err1 := freePort()
	probeP, err2 := freePort()
	if err1 != nil || err2 != nil || probeD == probeP {
		probeD, probeP = 0, 0
	}
	c.mu.Lock()
	noNaive := !c.info.Naive
	c.mu.Unlock()
	env := ConfigEnv{
		APIPort: port, Secret: secret, CachePath: filepath.Join(c.runDir(), "cache.db"),
		ProbeDirectPort: probeD, ProbeProxyPort: probeP, NoNaive: noNaive,
	}
	if c.ExtraEnv != nil {
		c.ExtraEnv(s, &env)
	}
	cfg, err := BuildConfig(s, env)
	if err != nil {
		return err
	}
	if c.Protected() {
		for _, k := range sanitizePaths(cfg, c.runDir()) {
			c.logs.Add("[naivka] режим администратора: убран путь к файлу из конфига (" + k + ")")
		}
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	cfgPath := filepath.Join(c.runDir(), "config.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return err
	}

	// Проверяем конфиг заранее — так ошибка будет понятной.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	check := exec.CommandContext(ctx, c.exePath(), "check", "-c", cfgPath)
	check.Dir = c.coreDir()
	hideWindow(check)
	out, cerr := check.CombinedOutput()
	cancel()
	if cerr != nil {
		msg := strings.TrimSpace(ansiRe.ReplaceAllString(string(out), ""))
		if msg == "" {
			msg = cerr.Error()
		}
		c.logs.Add("[naivka] проверка конфига: " + msg)
		return fmt.Errorf("Конфиг не прошёл проверку sing-box:\n%s", msg)
	}

	cmd := exec.Command(c.exePath(), "run", "-c", cfgPath, "-D", c.runDir())
	cmd.Dir = c.coreDir() // рядом лежит libcronet
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("Не удалось запустить sing-box: %v", err)
	}
	attachToJob(cmd.Process)
	c.logs.Add(fmt.Sprintf("[naivka] ядро запущено (режим %s, маршрут %s)", s.Mode, s.Routing))

	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			c.logs.Add(sc.Text())
		}
	}()
	done := make(chan struct{})
	// Запоминаем процесс до того, как начнём ждать его выхода: если ядро сразу упадёт,
	// onExit должен узнать «своё» ядро, иначе состояние так и осталось бы указывать на мёртвый процесс.
	c.mu.Lock()
	c.cmd, c.done, c.apiPort, c.secret, c.stopping = cmd, done, port, secret, false
	c.probeD, c.probeP = probeD, probeP
	c.mu.Unlock()
	go func() {
		werr := cmd.Wait()
		pw.Close()
		c.onExit(cmd, werr)
		close(done)
	}()

	// Ждём, пока поднимется Clash API (ядро может качать наборы правил).
	cl := NewClash(port, secret)
	deadline := time.Now().Add(45 * time.Second)
	for {
		select {
		case <-done:
			tail := c.logs.Tail(12)
			return fmt.Errorf("sing-box завершился при запуске:\n%s", tail)
		default:
		}
		if cl.Ping() == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return errors.New("sing-box не ответил за 45 секунд. Подробности — в журнале.")
		}
		time.Sleep(250 * time.Millisecond)
	}

	c.mu.Lock()
	c.running = true
	c.blocked = false
	c.startedAt = time.Now()
	c.mode = s.Mode
	c.mu.Unlock()

	if s.Mode == "proxy" {
		if err := setSystemProxy(c.dir, s.MixedPort); err != nil {
			c.logs.Add("[naivka] не удалось включить системный прокси: " + err.Error())
		} else {
			c.logs.Add(fmt.Sprintf("[naivka] системный прокси: 127.0.0.1:%d", s.MixedPort))
			c.mu.Lock()
			c.sysProxy = s.MixedPort
			c.mu.Unlock()
		}
	}
	return nil
}

func (c *Core) onExit(cmd *exec.Cmd, werr error) {
	c.mu.Lock()
	if c.cmd != cmd {
		c.mu.Unlock()
		return
	}
	wasRunning := c.running
	crashed := !c.stopping && wasRunning
	c.sysProxy = 0
	keep := crashed && c.keepProxy && c.mode == "proxy"
	c.running = false
	c.cmd = nil
	c.stopping = false
	var msg string
	if crashed {
		msg = "sing-box неожиданно завершился"
		if werr != nil {
			msg += ": " + werr.Error()
		}
		c.lastErr = msg
		c.blocked = keep
		c.logs.Add("[naivka] " + msg)
	}
	onCrash := c.OnCrash
	c.mu.Unlock()
	if !keep {
		restoreSystemProxy(c.dir)
	}
	if crashed && onCrash != nil {
		go onCrash(msg)
	}
}

func (c *Core) Stop() {
	c.mu.Lock()
	cmd, done := c.cmd, c.done
	c.blocked = false
	if cmd == nil {
		c.mu.Unlock()
		restoreSystemProxy(c.dir)
		return
	}
	c.stopping = true
	c.sysProxy = 0
	c.mu.Unlock()

	// Сначала возвращаем системный прокси — иначе интернет пропадёт до перезапуска.
	restoreSystemProxy(c.dir)
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	if c.OnStop != nil {
		c.OnStop()
	}
	c.logs.Add("[naivka] ядро остановлено")
}

// ---------- загрузка ядра с GitHub ----------

const singboxRepo = "https://github.com/SagerNet/sing-box"

func latestSingboxTag() (string, error) {
	hc := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := hc.Head(singboxRepo + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("GitHub вернул неожиданный ответ (%s)", resp.Status)
	}
	return loc[i+5:], nil
}

type progressWriter struct {
	c *Core
	n int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	p.c.mu.Lock()
	p.c.dl.Done = p.n
	p.c.mu.Unlock()
	return len(b), nil
}

func (c *Core) StartDownload() error {
	if runtime.GOOS != "windows" {
		return errors.New("Автозагрузка ядра сделана для Windows. Положите sing-box и libcronet в папку core вручную.")
	}
	c.mu.Lock()
	if c.running || c.starting {
		c.mu.Unlock()
		return errors.New("Сначала отключитесь — работающее ядро нельзя заменить")
	}
	if c.dl.Active {
		c.mu.Unlock()
		return nil
	}
	c.dl = DownloadState{Active: true}
	c.mu.Unlock()

	go func() {
		ver, err := c.download()
		c.mu.Lock()
		c.dl.Active = false
		if err != nil {
			c.dl.Error = err.Error()
		} else {
			c.dl.Version = ver
		}
		c.mu.Unlock()
		if err != nil {
			c.logs.Add("[naivka] загрузка ядра: " + err.Error())
		} else {
			c.logs.Add("[naivka] ядро обновлено до " + ver)
		}
		c.refreshInfo()
	}()
	return nil
}

func (c *Core) download() (string, error) {
	tag, err := latestSingboxTag()
	if err != nil {
		return "", fmt.Errorf("не удалось узнать последнюю версию: %v", err)
	}
	ver := strings.TrimPrefix(tag, "v")
	suffix := ""
	want := []string{"sing-box.exe", "libcronet.dll"}
	if legacyWindows() {
		// Для Windows 7 у sing-box есть отдельная сборка — без Naive и без libcronet.
		suffix = "-legacy-windows-7"
		want = []string{"sing-box.exe"}
	}
	name := fmt.Sprintf("sing-box-%s-windows-%s%s.zip", ver, runtime.GOARCH, suffix)
	asset := fmt.Sprintf("%s/releases/download/%s/%s", singboxRepo, tag, name)

	hc := &http.Client{Timeout: 10 * time.Minute}
	resp, err := hc.Get(asset)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub ответил %s на %s", resp.Status, asset)
	}
	c.mu.Lock()
	c.dl.Total = resp.ContentLength
	c.mu.Unlock()

	tmp, err := os.CreateTemp(c.runDir(), "core-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(io.MultiWriter(tmp, &progressWriter{c: c}), resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	if ok, err := verifyAsset(strings.TrimPrefix(singboxRepo, "https://github.com/"), tag, name, tmp.Name()); err != nil {
		return "", err
	} else if !ok {
		c.logs.Add("[naivka] GitHub не сообщил контрольную сумму ядра, архив получен только по HTTPS")
	}

	if err := os.MkdirAll(c.coreDir(), 0o755); err != nil {
		return "", err
	}
	if err := extractCore(tmp.Name(), c.coreDir(), want); err != nil {
		return "", err
	}
	return ver, nil
}

// extractCore достаёт нужные файлы из архива релиза (в нём они лежат во вложенной папке).
func extractCore(zipPath, dst string, want []string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	found := map[string]bool{}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		ok := false
		for _, w := range want {
			if strings.EqualFold(base, w) {
				ok = true
			}
		}
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmp := filepath.Join(dst, base+".new")
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dst, base)); err != nil {
			return fmt.Errorf("не удалось заменить %s (файл занят?): %v", base, err)
		}
		found[strings.ToLower(base)] = true
	}
	for _, w := range want {
		if !found[strings.ToLower(w)] {
			return fmt.Errorf("в архиве нет %s", w)
		}
	}
	return nil
}

// sanitizePaths убирает из конфига пути к файлам, кроме своей рабочей папки.
// Нужен в режиме администратора: настройки лежат там, где их может изменить
// обычный пользователь, и через «ручные правки» можно было бы заставить ядро
// с правами администратора писать в любой файл.
func sanitizePaths(v any, allowed string) []string {
	var removed []string
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch n := node.(type) {
		case map[string]any:
			for k, val := range n {
				// В транспортах (WebSocket, HTTPUpgrade, HTTP) path — часть адреса на сервере, а не файл.
				isTransport := strings.HasSuffix(path, ".transport")
				if s, ok := val.(string); ok && !isTransport && (k == "path" || k == "output" || strings.HasSuffix(k, "_path")) {
					if !strings.HasPrefix(filepath.Clean(s), filepath.Clean(allowed)+string(filepath.Separator)) {
						delete(n, k)
						removed = append(removed, path+"."+k)
					}
					continue
				}
				walk(val, path+"."+k)
			}
		case []any:
			for i, val := range n {
				walk(val, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(v, "")
	return removed
}
