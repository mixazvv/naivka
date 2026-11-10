//go:build !legacy

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFriendsLinksAfterAddressChange(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required for server script fixture")
	}
	dir := t.TempDir()
	files := map[string]string{
		"naivka.env":           "SERVER_ADDR=203.0.113.10\nREALITY_PORT=443\nPUBLIC_KEY=fixture\nSHORT_ID=1234\n",
		"config.json":          `{"inbounds":[{"tag":"naivka-reality","tls":{"server_name":"example.invalid"}}]}`,
		"friends/friends.json": `[{"id":"abcd1234","name":"Fixture","uuid":"fixture-uuid","created":1,"until":0,"paused":false}]`,
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"203.0.113.20", ""} {
		cmd := exec.Command("bash", "-c", friendsScript)
		cmd.Env = append(os.Environ(), "NAIVKA_TEST=1", "CONF_DIR="+dir, "ACTION=list", "SERVER_ADDR="+address)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("friends report failed: %s", output)
		}
		line := strings.TrimSpace(strings.TrimPrefix(string(output), "NAIVKA-FRIENDS "))
		decoded, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Friends []struct {
				Links []string `json:"links"`
			} `json:"friends"`
		}
		if err = json.Unmarshal(decoded, &report); err != nil {
			t.Fatal(err)
		}
		want := address
		if want == "" {
			want = "203.0.113.10"
		}
		if len(report.Friends) != 1 || !strings.Contains(report.Friends[0].Links[0], "@"+want+":443") {
			t.Fatal("wrong address in friend link")
		}
	}
}

func addressFixture(t *testing.T) *App {
	t.Helper()
	s := DefaultSettings()
	s.OwnHosts = []OwnHost{{ID: "vps", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root", HostKey: "SHA256:fixture"}}
	s.Servers = []Server{
		{ID: "manual", Type: "naive", Name: "Manual", Host: "203.0.113.10", Port: 443, SNI: "proxy.example", Username: "u", Password: "p"},
		{ID: "domain", Type: "naive", Host: "proxy.example", Port: 443},
		{ID: "subscription", Type: "naive", SubscriptionID: "sub", Host: "203.0.113.10", Port: 443},
	}
	s.Panels = []Panel{{ID: "panel", Host: "203.0.113.10", SSHPort: 22, SSHUser: "root", HostKey: "SHA256:fixture", Access: "public", URL: "https://203.0.113.10:8443/secret/path", Pass: "panel-secret"},
		{ID: "other", Host: "203.0.113.10", SSHPort: 2222, HostKey: "SHA256:other", URL: "https://panel.example/path"}}
	return &App{dir: t.TempDir(), settings: s}
}
func addressProbe(string, int) (string, string, error) { return "SHA256:fixture", "ssh-ed25519", nil }

func TestVPSAddressMigration(t *testing.T) {
	a := addressFixture(t)
	before := a.settings.clone()
	r := addressRequest{Host: "vps", Address: "2001:db8::42"}
	plan, err := a.changeVPSAddress(r, addressProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Servers) != 1 || plan.Servers[0].ID != "manual" || len(plan.Panels) != 1 {
		t.Fatal("unexpected plan targets")
	}
	if !reflect.DeepEqual(a.settings, before) {
		t.Fatal("preview mutated settings")
	}
	r.Apply, r.Confirm, r.Revision, r.Servers = true, true, plan.Revision, []string{"manual"}
	plan, err = a.changeVPSAddress(r, addressProbe)
	if err != nil {
		t.Fatal(err)
	}
	expected := before.clone()
	expected.OwnHosts[0].Host = r.Address
	expected.Servers[0].Host = r.Address
	expected.Panels[0].Host = r.Address
	expected.Panels[0].URL = "https://[2001:db8::42]:8443/secret/path"
	if !reflect.DeepEqual(a.settings, expected) {
		t.Fatal("unrelated settings/credentials were changed")
	}
	backup, err := LoadSettings(filepath.Join(a.dir, plan.Backup))
	if err != nil {
		t.Fatal(err)
	}
	if backup.Servers[0].Host != before.Servers[0].Host || backup.Servers[0].Password != "p" {
		t.Fatal("missing recoverable backup")
	}
	persisted, err := LoadSettings(a.dir)
	if err != nil || persisted.OwnHosts[0].Host != r.Address {
		t.Fatal("new address not persisted")
	}
}

func TestVPSAddressRejectsUnsafeChanges(t *testing.T) {
	for _, kind := range []string{"key", "unreachable", "stale", "confirm", "subscription", "connected", "disk"} {
		t.Run(kind, func(t *testing.T) {
			a := addressFixture(t)
			r := addressRequest{Host: "vps", Address: "203.0.113.20"}
			p, err := a.changeVPSAddress(r, addressProbe)
			if err != nil {
				t.Fatal(err)
			}
			r.Apply, r.Confirm, r.Revision, r.Servers = true, true, p.Revision, []string{"manual"}
			probe := addressProbe
			switch kind {
			case "key":
				probe = func(string, int) (string, string, error) { return "SHA256:imposter", "", nil }
			case "unreachable":
				probe = func(string, int) (string, string, error) { return "", "", errors.New("offline") }
			case "stale":
				a.settings.Servers[0].Password = "changed"
			case "confirm":
				r.Confirm = false
			case "subscription":
				r.Servers = []string{"subscription"}
			case "connected":
				a.life.want.Store(true)
			case "disk":
				if err := os.Mkdir(settingsPath(a.dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			before := a.settings.clone()
			if _, err := a.changeVPSAddress(r, probe); err == nil {
				t.Fatal("unsafe update accepted")
			}
			if !reflect.DeepEqual(a.settings, before) {
				t.Fatal("failure changed in-memory settings")
			}
		})
	}
	for _, address := range []string{"", "example.com", "0.0.0.0", "127.0.0.1", "224.0.0.1", "fe80::1", "203.0.113.10", "1.2.3.4;reboot"} {
		a := addressFixture(t)
		if _, _, err := planAddress(a.settings, addressRequest{Host: "vps", Address: address}); err == nil {
			t.Fatal("bad address accepted")
		}
	}
}

func TestVPSAddressSelectionAndDNS(t *testing.T) {
	a := addressFixture(t)
	r := addressRequest{Host: "vps", Address: "203.0.113.20"}
	p, err := a.changeVPSAddress(r, addressProbe)
	if err != nil {
		t.Fatal(err)
	}
	r.Apply, r.Confirm, r.Revision = true, true, p.Revision
	if _, err = a.changeVPSAddress(r, addressProbe); err != nil {
		t.Fatal(err)
	}
	if a.settings.Servers[0].Host != "203.0.113.10" {
		t.Fatal("unchecked endpoint changed")
	}
	a = addressFixture(t)
	a.settings.OwnHosts[0].Host = "proxy.example"
	p, err = a.changeVPSAddress(addressRequest{Host: "vps", Address: "203.0.113.20"}, addressProbe)
	if err != nil || len(p.Servers) != 0 {
		t.Fatal("domain endpoint must remain a domain")
	}
}
