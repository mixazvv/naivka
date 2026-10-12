package main

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// Эталонные значения PBKDF2-HMAC-SHA256 (RFC 7914, раздел 11, и общеизвестные векторы).
func TestPBKDF2Vectors(t *testing.T) {
	for _, c := range []struct {
		pass, salt string
		iter       int
		want       string
	}{
		{"password", "salt", 1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{"password", "salt", 4096, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
	} {
		if got := hex.EncodeToString(pbkdf2SHA256([]byte(c.pass), []byte(c.salt), c.iter, 32)); got != c.want {
			t.Errorf("iter=%d: %s", c.iter, got)
		}
	}
	// Ключ длиннее одного блока SHA-256 (RFC 7914: P="passwd", S="salt", c=1, dkLen=64).
	got := hex.EncodeToString(pbkdf2SHA256([]byte("passwd"), []byte("salt"), 1, 64))
	if !strings.HasPrefix(got, "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc") {
		t.Errorf("dkLen=64: %s", got)
	}
}

func TestEncryptedBackup(t *testing.T) {
	s := DefaultSettings()
	s.Servers = []Server{{ID: "a", Type: "naive", Host: "nl.example.com", Port: 443, Password: "s3cret-pass"}}
	if _, err := makeBackup(s, "short"); err == nil {
		t.Error("короткий пароль принят")
	}
	b, err := makeBackup(s, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "s3cret-pass") || strings.Contains(string(raw), "nl.example.com") {
		t.Fatal("в зашифрованной копии виден открытый текст")
	}
	if _, err := openBackup(raw, ""); err != errBackupPassword {
		t.Errorf("без пароля: %v", err)
	}
	if _, err := openBackup(raw, "wrong password"); err != errBackupPassword {
		t.Errorf("неверный пароль: %v", err)
	}
	plain, err := openBackup(raw, "correct horse")
	if err != nil || !strings.Contains(string(plain), "s3cret-pass") || !strings.Contains(string(plain), `"format":"naivka-backup"`) {
		t.Fatalf("расшифровка: %v %s", err, plain)
	}
	// Подмена заголовка (меньше итераций) ломает проверку подлинности.
	tampered := strings.Replace(string(raw), `"iter":600000`, `"iter":100000`, 1)
	if _, err := openBackup([]byte(tampered), "correct horse"); err == nil {
		t.Error("подменённый заголовок принят")
	}
	// Копия без пароля — как раньше, открытый текст.
	p, _ := makeBackup(s, "")
	praw, _ := json.Marshal(p)
	if out, err := openBackup(praw, ""); err != nil || string(out) != string(praw) {
		t.Errorf("копия без пароля: %v", err)
	}
}
