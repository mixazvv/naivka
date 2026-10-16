package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v2.3.4", "v2.3.3", true},
		{"v2.4.0", "v2.3.9", true},
		{"v2.10.0", "v2.9.0", true},
		{"v2.3.3", "v2.3.3", false},
		{"v2.3.2", "v2.3.3", false},
		{"v2.3.3.1", "v2.3.3", true},
		{"v2.3.4", "dev", false},
		{"", "v2.3.3", false},
		{"v2.x", "v2.3.3", false},
	}
	for _, c := range cases {
		if got := versionNewer(c.latest, c.current); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

func TestSumFor(t *testing.T) {
	h1, h2 := strings.Repeat("a", 64), strings.Repeat("B", 64)
	sums := h1 + "  Naivka-v2.3.4-windows.zip\n" + h2 + " *Naivka-v2.3.4-windows7.zip\n"
	if got := sumFor(strings.NewReader(sums), "Naivka-v2.3.4-windows.zip"); got != h1 {
		t.Errorf("windows: %q", got)
	}
	if got := sumFor(strings.NewReader(sums), "Naivka-v2.3.4-windows7.zip"); got != strings.ToLower(h2) {
		t.Errorf("windows7: %q", got)
	}
	if got := sumFor(strings.NewReader(sums), "other.zip"); got != "" {
		t.Errorf("missing: %q", got)
	}
}

func TestFindExe(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Naivka.exe", "MZ main")
	write("Naivka-debug.exe", "MZ debug")
	write("portable.txt", "text")
	if got := filepath.Base(findExe(dir, "Naivka-debug.exe")); got != "Naivka-debug.exe" {
		t.Errorf("same name: %s", got)
	}
	if got := filepath.Base(findExe(dir, "naivka-renamed.exe")); got != "Naivka.exe" {
		t.Errorf("fallback: %s", got)
	}
	write("Naivka.exe", "not a program")
	if got := findExe(dir, "naivka-renamed.exe"); got != "" {
		t.Errorf("non-exe accepted: %s", got)
	}
	if releaseAsset("v2.3.4") != "Naivka-v2.3.4-windows.zip" {
		t.Error(releaseAsset("v2.3.4"))
	}
}

// Скачивание обновления: архив сверяется с SHA256SUMS.txt, подменённый не принимается.
func TestSelfUpdateFetch(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, body := range map[string]string{"Naivka/Naivka.exe": "MZ new", "Naivka/portable.txt": "x"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	archive := zbuf.Bytes()
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  Naivka-v9.9.9-windows.zip\n"
	tamper := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/download/v9.9.9/SHA256SUMS.txt"):
			io.WriteString(w, sums)
		case strings.HasSuffix(r.URL.Path, "/releases/download/v9.9.9/Naivka-v9.9.9-windows.zip"):
			if tamper {
				w.Write(append([]byte{}, archive[:len(archive)-1]...))
				return
			}
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldBase, oldAPI := releaseBase, githubAPI
	releaseBase, githubAPI = srv.URL+"/", srv.URL+"/"
	defer func() { releaseBase, githubAPI = oldBase, oldAPI }()

	u := &selfUpdater{}
	fresh, err := u.fetch(filepath.Join(t.TempDir(), "update"), "v9.9.9", "Naivka.exe")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); string(b) != "MZ new" {
		t.Fatalf("fresh exe: %q", b)
	}
	tamper = true
	if _, err := u.fetch(filepath.Join(t.TempDir(), "update"), "v9.9.9", "Naivka.exe"); err == nil || !strings.Contains(err.Error(), "контрольной суммой") {
		t.Fatalf("tampered archive accepted: %v", err)
	}
}
