package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOfflineProbeKinds(t *testing.T) {
	for _, tc := range []struct {
		server Server
		want   string
	}{
		{Server{Type: "vless", Security: "reality"}, "tcp"},
		{Server{Type: "vless"}, "tcp"}, // пустое security в старых настройках означает Reality
		{Server{Type: "vless", Security: "none"}, "tcp"},
		{Server{Type: "shadowsocks"}, "tcp"},
		{Server{Type: "vless", Security: "tls"}, "tls"},
		{Server{Type: "trojan"}, "tls"},
		{Server{Type: "naive"}, "tls"},
		{Server{Type: "anytls"}, "tls"},
		{Server{Type: "naive", QUIC: true}, "unavailable"},
		{Server{Type: "hysteria2"}, "unavailable"},
		{Server{Type: "tuic"}, "unavailable"},
	} {
		if got := offlineProbeKind(tc.server); got != tc.want {
			t.Errorf("%s/%s: %s, want %s", tc.server.Type, tc.server.Security, got, tc.want)
		}
	}
}

func probeTestServer(t *testing.T, addr string) Server {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return Server{ID: "reality", Type: "vless", Security: "reality", Host: host, Port: p, SNI: "camouflage.invalid"}
}

func TestRealityOfflineProbeSendsNoTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		n, err := conn.Read(b[:])
		if n != 0 || !errors.Is(err, io.EOF) {
			done <- errors.New("offline Reality probe sent a handshake or failed to close")
			return
		}
		done <- nil
	}()
	if _, err := offlineProbe(probeTestServer(t, ln.Addr().String())); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe left an open connection")
	}
}

func TestOfflineTLSStillRejectsUntrustedCertificate(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.ErrorLog = nil
	srv.StartTLS()
	defer srv.Close()
	sv := probeTestServer(t, srv.Listener.Addr().String())
	sv.Security = "tls"
	if _, err := offlineProbe(sv); err == nil {
		t.Fatal("ordinary TLS accepted an untrusted certificate")
	} else if humanErr(err) != "проблема с сертификатом" {
		t.Fatalf("unexpected TLS error: %v", err)
	}
}

func TestRealityRunningProbeUsesCore(t *testing.T) {
	for _, fail := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/proxies/s-reality/delay" || r.URL.Query().Get("url") != "https://test.invalid/204" {
				t.Errorf("unexpected core request: %s", r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			if fail {
				w.WriteHeader(http.StatusBadGateway)
				io.WriteString(w, `{"message":"reality verification failed"}`)
			} else {
				io.WriteString(w, `{"delay":71}`)
			}
		}))
		s := DefaultSettings()
		s.Servers = []Server{{ID: "reality", Type: "vless", Security: "reality", Host: "must-not-dial.invalid", Port: 443}}
		s.TestURL = "https://test.invalid/204"
		apiAddr := probeTestServer(t, srv.Listener.Addr().String())
		a := &App{settings: s, core: &Core{running: true, apiPort: apiAddr.Port}}
		out, err := a.apiTest(httptest.NewRequest("POST", "/api/test", strings.NewReader(`{"id":"reality"}`)))
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		results := out.([]testResult)
		if len(results) != 1 || results[0].Kind != "proxy" {
			t.Fatalf("not a full proxy test: %#v", results)
		}
		r := results[0]
		if fail && r.Error != "reality verification failed" || !fail && (r.Error != "" || r.Ms != 71) {
			t.Fatalf("core result lost or replaced by TCP reachability: %#v", r)
		}
	}
}
