package main

import "testing"

func TestExperienceDoesNotRestartCore(t *testing.T) {
	s := DefaultSettings()
	original := s.fingerprint()
	s.Experience = ExperienceSettings{Effects: "off", Sound: "retro", Volume: 87, Atmosphere: "night"}
	if s.fingerprint() != original {
		t.Fatal("presentation settings restart core")
	}
	dir := t.TempDir()
	if err := SaveSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Experience != s.Experience {
		t.Fatal("experience settings not preserved", got.Experience)
	}
}
func TestExperienceNormalization(t *testing.T) {
	s := DefaultSettings()
	s.Experience = ExperienceSettings{Effects: "bad", Sound: "bad", Atmosphere: "bad", Volume: 200}
	s.normalize()
	if s.Experience.Effects != "balanced" || s.Experience.Sound != "off" || s.Experience.Atmosphere != "off" || s.Experience.Volume != 100 {
		t.Fatal(s.Experience)
	}
	if DefaultSettings().Experience.Sound != "off" {
		t.Fatal("sound enabled by default")
	}
}
