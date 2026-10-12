package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Конфиг Xray из всех серверов xHTTP подписки проходит проверку настоящим Xray:
//
//	XRAY=/путь/к/xray go test -run TestXrayConfig .
func TestXrayConfig(t *testing.T) {
	xr := os.Getenv("XRAY")
	raw, err := os.ReadFile("testdata/wl/sample.txt")
	if xr == "" || err != nil {
		t.Skip("нужны XRAY и снимок подписки")
	}
	servers, _ := ParseShareLinks(string(raw))
	xs, ports := map[string]Server{}, map[string]int{}
	for i, sv := range servers {
		if sv.Transport == "xhttp" {
			tag := "w-" + wlID(sv)
			xs[tag], ports[tag] = sv, 30000+i
		}
	}
	if len(xs) == 0 {
		t.Skip("в снимке нет серверов xHTTP")
	}
	b, _ := json.Marshal(buildXrayConfig(xs, ports))
	p := filepath.Join(t.TempDir(), "xray.json")
	_ = os.WriteFile(p, b, 0o600)
	if out, err := exec.Command(xr, "run", "-test", "-c", p).CombinedOutput(); err != nil {
		t.Fatalf("xray -test: %s", out)
	}
	t.Logf("конфиг Xray из %d серверов xHTTP принят", len(xs))
}

// Свой сервер xHTTP: в конфиге sing-box ровно один выход — мост к Xray, и он в автовыборе.
func TestOwnXHTTPServer(t *testing.T) {
	sv, errs := ParseShareLinks("vless://6f1c2e1a-1b2c-4d3e-8f9a-0b1c2d3e4f50@1.2.3.4:443?security=tls&sni=a.example&type=xhttp&path=%2Fx#X")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	s := DefaultSettings()
	s.Servers = sv
	s.normalize()
	tag := serverTag(s.Servers[0])
	cfg, err := BuildConfig(s, ConfigEnv{XrayPorts: map[string]int{tag: 40000}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range cfg["outbounds"].([]any) {
		if o.(M)["tag"] == tag {
			n++
			if o.(M)["type"] != "socks" {
				t.Errorf("ожидался мост socks, а не %v", o.(M)["type"])
			}
		}
	}
	if n != 1 {
		t.Fatalf("выходов с тегом %s: %d", tag, n)
	}
	// без Xray сервер просто пропускается, а не ломает конфиг
	if _, err := BuildConfig(s, ConfigEnv{}); err == nil {
		t.Error("без Xray и без других серверов ожидалась понятная ошибка")
	}
}
