//go:build !legacy

package main

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSSHDiagnosticPreservesReadFailure(t *testing.T) {
	msg := "ssh: handshake failed: read tcp 192.0.2.10:12345->198.51.100.20:22: wsarecv: An existing connection was forcibly closed by the remote host."
	got := sshDiagnostic(errors.New(msg))
	if !strings.Contains(got, "wsarecv:") || !strings.HasSuffix(got, "closed by the remote host.") {
		t.Fatal("SSH reason was lost")
	}
	for _, msg := range []string{"ssh: handshake failed: i/o timeout", "ssh: handshake failed: EOF", "connection refused"} {
		if !strings.Contains(sshDiagnostic(errors.New(msg)), msg) {
			t.Fatal("detail missing")
		}
	}
}

func TestSSHDiagnosticBoundsAndRedacts(t *testing.T) {
	got := sshDiagnostic(errors.New("secret-pass "+strings.Repeat("я", 4000)+" wsarecv: конечная причина"), "secret-pass")
	if strings.Contains(got, "secret-pass") || !strings.Contains(got, "[скрыто]") || !strings.HasSuffix(got, "конечная причина") || !utf8.ValidString(got) || len([]rune(got)) > 2200 {
		t.Fatal("invalid bounded or redacted diagnostic")
	}
	if sshDiagnostic(nil) != "" {
		t.Fatal("nil error")
	}
}
