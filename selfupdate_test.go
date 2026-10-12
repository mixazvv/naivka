package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.0.1", "v1.0.0", true}, {"v1.10.0", "v1.9.9", true}, {"v2.0.0", "v1.99.99", true},
		{"v1.0.0", "v1.0.0", false}, {"v1.0.0", "v1.0.1", false}, {"v1.0.0", "dev", true}} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// Подпись: верная принимается, подменённые суммы и чужой ключ — нет.
func TestVerifySums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := updateKey
	defer func() { updateKey = old }()
	updateKey = base64.StdEncoding.EncodeToString(pub)
	sums := []byte("abc123  Naivka.exe\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)))
	if err := verifySums(sums, sig); err != nil {
		t.Fatalf("верная подпись отклонена: %v", err)
	}
	if verifySums([]byte("evil000  Naivka.exe\n"), sig) == nil {
		t.Error("подменённые суммы приняты")
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if verifySums(sums, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(other, sums)))) == nil {
		t.Error("подпись чужим ключом принята")
	}
	if h, err := sumFor(sums, "Naivka.exe"); err != nil || h != "abc123" {
		t.Errorf("sumFor: %q %v", h, err)
	}
	if _, err := sumFor(sums, "Naivka-win7-64.exe"); err == nil {
		t.Error("ожидалась ошибка для отсутствующего файла")
	}
}
