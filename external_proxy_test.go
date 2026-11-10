package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const mieruTestLink = "mierus://alice:p%40ss@[2001:db8::1]?profile=example&port=10000-10005&protocol=TCP&port=20000&protocol=UDP&mtu=1400&multiplexing=MULTIPLEXING_HIGH&handshake-mode=HANDSHAKE_STANDARD"

func TestMieruImportRoundTrip(t *testing.T) {
	nodes, errs := ParseShareLinks(mieruTestLink)
	if len(errs) != 0 || len(nodes) != 1 {
		t.Fatalf("%v %v", nodes, errs)
	}
	s := nodes[0]
	if s.Host != "2001:db8::1" || s.Password != "p@ss" || s.Port != 10000 || len(s.Mieru.Bindings) != 2 {
		t.Fatal("lost connection settings")
	}
	again, errs := ParseShareLinks(ShareLink(s))
	if len(errs) != 0 || len(again) != 1 {
		t.Fatal(errs)
	}
	again[0].ID = s.ID
	if !reflect.DeepEqual(s, again[0]) {
		t.Fatal("share roundtrip changed profile")
	}
	cfg, err := buildMieruConfig(s, 18080)
	if err != nil {
		t.Fatal(err)
	}
	cfg["socks5ListenLAN"] = true
	cfg["rpcPort"] = 1234
	cfg["httpProxyPort"] = 8888
	b, _ := json.Marshal(cfg)

	parsed, skipped, err := parseSubscription(string(b))
	if err != nil || skipped != 0 || len(parsed) != 1 {
		t.Fatalf("%v %d %d", err, skipped, len(parsed))
	}
	safe, _ := buildMieruConfig(parsed[0], 19090)
	if safe["rpcPort"] != 0 || safe["httpProxyPort"] != nil || safe["socks5ListenLAN"] != false || safe["socks5Port"] != 19090 {
		t.Fatal("import applied remote listener options")
	}
	for _, bad := range []string{
		strings.Replace(mieruTestLink, "protocol=TCP", "protocol=BAD", 1),
		strings.Replace(mieruTestLink, "10000-10005", "10005-10000", 1),
		strings.Replace(mieruTestLink, "mtu=1400", "mtu=9000", 1),
		mieruTestLink + "&traffic-pattern=unknown",
		strings.Replace(mieruTestLink, "&protocol=UDP", "", 1),
	} {
		if _, errs := ParseShareLinks(bad); len(errs) == 0 {
			t.Fatal("accepted invalid or unsupported Mieru settings")
		}
	}
}

func TestExternalConfigRouting(t *testing.T) {
	nodes, _ := ParseShareLinks(mieruTestLink)
	s := DefaultSettings()
	s.Servers = nodes
	s.Selected = nodes[0].ID
	s.UDPProxy = true
	if _, err := BuildConfig(s, ConfigEnv{}); err == nil {
		t.Fatal("Mieru without a client must not become Naive")
	}
	cfg, err := BuildConfig(s, ConfigEnv{ExternalPorts: map[string]int{serverTag(nodes[0]): 18080}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	if binary := os.Getenv("SINGBOX"); binary != "" {
		path := filepath.Join(t.TempDir(), "external.json")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "check", "-c", path).CombinedOutput(); err != nil {
			t.Fatalf("sing-box rejected external bridge: %s", output)
		}
	}
	if !strings.Contains(string(b), `"type":"socks"`) || strings.Contains(string(b), `"type":"naive"`) {
		t.Fatal("missing SOCKS bridge")
	}
	if offlineProbeKind(nodes[0]) != "tcp" {
		t.Fatal("Mieru must not receive a TLS probe")
	}
	nodes[0].Mieru.Bindings[0].Protocol = "UDP"
	if offlineProbeKind(nodes[0]) != "unavailable" {
		t.Fatal("UDP cannot receive a TCP/TLS probe")
	}
	naive := Server{ID: "n", Type: "naive", Host: "192.0.2.1", Port: 443, SNI: "proxy.example", Username: "a", Password: "p@ss"}
	ncfg, err := buildNaiveConfig(map[string]Server{"s-n": naive}, map[string]int{"s-n": 18081})
	if err != nil {
		t.Fatal(err)
	}
	if ncfg["host-resolver-rules"] != "MAP proxy.example 192.0.2.1" {
		t.Fatal("SNI/address mapping lost")
	}
	naive.UoT = true
	if _, err := buildNaiveConfig(map[string]Server{"s-n": naive}, map[string]int{"s-n": 18081}); err == nil {
		t.Fatal("silently lost UoT")
	}
}

// Uses official clients on loopback only; no provider credentials or external
// VPN destinations. Opt in with NAIVE_CLIENT and MIERU_CLIENT paths.
func TestOfficialProxyLifecycle(t *testing.T) {
	for _, kind := range []string{"naive", "mieru"} {
		t.Run(kind, func(t *testing.T) {
			binary := os.Getenv(strings.ToUpper(kind) + "_CLIENT")
			if binary == "" {
				t.Skip("official client not provided")
			}
			a := &App{dir: t.TempDir(), settings: DefaultSettings()}
			a.core = NewCore(a.dir)
			a.mon = newMonitor(a)
			x := newExternalProxy(a, kind, "unused")
			if err := os.MkdirAll(filepath.Dir(x.exePath()), 0700); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(x.exePath(), b, 0700); err != nil {
				t.Fatal(err)
			}
			s := Server{ID: "fixture", Type: kind, Host: "127.0.0.1", Port: 9, Username: "test", Password: "private-test-password"}
			if kind == "mieru" {
				s.Mieru = &MieruOptions{Bindings: []MieruBinding{{Port: 9, Protocol: "TCP"}}}
			}
			if kind == "mieru" {
				cfg, _ := buildMieruConfig(s, 18081)
				js, _ := json.Marshal(cfg)
				path := filepath.Join(a.dir, "export-fixture.json")
				if err := os.WriteFile(path, js, 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(binary, "export", "config")
				cmd.Env = append(os.Environ(), "MIERU_CONFIG_JSON_FILE="+path)
				output, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				imported, errs := ParseShareLinks(strings.TrimSpace(string(output)))
				if len(errs) != 0 || len(imported) != 1 || imported[0].Password != s.Password || imported[0].Host != s.Host {
					t.Fatal("official protobuf export could not be imported")
				}
			}
			nodes := map[string]Server{"s-fixture": s}
			ports := x.Sync(nodes)
			defer x.Stop()
			if len(ports) != 1 {
				t.Fatal("official client failed to start")
			}
			port := ports["s-fixture"]
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = conn.Write([]byte{5, 1, 0})
			reply := make([]byte, 2)
			_, err = io.ReadFull(conn, reply)
			conn.Close()
			if err != nil || reply[0] != 5 || reply[1] != 0 {
				t.Fatalf("SOCKS handshake: %v %v", reply, err)
			}
			if x.Sync(nodes)["s-fixture"] != port {
				t.Fatal("unchanged profile restarted client")
			}
			paths := []string{}
			for _, c := range x.children {
				paths = append(paths, c.path)
			}
			x.Stop()
			for _, path := range paths {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("private config survived stop")
				}
			}
			conn, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
			if err == nil {
				conn.Close()
				t.Fatal("client survived Stop")
			}
		})
	}
}
