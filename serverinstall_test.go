//go:build !legacy

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	for _, v := range []string{"plain", "it's", `$(rm -rf /)`, "a b;c|d", "Тест'\"`"} {
		out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(v)).Output()
		if err != nil || string(out) != v {
			t.Errorf("%q → %q (%v)", v, out, err)
		}
	}
}

func TestServerInstallCheck(t *testing.T) {
	ok := serverInstallReq{Host: " 203.0.113.10 ", Password: "p", Fingerprint: "SHA256:x", SNI: "www.example.com", Confirm: true}
	if err := ok.check(true); err != nil || ok.Host != "203.0.113.10" || ok.Port != 22 || ok.User != "root" || ok.Name != "203.0.113.10" {
		t.Fatalf("%+v %v", ok, err)
	}
	for name, mod := range map[string]func(r *serverInstallReq){
		"нет подтверждения":   func(r *serverInstallReq) { r.Confirm = false },
		"нет отпечатка":       func(r *serverInstallReq) { r.Fingerprint = "" },
		"нет пароля и ключа":  func(r *serverInstallReq) { r.Password = "" },
		"плохой SNI":          func(r *serverInstallReq) { r.SNI = "bad sni;rm" },
		"плохой хост":         func(r *serverInstallReq) { r.Host = "host;rm -rf" },
		"плохой пользователь": func(r *serverInstallReq) { r.User = "root;id" },
		"плохой порт":         func(r *serverInstallReq) { r.Port = 70000 },
		"длинное имя":         func(r *serverInstallReq) { r.Name = strings.Repeat("я", 41) },
	} {
		r := serverInstallReq{Host: "vps.example.com", Password: "p", Fingerprint: "SHA256:x", SNI: "www.example.com", Confirm: true}
		mod(&r)
		if r.check(true) == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

func TestAddInstalledServersReplaces(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: DefaultSettings()}
	first, _ := ParseShareLinks("hysteria2://p1@203.0.113.10:443/?sni=x.example.com#A")
	if _, err := a.addInstalledServers(first); err != nil {
		t.Fatal(err)
	}
	id := a.settings.Servers[0].ID
	again, _ := ParseShareLinks("hysteria2://p2@203.0.113.10:443/?sni=x.example.com#A\nhysteria2://p3@203.0.113.11:443#B")
	if _, err := a.addInstalledServers(again); err != nil {
		t.Fatal(err)
	}
	if len(a.settings.Servers) != 2 || a.settings.Servers[0].ID != id || a.settings.Servers[0].Password != "p2" {
		t.Errorf("повторная установка: %+v", a.settings.Servers)
	}
}

// Скрипт для сервера встроен в Naivka и проходит проверку синтаксиса bash.
func TestInstallScriptEmbedded(t *testing.T) {
	if !strings.Contains(installScript, "NAIVKA-LINK") {
		t.Fatal("скрипт не встроен")
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(installScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("bash -n: %v %s", err, out)
	}
}
