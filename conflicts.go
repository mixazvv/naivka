package main

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Программы, которые мешают Naivka: другие VPN-клиенты (два TUN дерутся за маршруты, два
// системных прокси — за одну настройку Windows) и обходы блокировок на WinDivert (мешают zapret).
type conflictApp struct {
	name string
	exes []string // имена exe в нижнем регистре
	dpi  bool     // мешает только zapret
}

var knownConflicts = []conflictApp{
	{name: "v2rayN", exes: []string{"v2rayn.exe"}},
	{name: "Hiddify", exes: []string{"hiddify.exe", "hiddifycli.exe"}},
	{name: "Clash Verge", exes: []string{"clash-verge.exe", "verge-mihomo.exe", "clash-verge-service.exe"}},
	{name: "Clash for Windows", exes: []string{"clash for windows.exe"}},
	{name: "Mihomo Party", exes: []string{"mihomo party.exe", "mihomo-party.exe"}},
	{name: "FlClash", exes: []string{"flclash.exe"}},
	{name: "NekoRay / Throne", exes: []string{"nekoray.exe", "nekobox.exe", "throne.exe"}},
	{name: "Karing", exes: []string{"karing.exe"}},
	{name: "Happ", exes: []string{"happ.exe"}},
	{name: "Invisible Man XRay", exes: []string{"invisiblemanxray.exe"}},
	{name: "Outline", exes: []string{"outline.exe"}},
	{name: "AmneziaVPN", exes: []string{"amneziavpn.exe", "amneziavpn-service.exe"}},
	{name: "WireGuard", exes: []string{"wireguard.exe"}},
	{name: "OpenVPN", exes: []string{"openvpn.exe", "openvpn-gui.exe"}},
	{name: "Proton VPN", exes: []string{"protonvpn.exe", "protonvpn.client.exe"}},
	{name: "Windscribe", exes: []string{"windscribe.exe"}},
	{name: "Psiphon", exes: []string{"psiphon3.exe"}},
	{name: "Cloudflare WARP", exes: []string{"cloudflare warp.exe", "warp-svc.exe"}},
	{name: "GoodbyeDPI", exes: []string{"goodbyedpi.exe"}, dpi: true},
	{name: "ByeDPI", exes: []string{"ciadpi.exe", "byedpi.exe"}, dpi: true},
}

// ConflictInfo — найденная программа и что с ней делать.
type ConflictInfo struct {
	Name string `json:"name"`
	Hint string `json:"hint"`
}

// findConflicts — чистая часть проверки: по списку запущенных программ.
// ours — пути наших собственных sing-box и Xray: их не считаем чужими.
func findConflicts(procs []procEntry, ours []string, zapretOn bool) []ConflictInfo {
	running := map[string]bool{}
	foreignCore := map[string]bool{}
	for _, p := range procs {
		n := strings.ToLower(p.Name)
		running[n] = true
		if n == "sing-box.exe" || n == "xray.exe" || n == "mihomo.exe" || strings.HasPrefix(n, "mihomo-windows") {
			mine := false
			for _, o := range ours {
				if p.Path != "" && samePath(p.Path, o) {
					mine = true
				}
			}
			if !mine && p.Path != "" { // путь не прочитался — скорее всего, наш же с другими правами: не тревожим
				foreignCore[strings.TrimSuffix(filepath.Base(p.Path), filepath.Ext(p.Path))] = true
			}
		}
	}
	var out []ConflictInfo
	for _, c := range knownConflicts {
		if c.dpi && !zapretOn {
			continue
		}
		for _, e := range c.exes {
			if running[e] {
				hint := "Если в нём включено подключение — выключите: два VPN одновременно мешают друг другу, связь начинает пропадать."
				if c.dpi {
					hint = "Он работает так же, как zapret, и вместе они мешают друг другу. Закройте его или выключите zapret в Naivka."
				}
				out = append(out, ConflictInfo{Name: c.name, Hint: hint})
				break
			}
		}
	}
	for name := range foreignCore {
		out = append(out, ConflictInfo{Name: name + " (не из Naivka)",
			Hint: "Его запустила другая программа. Если это другой VPN-клиент — закройте его, иначе они мешают друг другу."})
	}
	return out
}

var conflictCache struct {
	mu   sync.Mutex
	at   time.Time
	list []ConflictInfo
	busy bool
}

// conflicts — найденные мешающие программы. Список процессов собирается в фоне не чаще раза
// в 15 секунд, поэтому запрос состояния никогда не ждёт.
func (a *App) conflicts() []ConflictInfo {
	c := &conflictCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > 15*time.Second && !c.busy {
		c.busy = true
		go func() {
			list := a.scanConflicts()
			c.mu.Lock()
			c.list, c.at, c.busy = list, time.Now(), false
			c.mu.Unlock()
		}()
	}
	return c.list
}

func (a *App) scanConflicts() []ConflictInfo {
	s := a.settingsCopy()
	return findConflicts(listProcesses(), append(a.core.ownCoreExes(), a.xray.exePath()), s.Zapret.Enabled)
}

// warnConflicts — при подключении: записать в ленту, что может мешать.
func (a *App) warnConflicts() {
	for _, c := range a.scanConflicts() {
		a.mon.Event("warn", "Запущен "+c.Name+". "+c.Hint, false)
	}
}
