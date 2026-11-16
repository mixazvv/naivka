package main

import "testing"

func TestServerListSortPresentationOnly(t *testing.T) {
	s := DefaultSettings()
	s.normalize()
	before := s.fingerprint()
	for _, mode := range []string{"manual", "ping", "ping-desc", "name"} {
		s.ServerSort = mode
		s.normalize()
		if s.fingerprint() != before {
			t.Fatal("sorting restarts core", mode)
		}
		dir := t.TempDir()
		if err := SaveSettings(dir, s); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadSettings(dir)
		if err != nil || loaded.ServerSort != mode {
			t.Fatal("sorting was not preserved", loaded, err)
		}
	}
	s.ServerSort = "invalid"
	s.normalize()
	if s.ServerSort != "manual" {
		t.Fatal("unknown sort did not fall back to manual")
	}
}
