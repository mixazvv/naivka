package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserRuleSetFiles(t *testing.T) {
	s := DefaultSettings()
	s.Rules = []Rule{{"domain", ".Twitch.tv", "proxy"}, {"process", "Discord.exe", "proxy"}, {"ip", "10.1.2.3", "direct"}, {"domain", "rutracker.org", "zapret"}}
	f := userRuleSetFiles(s)
	if len(f) != 8 {
		t.Fatalf("списков %d, а не 8", len(f))
	}
	var v struct {
		Rules []map[string][]string `json:"rules"`
	}
	_ = json.Unmarshal(f["user-proxy-d"], &v)
	if len(v.Rules) != 1 || v.Rules[0]["domain_suffix"][0] != "twitch.tv" {
		t.Fatalf("user-proxy-d: %s", f["user-proxy-d"])
	}
	_ = json.Unmarshal(f["user-proxy-o"], &v)
	if len(v.Rules) != 1 || v.Rules[0]["process_name"][0] != "Discord.exe" {
		t.Fatalf("user-proxy-o: %s", f["user-proxy-o"])
	}
	_ = json.Unmarshal(f["user-direct-o"], &v)
	if len(v.Rules) != 1 || v.Rules[0]["ip_cidr"][0] != "10.1.2.3/32" {
		t.Fatalf("user-direct-o: %s", f["user-direct-o"])
	}
	if !strings.Contains(string(f["user-block-d"]), `"rules":[]`) {
		t.Fatalf("пустой список: %s", f["user-block-d"])
	}
}

func TestChangedRulesAndLiveDetection(t *testing.T) {
	prev := DefaultSettings()
	prev.Rules = []Rule{{"domain", "twitch.tv", "proxy"}, {"domain", "vk.com", "direct"}}
	next := prev.clone()
	next.Rules = []Rule{{"domain", "twitch.tv", "direct"}, {"domain", "vk.com", "direct"}}
	ch := changedRules(prev.Rules, next.Rules)
	if len(ch) != 2 { // правило «через сервер» убрано, «напрямую» добавлено
		t.Fatalf("изменения: %+v", ch)
	}
	if prev.fingerprintNoRules() != next.fingerprintNoRules() || prev.fingerprint() == next.fingerprint() {
		t.Fatal("смена только правил должна определяться как «без перезапуска»")
	}
	next.Mode = "tun"
	if prev.fingerprintNoRules() == next.fingerprintNoRules() {
		t.Fatal("смена режима требует перезапуска")
	}
	if zapretRulesChanged(ch) || !zapretRulesChanged([]Rule{{"domain", "x.org", "zapret"}}) {
		t.Fatal("zapretRulesChanged")
	}
}

func TestConnMatchesRule(t *testing.T) {
	var c Conn
	c.Metadata.Host = "video-edge-1.ttvnw.net"
	c.Metadata.ProcessPath = `C:\Program Files\Twitch\Twitch.exe`
	c.Metadata.DestIP = "10.1.2.3"
	for _, tc := range []struct {
		r    Rule
		want bool
	}{
		{Rule{"domain", "ttvnw.net", "proxy"}, true},
		{Rule{"domain", "nw.net", "proxy"}, false},
		{Rule{"process", "twitch.exe", "proxy"}, true},
		{Rule{"ip", "10.1.0.0/16", "direct"}, true},
		{Rule{"ip", "10.1.2.4", "direct"}, false},
	} {
		if got := connMatchesRule(c, tc.r); got != tc.want {
			t.Errorf("%+v: %v", tc.r, got)
		}
	}
}

// Конфиг со своими правилами в файлах проходит проверку настоящим sing-box.
func TestUserRuleSetsWithSingbox(t *testing.T) {
	sb := os.Getenv("SINGBOX")
	if sb == "" {
		t.Skip("SINGBOX не задан")
	}
	servers, _ := ParseShareLinks("naive+https://alice:s3cret@nl.example.com:443#NL")
	dir := t.TempDir()
	s := DefaultSettings()
	s.Servers = servers
	s.Rules = []Rule{{"process", "Discord.exe", "proxy"}, {"domain", "twitch.tv", "direct"}, {"ip", "10.0.0.0/8", "block"}, {"domain", "rutracker.org", "zapret"}}
	s.normalize()
	if err := writeUserRuleSets(dir, s); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"proxy", "tun"} {
		s.Mode = mode
		cfg, err := BuildConfig(s, ConfigEnv{APIPort: 9090, Secret: "x", CachePath: filepath.Join(dir, "c.db"), UserRulesDir: dir, ZapretActive: true})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.MarshalIndent(cfg, "", " ")
		if strings.Contains(string(b), `"Discord.exe"`) {
			t.Fatal("правила должны быть в файлах, а не в конфиге")
		}
		p := filepath.Join(dir, mode+".json")
		_ = os.WriteFile(p, b, 0o600)
		if out, err := exec.Command(sb, "check", "-c", p).CombinedOutput(); err != nil {
			t.Fatalf("%s: sing-box check: %s", mode, out)
		}
	}
}

// Адреса видео Twitch — одно решение на семейство, и в списке ядра оно — суффикс домена.
func TestSmartTwitchFamily(t *testing.T) {
	for host, want := range map[string]string{
		"video-edge-c2a1b4.fra05.abs.hls.ttvnw.net": ".ttvnw.net",
		"usher.ttvnw.net":                           ".ttvnw.net",
		"www.twitch.tv":                             ".twitch.tv",
		"d2nvs31859zcd8.cloudfront.net":             "d2nvs31859zcd8.cloudfront.net",
		"example.com":                               "example.com",
	} {
		if got := smartKey(host); got != want {
			t.Errorf("smartKey(%s) = %s, а нужно %s", host, got, want)
		}
	}
	var v struct {
		Rules []map[string][]string `json:"rules"`
	}
	_ = json.Unmarshal(smartRuleSetJSON([]string{"example.com", ".ttvnw.net"}), &v)
	if len(v.Rules) != 2 || v.Rules[0]["domain"][0] != "example.com" || v.Rules[1]["domain_suffix"][0] != "ttvnw.net" {
		t.Fatalf("список: %+v", v.Rules)
	}
}
