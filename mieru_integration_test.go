package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMieruRealTransport(t *testing.T) {
	mita, mieru := os.Getenv("MITA_CLIENT"), os.Getenv("MIERU_CLIENT")
	if mita == "" || mieru == "" {
		t.Skip("set MITA_CLIENT and MIERU_CLIENT")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "mieru-round-trip") }))
	defer target.Close()
	_, targetPort, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	tp, _ := strconv.Atoi(targetPort)
	for _, transport := range []string{"TCP", "UDP"} {
		t.Run(transport, func(t *testing.T) {
			dir := t.TempDir()
			port, err := freePort()
			if err != nil {
				t.Fatal(err)
			}
			// Loopback access is enabled only in this isolated test server, never by
			// the production VPS installer.
			cfg := M{"portBindings": []MieruBinding{{Port: port, Protocol: transport}}, "users": []any{M{"name": "fixture", "password": "fixture-password", "allowPrivateIP": true, "allowLoopbackIP": true}}, "loggingLevel": "ERROR"}
			b, _ := json.Marshal(cfg)
			path := filepath.Join(dir, "server.json")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(mita, "run")
			cmd.Env = append(os.Environ(), "MITA_CONFIG_JSON_FILE="+path, "MITA_UDS_PATH="+filepath.Join(dir, "rpc.sock"), "MITA_INSECURE_UDS=1")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			// Poll the local management endpoint until the server is ready.
			ready := false
			for i := 0; i < 50; i++ {
				status := exec.Command(mita, "status")
				status.Env = cmd.Env
				if status.Run() == nil {
					ready = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !ready {
				t.Fatal("mita not ready")
			}
			a := &App{dir: dir, settings: DefaultSettings()}
			a.core = NewCore(dir)
			a.mon = newMonitor(a)
			x := newExternalProxy(a, "mieru", "unused")
			_ = os.MkdirAll(filepath.Dir(x.exePath()), 0700)
			b, err = os.ReadFile(mieru)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(x.exePath(), b, 0700); err != nil {
				t.Fatal(err)
			}
			s := Server{ID: "fixture", Type: "mieru", Host: "127.0.0.1", Port: port, Username: "fixture", Password: "fixture-password", Mieru: &MieruOptions{Bindings: []MieruBinding{{Port: port, Protocol: transport}}}}
			ports := x.Sync(map[string]Server{"fixture": s})
			defer x.Stop()
			if ports["fixture"] == 0 {
				t.Fatal("mieru not ready")
			}
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["fixture"])), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			_, _ = conn.Write([]byte{5, 1, 0})
			reply := make([]byte, 2)
			if _, err = io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
				t.Fatalf("SOCKS auth %v %v", reply, err)
			}
			_, _ = conn.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(tp >> 8), byte(tp)})
			header := make([]byte, 4)
			if _, err = io.ReadFull(conn, header); err != nil || header[1] != 0 {
				t.Fatalf("SOCKS connect %v %v", header, err)
			}
			n := 6
			if header[3] == 4 {
				n = 18
			}
			if header[3] != 1 && header[3] != 4 {
				t.Fatal("unexpected bound address")
			}
			if _, err = io.CopyN(io.Discard, conn, int64(n)); err != nil {
				t.Fatal(err)
			}
			_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n")
			response, err := io.ReadAll(conn)
			if err != nil || !strings.Contains(string(response), "mieru-round-trip") {
				t.Fatalf("tunnel response %q: %v", response, err)
			}
		})
	}
}
