package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionDiffAndPreviewCommit(t *testing.T) {
	old, _, err := parseSubscription("trojan://secret@one.example:443#One\ntrojan://secret@two.example:443#Two\ntrojan://secret@gone.example:443#Gone")
	if err != nil {
		t.Fatal(err)
	}
	next, _, _ := parseSubscription("trojan://secret@one.example:443#One\ntrojan://changed@two.example:443#Renamed\ntrojan://new@new.example:443#New")
	diff := diffSubscription(old, next)
	b, _ := json.Marshal(diff)
	if len(diff.Added) != 1 || len(diff.Removed) != 1 || len(diff.Changed) != 1 || diff.Unchanged != 1 || strings.Contains(string(b), "secret") || strings.Contains(string(b), "two.example") {
		t.Fatal(string(b))
	}
	for _, kind := range []string{"apply", "stale", "save-failure", "expired"} {
		t.Run(kind, func(t *testing.T) {
			s := DefaultSettings()
			s.AutoApply = false
			sub := Subscription{ID: "a", Name: "A", URL: "https://example.invalid/private"}
			s.Subscriptions = []Subscription{sub}
			for i := range old {
				old[i].ID = newID()
				old[i].SubscriptionID = "a"
			}
			s.Servers = append([]Server(nil), old...)
			s.Selected = old[0].ID
			s.Servers[0].Fav = true
			a := &App{dir: t.TempDir(), settings: s, core: &Core{}}
			before := a.settings.clone()
			p := subscriptionPreview{sub: sub, servers: next, expires: time.Now().Add(time.Minute), base: subscriptionRevision(sub, s.Servers)}
			if kind == "stale" {
				a.settings.Servers[0].Name = "edited"
			}
			if kind == "expired" {
				p.expires = time.Now().Add(-time.Minute)
			}
			if kind == "save-failure" {
				os.Mkdir(settingsPath(a.dir)+".tmp", 0700)
			}
			a.subscriptions.previews = map[string]subscriptionPreview{"token": p}
			_, err := a.commitSubscriptionPreview("a", "token")
			if kind != "apply" {
				if err == nil {
					t.Fatal("unsafe commit accepted")
				}
				if a.settings.Servers[1].Password != before.Servers[1].Password {
					t.Fatal("failure changed servers")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.settings.Servers[0].ID != before.Servers[0].ID || !a.settings.Servers[0].Fav {
				t.Fatal("identity/favorite lost")
			}
			if _, err = a.commitSubscriptionPreview("a", "token"); err == nil {
				t.Fatal("preview reused")
			}
		})
	}
}
func TestUpdateRecoveryGuardsAndArchive(t *testing.T) {
	for _, kind := range []string{"ready", "live", "unknown", "stale", "wrong-exe"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "Naivka.exe")
			intent := filepath.Join(dir, "update-intent-test.json")
			os.WriteFile(exe, []byte("current"), 0700)
			os.WriteFile(exe+".old", []byte("previous"), 0700)
			os.WriteFile(intent, []byte(`{"commit":true}`), 0600)
			os.WriteFile(settingsPath(dir), []byte("sealed-settings"), 0600)
			record := updateRecoveryRecord{Exe: exe, Parent: 123, Supervisor: 456}
			if kind == "wrong-exe" {
				record.Exe = filepath.Join(dir, "other.exe")
				os.WriteFile(record.Exe, []byte("unrelated"), 0700)
			}
			if kind != "unknown" {
				if err := writeUpdateRecovery(intent, record); err != nil {
					t.Fatal(err)
				}
			}
			alive := func(int) bool { return kind == "live" }
			p, err := inspectUpdateRecovery(dir, exe, alive)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "stale" {
				os.WriteFile(intent, []byte(`{"commit":false}`), 0600)
			}
			err = archiveInterruptedUpdate(dir, exe, p.Revision, alive)
			if kind != "ready" {
				if err == nil || !fileExists(intent) || !fileExists(exe+".old") {
					t.Fatal("unsafe recovery", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(exe)
			if string(b) != "current" {
				t.Fatal("running version changed")
			}
			if fileExists(intent) || fileExists(exe+".old") {
				t.Fatal("pending files not archived")
			}
			archives, _ := filepath.Glob(filepath.Join(dir, "version-backups", "recovered-*"))
			if len(archives) != 1 {
				t.Fatal(archives)
			}
			for name, want := range map[string]string{"Naivka.exe.old": "previous", "settings.json": "sealed-settings"} {
				b, _ := os.ReadFile(filepath.Join(archives[0], name))
				if string(b) != want {
					t.Fatal("backup lost", name)
				}
			}
		})
	}
	if !recoveryProcessAlive(os.Getpid()) {
		t.Fatal("current process reported dead")
	}
}

func TestSubscriptionDiffSharedEndpoint(t *testing.T) {
	old, _, _ := parseSubscription("trojan://a@same.example:443#A\ntrojan://b@same.example:443#B")
	next, _, _ := parseSubscription("trojan://c@same.example:443#C\ntrojan://a@same.example:443#A")
	d := diffSubscription(old, next)
	if d.Unchanged != 1 || len(d.Changed) != 1 || len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Fatal(d)
	}
}

type recoveryTestUI struct{}

func (recoveryTestUI) Run(bool)            {}
func (recoveryTestUI) Show()               {}
func (recoveryTestUI) SetTheme(bool)       {}
func (recoveryTestUI) SetLogo(logoColors)  {}
func (recoveryTestUI) Notify(string, bool) {}
func (recoveryTestUI) Refresh()            {}
func (recoveryTestUI) Close()              {}
func (recoveryTestUI) Ready() bool         { return true }

func TestUpdateRecoveryRoutes(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.Process.Pid
	if recoveryProcessAlive(deadPID) {
		t.Fatal("exited process still reported active")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" || r.Header.Get("X-Naivka-Token") != "test-token" {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	_, portText, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portText)
	exe, _ := os.Executable()
	for _, kind := range []string{"confirm", "busy", "active", "stale", "success"} {
		t.Run(kind, func(t *testing.T) {
			a := &App{dir: t.TempDir(), port: port, token: "test-token", settings: DefaultSettings(), ui: recoveryTestUI{}}
			a.core = NewCore(a.dir)
			a.mon = newMonitor(a)
			intent := filepath.Join(a.dir, "update-intent-test.json")
			os.WriteFile(intent, []byte(`{"commit":true}`), 0600)
			if err := writeUpdateRecovery(intent, updateRecoveryRecord{Exe: exe, Parent: deadPID}); err != nil {
				t.Fatal(err)
			}
			p, err := inspectUpdateRecovery(a.dir, exe, recoveryProcessAlive)
			if err != nil || !p.Ready {
				t.Fatal(p, err)
			}
			body := map[string]any{"confirm": true, "revision": p.Revision}
			if kind == "confirm" {
				body["confirm"] = false
			}
			if kind == "stale" {
				body["revision"] = "old"
			}
			if kind == "busy" {
				a.self.setBusy("download")
			}
			if kind == "active" {
				a.life.updating = true
			}
			view := a.updateView()
			if (kind == "busy" || kind == "active") && view.RecoveryPending != 0 {
				t.Fatal("active update advertised as interrupted")
			}
			if kind == "success" && view.RecoveryPending != 1 {
				t.Fatal("interrupted update not detected")
			}
			var post apiFunc
			a.updateRecoveryRoutes(func(method, path string, f apiFunc) {
				if method == "POST" {
					post = f
				}
			})
			raw, _ := json.Marshal(body)
			_, err = post(httptest.NewRequest("POST", "/api/app/recovery", strings.NewReader(string(raw))))
			if kind != "success" {
				if err == nil || !fileExists(intent) {
					t.Fatal("unsafe recovery accepted", kind, err)
				}
				return
			}
			if err != nil || fileExists(intent) {
				t.Fatal("recovery failed", err)
			}
			if a.self.snapshot().Busy != "" || a.self.snapshot().Error != "" {
				t.Fatal("recovery status not cleared")
			}
		})
	}
}
