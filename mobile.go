package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// lanShare — временная раздача профиля телефону по домашней сети.
// Ссылка содержит случайный ключ, живёт 10 минут и не больше 3 скачиваний.
type lanShare struct {
	mu        sync.Mutex
	srv       *http.Server
	token     string
	until     time.Time
	downloads int
}

const shareTTL = 10 * time.Minute

func (a *App) mobileConfig() ([]byte, error) {
	s := a.settingsCopy()
	env := ConfigEnv{Mobile: true}
	a.fillLists(s, &env, false)
	cfg, err := BuildConfig(s, env)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func (a *App) apiMobileShare(*http.Request) (any, error) {
	body, err := a.mobileConfig()
	if err != nil {
		return nil, err
	}
	a.stopShare()
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть порт в локальной сети: %v", err)
	}
	sh := &lanShare{token: randHex(16), until: time.Now().Add(shareTTL)}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+sh.token+"/naivka.json", func(w http.ResponseWriter, r *http.Request) {
		// Профиль с паролями отдаём только устройствам из домашней сети.
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsPrivate() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		sh.mu.Lock()
		ok := time.Now().Before(sh.until) && sh.downloads < 3
		if ok {
			sh.downloads++
		}
		n := sh.downloads
		sh.mu.Unlock()
		if !ok {
			http.Error(w, "expired", http.StatusGone)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="naivka.json"`)
		_, _ = w.Write(body)
		a.mon.Event("info", fmt.Sprintf("Профиль скачан на устройство %s", strings.Split(r.RemoteAddr, ":")[0]), false)
		if n >= 3 {
			go a.stopShare()
		}
	})
	sh.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go sh.srv.Serve(ln)
	a.mu.Lock()
	a.lan = sh
	a.mu.Unlock()
	time.AfterFunc(shareTTL, func() {
		a.mu.Lock()
		same := a.lan == sh
		a.mu.Unlock()
		if same {
			a.stopShare()
		}
	})

	port := ln.Addr().(*net.TCPAddr).Port
	var links []map[string]string
	for _, ip := range lanIPs() {
		u := fmt.Sprintf("http://%s:%d/%s/naivka.json", ip, port, sh.token)
		links = append(links, map[string]string{
			"ip":      ip,
			"url":     u,
			"singbox": "sing-box://import-remote-profile?url=" + url.QueryEscape(u) + "#Naivka",
		})
	}
	if len(links) == 0 {
		a.stopShare()
		return nil, fmt.Errorf("компьютер не подключён к локальной сети — скачайте профиль файлом")
	}
	return map[string]any{"links": links, "expires": sh.until.Unix()}, nil
}

func (a *App) stopShare() {
	a.mu.Lock()
	sh := a.lan
	a.lan = nil
	a.mu.Unlock()
	if sh != nil && sh.srv != nil {
		_ = sh.srv.Close()
	}
}

// lanIPs — адреса компьютера в домашней сети (без виртуальных адаптеров Naivka).
func lanIPs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || strings.Contains(strings.ToLower(ifc.Name), "naivka") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() || ipn.IP.Equal(net.ParseIP("172.19.0.1")) {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	return out
}
