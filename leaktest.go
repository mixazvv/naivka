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
	"regexp"
	"strings"
	"sync"
	"time"
)

// Проверка утечек DNS: кто на самом деле узнаёт адреса сайтов, которые вы открываете.
// Сервис bash.ws (тот же, что у dnsleaktest) выдаёт одноразовый адрес; Naivka открывает его
// поддомены так же, как браузер, — через локальный прокси Naivka, а сервис потом сообщает,
// какие DNS-серверы о них спрашивали. Если среди них сервер вашего провайдера — это утечка.
// Проверку WebRTC (второй частый путь утечки) делает само окно: оно на том же Chromium, что и браузер.

const leakBase = "https://bash.ws"

type LeakResolver struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	ASN     string `json:"asn"`
	Leak    bool   `json:"leak"` // это DNS вашего провайдера (или вашей страны мимо сервера)
}

type LeakResult struct {
	Running   bool           `json:"running"`
	Mode      string         `json:"mode"`
	Direct    *GeoInfo       `json:"direct,omitempty"` // ваш настоящий адрес
	Exit      *GeoInfo       `json:"exit,omitempty"`   // адрес, который видят сайты через сервер
	Resolvers []LeakResolver `json:"resolvers"`
	DNSLeak   bool           `json:"dnsLeak"`
	Error     string         `json:"error,omitempty"`
}

type bashwsEntry struct {
	IP          string `json:"ip"`
	CountryName string `json:"country_name"`
	ASN         string `json:"asn"`
	Type        string `json:"type"` // ip | dns | conclusion
}

var asnRe = regexp.MustCompile(`(?i)\bAS(\d+)\b`)

func asnNum(s string) string {
	if m := asnRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// classifyResolvers помечает DNS-серверы провайдера: тот же номер сети (AS), что и у вашего
// настоящего адреса. Пока подключены, сервер в вашей стране тоже подозрителен — запрос ушёл мимо сервера.
func classifyResolvers(entries []bashwsEntry, direct *GeoInfo, running bool) ([]LeakResolver, bool) {
	out := []LeakResolver{}
	leak := false
	myASN, myCountry := "", ""
	if direct != nil {
		myASN, myCountry = asnNum(direct.ASN), strings.ToLower(direct.Country)
	}
	for _, e := range entries {
		if e.Type != "dns" {
			continue
		}
		r := LeakResolver{IP: e.IP, Country: e.CountryName, ASN: e.ASN}
		if myASN != "" && asnNum(e.ASN) == myASN {
			r.Leak = true
		} else if running && myCountry != "" && strings.ToLower(e.CountryName) == myCountry {
			r.Leak = true
		}
		leak = leak || r.Leak
		out = append(out, r)
	}
	return out, leak
}

func (a *App) apiLeakTest(*http.Request) (any, error) {
	s := a.settingsCopy()
	running := a.core.Running()
	res := &LeakResult{Running: running, Mode: s.Mode, Resolvers: []LeakResolver{}}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// Настоящий адрес и адрес через сервер — параллельно с проверкой DNS.
	directPort, proxyPort := a.core.ProbePorts()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if g, err := lookupGeo(ctx, geoClient(directPort), ""); err == nil {
			res.Direct = g
		}
	}()
	if running && proxyPort > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g, err := lookupGeo(ctx, geoClient(proxyPort), ""); err == nil {
				res.Exit = g
			}
		}()
	}

	entries, err := a.dnsLeakEntries(ctx, s, running)
	wg.Wait()
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	res.Resolvers, res.DNSLeak = classifyResolvers(entries, res.Direct, running)
	return res, nil
}

func (a *App) dnsLeakEntries(ctx context.Context, s *Settings, running bool) ([]bashwsEntry, error) {
	api := geoClient(0) // запросы к самому сервису — напрямую: на результат они не влияют
	id, err := leakGet(ctx, api, leakBase+"/id")
	if err != nil {
		return nil, errors.New("сервис проверки bash.ws недоступен — проверьте интернет и попробуйте ещё раз")
	}
	id = strings.TrimSpace(id)
	if !regexp.MustCompile(`^[0-9a-zA-Z]{4,64}$`).MatchString(id) {
		return nil, errors.New("сервис проверки ответил что-то странное")
	}
	// Открываем поддомены так же, как браузер: через локальный прокси Naivka (пока подключены) —
	// адрес узнаёт тот, кому по правилам положено. Без подключения — как обычно, через систему.
	tr := &http.Transport{DisableKeepAlives: true, Proxy: nil}
	if running && s.Mode == "proxy" {
		u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.MixedPort))
		tr.Proxy = http.ProxyURL(u)
	}
	hc := &http.Client{Timeout: 4 * time.Second, Transport: tr}
	var wg sync.WaitGroup
	for i := 1; i <= 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := fmt.Sprintf("%d.%s.bash.ws", i, id)
			if tr.Proxy == nil {
				// В режиме TUN и без подключения имя разрешает система (а в TUN её перехватывает ядро).
				r := &net.Resolver{}
				c, cancel := context.WithTimeout(ctx, 4*time.Second)
				_, _ = r.LookupHost(c, host)
				cancel()
				return
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+host+"/", nil)
			if resp, err := hc.Do(req); err == nil {
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(1500 * time.Millisecond) // сервису нужно время, чтобы записать запросы
	body, err := leakGet(ctx, api, leakBase+"/dnsleak/test/"+id+"?json")
	if err != nil {
		return nil, errors.New("сервис проверки bash.ws не вернул результат — попробуйте ещё раз")
	}
	var entries []bashwsEntry
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		return nil, errors.New("сервис проверки вернул непонятный ответ")
	}
	return entries, nil
}

func leakGet(ctx context.Context, hc *http.Client, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ответ %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return string(b), err
}
