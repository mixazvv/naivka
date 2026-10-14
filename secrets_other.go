//go:build !windows

package main

import "errors"

func protectSecret(plain string) (string, error) { return plain, nil }
func unprotectSecret(string) (string, error) {
	return "", errors.New("шифрование доступно только в Windows")
}
