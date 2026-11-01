package main

import "testing"

func TestHomeLayoutNormalizesAndPreservesControls(t *testing.T) {
	l := HomeLayout{Version: 1, Wide: []HomeWidget{{ID: "routes", Span: 100, Hidden: true}, {ID: "routes", Span: 1}, {ID: "script", Hidden: true}, {ID: "ip", Span: 0, Hidden: true}}}
	l.normalize()
	if len(l.Wide) != len(homeWidgetIDs) || len(l.Narrow) != len(homeWidgetIDs) {
		t.Fatal("missing or duplicate widgets", l)
	}
	if l.Wide[0].Hidden || l.Wide[0].Span != 3 {
		t.Fatal("connection controls hidden", l)
	}
	if l.Wide[1].ID != "ip" || !l.Wide[1].Hidden || l.Wide[1].Span != 1 {
		t.Fatal(l)
	}
	s := DefaultSettings()
	fp := s.fingerprint()
	s.Layout = l
	if fp != s.fingerprint() {
		t.Fatal("layout changes core config")
	}
	dir := t.TempDir()
	if err := SaveSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Layout.Wide[0].ID != "routes" || got.Layout.Wide[1].ID != "ip" {
		t.Fatal("layout not persisted")
	}
}
func TestUnknownHomeLayoutFallsBack(t *testing.T) {
	l := HomeLayout{Version: 100, Wide: []HomeWidget{{ID: "routes", Hidden: true}}}
	l.normalize()
	if l.Version != 0 || len(l.Wide) != 0 {
		t.Fatal(l)
	}
}
