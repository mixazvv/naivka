package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestUpdateLegacyChild(t *testing.T) {
	if os.Getenv("NAIVKA_TEST_LEGACY_CHILD") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

func TestUpdateLegacyArchiveWithoutReboot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows updater")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	intent := filepath.Join(dir, "update-intent-test.json")
	if err := os.WriteFile(intent, []byte(`{"commit":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestUpdateLegacyChild$")
	child.Env = append(os.Environ(), "NAIVKA_TEST_LEGACY_CHILD=1")
	wait, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer wait.Close()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	busy, err := inspectUpdateRecovery(dir, exe, recoveryProcessAlive)
	wait.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	if err != nil || !busy.Active || busy.Ready {
		t.Fatal("active process overlooked", busy, err)
	}
	p, err := inspectUpdateRecovery(dir, exe, recoveryProcessAlive)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if err := archiveInterruptedUpdate(dir, exe, p.Revision, recoveryProcessAlive); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "version-backups", "recovered-*", filepath.Base(intent)))
	if len(files) != 1 || fileExists(intent) {
		t.Fatal("missing archived intent")
	}
}

func TestZapretProbeResponses(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/short":
			w.Header().Set("Content-Length", "100")
			w.Write([]byte("short"))
		case "/ok":
			w.Write([]byte("ok"))
		case "/busy":
			w.WriteHeader(503)
		case "/blocked":
			w.WriteHeader(451)
		default:
			w.WriteHeader(404)
		}
	}))
	defer proxy.Close()
	port := proxy.Listener.Addr().(*net.TCPAddr).Port
	z := Zapret{a: &App{core: &Core{running: true, probeD: port}}}
	targets := []target{
		{"site", "http://example.test/ok", false},
		{"cdn", "http://i.ytimg.com/", false},
		{"cut", "http://example.test/short", false},
		{"error", "http://example.test/busy", false},
		{"blocked", "http://example.test/blocked", false},
		{"missing", "http://example.test/", false},
		{"invalid", "http://%zz", true},
	}
	ok, _, sites := z.probeTargets(targets, 3*time.Second)
	if ok != 2 {
		t.Fatal(sites)
	}
	for i, site := range sites {
		if site.OK != (i < 2) {
			t.Fatal(site)
		}
	}
}

func TestNetworkAddressOrder(t *testing.T) {
	a := networkSignature([]string{"wifi IPv4", "wifi IPv6"})
	if a != networkSignature([]string{"wifi IPv6", "wifi IPv4"}) || a == networkSignature([]string{"usb IPv4"}) {
		t.Fatal("incorrect network change detection")
	}
}

func TestHealthOfflineBreaksFailureStreak(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer proxy.Close()
	port := proxy.Listener.Addr().(*net.TCPAddr).Port
	s := DefaultSettings()
	s.Watchdog, s.TestURL = true, "http://example.test/test"
	a := &App{settings: s, core: &Core{running: true, probeP: port, probeD: port, logs: &LogRing{max: 10}}}
	a.life.want.Store(true)
	a.mon = newMonitor(a)
	a.mon.fails = 1
	a.mon.healthCheck(s)
	if a.mon.fails != 0 || a.mon.health != "offline" {
		t.Fatalf("offline round: %+v", a.mon.health)
	}
}
