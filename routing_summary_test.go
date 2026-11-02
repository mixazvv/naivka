package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoutingSummaryFromGeneratedConfig(t *testing.T) {
	for _, mode := range []string{"proxy", "tun"} {
		for _, typ := range []string{"naive", "trojan"} {
			for _, proxy := range []bool{false, true} {
				s := DefaultSettings()
				s.Mode = mode
				s.UDPProxy = proxy
				s.Servers = []Server{{ID: "a", Name: "a", Type: typ, Host: "host.invalid", Port: 443, Username: "private-user", Password: "private-password"}}
				s.normalize()
				cfg, e := BuildConfig(s, ConfigEnv{APIPort: 9090})
				if e != nil {
					t.Fatal(e)
				}
				v := routingSummary(cfg, s)
				if !v.Quic || v.Mode != mode {
					t.Fatal(v)
				}
				compatible := proxy && typ == "trojan"
				if v.Server != compatible || v.Direct == compatible {
					t.Fatal(typ, proxy, v)
				}
				b, _ := json.Marshal(v)
				if strings.Contains(string(b), "private") || strings.Contains(string(b), "host.invalid") {
					t.Fatal("raw configuration leaked")
				}
			}
		}
	}
}
func TestRoutingSummaryOverrideAndNoFakeSuccess(t *testing.T) {
	s := DefaultSettings()
	s.Override = `{"route":{"final":"direct","rules":[]}}`
	s.Servers = []Server{{ID: "a", Type: "trojan", Host: "host.invalid", Port: 443, Password: "x"}}
	cfg, e := BuildConfig(s, ConfigEnv{})
	if e != nil {
		t.Fatal(e)
	}
	v := routingSummary(cfg, s)
	if v.Final != "direct" || v.Server || v.Quic || !v.Custom {
		t.Fatal(v)
	}
	s.Experience.Home = "full"
	s.Experience.Activity = "programs"
	v2 := routingSummary(cfg, s)
	if v2 != v {
		t.Fatal("UI settings affect routing summary")
	}
}
