package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Значок Naivka (буква N, линия которой кончается узлом) рисуется программой, а не берётся из .ico:
// так значок в трее и на панели задач перекрашивается вместе с интерфейсом. Геометрия та же,
// что у логотипа в web/index.html (поле 32×32); на мелких размерах штрих толще, чтобы не расплывался.

// logoColors — цвет плашки и буквы, #RRGGBB.
type logoColors struct {
	Bg  string `json:"bg"`
	Ink string `json:"ink"`
}

// Значок «отключено» всегда серый: по нему видно, что соединения нет.
var logoOff = logoColors{Bg: "#5E646B", Ink: "#ECEEEA"}

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func parseHexColor(s string) (color.NRGBA, bool) {
	s = strings.TrimSpace(s)
	if !hexColorRe.MatchString(s) {
		return color.NRGBA{}, false
	}
	v, _ := strconv.ParseUint(s[1:], 16, 32)
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}

func (c logoColors) valid() bool {
	_, a := parseHexColor(c.Bg)
	_, b := parseHexColor(c.Ink)
	return a && b
}

// defaultLogoColors — цвета значка по настройкам, пока страница не сообщила точные
// (стили «Никогда не проси об этом» и «Пиксели» со своими цветами, иначе — акцент).
func defaultLogoColors(s *Settings) logoColors {
	switch s.Theme {
	case "gold":
		return logoColors{Bg: "#A8731F", Ink: "#FFF4DD"}
	case "blocks":
		return logoColors{Bg: "#2F9E63", Ink: "#FFFFFF"}
	}
	bg := normalizeAccent(s.Accent)
	return logoColors{Bg: bg, Ink: inkFor(bg)}
}

// inkFor — цвет буквы на плашке: почти чёрный с оттенком плашки или белый, что контрастнее.
func inkFor(bg string) string {
	c, _ := parseHexColor(bg)
	dark := color.NRGBA{R: c.R / 10, G: c.G / 10, B: c.B / 10, A: 255}
	if contrastRatio(c, dark) >= contrastRatio(c, color.NRGBA{R: 255, G: 255, B: 255, A: 255}) {
		return fmt.Sprintf("#%02X%02X%02X", dark.R, dark.G, dark.B)
	}
	return "#FFFFFF"
}

func relLum(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrastRatio(a, b color.NRGBA) float64 {
	x, y := relLum(a), relLum(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}

// renderLogo рисует значок n×n со сглаживанием (4×4 замера на пиксель).
func renderLogo(n int, c logoColors) *image.NRGBA {
	bg, _ := parseHexColor(c.Bg)
	ink, _ := parseHexColor(c.Ink)
	small := n <= 24
	radius, sw := 8.0, 2.6
	pts := [][2]float64{{9, 23.5}, {9, 9}, {23, 23.5}, {23, 12.5}}
	dot, dotR := [2]float64{23, 9.3}, 3.0
	if n <= 40 {
		sw = 3
	}
	if small {
		radius, sw = 6, 3.4
		pts = [][2]float64{{8, 24.5}, {8, 8.5}, {23.5, 24}, {23.5, 12}}
		dot, dotR = [2]float64{23.5, 9}, 3.3
	}
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const ss = 4
	scale := 32 / float64(n)
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			var cover, inked float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					if !inRoundRect(x, y, 32, radius) {
						continue
					}
					cover++
					if math.Hypot(x-dot[0], y-dot[1]) <= dotR || distPolyline(x, y, pts) <= sw/2 {
						inked++
					}
				}
			}
			if cover == 0 {
				continue
			}
			k := inked / cover
			mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-k) + float64(b)*k)) }
			img.SetNRGBA(px, py, color.NRGBA{R: mix(bg.R, ink.R), G: mix(bg.G, ink.G), B: mix(bg.B, ink.B), A: uint8(math.Round(255 * cover / ss / ss))})
		}
	}
	return img
}

// logoPNG — значок в PNG: такой Windows принимает как картинку значка.
func logoPNG(n int, c logoColors) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, renderLogo(n, c))
	return b.Bytes()
}

func inRoundRect(x, y, size, r float64) bool {
	if x < 0 || y < 0 || x > size || y > size {
		return false
	}
	cx := math.Max(r, math.Min(size-r, x))
	cy := math.Max(r, math.Min(size-r, y))
	return math.Hypot(x-cx, y-cy) <= r
}

// distPolyline — расстояние от точки до ломаной: так получаются скруглённые концы и изломы штриха.
func distPolyline(x, y float64, pts [][2]float64) float64 {
	best := math.Inf(1)
	for i := 0; i+1 < len(pts); i++ {
		ax, ay, bx, by := pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1]
		dx, dy := bx-ax, by-ay
		t := ((x-ax)*dx + (y-ay)*dy) / (dx*dx + dy*dy)
		t = math.Max(0, math.Min(1, t))
		best = math.Min(best, math.Hypot(x-(ax+t*dx), y-(ay+t*dy)))
	}
	return best
}
