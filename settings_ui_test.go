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
	c.Accent, c.Oled, c.TrayMini, c.NativeFrame, c.OwnServer = "rose", true, false, true, false
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
	for _, old := range []string{"neon", "indigo", "violet", "orchid"} {
		s.Accent = old
		s.normalize()
		if s.Accent != "lime" {
			t.Fatalf("accent %q → %q, want lime", old, s.Accent)
		}
	}
	s.Accent = "coral"
	s.normalize()
	if s.Accent != "coral" {
		t.Fatalf("accent = %q, want coral", s.Accent)
	}
	if !DefaultSettings().TrayMini {
		t.Fatal("mini window should be on by default")
	}
	if !DefaultSettings().OwnServer {
		t.Fatal("own server section should be shown by default")
	}
}
