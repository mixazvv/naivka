package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const xrayRepo = "XTLS/Xray-core"

// Xray нужен только для серверов на транспорте xHTTP: sing-box его не умеет.
// Для каждого такого сервера Xray открывает локальный вход SOCKS, а sing-box шлёт туда трафик
// как в обычный сервер — правила, статистика и автовыбор остаются в sing-box.
type Xray struct {
	addonBase
	a *App

	pmu   sync.Mutex
	cmd   *exec.Cmd
	done  chan struct{}
	key   string
	ports map[string]int

	stopping bool
	revives  crashBudget
}

func newXray(a *App) *Xray {
	x := &Xray{a: a}
	x.state.Installed = readVersion(x.dir())
	return x
}

func (x *Xray) dir() string { return x.a.addonDir("xray") }

func xrayExe() string {
	if runtime.GOOS == "windows" {
		return "xray.exe"
	}
	return "xray"
}

func (x *Xray) exePath() string { return filepath.Join(x.dir(), xrayExe()) }

func (x *Xray) Present() bool { return fileExists(x.exePath()) }

func (x *Xray) setLatest(tag string) {
	x.mu.Lock()
	x.state.Latest = tag
	x.mu.Unlock()
}

func xrayAsset() string {
	switch {
	case runtime.GOOS != "windows":
		return "Xray-linux-64.zip"
	case legacyWindows() && runtime.GOARCH == "386":
		return "Xray-win7-32.zip"
	case legacyWindows():
		return "Xray-win7-64.zip"
	case runtime.GOARCH == "arm64":
		return "Xray-windows-arm64-v8a.zip"
	case runtime.GOARCH == "386":
		return "Xray-windows-32.zip"
	}
	return "Xray-windows-64.zip"
}

func (x *Xray) Install() error {
	if !x.setBusy("download") {
		return nil
	}
	go func() {
		err := x.install()
		x.finish(err)
		if err != nil {
			x.a.mon.Event("error", "Xray: не удалось установить: "+err.Error(), false)
			return
		}
		x.a.mon.Event("ok", "Xray "+readVersion(x.dir())+" установлен — серверы xHTTP заработают после переподключения", false)
	}()
	return nil
}

func (x *Xray) install() error {
	tag, err := latestTag(xrayRepo)
	if err != nil {
		return fmt.Errorf("не удалось узнать последнюю версию: %v", err)
	}
	x.setLatest(tag)
	if err := os.MkdirAll(x.dir(), 0o755); err != nil {
		return err
	}
	zipPath := filepath.Join(x.dir(), "xray.zip")
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", xrayRepo, tag, xrayAsset())
	if err := downloadTo(url, zipPath, x.progress); err != nil {
		return err
	}
	defer os.Remove(zipPath)
	if err := verifyAsset(xrayRepo, tag, xrayAsset(), zipPath); err != nil {
		return err
	}
	x.Stop()
	if err := extractCore(zipPath, x.dir(), []string{xrayExe()}); err != nil {
		return err
	}
	_ = os.Chmod(x.exePath(), 0o755)
	if err := os.WriteFile(filepath.Join(x.dir(), "version.txt"), []byte(tag), 0o644); err != nil {
		return err
	}
	x.mu.Lock()
	x.state.Installed = tag
	x.mu.Unlock()
	return nil
}

// xrayOutbound — сервер VLESS + xHTTP в формате Xray.
func xrayOutbound(sv Server, tag string) map[string]any {
	user := map[string]any{"id": sv.UUID, "encryption": "none"}
	if sv.Flow != "" {
		user["flow"] = sv.Flow
	}
	sni := sv.SNI
	if sni == "" {
		sni = sv.Host
	}
	xh := map[string]any{"path": orDefault(sv.Path, "/")}
	if sv.HostHeader != "" {
		xh["host"] = sv.HostHeader
	}
	if sv.XMode != "" {
		xh["mode"] = sv.XMode
	}
	if sv.XExtra != "" {
		// Только объект: остаётся внутри xhttpSettings и не трогает остальной конфиг Xray.
		var extra map[string]any
		if json.Unmarshal([]byte(sv.XExtra), &extra) == nil && extra != nil {
			xh["extra"] = extra
		}
	}
	stream := map[string]any{"network": "xhttp", "xhttpSettings": xh, "security": sv.Security}
	switch sv.Security {
	case "reality":
		stream["realitySettings"] = map[string]any{"serverName": sni, "fingerprint": orDefault(sv.Fingerprint, "chrome"),
			"publicKey": sv.PublicKey, "shortId": sv.ShortID}
	case "tls":
		t := map[string]any{"serverName": sni, "fingerprint": orDefault(sv.Fingerprint, "chrome")}
		if sv.ALPN != "" {
			t["alpn"] = strings.Split(sv.ALPN, ",")
		}
		if sv.Insecure {
			t["allowInsecure"] = true
		}
		stream["tlsSettings"] = t
	default:
		stream["security"] = "none"
	}
	return map[string]any{
		"tag": tag, "protocol": "vless",
		"settings":       map[string]any{"vnext": []any{map[string]any{"address": sv.Host, "port": sv.Port, "users": []any{user}}}},
		"streamSettings": stream,
	}
}

func buildXrayConfig(servers map[string]Server, ports map[string]int) map[string]any {
	tags := make([]string, 0, len(servers))
	for t := range servers {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	var in, out, rules []any
	for _, t := range tags {
		in = append(in, map[string]any{"tag": "in-" + t, "listen": "127.0.0.1", "port": ports[t], "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}})
		out = append(out, xrayOutbound(servers[t], "out-"+t))
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []any{"in-" + t}, "outboundTag": "out-" + t})
	}
	return map[string]any{
		"log":       map[string]any{"loglevel": "warning", "access": "none"}, // без записи о каждом соединении
		"inbounds":  in,
		"outbounds": out,
		"routing":   map[string]any{"rules": rules},
	}
}

// Sync приводит запущенный Xray к нужному набору серверов xHTTP (тег → сервер).
// Если набор не изменился — Xray не перезапускается.
func (x *Xray) Sync(servers map[string]Server) map[string]int {
	if len(servers) == 0 || !x.Present() {
		x.Stop()
		return nil
	}
	b, _ := json.Marshal(servers)
	key := string(b)
	x.pmu.Lock()
	if x.cmd != nil && x.key == key {
		ports := x.ports
		x.pmu.Unlock()
		return ports
	}
	x.pmu.Unlock()
	x.Stop()
	ports := map[string]int{}
	for t := range servers {
		if p, err := freePort(); err == nil {
			ports[t] = p
		}
	}
	cfgPath := filepath.Join(x.a.core.runDir(), "xray.json")
	cb, _ := json.MarshalIndent(buildXrayConfig(servers, ports), "", "  ")
	if err := os.WriteFile(cfgPath, cb, 0o600); err != nil {
		x.a.core.logs.Add("[xray] " + err.Error())
		return nil
	}
	if err := x.start(cfgPath, ports); err != nil {
		x.a.mon.Event("error", "Xray не запустился: "+firstLine(err.Error())+" — серверы xHTTP пропущены", false)
		return nil
	}
	x.pmu.Lock()
	x.key, x.ports = key, ports
	x.pmu.Unlock()
	return ports
}

func (x *Xray) start(cfgPath string, ports map[string]int) error {
	cmd := exec.Command(x.exePath(), "run", "-c", cfgPath)
	cmd.Dir = x.dir()
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	attachToJob(cmd.Process)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			x.a.core.logs.Add("[xray] " + sc.Text())
		}
	}()
	done := make(chan struct{})
	go func() {
		werr := cmd.Wait()
		pw.Close()
		x.pmu.Lock()
		// Упал уже работавший Xray (входы были открыты), а не при запуске и не по Stop.
		crashed := x.cmd == cmd && !x.stopping && x.key != ""
		if x.cmd == cmd {
			x.cmd, x.key, x.ports = nil, "", nil
		}
		x.pmu.Unlock()
		close(done)
		if crashed {
			x.onCrash(werr)
		}
	}()
	x.pmu.Lock()
	x.cmd, x.done, x.stopping = cmd, done, false
	x.pmu.Unlock()
	// ждём, пока Xray откроет входы
	var any int
	for _, p := range ports {
		any = p
		break
	}
	for i := 0; i < 40; i++ {
		select {
		case <-done:
			return errors.New("Xray сразу завершился:\n" + x.a.core.logs.Tail(6))
		default:
		}
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", any), 200*time.Millisecond); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("Xray не открыл входы за 4 секунды")
}

// onCrash: Xray закрылся сам — серверы xHTTP перестали работать. Входы Xray получают
// новые порты, поэтому поднимаем его вместе с ядром.
func (x *Xray) onCrash(werr error) {
	msg := "Xray неожиданно закрылся"
	if werr != nil {
		msg += ": " + werr.Error()
	}
	if !x.a.core.Running() {
		return
	}
	if !x.revives.allow(3, 10*time.Minute) {
		x.a.mon.Event("error", msg+". Он падает раз за разом — серверы xHTTP не работают до переподключения. Подробности — в журнале", true)
		return
	}
	x.a.mon.Event("warn", msg+" — запускаю снова", false)
	time.Sleep(2 * time.Second)
	x.a.restartCore()
}

func (x *Xray) Stop() {
	x.pmu.Lock()
	cmd, done := x.cmd, x.done
	if cmd != nil {
		x.stopping = true
	}
	x.pmu.Unlock()
	if cmd == nil {
		return
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func (x *Xray) Running() bool {
	x.pmu.Lock()
	defer x.pmu.Unlock()
	return x.cmd != nil
}

type XrayView struct {
	AddonState
	Running bool `json:"running"`
	Servers int  `json:"servers"`
}

func (x *Xray) View() XrayView {
	v := XrayView{AddonState: x.snapshot()}
	x.pmu.Lock()
	v.Running, v.Servers = x.cmd != nil, len(x.ports)
	x.pmu.Unlock()
	return v
}
