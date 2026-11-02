package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionReleaseRequirements(t *testing.T) {
	r := versionRelease{Tag: "v2.8.0"}
	for _, name := range []string{releaseAsset(r.Tag), "SHA256SUMS.txt", "SHA256SUMS.txt.sig"} {
		r.Assets = append(r.Assets, struct {
			Name string `json:"name"`
		}{name})
	}
	if !versionAvailable(r) {
		t.Fatal("signed platform release rejected")
	}
	for _, tag := range []string{"../v2.8.0", "v2.8.0/other", "v+2.8", "v2.8.0-beta", "dev", "v2.8.0?x"} {
		r.Tag = tag
		if versionAvailable(r) {
			t.Fatal(tag)
		}
	}
	r.Tag = "v2.8.0"
	r.Prerelease = true
	if versionAvailable(r) {
		t.Fatal("prerelease")
	}
	r.Prerelease = false
	r.Assets = r.Assets[:2]
	if versionAvailable(r) {
		t.Fatal("unsigned release")
	}
}
func TestVersionBackupPreservesUnknownAndSealedSettings(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app.exe")
	os.WriteFile(exe, []byte("original"), 0755)
	original := []byte(`{"autoConnect":true,"futureField":{"secret":"dpapi:sealed"},"servers":[]}`)
	os.WriteFile(settingsPath(dir), original, 0600)
	backup, e := createVersionBackup(exe, dir, "2.26.0", "v2.8.0")
	if e != nil {
		t.Fatal(e)
	}
	cfg := updateSupervisorConfig{Exe: exe, Dir: dir, Backup: backup, Downgrade: true}
	if e = prepareVersionSettings(cfg); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(settingsPath(dir))
	var raw map[string]json.RawMessage
	json.Unmarshal(b, &raw)
	if string(raw["autoConnect"]) != "false" || !strings.Contains(string(raw["futureField"]), "dpapi:sealed") {
		t.Fatal(string(b))
	}
	saved, _ := os.ReadFile(filepath.Join(backup, "settings.json"))
	if string(saved) != string(original) {
		t.Fatal("backup lost original raw fields")
	}
	os.WriteFile(settingsPath(dir), []byte(`{"autoConnect":false}`), 0600)
	if e = restoreVersionSettings(cfg); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(settingsPath(dir))
	if string(b) != string(original) {
		t.Fatal("rollback lost settings")
	}
	b, _ = os.ReadFile(filepath.Join(backup, "Naivka.exe"))
	if string(b) != "original" {
		t.Fatal("wrong executable backup")
	}
	os.WriteFile(settingsPath(dir), []byte("null"), 0600)
	if e = prepareVersionSettings(cfg); e == nil {
		t.Fatal("null config accepted")
	}
}
func TestVersionCatalogPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `[]`)
			return
		}
		rows := make([]versionRelease, 50)
		for i := range rows {
			rows[i].Tag = "non-version"
		}
		rows[0].Tag = "v2.8.0"
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	old := versionAPIBase
	versionAPIBase = server.URL
	defer func() { versionAPIBase = old }()
	page, e := versionList(context.Background(), 1)
	if e != nil || !page.HasMore || len(page.Versions) != 1 {
		t.Fatal(page, e)
	}
	page, e = versionList(context.Background(), 2)
	if e != nil || page.HasMore || len(page.Versions) != 0 {
		t.Fatal(page, e)
	}
	if _, e = versionList(context.Background(), -1); e == nil {
		t.Fatal("invalid page")
	}
}
func TestVersionHistoricalChild(t *testing.T) {
	mode := os.Getenv("NAIVKA_HISTORICAL_TEST")
	if mode == "" {
		return
	}
	if mode == "exit" {
		os.Exit(2)
	}
	if mode == "hang" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	dir := os.Getenv("NAIVKA_HISTORICAL_DIR")
	exe := filepath.Join(dir, "app.exe")
	b, _ := os.ReadFile(settingsPath(dir))
	var settings map[string]json.RawMessage
	json.Unmarshal(b, &settings)
	if string(settings["autoConnect"]) != "false" {
		os.Exit(9)
	}
	// Old cleanup implementations may delete .old before reporting readiness.
	os.Remove(exe + ".old")
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		os.Exit(3)
	}
	pid := os.Getpid()
	if mode == "wrong-pid" {
		pid++
	}
	writeInstance(dir, instanceInfo{PID: pid, Port: ln.Addr().(*net.TCPAddr).Port, Token: strings.Repeat("a", 48)})
	http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Naivka-Token") != strings.Repeat("a", 48) {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/api/quit" {
			go func() { time.Sleep(20 * time.Millisecond); os.Exit(0) }()
			fmt.Fprint(w, `{}`)
			return
		}
		version := "2.8.0"
		if mode == "wrong-version" {
			version = "9.9.9"
		}
		json.NewEncoder(w).Encode(map[string]string{"version": version})
	}))
	os.Exit(0)
}
func TestVersionHistoricalRunner(t *testing.T) {
	for _, mode := range []string{"ready", "exit", "hang", "wrong-pid", "wrong-version", "start-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "app.exe")
			os.WriteFile(exe, []byte("original"), 0755)
			original := []byte(`{"autoConnect":true,"newField":"preserve"}`)
			os.WriteFile(settingsPath(dir), original, 0600)
			backup, e := createVersionBackup(exe, dir, "2.26.0", "v2.8.0")
			if e != nil {
				t.Fatal(e)
			}
			os.Rename(exe, exe+".old")
			os.WriteFile(exe, []byte("candidate"), 0755)
			f, _ := os.CreateTemp(dir, "update-intent-*.json")
			f.Close()
			intent := updateIntent{Commit: true, Resume: false}
			writeUpdateIntent(f.Name(), intent)
			cfg := updateSupervisorConfig{Exe: exe, Dir: dir, Backup: backup, Intent: f.Name(), Version: "v2.8.0", Downgrade: true, Historical: true}
			if e = prepareVersionSettings(cfg); e != nil {
				t.Fatal(e)
			}
			var children []*exec.Cmd
			var rollbackArgs []string
			runner := updateRunner{timeout: 1400 * time.Millisecond, stable: 100 * time.Millisecond, command: func(target string, args, env []string) *exec.Cmd {
				childMode := mode
				for _, a := range args {
					if a == "--update-rollback" {
						childMode = "hang"
						rollbackArgs = append([]string{}, args...)
					}
				}
				var cmd *exec.Cmd
				if childMode == "start-error" {
					cmd = exec.Command(filepath.Join(dir, "absent"))
				} else {
					cmd = exec.Command(os.Args[0], "-test.run=^TestVersionHistoricalChild$")
				}
				cmd.Env = append(env, "NAIVKA_HISTORICAL_TEST="+childMode, "NAIVKA_HISTORICAL_DIR="+dir)
				children = append(children, cmd)
				return cmd
			}}
			defer func() {
				for _, c := range children {
					if c.Process != nil {
						c.Process.Kill()
					}
				}
			}()
			result := runner.runHistorical(cfg, intent)
			if mode == "ready" {
				if result != "UPDATED" {
					t.Fatal(result)
				}
				if !fileExists(filepath.Join(backup, "Naivka.exe")) {
					t.Fatal("permanent backup removed")
				}
				return
			}
			if !strings.HasSuffix(result, "_ROLLED_BACK") {
				t.Fatal(result)
			}
			b, _ := os.ReadFile(exe)
			if string(b) != "original" {
				t.Fatal("executable not restored")
			}
			b, _ = os.ReadFile(settingsPath(dir))
			if string(b) != string(original) {
				t.Fatal("settings not restored")
			}
			if len(rollbackArgs) == 0 || strings.Contains(strings.Join(rollbackArgs, " "), "--connect") {
				t.Fatal("unexpected reconnect", rollbackArgs)
			}
		})
	}
}
