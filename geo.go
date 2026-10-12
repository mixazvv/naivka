package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GeoInfo — что внешний мир знает об адресе.
type GeoInfo struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	ISP         string `json:"isp"`
	ASN         string `json:"asn"`
	Hosting     *bool  `json:"hosting,omitempty"` // адрес дата-центра (nil — неизвестно)
	Source      string `json:"source"`
}

type geoProvider struct {
	name  string
	url   func(ip string) string // ip == "" — «мой адрес»
	parse func([]byte) (*GeoInfo, error)
}

const ipAPIFields = "status,message,country,countryCode,city,isp,org,as,query,hosting"

// Сначала сервисы с HTTPS: запрос с вашим IP и адресами серверов не идёт открытым текстом.
// ip-api.com бесплатно отвечает только по HTTP, поэтому он последний запасной.
var geoProviders = []geoProvider{
	{
		name: "ipwho.is",
		url:  func(ip string) string { return "https://ipwho.is/" + url.PathEscape(ip) + "?lang=ru" },
		parse: func(b []byte) (*GeoInfo, error) {
			var v struct {
				Success     bool   `json:"success"`
				Message     string `json:"message"`
				IP          string `json:"ip"`
				Country     string `json:"country"`
				CountryCode string `json:"country_code"`
				City        string `json:"city"`
				Connection  struct {
					ASN json.Number `json:"asn"`
					Org string      `json:"org"`
					ISP string      `json:"isp"`
				} `json:"connection"`
			}
			if err := json.Unmarshal(b, &v); err != nil {
				return nil, err
			}
			if !v.Success {
				return nil, errors.New(v.Message)
			}
			g := &GeoInfo{IP: v.IP, Country: v.Country, CountryCode: v.CountryCode, City: v.City, ISP: v.Connection.ISP}
			if g.ISP == "" {
				g.ISP = v.Connection.Org
			}
			if s := v.Connection.ASN.String(); s != "" && s != "0" {
				g.ASN = "AS" + s
			}
			return g, nil
		},
	},
	{
		name: "ipinfo.io",
		url: func(ip string) string {
			if ip == "" {
				return "https://ipinfo.io/json"
			}
			return "https://ipinfo.io/" + url.PathEscape(ip) + "/json"
		},
		parse: func(b []byte) (*GeoInfo, error) {
			var v struct {
				IP      string `json:"ip"`
				City    string `json:"city"`
				Country string `json:"country"`
				Org     string `json:"org"` // «AS13335 Cloudflare, Inc.»
			}
			if err := json.Unmarshal(b, &v); err != nil {
				return nil, err
			}
			if v.IP == "" {
				return nil, errors.New("пустой ответ")
			}
			g := &GeoInfo{IP: v.IP, CountryCode: v.Country, City: v.City, ISP: v.Org}
			if strings.HasPrefix(v.Org, "AS") {
				if i := strings.IndexByte(v.Org, ' '); i > 0 {
					g.ASN, g.ISP = v.Org[:i], v.Org[i+1:]
				}
			}
			return g, nil
		},
	},
	{
		name: "ip-api.com",
		url: func(ip string) string {
			return "http://ip-api.com/json/" + url.PathEscape(ip) + "?lang=ru&fields=" + ipAPIFields
		},
		parse: parseIPAPI,
	},
}

func parseIPAPI(b []byte) (*GeoInfo, error) {
	var v struct {
		Status      string `json:"status"`
		Message     string `json:"message"`
		Query       string `json:"query"`
		Country     string `json:"country"`
		CountryCode string `json:"countryCode"`
		City        string `json:"city"`
		ISP         string `json:"isp"`
		Org         string `json:"org"`
		AS          string `json:"as"` // «AS13335 Cloudflare, Inc.»
		Hosting     *bool  `json:"hosting"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if v.Status != "success" {
		if v.Message == "" {
			v.Message = "сервис не нашёл адрес"
		}
		return nil, errors.New(v.Message)
	}
	g := &GeoInfo{IP: v.Query, Country: v.Country, CountryCode: v.CountryCode, City: v.City, ISP: v.ISP, Hosting: v.Hosting}
	if g.ISP == "" {
		g.ISP = v.Org
	}
	if i := strings.IndexByte(v.AS, ' '); i > 0 {
		g.ASN = v.AS[:i]
	}
	return g, nil
}

// geoClient — HTTP-клиент: напрямую (proxyPort == 0) или через служебный вход sing-box.
func geoClient(proxyPort int) *http.Client {
	tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 8 * time.Second}
	if proxyPort > 0 {
		u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

func lookupGeo(ctx context.Context, hc *http.Client, ip string) (*GeoInfo, error) {
	var errs []string
	for _, p := range geoProviders {
		req, _ := http.NewRequestWithContext(ctx, "GET", p.url(ip), nil)
		req.Header.Set("User-Agent", "Naivka")
		resp, err := hc.Do(req)
		if err != nil {
			errs = append(errs, p.name+": "+humanErr(err))
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			errs = append(errs, fmt.Sprintf("%s: %s", p.name, resp.Status))
			continue
		}
		g, err := p.parse(body)
		if err != nil {
			errs = append(errs, p.name+": "+err.Error())
			continue
		}
		g.Source = p.name
		return g, nil
	}
	return nil, errors.New("не удалось определить адрес (" + strings.Join(errs, "; ") + ")")
}

type geoResult struct {
	Info  *GeoInfo `json:"info,omitempty"`
	Error string   `json:"error,omitempty"`
}

// MyIP: как сайты видят вас напрямую и через сервер.
func (a *App) apiIP(*http.Request) (any, error) {
	directPort, proxyPort := a.core.ProbePorts()
	running := a.core.Running()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	out := map[string]any{"running": running}
	var wg sync.WaitGroup
	var mu sync.Mutex
	run := func(key string, port int) {
		defer wg.Done()
		g, err := lookupGeo(ctx, geoClient(port), "")
		r := geoResult{Info: g}
		if err != nil {
			r.Error = err.Error()
		}
		mu.Lock()
		out[key] = r
		mu.Unlock()
	}
	wg.Add(1)
	go run("direct", directPort) // при отключении directPort == 0 — просто напрямую
	if running && proxyPort > 0 {
		wg.Add(1)
		go run("proxy", proxyPort)
	}
	wg.Wait()
	return out, nil
}

// ---------- страна серверов ----------

type serverGeoCache struct {
	mu   sync.Mutex
	data map[string]cachedGeo // ключ — host
}

type cachedGeo struct {
	res geoResult
	at  time.Time
}

func (a *App) apiServerGeo(*http.Request) (any, error) {
	a.mu.Lock()
	servers := append([]Server(nil), a.settings.Servers...)
	a.mu.Unlock()
	directPort, _ := a.core.ProbePorts()

	a.geo.mu.Lock()
	if a.geo.data == nil {
		a.geo.data = map[string]cachedGeo{}
	}
	a.geo.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// Хосты без свежего кэша: определяем IP и спрашиваем сервисы геолокации.
	type job struct{ host, ip string }
	var jobs []job
	seen := map[string]bool{}
	for _, sv := range servers {
		if seen[sv.Host] {
			continue
		}
		seen[sv.Host] = true
		a.geo.mu.Lock()
		c, ok := a.geo.data[sv.Host]
		a.geo.mu.Unlock()
		if ok && time.Since(c.at) < 30*time.Minute && c.res.Error == "" {
			continue
		}
		ip, err := resolveHost(ctx, sv.Host)
		if err != nil {
			a.geo.put(sv.Host, geoResult{Error: "домен не найден"})
			continue
		}
		jobs = append(jobs, job{sv.Host, ip})
	}
	if len(jobs) > 0 {
		hc := geoClient(directPort)
		sem := make(chan struct{}, 4) // не больше 4 запросов разом: у бесплатных сервисов есть лимиты
		var wg sync.WaitGroup
		for _, j := range jobs {
			wg.Add(1)
			go func(j job) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if g, err := lookupGeo(ctx, hc, j.ip); err == nil {
					a.geo.put(j.host, geoResult{Info: g})
				} else {
					a.geo.put(j.host, geoResult{Error: "не удалось определить страну", Info: &GeoInfo{IP: j.ip}})
				}
			}(j)
		}
		wg.Wait()
	}

	out := map[string]geoResult{}
	a.geo.mu.Lock()
	for _, sv := range servers {
		if c, ok := a.geo.data[sv.Host]; ok {
			out[sv.ID] = c.res
		}
	}
	a.geo.mu.Unlock()
	return out, nil
}

func (c *serverGeoCache) put(host string, r geoResult) {
	c.mu.Lock()
	c.data[host] = cachedGeo{res: r, at: time.Now()}
	c.mu.Unlock()
}

func resolveHost(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("не найден")
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return ips[0].String(), nil
}
