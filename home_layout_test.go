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

func TestHomeLayoutCoordinatesMigrationAndCollisions(t *testing.T) {
	l := HomeLayout{Version: 1, Wide: []HomeWidget{{ID: "routes", Span: 2, Col: 2, Row: 3, Height: 540}, {ID: "speed", Span: 1, Col: 1, Row: 3, Height: 220}, {ID: "ip", Span: 2, Col: 2, Row: 3, Height: 9000}, {ID: "controls", Span: 3, Row: 99, Col: -5, Hidden: true}}, Narrow: []HomeWidget{{ID: "routes", Span: 3, Col: 3, Row: 2, Height: 480}}}
	l.normalize()
	if l.Version != 2 || l.Wide[0].Row != 3 || l.Wide[0].Col != 2 || l.Wide[0].Height != 540 || l.Wide[1].Col != 1 {
		t.Fatal(l)
	}
	check := func(list []HomeWidget, cols int) {
		occupied := map[[2]int]bool{}
		for _, w := range list {
			if w.Col < 1 || w.Row < 1 || w.Row > 64 || w.Height > 1200 || w.Height < 0 {
				t.Fatal(w)
			}
			span := w.Span
			if cols == 1 {
				span = 1
			}
			for x := w.Col; x < w.Col+span; x++ {
				k := [2]int{w.Row, x}
				if x > cols || occupied[k] {
					t.Fatal("overlapping or out of bounds", list)
				}
				occupied[k] = true
			}
		}
	}
	check(l.Wide, 3)
	check(l.Narrow, 1)
	if l.Wide[2].Height != 1200 || l.Narrow[0].Col != 1 {
		t.Fatal(l)
	}
	before := l
	l.normalize()
	for i := range l.Wide {
		if l.Wide[i] != before.Wide[i] {
			t.Fatal("normalization not idempotent")
		}
	}
}

func TestHomeLayoutRejectsOverflowCoordinates(t *testing.T) {
	l := HomeLayout{Version: 2, Wide: []HomeWidget{{ID: "controls", Span: 3, Col: int(^uint(0) >> 1), Row: 1}}}
	l.normalize()
	if l.Wide[0].Col != 1 || l.Wide[0].Row != 1 {
		t.Fatal("overflow moved controls outside grid", l.Wide[0])
	}
}
