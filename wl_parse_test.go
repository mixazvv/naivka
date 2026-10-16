package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Подписка в формате zieng2/wl (снимок в testdata, адреса и ключи заменены вымышленными): разбирается всё, кроме xHTTP,
// и получившийся конфиг принимает sing-box.
func TestWLSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/wl/sample.txt")
	if err != nil {
		t.Skip("нет снимка подписки")
	}
	servers, errs := ParseShareLinks(string(raw))
	xhttp := 0
	for _, e := range errs {
		if strings.Contains(e, "xHTTP") {
			xhttp++
		} else {
			t.Errorf("не разобрано: %s", e)
		}
	}
	kinds := map[string]int{}
	for _, sv := range servers {
		kinds[sv.Security+"/"+sv.Transport]++
	}
	t.Logf("разобрано %d серверов %v, пропущено xHTTP: %d", len(servers), kinds, xhttp)
	if len(servers) < 50 {
		t.Fatalf("слишком мало серверов: %d", len(servers))
	}
	// ссылка → сервер → ссылка: параметры не теряются
	for _, sv := range servers {
		back, e := ParseShareLinks(ShareLink(sv))
		if len(e) > 0 || len(back) != 1 {
			t.Fatalf("обратное преобразование %s: %v", sv.Name, e)
		}
		b := back[0]
		b.ID = sv.ID
		if b != sv {
			t.Fatalf("после обратного преобразования отличается:\n%+v\n%+v", sv, b)
		}
	}
	sb := os.Getenv("SINGBOX")
	if sb == "" {
		return
	}
	s := DefaultSettings()
	s.Servers = servers
	s.normalize()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildConfig(s, ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	p := filepath.Join(t.TempDir(), "wl.json")
	_ = os.WriteFile(p, b, 0o600)
	if out, err := exec.Command(sb, "check", "-c", p).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %s", out)
	}
}

// Всё вместе: свои серверы + подписка как запасная группа, UDP через VLESS,
// реклама, раздача в сеть и входы для теста скорости.
func TestConfigAllFeatures(t *testing.T) {
	sb := os.Getenv("SINGBOX")
	raw, err := os.ReadFile("testdata/wl/sample.txt")
	if sb == "" || err != nil {
		t.Skip("нужны SINGBOX и снимок подписки")
	}
	wl, _ := ParseShareLinks(string(raw))
	own, _ := ParseShareLinks("naive+https://a:b@nl.example.com:443#N\n" +
		"vless://d39351d5-2ec2-4ac7-aab4-8ca53a5816fc@nl.example.com:8443?security=reality&sni=nl.example.com&fp=chrome&pbk=7LjM4Jm1_GdlJqZqBs5AtMnkaJV4qcSxMCB6YlI8hmQ&sid=d99fd31c5db21a7c&type=tcp&flow=xtls-rprx-vision#R")
	for _, mode := range []string{"ru-direct", "global"} {
		for _, tun := range []bool{false, true} {
			s := DefaultSettings()
			s.Servers = own
			s.Routing = mode
			if tun {
				s.Mode = "tun"
			}
			s.Selected = "wl"
			s.UDPProxy, s.AdBlock = true, true
			s.Share.Enabled = true
			s.Rules = []Rule{{"process", "Discord.exe", "proxy"}}
			s.normalize()
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			probes := map[string]int{"wl": 9301}
			for i, sv := range own {
				probes[serverTag(sv)] = 9310 + i
			}
			cfg, err := BuildConfig(s, ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db", WLServers: wl[:20], ServerProbes: probes})
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(cfg)
			js := string(b)
			p := filepath.Join(t.TempDir(), "all.json")
			_ = os.WriteFile(p, b, 0o600)
			if out, err := exec.Command(sb, "check", "-c", p).CombinedOutput(); err != nil {
				t.Fatalf("%s tun=%v: %s", mode, tun, out)
			}
			for _, want := range []string{`"tag":"wl"`, `"udp-auto"`, `geosite-category-ads-all`, `"lan-in"`, `"probe-wl"`, `"source_ip_is_private":true`, `"invert":true`, `"127.0.0.0/8"`} {
				if !strings.Contains(js, want) {
					t.Errorf("%s: нет %s", mode, want)
				}
			}
			// UDP никогда не должен уходить в Naive: везде, где правило ведёт «через сервер», перед ним — копия для UDP
			rules := cfg["route"].(M)["rules"].([]any)
			for i, r := range rules {
				m := r.(M)
				if m["outbound"] == tagProxy && m["network"] == nil {
					prev, _ := rules[i-1].(M)
					if prev["network"] != "udp" || prev["outbound"] != "udp-auto" {
						t.Errorf("%s: перед правилом %v нет UDP-копии", mode, m)
					}
				}
			}
		}
	}
}
