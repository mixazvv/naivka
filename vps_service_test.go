//go:build !legacy

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestVPSServiceValidationAndSavedHosts(t *testing.T) {
	for _, service := range []string{"ssh", "sshd", "nginx", "sing-box;reboot", "/bin/sh", "sing-box.service", ""} {
		if (vpsServiceReq{Service: service, friendsReq: friendsReq{Action: "logs"}}).validate() == nil {
			t.Fatal("unsafe unit", service)
		}
	}
	for _, op := range []string{"start", "stop", "restart"} {
		req := vpsServiceReq{Service: "sing-box", friendsReq: friendsReq{Action: op}}
		if req.validate() == nil {
			t.Fatal("missing confirmation accepted")
		}
		req.Confirm = true
		if req.validate() != nil {
			t.Fatal(op)
		}
	}
	a := &App{settings: DefaultSettings()}
	a.settings.OwnHosts = []OwnHost{{ID: "saved", Host: "example.invalid", SSHPort: 22, SSHUser: "root", HostKey: "SHA256:fixed"}}
	var handler apiFunc
	a.vpsServiceRoutes(func(method, path string, f apiFunc) { handler = f })
	for _, raw := range []string{`{"host":"saved","service":"sing-box","action":"logs"}`, `{"host":"unknown","service":"sing-box","action":"logs"}`, `{"host":"saved","service":"ssh","action":"restart","confirm":true}`} {
		res, err := handler(httptest.NewRequest("POST", "/api/vps/service", strings.NewReader(raw)))
		if strings.Contains(raw, `"host":"saved"`) && strings.Contains(raw, `"action":"logs"`) {
			if err != nil || !res.(map[string]bool)["needAuth"] {
				t.Fatal(res, err)
			}
		} else if err == nil {
			t.Fatal("invalid operation accepted")
		}
	}
}

func TestVPSLogFilteringAndReportInfo(t *testing.T) {
	raw := "\"password\":\"short-value\"\nAuthorization: Bearer short-bearer\n\x1b[31merror\x1b[0m password=secret-value token: bearer-value vless://id@host:443?pbk=key\n11111111-1111-4111-8111-111111111111\nABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq\x00"
	clean := cleanVPSLog(raw)
	for _, bad := range []string{"secret-value", "bearer-value", "short-value", "short-bearer", "vless://", "11111111-", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "\x00", "\x1b"} {
		if strings.Contains(clean, bad) {
			t.Fatal("log value exposed", bad, clean)
		}
	}
	r, err := parseVPSReport("protocol=1\ninfo_os=Debian GNU/Linux 13\ninfo_arch=x86_64\ninfo_password=secret\ninfo_kernel=bad\x00value\ninfo_hostname=" + strings.Repeat("x", 121) + "\n")
	if err != nil || r.Info["os"] != "Debian GNU/Linux 13" || r.Info["arch"] != "x86_64" || len(r.Info) != 2 {
		t.Fatal(r.Info, err)
	}
}

func TestVPSServiceFixedScript(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	mock := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(log) + "\ncase \"$1\" in show) echo loaded;; is-active) echo active;; esac\n"
	os.WriteFile(filepath.Join(dir, "systemctl"), []byte(mock), 0700)
	script := strings.Replace(vpsServiceScript, "export PATH=/usr/sbin:/usr/bin:/sbin:/bin", "export PATH="+shellQuote(dir+":/usr/bin:/bin"), 1)
	script = strings.ReplaceAll(script, "/etc/sing-box.install.lock", filepath.Join(dir, "action-lock"))
	for _, args := range [][]string{{"sing-box", "restart"}, {"ssh", "restart"}, {"sing-box", "restart;touch /tmp/forbidden"}} {
		cmd := exec.Command("sh", "-s", "--", args[0], args[1])
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if args[0] == "sing-box" && args[1] == "restart" {
			if err != nil || string(out) != "active\n" {
				t.Fatal(string(out), err)
			}
		} else if err == nil {
			t.Fatal("script accepted unsafe command")
		}
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "restart sing-box\n") != 1 || strings.Contains(string(b), "restart ssh") {
		t.Fatal(string(b))
	}
}

// SSH channel stalls are interrupted by the caller deadline without retaining logs.
func TestVPSServiceDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("local TCP unavailable:", err)
	}
	defer listener.Close()
	shutdown := make(chan struct{})
	defer close(shutdown)
	// NewClientConn needs a peer handshake: use an SSH server with an ephemeral key.
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(key)
	go func() {
		server, e := listener.Accept()
		if e != nil {
			return
		}
		conn, chans, reqs, e := ssh.NewServerConn(server, cfg)
		if e != nil {
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(reqs)
		for ch := range chans {
			channel, requests, e := ch.Accept()
			if e != nil {
				return
			}
			go func() {
				defer channel.Close()
				for req := range requests {
					if req.Type == "exec" {
						req.Reply(true, nil)
						<-shutdown
						return
					}
					req.Reply(false, nil)
				}
			}()
		}
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(client, "fixture", &ssh.ClientConfig{User: "root", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	cl := ssh.NewClient(cc, chans, reqs)
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = executeVPSService(ctx, cl, vpsServiceReq{Service: "sing-box", friendsReq: friendsReq{Action: "logs"}})
	if err == nil || !strings.Contains(err.Error(), "Ожидание VPS завершилось") {
		t.Fatal(err)
	}
}
