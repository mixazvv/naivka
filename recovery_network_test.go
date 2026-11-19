package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestUpdateLegacyBootRecovery(t *testing.T) {
	cache, dir := t.TempDir(), t.TempDir()
	exe := filepath.Join(dir, "Naivka.exe")
	intent := filepath.Join(dir, "update-intent-test.json")
	if err := os.WriteFile(intent, []byte(`{"commit":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	alive := func(int) bool { return false }
	p, err := inspectUpdateRecovery(dir, exe, alive)
	if err != nil || p.Ready {
		t.Fatal(p, err)
	}
	check := func(plan updateRecoveryPlan, boot string, write, ready bool) {
		t.Helper()
		if err := checkRecoveryBoot(&plan, cache, dir, exe, boot, write); err != nil || plan.Ready != ready {
			t.Fatal(plan, err)
		}
	}
	check(p, "boot-a", false, false)
	check(p, "boot-b", false, false)
	check(p, "boot-a", true, false)
	check(p, "boot-a", false, false)
	check(p, "", false, false)
	check(p, "boot-b", false, true)
	if err := os.WriteFile(intent, []byte(`{"commit":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := inspectUpdateRecovery(dir, exe, alive)
	if err != nil {
		t.Fatal(err)
	}
	check(changed, "boot-b", false, false)
	if runtime.GOOS == "windows" {
		t.Setenv("LocalAppData", cache)
		boot := recoveryBootID()
		if boot == "" || recoveryBootID() != boot {
			t.Fatal("no stable Windows boot GUID")
		}
		check(changed, "previous-test-boot", true, false)
		if err := archiveInterruptedUpdate(dir, exe, changed.Revision, alive); err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(filepath.Join(dir, "version-backups", "recovered-*", filepath.Base(intent)))
		if len(files) != 1 || fileExists(intent) {
			t.Fatal("legacy transaction was not archived")
		}
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
