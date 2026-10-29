package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Системный прокси Windows Naivka включает через PAC-скрипт (AutoConfigURL), а не прямым адресом:
// скрипт раздаёт сама Naivka, пока запущена. Выключили компьютер или Naivka упала — скрипт
// недоступен, и Windows с браузерами по стандарту идут напрямую: интернет работает, хотя прокси
// формально включён. Пока Naivka жива, скрипт велит «только через прокси» (без запасного DIRECT):
// упало ядро — трафик упирается в закрытый порт, а не идёт мимо сервера.

// usePAC — включать ли системный прокси через скрипт (настройка «ProxyPAC»); main подставляет свою.
var usePAC = func() bool { return false }

// pacScript — скрипт для прокси 127.0.0.1:port; локальные адреса — напрямую (как bypassList).
func pacScript(port int) string {
	var b strings.Builder
	b.WriteString("function FindProxyForURL(url, host) {\n")
	b.WriteString("  if (isPlainHostName(host)")
	for _, p := range pacBypass() {
		fmt.Fprintf(&b, " ||\n      shExpMatch(host, %q)", p)
	}
	b.WriteString(")\n    return \"DIRECT\";\n")
	fmt.Fprintf(&b, "  return \"PROXY 127.0.0.1:%d\";\n}\n", port)
	return b.String()
}

func pacBypass() []string {
	parts := []string{"localhost", "127.*", "10.*", "192.168.*", "[::1]", "::1"}
	for i := 16; i <= 31; i++ {
		parts = append(parts, fmt.Sprintf("172.%d.*", i))
	}
	return parts
}

// pacServer раздаёт скрипт на 127.0.0.1 со случайным портом и случайным путём, пока жив процесс.
var pacSrv struct {
	mu   sync.Mutex
	url  string
	port int // прокси, который сейчас отдаёт скрипт
}

// pacURL — адрес скрипта для прокси на port (запускает раздачу при первом вызове).
func pacURL(port int) (string, error) {
	pacSrv.mu.Lock()
	defer pacSrv.mu.Unlock()
	pacSrv.port = port
	if pacSrv.url != "" {
		return pacSrv.url, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	path := "/naivka-" + randHex(8) + ".pac"
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		pacSrv.mu.Lock()
		p := pacSrv.port
		pacSrv.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(pacScript(p)))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	pacSrv.url = "http://" + ln.Addr().String() + path
	return pacSrv.url, nil
}
