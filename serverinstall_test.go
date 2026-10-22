//go:build !legacy

package main

import (
	"encoding/base64"
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
	ok := serverInstallReq{Host: " 203.0.113.10 ", Password: "p", Fingerprint: "SHA256:x", SNI: "www.example.com", Confirm: true, Protocols: true}
	if err := ok.check(); err != nil || ok.Host != "203.0.113.10" || ok.Port != 22 || ok.User != "root" || ok.Name != "203.0.113.10" {
		t.Fatalf("%+v %v", ok, err)
	}
	naive := serverInstallReq{Host: "vps.example.com", Password: "p", Fingerprint: "SHA256:x", Confirm: true, Protocols: true, Naive: true, Domain: " Proxy.Example.com "}
	if err := naive.check(); err != nil || naive.Domain != "proxy.example.com" {
		t.Errorf("NaiveProxy без SNI, с доменом: %+v %v", naive, err)
	}
	panelOnly := serverInstallReq{Host: "vps.example.com", Password: "p", Fingerprint: "SHA256:x", Confirm: true, Panel: "s-ui", PanelAccess: "public", PanelConfirm: true}
	if err := panelOnly.check(); err != nil || panelOnly.PanelAccess != "tunnel" {
		t.Errorf("только панель (s-ui всегда через туннель): %+v %v", panelOnly, err)
	}
	for name, mod := range map[string]func(r *serverInstallReq){
		"нет подтверждения":      func(r *serverInstallReq) { r.Confirm = false },
		"нет отпечатка":          func(r *serverInstallReq) { r.Fingerprint = "" },
		"нет пароля и ключа":     func(r *serverInstallReq) { r.Password = "" },
		"плохой SNI":             func(r *serverInstallReq) { r.SNI = "bad sni;rm" },
		"плохой хост":            func(r *serverInstallReq) { r.Host = "host;rm -rf" },
		"плохой пользователь":    func(r *serverInstallReq) { r.User = "root;id" },
		"плохой порт":            func(r *serverInstallReq) { r.Port = 70000 },
		"длинное имя":            func(r *serverInstallReq) { r.Name = strings.Repeat("я", 41) },
		"Naive без домена":       func(r *serverInstallReq) { r.Naive = true; r.Domain = "" },
		"Naive с плохим доменом": func(r *serverInstallReq) { r.Naive = true; r.Domain = "x;reboot" },
		"ничего не выбрано":      func(r *serverInstallReq) { r.Protocols = false },
		"чужая панель":           func(r *serverInstallReq) { r.Panel = "marzban"; r.PanelConfirm = true },
		"панель без согласия":    func(r *serverInstallReq) { r.Panel = "3x-ui" },
	} {
		r := serverInstallReq{Host: "vps.example.com", Password: "p", Fingerprint: "SHA256:x", SNI: "www.example.com", Confirm: true, Protocols: true}
		mod(&r)
		if r.check() == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

func TestParsePanelLine(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	p, err := parsePanelLine(enc(`{"type":"3x-ui","version":"v2.9.0","access":"tunnel","port":34567,"path":"/abc/","user":"admin1","pass":"secret","url":"https://evil.example/"}`))
	if err != nil || p.Port != 34567 || p.Path != "/abc/" || p.URL != "" || p.Access != "tunnel" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, bad := range []string{"!!!", enc(`{"type":"marzban","port":1,"user":"a","pass":"b"}`), enc(`{"type":"s-ui","port":0,"user":"a","pass":"b"}`), enc(`{"type":"s-ui","port":1,"user":"","pass":"b"}`)} {
		if _, err := parsePanelLine(bad); err == nil {
			t.Errorf("принято: %s", bad)
		}
	}
}

func TestAddPanelReplaces(t *testing.T) {
	a := &App{dir: t.TempDir(), settings: DefaultSettings()}
	id1, err := a.addPanel(Panel{Type: "3x-ui", Host: "h", Port: 1, User: "u", Pass: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := a.addPanel(Panel{Type: "3x-ui", Host: "h", Port: 2, User: "u", Pass: "p2"})
	_, _ = a.addPanel(Panel{Type: "s-ui", Host: "h", Port: 3, User: "u", Pass: "p3"})
	if id1 != id2 || len(a.settings.Panels) != 2 || a.settings.Panels[0].Pass != "p2" {
		t.Errorf("%+v", a.settings.Panels)
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
	for name, sc := range map[string]string{"install-server.sh": installScript, "install-panel.sh": panelScript, "friends.sh": friendsScript} {
		if !strings.Contains(sc, "NAIVKA-") {
			t.Fatalf("%s не встроен", name)
		}
		cmd := exec.Command("bash", "-n")
		cmd.Stdin = strings.NewReader(sc)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: bash -n: %v %s", name, err, out)
		}
	}
}

func TestParseFriendsLine(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	list, err := parseFriendsLine(enc(`{"friends":[
		{"id":"0123abcd","name":"Петя","until":0,"links":["hysteria2://p@203.0.113.10:443/?sni=x.example.com#P","javascript:alert(1)"]},
		{"id":"../../x","name":"bad"}]}`))
	if err != nil || len(list) != 1 || list[0].Name != "Петя" || len(list[0].Links) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := parseFriendsLine("!!!"); err == nil {
		t.Error("мусор принят")
	}
}

func TestFriendsReqCheck(t *testing.T) {
	ok := []friendsReq{{Action: "list"}, {Action: "add", Name: " Петя "}, {Action: "add", Name: "Аня", Days: 30},
		{Action: "remove", FID: "0123abcd"}, {Action: "renew", FID: "0123abcd", Days: 0}}
	for _, r := range ok {
		if err := r.check(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []friendsReq{{Action: "rm -rf"}, {Action: "add"}, {Action: "add", Name: strings.Repeat("я", 41)},
		{Action: "add", Name: "a\nb"}, {Action: "add", Name: "a", Days: -1}, {Action: "remove", FID: "x;reboot"}, {Action: "pause"}}
	for _, r := range bad {
		if r.check() == nil {
			t.Errorf("принято: %+v", r)
		}
	}
}
