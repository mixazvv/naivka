package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSums(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Naivka.exe"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("old"), 0o644)
	b, err := buildSums(dir, "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "# naivka v9.9.9\n") || !strings.Contains(s, "  Naivka.exe\n") || strings.Contains(s, "SHA256SUMS") {
		t.Fatal(s)
	}
	_, k, _ := ed25519.GenerateKey(nil)
	if !ed25519.Verify(k.Public().(ed25519.PublicKey), b, ed25519.Sign(k, b)) {
		t.Fatal("подпись")
	}
}
