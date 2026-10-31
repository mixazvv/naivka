package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Старое включение бета-котёнка не мешает загрузить настройки обычного талисмана.
func TestLegacyBetaMascotSettings(t *testing.T) {
	s := DefaultSettings()
	if err := json.Unmarshal([]byte(`{"mascot":true,"mascotBeta":true,"petTaskbar":true}`), s); err != nil {
		t.Fatal(err)
	}
	s.normalize()
	if !s.Mascot || !s.PetTaskbar {
		t.Fatal("existing mascot preferences were lost")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "mascotBeta") {
		t.Fatal("removed beta setting was persisted")
	}
}

// Избранное, порядок серверов и оформление не требуют перезапуска ядра.
func TestFingerprintIgnoresUIFields(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Host: "a.example", Port: 443}, {ID: "b", Type: "naive", Host: "b.example", Port: 443}}
	base := s.fingerprint()

	c := s.clone()
	c.Servers[0], c.Servers[1] = c.Servers[1], c.Servers[0]
	c.Servers[0].Fav = true
	c.Accent, c.Oled, c.TrayMini, c.NativeFrame, c.OwnServer = "#3FA9F5", true, false, true, false
	if c.fingerprint() != base {
		t.Fatal("UI-only changes must not require a core restart")
	}
	c.Servers[0].Host = "c.example"
	if c.fingerprint() == base {
		t.Fatal("server changes must still be detected")
	}
}

func TestNormalizeAccent(t *testing.T) {
	for in, want := range map[string]string{
		"": "#B6E35A", "neon": "#B6E35A", "indigo": "#B6E35A", "violet": "#B6E35A",
		"lime": "#B6E35A", "coral": "#FF9A6E", "rose": "#F58FC0",
		"#3fa9f5": "#3FA9F5", "3FA9F5": "#3FA9F5", "#3FA9F": "#B6E35A", "#GGGGGG": "#B6E35A", "#3FA9F5;x": "#B6E35A",
	} {
		s := DefaultSettings()
		s.Accent = in
		s.normalize()
		if s.Accent != want {
			t.Fatalf("accent %q → %q, want %q", in, s.Accent, want)
		}
	}
	if !DefaultSettings().TrayMini {
		t.Fatal("mini window should be on by default")
	}
	if !DefaultSettings().OwnServer {
		t.Fatal("own server section should be shown by default")
	}
}

func TestNormalizeThemeDarkOnly(t *testing.T) {
	for in, want := range map[string]string{"light": "dark", "system": "dark", "dark": "dark", "gold": "gold", "blocks": "blocks"} {
		s := DefaultSettings()
		s.Theme = in
		s.normalize()
		if s.Theme != want {
			t.Fatalf("theme %q → %q, want %q", in, s.Theme, want)
		}
	}
}
