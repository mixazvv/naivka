package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Проверка сгенерированных конфигов настоящим sing-box:
//
//	SINGBOX=/путь/к/sing-box go test -run TestConfigsWithSingbox .
func TestConfigsWithSingbox(t *testing.T) {
	sb := os.Getenv("SINGBOX")
	if sb == "" {
		t.Skip("SINGBOX не задан")
	}
	links := "naive+https://alice:s3cret@nl.example.com:443#Нидерланды\n" +
		"vless://d39351d5-2ec2-4ac7-aab4-8ca53a5816fc@nl.example.com:8443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=nl.example.com&fp=chrome&pbk=7LjM4Jm1_GdlJqZqBs5AtMnkaJV4qcSxMCB6YlI8hmQ&sid=d99fd31c5db21a7c&type=tcp#Reality"
	servers, errs := ParseShareLinks(links)
	if len(errs) > 0 || len(servers) != 2 {
		t.Fatalf("ссылки: %v %v", servers, errs)
	}
	// ссылка → сервер → ссылка → сервер: ничего не теряется
	back, errs := ParseShareLinks(ShareLink(servers[1]) + "\n" + ShareLink(servers[0]))
	if len(errs) > 0 || back[0].PublicKey != servers[1].PublicKey || back[1].Password != "s3cret" || back[0].Name != "Reality" {
		t.Fatalf("обратное преобразование: %+v %v", back, errs)
	}
	for _, c := range []struct {
		name string
		mode string
		env  ConfigEnv
	}{
		{"proxy", "proxy", ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db", ProbeDirectPort: 9101, ProbeProxyPort: 9102}},
		{"tun", "tun", ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db"}},
		{"win7", "proxy", ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db", NoNaive: true}},
		{"mobile", "proxy", ConfigEnv{Mobile: true}},
		{"zapret+tg", "tun", ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db", ZapretActive: true, ZapretDomains: []string{"youtube.com", "discord.com"}, TgWsProxy: true}},
		{"twitch", "tun", ConfigEnv{APIPort: 9090, Secret: "x", CachePath: "c.db"}},
		{"twitch-mobile", "proxy", ConfigEnv{Mobile: true}},
	} {
		s := DefaultSettings()
		s.TwitchAdFree = strings.HasPrefix(c.name, "twitch")
		s.AdBlock = c.name == "twitch"
		s.Servers = servers
		s.Mode = c.mode
		s.Rules = []Rule{{"process", "Telegram.exe", "proxy"}, {"domain", "youtube.com", "proxy"}, {"domain", "rutracker.org", "zapret"}}
		s.normalize()
		cfg, err := BuildConfig(s, c.env)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		b, _ := json.MarshalIndent(cfg, "", " ")
		p := filepath.Join(t.TempDir(), c.name+".json")
		_ = os.WriteFile(p, b, 0o600)
		if out, err := exec.Command(sb, "check", "-c", p).CombinedOutput(); err != nil {
			t.Errorf("%s: sing-box check: %s", c.name, out)
		}
		js := string(b)
		if c.env.NoNaive && json.Valid(b) && contains(js, `"naive"`) {
			t.Errorf("win7: в конфиге остался naive")
		}
		if c.name == "zapret+tg" && (!contains(js, "TgWsProxy.exe") || !contains(js, "discord.com") || !contains(js, "rutracker.org")) {
			t.Errorf("zapret+tg: нет правил zapret или tg-ws-proxy")
		}
		if c.env.Mobile && (contains(js, "process_name") || contains(js, "clash_api") || contains(js, "mixed")) {
			t.Errorf("mobile: лишнее в профиле телефона")
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// Twitch без рекламы: плейлисты — в группу прокси TTV LOL PRO, их QUIC отклоняется,
// а сами куски видео и остальной Twitch идут по обычным правилам.
func TestTwitchAdFreeConfig(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Host: "nl.example.com", Port: 443}}
	s.normalize()
	cfg, err := BuildConfig(s, ConfigEnv{APIPort: 9090})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(cfg); contains(string(b), tagTwitch) {
		t.Fatal("выключено, а прокси Twitch в конфиге")
	}

	s.TwitchAdFree = true
	s.UDPProxy = true
	s.Servers = append(s.Servers, Server{ID: "b", Type: "vless", Host: "nl.example.com", Port: 8443, UUID: "d39351d5-2ec2-4ac7-aab4-8ca53a5816fc", Security: "reality", PublicKey: "k", SNI: "nl.example.com"})
	cfg, err = BuildConfig(s, ConfigEnv{APIPort: 9090})
	if err != nil {
		t.Fatal(err)
	}
	group := map[string]bool{}
	for _, o := range cfg["outbounds"].([]any) {
		if m := o.(M); m["tag"] == tagTwitch {
			for _, t := range m["outbounds"].([]any) {
				group[t.(string)] = true
			}
		}
	}
	if len(group) != len(twitchProxies) {
		t.Fatalf("группа %s: %v", tagTwitch, group)
	}
	var tcp, udp int
	for _, r := range cfg["route"].(M)["rules"].([]any) {
		m := r.(M)
		if m["domain"] == nil || !contains(fmt.Sprint(m["domain"]), "usher.ttvnw.net") {
			continue
		}
		switch {
		case m["outbound"] == tagTwitch && m["network"] == nil:
			tcp++
		case m["network"] == "udp" && m["action"] == "reject":
			udp++
		default:
			t.Errorf("лишнее правило для плейлистов Twitch: %v", m)
		}
	}
	if tcp != 1 || udp != 1 {
		t.Errorf("правила плейлистов: tcp=%d udp=%d", tcp, udp)
	}
}
