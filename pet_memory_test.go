package main

import (
	"testing"
	"time"
)

func TestPetMemoryBoundsAndPrivacy(t *testing.T) {
	m := PetMemory{Version: 1, Created: time.Now().Unix() + 3600, LastEvent: -1, Visits: map[string]int{"tileSpeed": 2000, "https://private.example": 9, "homeServer": -1}, Events: map[string]int64{"toy": 1, "unknown": 1, "night-star": time.Now().Unix() + 10}}
	m.normalize()
	if m.Created != 0 || m.LastEvent != 0 || len(m.Visits) != 1 || m.Visits["tileSpeed"] != 1000 || len(m.Events) != 1 {
		t.Fatal(m)
	}
	m.Version = 99
	m.normalize()
	if m.Version != 0 || len(m.Visits) > 0 {
		t.Fatal("unknown version not reset")
	}
}
func TestPetMemoryDoesNotRestartCore(t *testing.T) {
	s := DefaultSettings()
	fp := s.fingerprint()
	s.PetMemory = PetMemory{Version: 1, Created: 1, Visits: map[string]int{"tileSpeed": 3}}
	if s.fingerprint() != fp {
		t.Fatal("habits change network config")
	}
	dir := t.TempDir()
	if err := SaveSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.PetMemory.Visits["tileSpeed"] != 3 {
		t.Fatal("habits lost on restart")
	}
}
