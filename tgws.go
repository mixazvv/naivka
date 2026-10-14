package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const tgwsRepo = "Flowseal/tg-ws-proxy"

// TgWs управляет программой tg-ws-proxy от Flowseal: Naivka её скачивает, обновляет,
// пишет ей настройки и запускает вместе с собой. Окна и значок в трее у неё свои.
type TgWs struct {
	addonBase
	a *App

	pmu      sync.Mutex
	cmd      *exec.Cmd
	running  bool
	stopping bool
	lastErr  string
	revives  crashBudget

	// Посредник: Telegram Desktop → Naivka (порт из настроек) → tg-ws-proxy (внутренний порт).
	// Так Naivka видит трафик Telegram, хотя tg-ws-proxy ходит в интернет сама.
	relay     net.Listener
	inner     int
	down, up  atomic.Int64
	conns     atomic.Int64
	relayConn map[net.Conn]bool
}

func newTgWs(a *App) *TgWs {
	t := &TgWs{a: a}
	t.state.Installed = readVersion(t.dir())
	return t
}

func (t *TgWs) dir() string     { return t.a.addonDir("tgws") }
func (t *TgWs) exePath() string { return filepath.Join(t.dir(), "TgWsProxy.exe") }

func (t *TgWs) setLatest(tag string) {
	t.mu.Lock()
	t.state.Latest = tag
	t.mu.Unlock()
}

func tgwsAsset() string {
	switch {
	case runtime.GOOS != "windows":
		return "TgWsProxy_linux_amd64"
	case legacyWindows() && runtime.GOARCH == "386":
		return "TgWsProxy_windows_7_32bit.exe"
	case legacyWindows():
		return "TgWsProxy_windows_7_64bit.exe"
	case runtime.GOARCH == "arm64":
		return "TgWsProxy_windows_arm64.exe"
	}
	return "TgWsProxy_windows.exe"
}

func (t *TgWs) Install() error {
	if !t.setBusy("download") {
		return nil
	}
	go func() {
		err := t.install()
		t.finish(err)
		if err != nil {
			t.a.mon.Event("error", "tg-ws-proxy: не удалось установить: "+err.Error(), false)
			return
		}
		t.a.mon.Event("ok", "tg-ws-proxy обновлён до "+readVersion(t.dir()), false)
	}()
	return nil
}

func (t *TgWs) install() error {
	tag, err := latestTag(tgwsRepo)
	if err != nil {
		return fmt.Errorf("не удалось узнать последнюю версию: %v", err)
	}
	t.setLatest(tag)
	if err := os.MkdirAll(t.dir(), 0o755); err != nil {
		return err
	}
	tmp := t.exePath() + ".new"
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", tgwsRepo, tag, tgwsAsset())
	if err := downloadTo(url, tmp, t.progress); err != nil {
		return err
	}
	if ok, err := verifyAsset(tgwsRepo, tag, tgwsAsset(), tmp); err != nil {
		os.Remove(tmp)
		return err
	} else if !ok {
		t.a.mon.Event("info", "tg-ws-proxy: GitHub не сообщил контрольную сумму, файл получен только по HTTPS", false)
	}
	wasRunning := t.Running()
	t.Stop()
	if err := os.Rename(tmp, t.exePath()); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("не удалось заменить программу (она занята?): %v", err)
	}
	_ = os.Chmod(t.exePath(), 0o755)
	if err := os.WriteFile(filepath.Join(t.dir(), "version.txt"), []byte(tag), 0o644); err != nil {
		return err
	}
	t.mu.Lock()
	t.state.Installed = tag
	t.mu.Unlock()
	if wasRunning {
		return t.Start(t.a.settingsCopy())
	}
	return nil
}

// writeConfig: портативные настройки tg-ws-proxy (рядом с exe). Свои поля берём из Naivka,
// остальные, которые пользователь мог поменять в её окне, сохраняем.
func (t *TgWs) writeConfig(s *Settings) error {
	data := filepath.Join(t.dir(), "TgWsProxy_data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(data, "config.json")
	cfg := map[string]any{}
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	cfg["host"] = "127.0.0.1"
	cfg["port"] = t.inner
	cfg["secret"] = s.Telegram.Secret
	cfg["check_updates"] = false // обновляет Naivka
	cfg["autostart"] = false     // запускает Naivka
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		return err
	}
	// Окно-инструкцию первого запуска заменяет кнопка «Подключить Telegram» в Naivka.
	return os.WriteFile(filepath.Join(data, ".first_run_done_mtproto"), nil, 0o644)
}

func (t *TgWs) Running() bool {
	t.pmu.Lock()
	defer t.pmu.Unlock()
	return t.running
}

func (t *TgWs) Start(s *Settings) error {
	if _, err := os.Stat(t.exePath()); err != nil {
		return errors.New("tg-ws-proxy ещё не установлен")
	}
	t.Stop()
	inner, err := freePort()
	if err != nil {
		return err
	}
	t.inner = inner
	if err := t.writeConfig(s); err != nil {
		return err
	}
	if err := t.startRelay(s.Telegram.Port); err != nil {
		return err
	}
	cmd := exec.Command(t.exePath(), "--portable")
	cmd.Dir = t.dir()
	if err := cmd.Start(); err != nil {
		t.stopRelay()
		return fmt.Errorf("не удалось запустить tg-ws-proxy: %v", err)
	}
	attachToJob(cmd.Process)
	t.pmu.Lock()
	t.cmd, t.running, t.stopping, t.lastErr = cmd, true, false, ""
	t.pmu.Unlock()
	go func() {
		werr := cmd.Wait()
		t.pmu.Lock()
		mine := t.cmd == cmd
		crashed := mine && !t.stopping
		if mine {
			t.running, t.cmd = false, nil
		}
		t.pmu.Unlock()
		if mine { // запоздавшее ожидание старого процесса не должно закрыть посредника нового
			t.stopRelay()
		}
		t.pmu.Lock()
		if crashed {
			t.lastErr = "tg-ws-proxy закрылся"
			if werr != nil {
				t.lastErr += ": " + werr.Error()
			}
		}
		msg := t.lastErr
		t.pmu.Unlock()
		switch {
		case crashed && werr == nil:
			// Код выхода 0 — скорее всего, закрыли через «Выход» в его меню: не навязываемся.
			t.a.mon.Event("warn", msg+" (возможно, через «Выход» в его меню)", false)
		case crashed:
			go t.revive(msg)
		}
	}()
	return nil
}

// revive: tg-ws-proxy упал с ошибкой — поднимаем снова, но не больше 3 раз за 10 минут.
func (t *TgWs) revive(msg string) {
	if !t.revives.allow(3, 10*time.Minute) {
		t.a.mon.Event("error", msg+". Он падает раз за разом — Telegram через него не работает. Подробности — в журнале", true)
		return
	}
	t.a.mon.Event("warn", msg+" — запускаю снова", false)
	time.Sleep(2 * time.Second)
	s := t.a.settingsCopy()
	if !s.Telegram.Enabled || t.Running() {
		return
	}
	if err := t.Start(s); err != nil {
		t.a.mon.Event("error", "tg-ws-proxy не запустился: "+firstLine(err.Error()), true)
	}
}

func (t *TgWs) Stop() {
	t.pmu.Lock()
	cmd := t.cmd
	if cmd == nil {
		t.pmu.Unlock()
		return
	}
	t.stopping = true
	t.pmu.Unlock()
	_ = cmd.Process.Kill()
	t.stopRelay()
	deadline := time.Now().Add(3 * time.Second)
	for t.Running() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

func (t *TgWs) startRelay(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		who := "другая программа"
		if _, path := portOwner(port); path != "" {
			who = filepath.Base(path)
		}
		return fmt.Errorf("порт %d для Telegram занят (%s) — возможно, tg-ws-proxy запущен отдельно. Закройте её или смените порт в разделе «Обход»", port, who)
	}
	t.pmu.Lock()
	t.relay, t.relayConn = ln, map[net.Conn]bool{}
	inner := t.inner
	t.pmu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go t.pipe(c, inner)
		}
	}()
	return nil
}

func (t *TgWs) pipe(c net.Conn, inner int) {
	defer c.Close()
	u, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inner), 5*time.Second)
	if err != nil {
		return
	}
	defer u.Close()
	t.pmu.Lock()
	if t.relayConn == nil {
		t.pmu.Unlock()
		return
	}
	t.relayConn[c], t.relayConn[u] = true, true
	t.pmu.Unlock()
	t.conns.Add(1)
	defer func() {
		t.conns.Add(-1)
		t.pmu.Lock()
		delete(t.relayConn, c)
		delete(t.relayConn, u)
		t.pmu.Unlock()
	}()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn, counter *atomic.Int64) {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				counter.Add(int64(n))
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(u, c, &t.up)   // Telegram → tg-ws-proxy: отдача
	go cp(c, u, &t.down) // tg-ws-proxy → Telegram: загрузка
	<-done
	<-done
}

func (t *TgWs) stopRelay() {
	t.pmu.Lock()
	ln, conns := t.relay, t.relayConn
	t.relay, t.relayConn = nil, nil
	t.pmu.Unlock()
	if ln != nil {
		ln.Close()
	}
	for c := range conns {
		c.Close()
	}
}

// Traffic — сколько всего прошло через Telegram и сколько соединений открыто сейчас.
func (t *TgWs) Traffic() (down, up, conns int64) { return t.down.Load(), t.up.Load(), t.conns.Load() }

func tgLink(s *Settings) string {
	return fmt.Sprintf("tg://proxy?server=127.0.0.1&port=%d&secret=dd%s", s.Telegram.Port, s.Telegram.Secret)
}

type TgView struct {
	AddonState
	Running bool   `json:"running"`
	Error   string `json:"lastError"`
	Link    string `json:"link"`
	Port    int    `json:"port"`
	Conns   int64  `json:"conns"`
}

func (t *TgWs) View() TgView {
	s := t.a.settingsCopy()
	v := TgView{AddonState: t.snapshot(), Link: tgLink(s), Port: s.Telegram.Port}
	t.pmu.Lock()
	v.Running, v.Error = t.running, t.lastErr
	v.Conns = t.conns.Load()
	t.pmu.Unlock()
	return v
}
