package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Разбор настоящих bat-файлов Flowseal (копии в testdata/zapret, лицензия MIT).
func TestParseStrategy(t *testing.T) {
	bin, lists := `C:\Program Files\Naivka\zapret\1.10.3\bin`, `C:\Program Files\Naivka\zapret\1.10.3\lists`
	for _, name := range []string{"general", "general (ALT)"} {
		args, err := parseStrategy(filepath.Join("testdata", "zapret", name+".bat"), bin, lists, "12", "12")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		joined := strings.Join(args, " ")
		if len(args) < 40 || !strings.HasPrefix(args[0], "--wf-tcp=80,443") {
			t.Fatalf("%s: странные аргументы: %v", name, args[:3])
		}
		if strings.Contains(joined, "%") || strings.Contains(joined, `"`) || strings.Contains(joined, "^") {
			t.Errorf("%s: остались переменные, кавычки или ^: %s", name, joined[:200])
		}
		want := "--hostlist=" + lists + string(filepath.Separator) + "list-general.txt"
		if !strings.Contains(joined, want) {
			t.Errorf("%s: нет пути к списку %s", name, want)
		}
		if strings.Count(joined, "--new") < 5 {
			t.Errorf("%s: мало блоков --new", name)
		}
		// путь с пробелом остаётся одним аргументом
		for _, a := range args {
			if strings.HasPrefix(a, "Files") {
				t.Errorf("%s: путь с пробелом разрезан: %q", name, a)
			}
		}
	}
	tcp, udp := gameRanges("tcp")
	if tcp != "1024-65535" || udp != "12" {
		t.Errorf("gameRanges(tcp) = %s %s", tcp, udp)
	}
}

func TestZapretDomainsAndLists(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "addons", "zapret", "1.10.3")
	_ = os.MkdirAll(filepath.Join(root, "lists"), 0o755)
	for _, f := range []string{"list-general.txt", "list-google.txt"} {
		b, _ := os.ReadFile(filepath.Join("testdata", "zapret", "lists", f))
		_ = os.WriteFile(filepath.Join(root, "lists", f), b, 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "addons", "zapret", "version.txt"), []byte("1.10.3"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "lists", "ipset-all.txt.backup"), []byte("1.2.3.0/24\n"), 0o644)
	a := &App{dir: dir, settings: DefaultSettings()}
	z := newZapret(a)
	s := DefaultSettings()
	s.Rules = []Rule{{"domain", "example.org", "zapret"}, {"domain", "other.org", "proxy"}}
	s.Servers = []Server{{ID: "x", Type: "naive", Host: "203.0.113.5", Port: 443}}
	d := z.Domains(s)
	if len(d) < 20 || !contains(strings.Join(d, ","), "youtube.com") || !contains(strings.Join(d, ","), "discord.com") {
		t.Fatalf("домены zapret: %d %v", len(d), d[:5])
	}
	for _, mode := range []string{"none", "any", "loaded"} { // "any" из старых настроек — как "none"
		s.Zapret.IPSet = mode
		if err := z.writeLists(s); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		b, _ := os.ReadFile(filepath.Join(root, "lists", "ipset-all.txt"))
		got := strings.TrimSpace(string(b))
		want := map[string]string{"none": "203.0.113.113/32", "any": "203.0.113.113/32", "loaded": "1.2.3.0/24"}[mode]
		if got != want {
			t.Errorf("ipset %s: %q", mode, got)
		}
	}
	gen, _ := os.ReadFile(filepath.Join(root, "lists", "list-general-user.txt"))
	ex, _ := os.ReadFile(filepath.Join(root, "lists", "ipset-exclude-user.txt"))
	if !strings.Contains(string(gen), "example.org") || strings.Contains(string(gen), "other.org") {
		t.Errorf("list-general-user: %s", gen)
	}
	if !strings.Contains(string(ex), "203.0.113.5") {
		t.Errorf("адрес сервера не исключён из zapret: %s", ex)
	}
}
