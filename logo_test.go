package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderLogo(t *testing.T) {
	c := logoColors{Bg: "#B6E35A", Ink: "#11170A"}
	for _, n := range []int{16, 20, 24, 32, 48, 256} {
		img := renderLogo(n, c)
		if a := img.NRGBAAt(0, 0).A; a > 40 {
			t.Fatalf("%d: corner should be transparent, alpha %d", n, a)
		}
		// Низ посередине — плашка, середина левой линии N — буква.
		bg := img.NRGBAAt(n/2, n*28/32)
		if bg.R != 0xB6 || bg.G != 0xE3 || bg.A != 255 {
			t.Fatalf("%d: background pixel %+v", n, bg)
		}
		x := 8
		if n > 24 {
			x = 9
		}
		ink := img.NRGBAAt(x*n/32, n/2)
		if ink.R > 0x40 || ink.A != 255 {
			t.Fatalf("%d: stroke pixel %+v", n, ink)
		}
		if dir := os.Getenv("LOGO_DUMP"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("logo-%03d.png", n)), logoPNG(n, c), 0o644)
		}
	}
	if _, err := png.Decode(bytes.NewReader(logoPNG(32, logoOff))); err != nil {
		t.Fatal(err)
	}
}

func TestLogoColors(t *testing.T) {
	if got := inkFor("#B6E35A"); got != "#121609" {
		t.Fatalf("ink on lime = %s", got)
	}
	if got := inkFor("#203080"); got != "#FFFFFF" {
		t.Fatalf("ink on navy = %s", got)
	}
	s := DefaultSettings()
	if c := defaultLogoColors(s); c.Bg != "#B6E35A" || !c.valid() {
		t.Fatalf("default %+v", c)
	}
	s.Theme = "gold"
	if c := defaultLogoColors(s); c.Bg != "#A8731F" {
		t.Fatalf("gold %+v", c)
	}
	s.Theme, s.Accent = "dark", "#9B5CFF"
	if c := defaultLogoColors(s); c.Bg != "#9B5CFF" || c.Ink == "" {
		t.Fatalf("custom %+v", c)
	}
	if (logoColors{Bg: "red", Ink: "#000000"}).valid() {
		t.Fatal("bad colour accepted")
	}
}
