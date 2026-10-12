package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	got := splitCommand(`"C:\Program Files (x86)\Windows Kits\signtool.exe" sign /sha1 AB12 /tr http://ts {file}`)
	want := []string{`C:\Program Files (x86)\Windows Kits\signtool.exe`, "sign", "/sha1", "AB12", "/tr", "http://ts", "{file}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%q", got)
	}
}

func TestWingetFiles(t *testing.T) {
	dir := t.TempDir()
	zip := filepath.Join(dir, "Naivka-winget.zip")
	if err := os.WriteFile(zip, []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	wingetFiles("Ivan/naivka", "v1.2.3", zip, filepath.Join(dir, "w"))
	base := filepath.Join(dir, "w", "manifests", "i", "Ivan", "Naivka", "1.2.3")
	inst, err := os.ReadFile(filepath.Join(base, "Ivan.Naivka.installer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(inst)
	for _, want := range []string{"PackageIdentifier: Ivan.Naivka", "PackageVersion: 1.2.3",
		"InstallerUrl: https://github.com/Ivan/naivka/releases/download/v1.2.3/Naivka-winget.zip",
		"InstallerSha256: 4A70FE9AA6436E02C2DEA340FBD1E352E4EF2D8CE6CA52AD25D4B95471FC8BF2", `RelativeFilePath: Naivka\Naivka.exe`} {
		if !strings.Contains(s, want) {
			t.Errorf("нет %q в\n%s", want, s)
		}
	}
	for _, f := range []string{"Ivan.Naivka.yaml", "Ivan.Naivka.locale.ru-RU.yaml"} {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Error(err)
		}
	}
}
