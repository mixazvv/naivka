package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Резервная копия с паролем: ключ из пароля — PBKDF2-SHA256, шифр — AES-256-GCM.
// Всё на стандартной библиотеке, чтобы собиралось и в сборке для Windows 7 (Go 1.20).

const (
	backupFormat    = "naivka-backup"
	backupEncFormat = "naivka-backup-encrypted"
	backupIter      = 600_000 // рекомендация OWASP для PBKDF2-SHA256
	backupMinPass   = 8
)

// errBackupPassword — копия зашифрована, а пароль не задан или не подошёл.
// Интерфейс по этому тексту понимает, что надо спросить пароль.
var errBackupPassword = errors.New("needPassword")

type encryptedBackup struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Created string `json:"created"`
	KDF     string `json:"kdf"`
	Iter    int    `json:"iter"`
	Salt    string `json:"salt"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

// pbkdf2SHA256 — PBKDF2 (RFC 8018) с HMAC-SHA256.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	var out []byte
	for block := uint32(1); len(out) < keyLen; block++ {
		prf.Reset()
		prf.Write(salt)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], block)
		prf.Write(b[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func backupCipher(password string, salt []byte, iter int) (cipher.AEAD, error) {
	block, err := aes.NewCipher(pbkdf2SHA256([]byte(password), salt, iter, 32))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// makeBackup — содержимое файла копии. Пустой пароль — открытый текст, как раньше.
func makeBackup(s *Settings, password string) (any, error) {
	created := time.Now().Format(time.RFC3339)
	plain := map[string]any{"format": backupFormat, "version": 1, "created": created, "settings": s}
	if password == "" {
		return plain, nil
	}
	if len([]rune(password)) < backupMinPass {
		return nil, fmt.Errorf("пароль копии должен быть не короче %d символов", backupMinPass)
	}
	raw, err := json.Marshal(plain)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := backupCipher(password, salt, backupIter)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	enc := encryptedBackup{Format: backupEncFormat, Version: 1, Created: created, KDF: "pbkdf2-sha256", Iter: backupIter,
		Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce)}
	// Заголовок без данных — дополнительные данные AEAD: подменить число итераций или формат незаметно нельзя.
	ad, _ := json.Marshal(enc)
	enc.Data = base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, raw, ad))
	return enc, nil
}

// openBackup: зашифрованную копию расшифровывает, остальное возвращает как есть.
func openBackup(raw []byte, password string) ([]byte, error) {
	var enc encryptedBackup
	if json.Unmarshal(raw, &enc) != nil || enc.Format != backupEncFormat {
		return raw, nil
	}
	if password == "" {
		return nil, errBackupPassword
	}
	if enc.KDF != "pbkdf2-sha256" || enc.Iter < 100_000 || enc.Iter > 10_000_000 {
		return nil, errors.New("копия зашифрована неизвестным способом — возможно, она из более новой версии MZ")
	}
	salt, err1 := base64.StdEncoding.DecodeString(enc.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(enc.Nonce)
	data, err3 := base64.StdEncoding.DecodeString(enc.Data)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("файл копии повреждён")
	}
	aead, err := backupCipher(password, salt, enc.Iter)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("файл копии повреждён")
	}
	hdr := enc
	hdr.Data = ""
	ad, _ := json.Marshal(hdr)
	plain, err := aead.Open(nil, nonce, data, ad)
	if err != nil {
		return nil, errBackupPassword
	}
	return plain, nil
}
