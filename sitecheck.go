package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProbeResult struct {
	OK      bool   `json:"ok"`
	Code    int    `json:"code"`
	Ms      int    `json:"ms"`
	Error   string `json:"error,omitempty"`
	Stub    bool   `json:"stub"` // попали на страницу-заглушку провайдера
	Skipped bool   `json:"skipped"`
}

type CheckResult struct {
	Host      string      `json:"host"`
	Rule      string      `json:"rule"`
	RouteKind string      `json:"routeKind"` // proxy | direct | block | ""
	RouteName string      `json:"routeName"`
	Direct    ProbeResult `json:"direct"`
	Proxy     ProbeResult `json:"proxy"`
	Verdict   string      `json:"verdict"`
	Suggest   string      `json:"suggest"` // proxy | direct | zapret | ""
	// zapret: работает ли он и есть ли сайт в его списках. Если есть — прямая проверка уже шла через zapret.
	ZapretOn     bool `json:"zapretOn"`
	ZapretCovers bool `json:"zapretCovers"`
}

// Адреса страниц, на которые провайдеры отправляют вместо заблокированного сайта.
var stubHosts = []string{"warning.rt.ru", "blocklist.rkn.gov.ru", "zapret-info", "blocked.", "block.", "stop.", "eais.rkn.gov.ru"}

func (a *App) apiCheck(r *http.Request) (any, error) {
	var req struct {
		Target string `json:"target"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	host, err := checkTarget(req.Target)
	if err != nil {
		return nil, err
	}
	res := &CheckResult{Host: host}
	directPort, proxyPort := a.core.ProbePorts()
	running := a.core.Running()
	if a.zapret.Running() {
		res.ZapretOn = true
		s := a.settingsCopy()
		res.ZapretCovers = domainListed(host, append(a.zapret.Domains(s), zapretRuleDomains(s)...))
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); res.Direct = probeSite(host, directPort) }()
	go func() {
		defer wg.Done()
		if running && proxyPort > 0 {
			res.Proxy = probeSite(host, proxyPort)
		} else {
			res.Proxy = ProbeResult{Skipped: true}
		}
	}()
	if running {
		a.detectRoute(host, res)
	}
	wg.Wait()
	verdict(res, running)
	return res, nil
}

// domainListed — входит ли сайт (или его родительский домен) в список.
func domainListed(host string, list []string) bool {
	for _, d := range list {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
			return true
		}
	}
	return false
}

// zapretRuleDomains — сайты, которые пользователь сам отправил «через zapret».
func zapretRuleDomains(s *Settings) []string {
	var out []string
	for _, r := range s.Rules {
		if r.Action == "zapret" && r.Kind == "domain" {
			out = append(out, r.Value)
		}
	}
	return out
}

func checkTarget(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", errors.New("Введите адрес сайта")
	}
	if ip := net.ParseIP(strings.Trim(t, "[]")); ip != nil {
		return ip.String(), nil
	}
	d, err := normalizeDomain(t)
	if err != nil {
		return "", errors.New("Не похоже на адрес сайта")
	}
	return d, nil
}

// detectRoute открывает через локальный прокси туннель к сайту и смотрит в ядре,
// какое правило и какой маршрут ему достались.
func (a *App) detectRoute(host string, res *CheckResult) {
	cl := a.core.Clash()
	s := a.settingsCopy()
	if cl == nil {
		return
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.MixedPort), 3*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	localPort := strconv.Itoa(conn.LocalAddr().(*net.TCPAddr).Port)
	target := net.JoinHostPort(host, "443")
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)

	found := make(chan struct{})
	go func() {
		defer close(found)
		for i := 0; i < 30; i++ {
			v, err := cl.ConnectionsRaw()
			if err == nil {
				for _, c := range v.Connections {
					if c.Metadata.SourcePort == localPort {
						res.Rule = c.Rule
						route, direct := classify(c.Chains)
						res.RouteKind = map[bool]string{true: "direct", false: "proxy"}[direct]
						res.RouteName = a.serverName(route)
						if direct {
							res.RouteName = "Напрямую"
						}
						return
					}
				}
			}
			time.Sleep(150 * time.Millisecond)
		}
	}()
	line, _ := bufio.NewReader(conn).ReadString('\n')
	<-found
	if res.RouteKind == "" && line != "" && !strings.Contains(line, " 200") {
		res.RouteKind = "block"
		res.RouteName = "Блокируется правилом"
	}
}

func probeSite(host string, proxyPort int) ProbeResult {
	hc := geoClient(proxyPort)
	hc.Timeout = 12 * time.Second
	var final string
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		final = req.URL.Host
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+host+"/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36")
	start := time.Now()
	resp, err := hc.Do(req)
	ms := int(time.Since(start) / time.Millisecond)
	if err != nil {
		return ProbeResult{Ms: ms, Error: humanErr(err)}
	}
	_, _ = io.CopyN(io.Discard, resp.Body, 64<<10)
	resp.Body.Close()
	r := ProbeResult{OK: resp.StatusCode < 500, Code: resp.StatusCode, Ms: ms}
	for _, h := range stubHosts {
		if final != "" && strings.Contains(final, h) {
			r.Stub, r.OK = true, false
			r.Error = "заглушка провайдера (" + final + ")"
		}
	}
	return r
}

func verdict(r *CheckResult, running bool) {
	d, p := r.Direct, r.Proxy
	switch {
	case !running:
		if d.OK {
			r.Verdict = "Сайт открывается напрямую. Подключитесь, чтобы проверить и путь через сервер."
		} else {
			r.Verdict = "Напрямую сайт не открывается. Подключитесь, чтобы проверить, откроется ли через сервер."
		}
	case d.OK && r.ZapretCovers && r.RouteKind != "proxy":
		r.Verdict = "Сайт открывается без сервера: он в списках zapret, и zapret обходит его замедление."
	case !d.OK && r.ZapretOn && !r.ZapretCovers && p.OK:
		r.Verdict = "Напрямую сайт не открывается, через сервер — открывается. Можно попробовать и через zapret: это не тратит трафик сервера, но помогает не со всеми сайтами."
		if r.RouteKind == "direct" {
			r.Suggest = "proxy"
		}
	case !d.OK && p.OK:
		r.Verdict = "Сайт недоступен напрямую — похоже на блокировку. Через сервер открывается."
		if r.RouteKind == "direct" {
			r.Verdict += " Сейчас он идёт напрямую, поэтому не откроется: добавьте правило «Через сервер»."
			r.Suggest = "proxy"
		}
	case d.OK && !p.OK:
		r.Verdict = "Напрямую сайт открывается, а через сервер — нет. Возможно, он не пускает зарубежные адреса."
		if r.RouteKind == "proxy" {
			r.Verdict += " Сейчас он идёт через сервер: добавьте правило «Напрямую»."
			r.Suggest = "direct"
		}
	case d.OK && p.OK:
		r.Verdict = "Сайт открывается и напрямую, и через сервер."
	default:
		r.Verdict = "Сайт не открывается ни напрямую, ни через сервер. Возможно, он сам не работает или адрес неверный."
	}
}
