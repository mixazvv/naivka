package main

import (
	"encoding/json"
	"strings"
)

// Only booleans/enums leave the preview. Never return domains, keys or raw rules.
// The same BuildConfig result used by /api/config supplies this description.
type routingView struct {
	Quic   bool   `json:"quic"`
	Server bool   `json:"server"`
	Direct bool   `json:"direct"`
	Final  string `json:"final"`
	Mode   string `json:"mode"`
	Custom bool   `json:"custom"`
}

func routingSummary(cfg M, s *Settings) routingView {
	v := routingView{Final: "other", Mode: s.Mode, Custom: strings.TrimSpace(s.ExtraRules+s.ExtraOutbounds+s.Override) != ""}
	var route struct {
		Final string `json:"final"`
		Rules []struct {
			Network  json.RawMessage `json:"network"`
			Port     json.RawMessage `json:"port"`
			Action   string          `json:"action"`
			Outbound string          `json:"outbound"`
		} `json:"rules"`
	}
	b, _ := json.Marshal(cfg["route"])
	if json.Unmarshal(b, &route) != nil {
		return v
	}
	if route.Final == tagDirect {
		v.Final = "direct"
	} else if route.Final == tagProxy || route.Final == tagAuto {
		v.Final = "proxy"
	}
	for _, r := range route.Rules {
		var n string
		var list []string
		udp := json.Unmarshal(r.Network, &n) == nil && n == "udp"
		if json.Unmarshal(r.Network, &list) == nil {
			for _, n := range list {
				if n == "udp" {
					udp = true
				}
			}
		}
		if !udp {
			continue
		}
		if r.Outbound == "udp-auto" || r.Outbound == "wl" {
			v.Server = true
		}
		if r.Outbound == tagDirect {
			v.Direct = true
		}
		var port int
		if json.Unmarshal(r.Port, &port) == nil && port == 443 && r.Action == "reject" {
			v.Quic = true
		}
	}
	return v
}
func (a *App) previewConfig() (M, *Settings, error) {
	a.mu.Lock()
	s := a.settings.clone()
	a.mu.Unlock()
	env := ConfigEnv{APIPort: 0, Secret: "••••", CachePath: "cache.db"}
	a.fillEnv(s, &env)
	cfg, err := BuildConfig(s, env)
	return cfg, s, err
}
