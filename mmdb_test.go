package main

import (
	"net"
	"os"
	"testing"
)

// Настоящая база (если скачана): NAIVKA_MMDB=путь/server-country.mmdb go test -run MMDB
func TestMMDBReal(t *testing.T) {
	p := os.Getenv("NAIVKA_MMDB")
	if p == "" {
		t.Skip("NAIVKA_MMDB не задан")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openMMDB(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "77.88.8.8", "95.163.0.1", "2a00:1450:4001::1", "127.0.0.1"} {
		v, err := db.lookup(net.ParseIP(ip))
		t.Logf("%s → %v %v", ip, v, err)
	}
}

// Мусор и обрезанный файл не роняют Naivka.
func TestMMDBGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("hello"), append([]byte("xx"), mmdbMarker...), append(make([]byte, 64), append(mmdbMarker, 0xE3, 0xFF)...)} {
		if _, err := openMMDB(b); err == nil {
			t.Errorf("принято: %q", b)
		}
	}
	p := os.Getenv("NAIVKA_MMDB")
	if p == "" {
		return
	}
	b, _ := os.ReadFile(p)
	for _, n := range []int{100, len(b) / 2, len(b) - 10} {
		cut := append([]byte(nil), b[:n]...)
		if db, err := openMMDB(cut); err == nil {
			func() {
				defer func() { _ = recover() }()
				_, _ = db.lookup(net.ParseIP("8.8.8.8"))
			}()
		}
	}
}

func TestIPCountryLookup(t *testing.T) {
	p := os.Getenv("NAIVKA_MMDB")
	if p == "" {
		t.Skip("NAIVKA_MMDB не задан")
	}
	dir := t.TempDir()
	b, _ := os.ReadFile(p)
	g := newIPCountry(dir)
	_ = os.MkdirAll(dir+"/geo", 0o700)
	_ = os.WriteFile(g.path, b, 0o600)
	if cc := g.CountryFor(nil, "", "8.8.8.8"); cc != "US" {
		t.Errorf("8.8.8.8 → %q", cc)
	}
	if cc := g.CountryFor(nil, "", "192.168.1.1"); cc != "" {
		t.Errorf("частный адрес → %q", cc)
	}
	if !g.Ready() {
		t.Error("база не открылась")
	}
	var nilG *ipCountry
	if nilG.CountryFor(nil, "x", "8.8.8.8") != "" || nilG.Ready() {
		t.Error("nil")
	}
}
