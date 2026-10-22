package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
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

func TestExeCandidates(t *testing.T) {
	if got := exeCandidates("Naivka-debug.exe"); got[0] != "Naivka-debug.exe" || got[len(got)-1] != "Naivka.exe" {
		t.Error(got)
	}
	if releaseAsset("v2.3.4") != "Naivka-v2.3.4-windows.zip" {
		t.Error(releaseAsset("v2.3.4"))
	}
}

// Скачивание обновления: архив сверяется с SHA256SUMS.txt, подменённый не принимается.
func TestSelfUpdateFetch(t *testing.T) {
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, body := range map[string]string{"Naivka/Naivka.exe": "MZ new", "Naivka/Naivka-debug.exe": "MZ dbg", "Naivka/portable.txt": "x"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	archive := zbuf.Bytes()
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  Naivka-v9.9.9-windows.zip\n"
	// Свой тестовый ключ вместо ключа релизов.
	pub, priv, _ := ed25519.GenerateKey(nil)
	oldKey := updatePubKey
	updatePubKey = base64.StdEncoding.EncodeToString(pub)
	defer func() { updatePubKey = oldKey }()
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums)))
	tamper := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/download/v9.9.9/SHA256SUMS.txt"):
			io.WriteString(w, sums)
		case strings.HasSuffix(r.URL.Path, "/releases/download/v9.9.9/SHA256SUMS.txt.sig"):
			io.WriteString(w, sig+"\n")
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
	exe, dbg, err := u.fetch("v9.9.9", "Naivka.exe")
	if err != nil {
		t.Fatal(err)
	}
	if string(exe) != "MZ new" || string(dbg) != "MZ dbg" {
		t.Fatalf("exe %q, debug %q", exe, dbg)
	}
	// Подменённый exe ставится на место старого, старый остаётся как .old.
	dir := t.TempDir()
	target := filepath.Join(dir, "Naivka.exe")
	os.WriteFile(target, []byte("MZ old"), 0o755)
	if err := writeExe(target, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "MZ new" {
		t.Fatalf("target: %q", b)
	}
	if b, _ := os.ReadFile(target + ".old"); string(b) != "MZ old" {
		t.Fatalf("old: %q", b)
	}
	tamper = true
	if _, _, err := u.fetch("v9.9.9", "Naivka.exe"); err == nil || !strings.Contains(err.Error(), "контрольной суммой") {
		t.Fatalf("tampered archive accepted: %v", err)
	}
	// Подменены и архив, и суммы, но подписать их нечем — обновление не принимается.
	tamper = false
	sum2 := sha256.Sum256([]byte("evil"))
	sums = hex.EncodeToString(sum2[:]) + "  Naivka-v9.9.9-windows.zip\n"
	if _, _, err := u.fetch("v9.9.9", "Naivka.exe"); err == nil || !strings.Contains(err.Error(), "подпись") {
		t.Fatalf("unsigned sums accepted: %v", err)
	}
	sig = ""
	if _, _, err := u.fetch("v9.9.9", "Naivka.exe"); err == nil {
		t.Fatal("update without signature accepted")
	}
}
