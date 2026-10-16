package main

import (
	"strings"
	"testing"
)

func TestPuny(t *testing.T) {
	for in, want := range map[string]string{"рф": "p1ai", "рус": "p1acf", "пример": "e1afmkfd", "москва": "80adxhks", "bücher": "bcher-kva"} {
		if got := punyEncode(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestParseText(t *testing.T) {
	text := `# мои сайты
https://www.YouTube.com/watch?v=1, *.googlevideo.com; discord.gg
сайт.рф
203.0.113.7
198.51.100.0/22
Telegram.exe
C:\Program Files\Some App\My App.exe
0.0.0.0 ads.example.com tracker.example.net
[Напрямую]
sberbank.ru
youtube.com
domain:gosuslugi.ru
[block]
300.1.1.1
keyword:ads
-bad-.com
`
	res := ParseRules(text, "proxy")
	got := map[string]string{}
	for _, r := range res.Rules {
		got[r.Kind+"|"+r.Value] = r.Action
	}
	want := map[string]string{
		"domain|youtube.com": "proxy", "domain|googlevideo.com": "proxy", "domain|discord.gg": "proxy",
		"domain|xn--80aswg.xn--p1ai": "proxy", "ip|203.0.113.7": "proxy", "ip|198.51.100.0/22": "proxy",
		"process|Telegram.exe": "proxy", "process|My App.exe": "proxy",
		"domain|ads.example.com": "proxy", "domain|tracker.example.net": "proxy",
		"domain|sberbank.ru": "direct", "domain|gosuslugi.ru": "direct",
	}
	for k, a := range want {
		if got[k] != a {
			t.Errorf("%s: got %q want %q", k, got[k], a)
		}
	}
	if len(res.Rules) != len(want) {
		t.Errorf("rules: %d, want %d: %+v", len(res.Rules), len(want), res.Rules)
	}
	if len(res.Errors) != 3 {
		t.Errorf("errors: %v", res.Errors)
	}
	t.Log(res.Errors)
}

func TestRoundTrip(t *testing.T) {
	src := []Rule{{"domain", "youtube.com", "proxy"}, {"ip", "10.0.0.0/8", "direct"}, {"process", "My App.exe", "block"}, {"domain", "xn--p1ai", "direct"}}
	res := ParseRules(RulesAsText(src), "proxy")
	if len(res.Rules) != len(src) || len(res.Errors) != 0 {
		t.Fatalf("text roundtrip: %+v %v", res.Rules, res.Errors)
	}
	have := map[Rule]bool{}
	for _, r := range res.Rules {
		have[r] = true
	}
	for _, r := range src {
		if !have[r] {
			t.Errorf("text roundtrip lost %+v", r)
		}
	}
	js := `{"format":"naivka-rules","version":1,"rules":[{"kind":"domain","value":"YouTube.com","action":"block"},{"kind":"ip","value":"1.2.3.4","action":"weird"}]}`
	r2 := ParseRules(js, "direct")
	if !r2.FromJSON || len(r2.Rules) != 2 || r2.Rules[0].Value != "youtube.com" || r2.Rules[0].Action != "block" || r2.Rules[1].Action != "direct" {
		t.Errorf("json: %+v", r2)
	}
}

func geoProviderNamed(name string) geoProvider {
	for _, p := range geoProviders {
		if p.name == name {
			return p
		}
	}
	panic("нет провайдера " + name)
}

// Все сервисы — только по HTTPS: адреса серверов не должны идти открытым текстом.
func TestGeoProvidersHTTPSOnly(t *testing.T) {
	for _, p := range geoProviders {
		if u := p.url("1.2.3.4"); !strings.HasPrefix(u, "https://") {
			t.Errorf("%s (%s) без HTTPS", p.name, u)
		}
	}
}
