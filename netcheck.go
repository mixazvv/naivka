package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Сторожа (проверка связи через сервер, обрывы zapret, обрывы DNS) видят только «не открылось».
// Когда пропадает сам интернет — отвалился Wi-Fi, лёг провайдер, — не открывается всё сразу,
// и каждый сторож решил бы, что сломалась его часть: перешёл бы на другой сервер, увёл бы сайты
// zapret на сервер, сменил бы DNS. Поэтому перед таким решением спрашиваем internetUp.

// internetIPs — адреса для проверки «есть ли интернет вообще». Обращаемся по IP и обычным HTTP:
// без DNS (он может быть сломан) и без шифрования. Хватает ответа любого. Среди них — адреса ya.ru:
// при «белых списках» у провайдера зарубежные адреса закрыты, а интернет при этом есть.
var internetIPs = []string{"http://1.1.1.1/", "http://8.8.8.8/", "http://77.88.55.242/", "http://5.255.255.242/"}

// internetUp — открывается ли хоть что-то напрямую, мимо сервера, zapret и DNS.
func (a *App) internetUp() bool {
	if !networkUp() {
		return false
	}
	port, _ := a.core.ProbePorts()
	if port == 0 {
		return true // ядро не запущено — проверить нечем, не мешаем
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var ok atomic.Bool
	done := make(chan struct{}, len(internetIPs))
	for _, u := range internetIPs {
		go func(u string) {
			defer func() { done <- struct{}{} }()
			hc := geoClient(port)
			hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return
			}
			if resp, err := hc.Do(req); err == nil {
				resp.Body.Close()
				ok.Store(true)
				cancel() // ответил один — остальных не ждём
			}
		}(u)
	}
	for range internetIPs {
		<-done
	}
	return ok.Load()
}

// dnsQuery — DNS-запрос типа A в формате для DoH (RFC 8484, GET ?dns=).
func dnsQuery(name string) string {
	b := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(b[2:], 0x0100) // рекурсивный запрос
	binary.BigEndian.PutUint16(b[4:], 1)      // один вопрос
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, 0, 1, 0, 1) // конец имени, тип A, класс IN
	return base64.RawURLEncoding.EncodeToString(b)
}

// dohWorks — отвечает ли DoH-сервер из настроек напрямую (так же, как к нему ходит ядро).
func (a *App) dohWorks(d directDNS) bool {
	port, _ := a.core.ProbePorts()
	if port == 0 || d.Kind != "https" {
		return false
	}
	port443 := 443
	if d.Port > 0 {
		port443 = d.Port
	}
	host := net.JoinHostPort(d.Host, strconv.Itoa(port443))
	path := d.Path
	if path == "" {
		path = "/dns-query"
	}
	for try := 0; try < 2; try++ {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		req, err := http.NewRequestWithContext(ctx, "GET", "https://"+host+path+"?dns="+dnsQuery("ya.ru"), nil)
		if err != nil {
			cancel()
			return false
		}
		req.Header.Set("Accept", "application/dns-message")
		resp, err := geoClient(port).Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
	}
	return false
}
