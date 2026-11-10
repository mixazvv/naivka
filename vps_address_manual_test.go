//go:build !legacy

package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestVPSManualAddressMigration(t *testing.T) {
	a := addressFixture(t)
	a.settings.OwnHosts = nil
	r := addressRequest{Address: "2001:db8::42", Manual: &manualAddress{Old: "203.0.113.10", Port: 22, User: "root"}}
	before := a.settings.clone()
	p, err := a.changeVPSAddress(r, addressProbe)
	if err != nil {
		t.Fatal(err)
	}
	if p.Fingerprint != "SHA256:fixture" || len(p.Servers) != 1 || len(p.Panels) != 1 {
		t.Fatal("missing manual plan or matching panel")
	}
	if !reflect.DeepEqual(a.settings, before) {
		t.Fatal("manual preview saved data")
	}
	r.Apply, r.Confirm, r.KeyConfirmed, r.Revision, r.Servers = true, true, true, p.Revision, []string{"manual"}
	r.Manual.Fingerprint = p.Fingerprint
	result, err := a.changeVPSAddress(r, addressProbe)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.settings.OwnHosts) != 0 {
		t.Fatal("manual mode created an own-host record")
	}
	if a.settings.Servers[0].Host != r.Address || a.settings.Servers[0].Password != "p" || a.settings.Servers[0].SNI != "proxy.example" || a.settings.Servers[2].Host != "203.0.113.10" {
		t.Fatal("wrong endpoints or credentials after manual migration")
	}
	if a.settings.Panels[0].Host != r.Address || a.settings.Panels[1].Host != "203.0.113.10" {
		t.Fatal("wrong panel targets")
	}
	backup, err := LoadSettings(filepath.Join(a.dir, result.Backup))
	if err != nil || backup.Servers[0].Host != "203.0.113.10" {
		t.Fatal("manual backup missing")
	}
}

func TestVPSManualAddressRejectsUnreviewedTargets(t *testing.T) {
	for _, kind := range []string{"confirmation", "key", "old", "port", "user", "selection", "saved", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			a := addressFixture(t)
			a.settings.OwnHosts = nil
			r := addressRequest{Address: "203.0.113.20", Manual: &manualAddress{Old: "203.0.113.10", Port: 22, User: "root"}}
			p, err := a.changeVPSAddress(r, addressProbe)
			if err != nil {
				t.Fatal(err)
			}
			r.Apply, r.Confirm, r.KeyConfirmed, r.Revision, r.Servers = true, true, true, p.Revision, []string{"manual"}
			r.Manual.Fingerprint = p.Fingerprint
			switch kind {
			case "confirmation":
				r.KeyConfirmed = false
			case "key":
				r.Manual.Fingerprint = "SHA256:changed"
			case "old":
				r.Manual.Old = "203.0.113.99"
			case "port":
				r.Manual.Port = 2222
			case "user":
				r.Manual.User = "other"
			case "selection":
				r.Servers = nil
				a.settings.Panels = nil
				r.Revision = manualAddressRevision(a.settings, OwnHost{Host: r.Manual.Old, SSHPort: 22, SSHUser: "root", HostKey: r.Manual.Fingerprint}, r.Address)
			case "saved":
				a.settings.OwnHosts = []OwnHost{{ID: "existing", Host: r.Manual.Old, SSHPort: 22, HostKey: "SHA256:different"}}
			case "mixed":
				r.Host = "saved"
			}
			before := a.settings.clone()
			if _, err = a.changeVPSAddress(r, addressProbe); err == nil {
				t.Fatal("accepted an unreviewed manual target")
			}
			if !reflect.DeepEqual(a.settings, before) {
				t.Fatal("rejection modified settings")
			}
		})
	}
	for _, old := range []string{"", "proxy.example", "0.0.0.0", "127.0.0.1", "::1", "1.2.3.4;id"} {
		a := addressFixture(t)
		a.settings.OwnHosts = nil
		if _, _, err := planAddress(a.settings, addressRequest{Address: "203.0.113.20", Manual: &manualAddress{Old: old}}); err == nil {
			t.Fatal("invalid old IP accepted")
		}
	}
}
