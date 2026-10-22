package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseInlineList(t *testing.T) {
	il := parseInlineList("# комментарий\nexample.com\n.sub.example.org\n0.0.0.0 ads.example.net\ndomain:full.example\n1.2.3.4\n10.0.0.0/8\nTelegram.exe\n")
	want := map[string]bool{"example.com": true, "sub.example.org": true, "ads.example.net": true, "full.example": true}
	if len(il.Domains) != len(want) {
		t.Fatalf("домены: %v", il.Domains)
	}
	for _, d := range il.Domains {
		if !want[d] {
			t.Errorf("лишний домен %q", d)
		}
	}
	if strings.Join(il.IPs, ",") != "1.2.3.4/32,10.0.0.0/8" {
		t.Errorf("IP: %v", il.IPs)
	}
}

func TestRuleListNormalize(t *testing.T) {
	for u, f := range map[string]string{
		"https://x/russia_inside.srs": "srs", "https://x/a.json?raw=1": "json", "https://x/inside-raw.lst": "text", "https://x/list": "text",
	} {
		l := RuleList{URL: u}
		l.normalize()
		if l.Format != f || l.Action != "proxy" || l.ID == "" || l.Name == "" {
			t.Errorf("%s: %+v", u, l)
		}
	}
	if (RuleList{Name: "x", URL: "http://x/a.srs"}).validate() == nil {
		t.Error("ссылка без https принята")
	}
}

func TestRuleListsConfig(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Host: "nl.example.com", Port: 443}}
	s.RuleLists = []RuleList{
		{ID: "0000aa01", Name: "srs", URL: "https://x/russia_inside.srs", Action: "proxy", Enabled: true},
		{ID: "0000aa02", Name: "txt", URL: "https://x/direct.lst", Action: "direct", Enabled: true},
		{ID: "r3", Name: "off", URL: "https://x/off.srs", Action: "block", Enabled: false},
		{ID: "r4", Name: "not yet", URL: "https://x/missing.txt", Action: "block", Enabled: true},
	}
	s.normalize()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	env := ConfigEnv{APIPort: 9090, InlineLists: map[string]*inlineList{"0000aa02": {Domains: []string{"gosuslugi.ru"}, IPs: []string{"5.6.7.0/24"}}}}
	cfg, err := BuildConfig(s, env)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg["route"])
	js := string(b)
	for _, want := range []string{`"tag":"list-0000aa01"`, `"format":"binary"`, `"tag":"list-0000aa02"`, `"type":"inline"`, `gosuslugi.ru`, `5.6.7.0/24`} {
		if !contains(js, want) {
			t.Errorf("нет %s", want)
		}
	}
	if contains(js, "list-r3") || contains(js, "list-r4") {
		t.Error("выключенный или нескачанный список попал в конфиг")
	}
}

func TestListCacheDownload(t *testing.T) {
	body := "example.com\nexample.org\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer srv.Close()
	c := newListCache(t.TempDir())
	l := RuleList{ID: "x", Name: "x", URL: srv.URL, Format: "text"}
	il, err := c.get(l, true, false)
	if err != nil || len(il.Domains) != 2 {
		t.Fatalf("%v %v", il, err)
	}
	// Сеть пропала — работает прошлая копия.
	srv.Close()
	if il, err := c.get(l, true, true); err != nil || len(il.Domains) != 2 || c.lastError("x") == "" {
		t.Errorf("без сети: %v %v %q", il, err, c.lastError("x"))
	}
	// Пустой файл не затирает рабочий список.
	if err := c.download("http://127.0.0.1:1/none", c.path("x")); err == nil {
		t.Error("ожидалась ошибка")
	}
	if _, err := os.Stat(c.path("x")); err != nil {
		t.Error("рабочий список удалён")
	}
}

// Чужой список не может забрать себе весь интернет, а id из копии — выйти за папку lists.
func TestListSafety(t *testing.T) {
	il := parseInlineList("0.0.0.0/0\n::/0\n10.0.0.0/4\ncom\n.ru\nexample.com\n5.6.7.0/24\n2001:db8::/32")
	if len(il.Domains) != 1 || il.Domains[0] != "example.com" || len(il.IPs) != 2 {
		t.Fatalf("%+v", il)
	}
	l := RuleList{ID: `..\..\x`, URL: "https://x/a.lst"}
	l.normalize()
	if !listIDRe.MatchString(l.ID) {
		t.Fatalf("id %q", l.ID)
	}
	s := DefaultSettings()
	s.RuleLists = []RuleList{{ID: "0000aa01", URL: "https://x/a.lst"}, {ID: "0000aa01", URL: "https://x/b.lst"}}
	s.normalize()
	if s.Validate() == nil {
		t.Fatal("повтор id принят")
	}
}

// Адрес панели — только веб-адрес: file:, UNC и прочее превращается во вход через туннель.
func TestPanelURLSafety(t *testing.T) {
	s := DefaultSettings()
	s.Panels = []Panel{{Access: "public", URL: `\\attacker\s\x.exe`}, {Access: "public", URL: "file:///C:/x.exe"}, {Access: "public", URL: "https://203.0.113.1:2053/p/"}}
	s.normalize()
	if s.Panels[0].Access != "tunnel" || s.Panels[1].URL != "" || s.Panels[2].Access != "public" {
		t.Fatalf("%+v", s.Panels)
	}
}
