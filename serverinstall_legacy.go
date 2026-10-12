//go:build legacy

package main

import (
	"errors"
	"net/http"
)

// В сборке для Windows 7/8 (Go 1.20) установки сервера по SSH нет: библиотеке SSH нужен Go новее.
const serverInstallSupported = false

type ServerInstallView struct{}

type serverInstaller struct{}

func (a *App) serverInstallRoutes(api func(method, path string, f apiFunc)) {
	err := errors.New("Установка сервера есть только в Naivka для Windows 10 и 11")
	for _, p := range []string{"/api/server/hostkey", "/api/server/install"} {
		api("POST", p, func(*http.Request) (any, error) { return nil, err })
	}
}
