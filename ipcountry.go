package main

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Страна сайта — для глобуса трафика в «Сводке». Берётся из открытой базы server-country
// (sapics/ip-location-db, общественное достояние, PDDL): файл .mmdb скачивается с GitHub раз в месяц
// и читается на месте — адреса сайтов никуда не отправляются.
//
// Если у соединения нет IP (сайт открыт по имени через сервер), имя разрешается через DNS самого ядра
// (Clash API /dns/query): тем же путём, что и трафик, поэтому провайдер не узнаёт лишнего.

var geoDBURL = "https://github.com/sapics/ip-location-db/releases/download/latest/server-country.mmdb"

const (
	geoDBMaxAge = 30 * 24 * time.Hour
	geoDBLimit  = 64 << 20
	hostCCTTL   = 30 * time.Minute
)

type hostCC struct {
	cc string
	at time.Time
}

type ipCountry struct {
	path string

	mu        sync.Mutex
	db        *mmdb
	tried     bool // база уже открывалась или скачивалась в этом запуске
	hosts     map[string]hostCC
	resolving map[string]bool
}

func newIPCountry(dir string) *ipCountry {
	return &ipCountry{path: filepath.Join(dir, "geo", "server-country.mmdb"), hosts: map[string]hostCC{}, resolving: map[string]bool{}}
}

// Ready — база открыта.
func (g *ipCountry) Ready() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.db != nil
}

// ensure открывает базу с диска и, если её нет или она старше месяца, скачивает свежую в фоне.
// Вызывается под g.mu.
func (g *ipCountry) ensure() {
	if g.tried {
		return
	}
	g.tried = true
	fresh := false
	if b, err := os.ReadFile(g.path); err == nil {
		if db, err := openMMDB(b); err == nil {
			g.db = db
			if st, err := os.Stat(g.path); err == nil && time.Since(st.ModTime()) < geoDBMaxAge {
				fresh = true
			}
		}
	}
	if !fresh {
		go g.download()
	}
}

func (g *ipCountry) download() {
	b, err := downloadBytes(geoDBURL, geoDBLimit, nil)
	if err != nil {
		return
	}
	db, err := openMMDB(b) // только проверенный файл заменяет прежний
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o700); err != nil {
		return
	}
	tmp := g.path + ".part"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, g.path); err != nil {
		os.Remove(tmp)
		return
	}
	g.mu.Lock()
	g.db = db
	g.mu.Unlock()
}

// lookupIP — страна адреса или "" (нет базы, частный адрес, адреса нет в базе).
// Вызывается под g.mu. Повреждённая база не должна уронить Naivka.
func (g *ipCountry) lookupIP(ip net.IP) (cc string) {
	if g.db == nil || ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	defer func() {
		if recover() != nil {
			g.db, cc = nil, ""
		}
	}()
	v, err := g.db.lookup(ip)
	if err != nil {
		return ""
	}
	if m, ok := v.(map[string]any); ok {
		if s, ok := m["country_code"].(string); ok && len(s) == 2 {
			return s
		}
	}
	return ""
}

// CountryFor — страна соединения: по IP, а если его нет или он служебный (fake-ip) — по имени
// из кэша. Не ждёт: имя без кэша разрешается в фоне, страна появится в следующем замере.
func (g *ipCountry) CountryFor(cl *Clash, host, destIP string) string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensure()
	if g.db == nil {
		return ""
	}
	if cc := g.lookupIP(net.ParseIP(destIP)); cc != "" {
		return cc
	}
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	if h, ok := g.hosts[host]; ok && time.Since(h.at) < hostCCTTL {
		return h.cc
	}
	if cl != nil && !g.resolving[host] && len(g.resolving) < 4 {
		g.resolving[host] = true
		go g.resolve(cl, host)
	}
	return ""
}

func (g *ipCountry) resolve(cl *Clash, host string) {
	ip, _ := cl.ResolveIP(host)
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.resolving, host)
	if len(g.hosts) > 4000 {
		g.hosts = map[string]hostCC{}
	}
	g.hosts[host] = hostCC{cc: g.lookupIP(ip), at: time.Now()}
}

// ResolveIP разрешает имя через DNS ядра (правила те же, что у трафика).
func (c *Clash) ResolveIP(host string) (net.IP, error) {
	for _, typ := range []string{"A", "AAAA"} {
		var v struct {
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		if err := c.do("GET", "/dns/query?name="+url.QueryEscape(host)+"&type="+typ, nil, &v); err != nil {
			return nil, err
		}
		for _, a := range v.Answer {
			if a.Type == 1 || a.Type == 28 {
				if ip := net.ParseIP(a.Data); ip != nil {
					return ip, nil
				}
			}
		}
	}
	return nil, errors.New("нет адреса")
}
