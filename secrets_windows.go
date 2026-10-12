//go:build windows

package main

import (
	"encoding/base64"
	"errors"
	"syscall"
	"unsafe"
)

// Пароли и ключи серверов в settings.json шифруются средствами Windows (DPAPI):
// расшифровать их может только ваша учётная запись на этом компьютере.

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	n    uint32
	data *byte
}

func blobOf(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{n: uint32(len(b)), data: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	out := make([]byte, b.n)
	copy(out, unsafe.Slice(b.data, b.n))
	return out
}

var entropy = []byte("Naivka settings v1")

func protectSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	var out dataBlob
	r, _, err := procCryptProtectData.Call(uintptr(unsafe.Pointer(blobOf([]byte(plain)))), 0,
		uintptr(unsafe.Pointer(blobOf(entropy))), 0, 0, 1 /* CRYPTPROTECT_UI_FORBIDDEN */, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return "", err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return secretPrefix + base64.StdEncoding.EncodeToString(out.bytes()), nil
}

func unprotectSecret(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(s[len(secretPrefix):])
	if err != nil {
		return "", err
	}
	var out dataBlob
	r, _, _ := procCryptUnprotectData.Call(uintptr(unsafe.Pointer(blobOf(raw))), 0,
		uintptr(unsafe.Pointer(blobOf(entropy))), 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return "", errors.New("не удалось расшифровать (настройки с другого компьютера или пользователя)")
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return string(out.bytes()), nil
}
