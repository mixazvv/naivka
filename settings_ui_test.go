package main

import "testing"

// Избранное, порядок серверов и оформление не требуют перезапуска ядра.
func TestFingerprintIgnoresUIFields(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Host: "a.example", Port: 443}, {ID: "b", Type: "naive", Host: "b.example", Port: 443}}
	base := s.fingerprint()

	c := s.clone()
	c.Servers[0], c.Servers[1] = c.Servers[1], c.Servers[0]
	c.Servers[0].Fav = true
	c.Accent, c.Oled, c.TrayMini, c.NativeFrame = "orchid", true, false, true
	if c.fingerprint() != base {
		t.Fatal("UI-only changes must not require a core restart")
	}
	c.Servers[0].Host = "c.example"
	if c.fingerprint() == base {
		t.Fatal("server changes must still be detected")
	}
}

func TestNormalizeAccent(t *testing.T) {
	s := DefaultSettings()
	s.Accent = "neon"
	s.normalize()
	if s.Accent != "indigo" {
		t.Fatalf("accent = %q, want indigo", s.Accent)
	}
	s.Accent = "violet"
	s.normalize()
	if s.Accent != "violet" {
		t.Fatalf("accent = %q, want violet", s.Accent)
	}
	if !DefaultSettings().TrayMini {
		t.Fatal("mini window should be on by default")
	}
}
