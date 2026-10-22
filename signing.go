package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
)

// Подпись обновлений. Сборка на GitHub подписывает SHA256SUMS.txt закрытым ключом из секрета
// NAIVKA_SIGN_KEY (Ed25519, файл SHA256SUMS.txt.sig — подпись в base64). Открытый ключ ниже
// встроен в программу: даже с доступом к репозиторию подделку без закрытого ключа не подписать,
// и Naivka её не поставит.
var updatePubKey = "UWMMxmeYSVDmzyhWX3CGwAx2hWTDdOWdMXVMcZeF3Mw="

func verifySums(sums, sig []byte) error {
	key, err := base64.StdEncoding.DecodeString(updatePubKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("в программе повреждён ключ проверки обновлений")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(key), sums, raw) {
		return errors.New("подпись обновления не сошлась — его выпустил не автор Naivka, ничего не заменено")
	}
	return nil
}
