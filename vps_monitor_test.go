//go:build !legacy

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVPSReportUnknownAndFiltered(t *testing.T) {
	r, err := parseVPSReport("protocol=1\nload=NaN\nuptime=-1\ncpus=+Inf\nmemory_used=0\nnetwork_rx=123\nroot_login=secret-key\npassword=do-not-echo\npassword_auth=yes\nfirewall=arbitrary\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Metrics) != 2 || r.Metrics["network_rx"] != 123 {
		t.Fatal(r.Metrics)
	}
	checks := map[string]vpsCheck{}
	for _, c := range r.Checks {
		checks[c.ID] = c
	}
	if checks["root_login"].Level != "unknown" || checks["password_auth"].Level != "warning" || checks["firewall"].Level != "unknown" {
		t.Fatal(checks)
	}
	for _, id := range []string{"os_updates", "external_ports", "service_xray", "certificate", "config_mode"} {
		if checks[id].Level != "unknown" {
			t.Fatal(id, checks[id])
		}
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "secret-key") || strings.Contains(string(encoded), "do-not-echo") {
		t.Fatal("untrusted output leaked")
	}
}
func TestVPSReportPermissionModes(t *testing.T) {
	for _, tc := range []struct{ mode, level string }{{"600", "ok"}, {"400", "ok"}, {"640", "warning"}, {"604", "warning"}, {"666", "warning"}, {"777", "warning"}, {"888", "unknown"}, {"", "unknown"}, {"0600", "unknown"}} {
		r, err := parseVPSReport("protocol=1\nconfig_mode=" + tc.mode + "\n")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range r.Checks {
			if c.ID == "config_mode" && c.Level != tc.level {
				t.Errorf("%s: %s", tc.mode, c.Level)
			}
		}
	}
}
func TestVPSReportBoundedOutput(t *testing.T) {
	var out vpsOutput
	n, err := out.Write([]byte(strings.Repeat("x", vpsOutputLimit+25)))
	if err != nil || n != vpsOutputLimit+25 || len(out.data) != vpsOutputLimit || !out.overflow {
		t.Fatal(n, err, len(out.data))
	}
	if n, err = out.Write([]byte("extra")); n != 5 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := parseVPSReport(string(out.data)); err == nil {
		t.Fatal("missing protocol accepted")
	}
	if _, err := parseVPSReport("protocol=1\n" + strings.Repeat("x", vpsOutputLimit)); err == nil {
		t.Fatal("oversize accepted")
	}
}
func TestVPSCancelledChannelSetupClosesTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	closed := make(chan struct{})
	finished := make(chan struct{})
	go func() { <-started; cancel() }()
	_, err := waitVPS(ctx, func() { close(closed) }, func() (vpsReport, error) {
		close(started)
		<-closed
		close(finished)
		return vpsReport{}, errors.New("transport closed")
	})
	if err == nil {
		t.Fatal("cancellation ignored")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker leaked")
	}
}
func TestVPSCompletedProbeKeepsTransport(t *testing.T) {
	closed := false
	r, err := waitVPS(context.Background(), func() { closed = true }, func() (vpsReport, error) { return parseVPSReport("protocol=1\nload=0.5\n") })
	if err != nil || closed || r.Metrics["load"] != 0.5 {
		t.Fatal(r, err, closed)
	}
}

func TestVPSRouteRequiresSavedHostAndAuthentication(t *testing.T) {
	a := &App{settings: DefaultSettings()}
	a.settings.OwnHosts = []OwnHost{{ID: "saved", Host: "example.invalid", SSHPort: 22, SSHUser: "root", HostKey: "SHA256:fixed"}}
	var handler apiFunc
	a.vpsRoutes(func(method, path string, f apiFunc) {
		if method != "POST" || path != "/api/vps/report" {
			t.Fatal(method, path)
		}
		handler = f
	})
	if handler == nil {
		t.Fatal("route missing")
	}
	for _, raw := range []string{`{"host":"unknown","password":"x"}`, `{"host":"saved"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/vps/report", strings.NewReader(raw))
		response, err := handler(req)
		if strings.Contains(raw, "unknown") {
			if err == nil {
				t.Fatal("arbitrary target accepted")
			}
		} else {
			need, ok := response.(map[string]bool)
			if err != nil || !ok || !need["needAuth"] {
				t.Fatal(response, err)
			}
		}
	}
	if len(a.friends.conns) != 0 {
		t.Fatal("unauthenticated request opened SSH")
	}
}

func TestVPSProbeScriptFixtures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux sh/awk probe")
	}
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("awk unavailable")
	}
	dir := t.TempDir()
	write := func(name, data string, mode os.FileMode) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/stat", "cpu  10 0 20 100 0 0 0 0 0 0\n", 0600)
	write("proc/uptime", "3600.0 2.0\n", 0600)
	write("proc/loadavg", "0.25 0.10 0.05 1/100 123\n", 0600)
	write("proc/cpuinfo", "processor : 0\nprocessor : 1\n", 0600)
	write("proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 250 kB\n", 0600)
	write("proc/net/dev", "header\nheader\n    lo: 999 0 0 0 0 0 0 0 888 0 0 0 0 0 0 0\n  eth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\nlonginterface00: 30 0 0 0 0 0 0 0 40 0 0 0 0 0 0 0\n", 0600)
	write("bin/sleep", "#!/bin/sh\nexit 0\n", 0700)
	write("bin/df", "#!/bin/sh\nprintf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/root 100 40 60 40%% /\n'\n", 0700)
	write("bin/sshd", "#!/bin/sh\nprintf 'permitrootlogin yes\npasswordauthentication no\n'\n", 0700)
	write("bin/ufw", "#!/bin/sh\nprintf 'Status: inactive\n'\n", 0700)
	write("bin/systemctl", "#!/bin/sh\nexit 1\n", 0700)
	script := strings.ReplaceAll(vpsReportScript, "/proc/", filepath.ToSlash(dir)+"/proc/")
	script = strings.ReplaceAll(script, "/etc/sing-box/", filepath.ToSlash(dir)+"/absent/")
	script = strings.Replace(script, "export PATH=/usr/sbin:/usr/bin:/sbin:/bin", "export PATH="+shellQuote(filepath.Join(dir, "bin")+":/usr/bin:/bin"), 1)
	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	r, err := parseVPSReport(string(out))
	if err != nil {
		t.Fatal(err, string(out))
	}
	for k, want := range map[string]float64{"uptime": 3600, "load": 0.25, "cpus": 2, "memory_total": 1024000, "memory_used": 768000, "disk_total": 102400, "disk_used": 40960, "network_rx": 130, "network_tx": 240} {
		if got, ok := r.Metrics[k]; !ok || got != want {
			t.Errorf("%s: %v, want %v; %s", k, got, want, out)
		}
	}
	for _, c := range r.Checks {
		if c.ID == "root_login" && c.Value != "yes" || c.ID == "firewall" && c.Value != "inactive" {
			t.Error(c)
		}
	}
}
