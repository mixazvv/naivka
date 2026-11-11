//go:build !legacy

package main

import "strings"

// Keep the OS reason at the end of a wrapped SSH error. humanErr's short
// summary can otherwise discard everything after "wsarecv:".
func sshDiagnostic(err error, secrets ...string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[скрыто]")
		}
	}
	r := []rune(s)
	if len(r) > 2048 {
		s = string(r[:512]) + " … " + string(r[len(r)-1536:])
	}
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "forcibly closed"), strings.Contains(lower, "connection reset"):
		return "Соединение сброшено сервером или сетевым фильтром. Подробности: " + s
	case strings.Contains(lower, "aborted"):
		return "Соединение прервано. Подробности: " + s
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"):
		return "Истекло время ожидания SSH. Подробности: " + s
	}
	return s
}
