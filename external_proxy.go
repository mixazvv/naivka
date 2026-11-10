package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExternalProxy owns foreground processes and private configuration files.
// sing-box remains responsible for routing, TUN, statistics and selection.
type ExternalProxy struct {
	addonBase
	revives    crashBudget
	a          *App
	kind, repo string
	op         sync.Mutex
	children   []*proxyChild
	key        string
	ports      map[string]int
}
type proxyChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	path string
}

func newExternalProxy(a *App, kind, repo string) *ExternalProxy {
	x := &ExternalProxy{a: a, kind: kind, repo: repo}
	x.state.Installed = readVersion(x.dir())
	return x
}
func (x *ExternalProxy) dir() string { return x.a.addonDir(x.kind) }
func (x *ExternalProxy) exe() string {
	if runtime.GOOS == "windows" {
		return x.kind + ".exe"
	}
	return x.kind
}
func (x *ExternalProxy) exePath() string    { return filepath.Join(x.dir(), "current", x.exe()) }
func (x *ExternalProxy) Present() bool      { return fileExists(x.exePath()) }
func (x *ExternalProxy) setLatest(v string) { x.mu.Lock(); x.state.Latest = v; x.mu.Unlock() }
func externalAsset(kind, tag, goos, arch string) (string, error) {
	if goos != "windows" {
		return "", errors.New("автоустановка этого компонента доступна в Windows")
	}
	if arch != "amd64" && arch != "arm64" && arch != "386" {
		return "", errors.New("неподдерживаемая архитектура")
	}
	if kind == "naive" {
		if arch == "amd64" {
			arch = "x64"
		}
		if arch == "386" {
			arch = "x86"
		}
		return "naiveproxy-" + tag + "-win-" + arch + ".zip", nil
	}
	if arch == "386" {
		arch = "x86"
	}
	return "mieru_" + strings.TrimPrefix(tag, "v") + "_windows_" + arch + ".zip", nil
}
func (x *ExternalProxy) Install() error {
	if legacyWindows() {
		return errors.New("официальный компонент требует современной Windows; существующие подключения sing-box сохранены")
	}
	if !x.setBusy("download") {
		return nil
	}
	go func() {
		err := x.install()
		x.finish(err)
		if err != nil {
			x.a.mon.Event("error", x.kind+": "+err.Error(), false)
		} else {
			x.a.mon.Event("ok", x.kind+" установлен — переподключитесь", false)
		}
	}()
	return nil
}
func (x *ExternalProxy) install() error {
	tag, err := latestTag(x.repo)
	if err != nil {
		return err
	}
	x.setLatest(tag)
	asset, err := externalAsset(x.kind, tag, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(x.dir(), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(x.dir(), "stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "download.zip")
	if err = downloadTo("https://github.com/"+x.repo+"/releases/download/"+tag+"/"+asset, archive, x.progress); err != nil {
		return err
	}
	if err = verifyAsset(x.repo, tag, asset, archive); err != nil {
		return err
	}
	if err = extractCore(archive, stage, []string{x.exe()}); err != nil {
		return err
	}
	_ = os.Remove(archive)
	x.op.Lock()
	defer x.op.Unlock()
	if len(x.children) != 0 {
		return errors.New("сначала отключите VPN, затем повторите обновление компонента")
	}
	current, backup := filepath.Join(x.dir(), "current"), filepath.Join(x.dir(), "previous")
	if err = os.RemoveAll(backup); err != nil {
		return err
	}
	had := fileExists(filepath.Join(current, x.exe()))
	if had {
		if err = os.Rename(current, backup); err != nil {
			return err
		}
	}
	if err = os.Rename(stage, current); err != nil {
		if had {
			_ = os.Rename(backup, current)
		}
		return err
	}
	if err = os.WriteFile(filepath.Join(x.dir(), "version.txt"), []byte(tag), 0600); err != nil {
		_ = os.RemoveAll(current)
		if had {
			_ = os.Rename(backup, current)
		}
		return err
	}
	x.mu.Lock()
	x.state.Installed = tag
	x.mu.Unlock()
	return nil
}

func buildNaiveConfig(servers map[string]Server, ports map[string]int) (M, error) {
	tags := make([]string, 0, len(servers))
	for tag := range servers {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	var listeners, proxies, resolvers []string
	mapped := map[string]string{}
	for _, tag := range tags {
		s := servers[tag]
		if s.UoT {
			return nil, errors.New("официальный NaïveProxy не поддерживает UDP-over-TCP; для этого профиля используется sing-box")
		}
		if s.Host == "" || s.Port < 1 || s.Port > 65535 || ports[tag] < 1 {
			return nil, errors.New("NaïveProxy: неверный адрес или порт")
		}
		scheme := "https"
		if s.QUIC {
			scheme = "quic"
		}
		host := orDefault(s.SNI, s.Host)
		if previous, ok := mapped[host]; ok && previous != s.Host {
			return nil, errors.New("NaïveProxy: один SNI указывает на разные адреса; разделите профили")
		}
		mapped[host] = s.Host
		u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(s.Port)), User: url.UserPassword(s.Username, s.Password)}
		if host != s.Host {
			if strings.ContainsAny(host+s.Host, " ,\r\n\t") {
				return nil, errors.New("NaïveProxy: неверный SNI")
			}
			resolvers = append(resolvers, "MAP "+host+" "+s.Host)
		}
		listeners = append(listeners, "socks://127.0.0.1:"+strconv.Itoa(ports[tag]))
		proxies = append(proxies, u.String())
	}
	cfg := M{"listen": listeners, "proxy": proxies}
	if len(resolvers) > 0 {
		cfg["host-resolver-rules"] = strings.Join(resolvers, ", ")
	}
	return cfg, nil
}

func (x *ExternalProxy) stopLocked() {
	for _, c := range x.children {
		_ = c.cmd.Process.Kill()
	}
	for _, c := range x.children {
		<-c.done
		_ = os.Remove(c.path)
	}
	x.children, x.ports, x.key = nil, nil, ""
}
func (x *ExternalProxy) Stop() {
	if x == nil {
		return
	}
	x.op.Lock()
	defer x.op.Unlock()
	x.stopLocked()
}

func (x *ExternalProxy) Sync(servers map[string]Server) map[string]int {
	x.op.Lock()
	defer x.op.Unlock()
	b, _ := json.Marshal(servers)
	key := string(b)
	alive := len(x.children) > 0
	for _, c := range x.children {
		select {
		case <-c.done:
			alive = false
		default:
		}
	}
	if alive && x.key == key {
		return x.ports
	}
	x.stopLocked()
	if len(servers) == 0 || !x.Present() {
		return nil
	}
	if x.kind == "mieru" && len(servers) > 32 {
		x.a.mon.Event("error", "Mieru: максимум 32 активных профиля; удалите лишние профили", false)
		return nil
	}
	ports := map[string]int{}
	allocated := map[int]bool{}
	for tag := range servers {
		p, err := freePort()
		if err != nil {
			return nil
		}
		for allocated[p] {
			p, err = freePort()
			if err != nil {
				return nil
			}
		}
		allocated[p] = true
		ports[tag] = p
	}
	start := func(cfg M, checkPorts []int) error {
		f, err := os.CreateTemp(x.a.core.runDir(), x.kind+"-*.json")
		if err != nil {
			return err
		}
		path := f.Name()
		cb, err := json.Marshal(cfg)
		if err == nil {
			_, err = f.Write(cb)
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
			return err
		}
		cmd := exec.Command(x.exePath(), path)
		if x.kind == "mieru" {
			cmd = exec.Command(x.exePath(), "run")
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "MIERU_CONFIG_FILE=") && !strings.HasPrefix(e, "MIERU_CONFIG_JSON_FILE=") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, "MIERU_CONFIG_JSON_FILE="+path)
		}
		cmd.Dir = filepath.Dir(x.exePath())
		hideWindow(cmd)
		// Upstream output may include credentials. Never copy it into reports.
		if err = cmd.Start(); err != nil {
			_ = os.Remove(path)
			return err
		}
		attachToJob(cmd.Process)
		c := &proxyChild{cmd: cmd, done: make(chan struct{}), path: path}
		x.children = append(x.children, c)
		go func() { _ = cmd.Wait(); close(c.done); x.childExited(c) }()
		deadline := time.Now().Add(8 * time.Second)
		for _, port := range checkPorts {
			ready := false
			for time.Now().Before(deadline) {
				select {
				case <-c.done:
					return errors.New("компонент завершился при запуске; проверьте параметры сервера")
				default:
				}
				conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
				if err == nil {
					_ = conn.Close()
					ready = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !ready {
				return errors.New("компонент не открыл локальный прокси за 8 секунд")
			}
		}
		return nil
	}
	var err error
	if x.kind == "naive" {
		var cfg M
		cfg, err = buildNaiveConfig(servers, ports)
		if err == nil {
			var ps []int
			for _, p := range ports {
				ps = append(ps, p)
			}
			err = start(cfg, ps)
		}
	} else {
		for tag, s := range servers {
			var cfg M
			cfg, err = buildMieruConfig(s, ports[tag])
			if err == nil {
				err = start(cfg, []int{ports[tag]})
			}
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		x.stopLocked()
		x.a.mon.Event("error", fmt.Sprintf("%s: %s", x.kind, err), false)
		return nil
	}
	x.key, x.ports = key, ports
	return ports
}

func (x *ExternalProxy) childExited(child *proxyChild) {
	x.op.Lock()
	active := false
	for _, c := range x.children {
		if c == child && x.key != "" {
			active = true
			break
		}
	}
	x.op.Unlock()
	if !active || !x.a.core.Running() {
		return
	}
	if !x.revives.allow(3, 10*time.Minute) {
		x.a.mon.Event("error", x.kind+" несколько раз завершился — переподключитесь и проверьте настройки", true)
		return
	}
	x.a.mon.Event("warn", x.kind+" неожиданно завершился — перезапускаю подключение", false)
	x.a.restartCore()
}
