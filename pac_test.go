package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPacScript(t *testing.T) {
	s := pacScript(2080)
	for _, want := range []string{"function FindProxyForURL(url, host)", `return "PROXY 127.0.0.1:2080";`, `shExpMatch(host, "192.168.*")`, `shExpMatch(host, "172.31.*")`, "isPlainHostName(host)"} {
		if !strings.Contains(s, want) {
			t.Errorf("в скрипте нет %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "DIRECT\";\n}") || strings.Contains(s, "; DIRECT") {
		t.Errorf("без запасного DIRECT для внешних адресов:\n%s", s)
	}
}

func TestPacURLServes(t *testing.T) {
	u, err := pacURL(3001)
	if err != nil {
		t.Fatal(err)
	}
	if u2, _ := pacURL(3002); u2 != u {
		t.Fatalf("адрес скрипта сменился: %s → %s", u, u2)
	}
	r, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "PROXY 127.0.0.1:3002") || r.Header.Get("Content-Type") != "application/x-ns-proxy-autoconfig" {
		t.Fatalf("скрипт: %s %q", r.Header.Get("Content-Type"), b)
	}
	if r2, err := http.Get(strings.TrimSuffix(u, ".pac") + "x.pac"); err == nil {
		r2.Body.Close()
		if r2.StatusCode != 404 {
			t.Fatalf("чужой путь: %d", r2.StatusCode)
		}
	}
}
