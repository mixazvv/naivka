package main

import (
	"encoding/json"
	"strings"
)

// Extract server endpoints only. Remote routing, DNS, inbounds and executable
// helpers are never applied to the user's running configuration.
type subscriptionXrayOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Settings struct {
		Vnext []struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
			Users   []struct {
				ID         string `json:"id"`
				Flow       string `json:"flow"`
				Encryption string `json:"encryption"`
				Security   string `json:"security"`
			} `json:"users"`
		} `json:"vnext"`
		Servers []struct {
			Address  string `json:"address"`
			Port     int    `json:"port"`
			Password string `json:"password"`
			Method   string `json:"method"`
		} `json:"servers"`
	} `json:"settings"`
	Stream map[string]json.RawMessage `json:"streamSettings"`
	Proxy  json.RawMessage            `json:"proxySettings"`
	Mux    struct {
		Enabled bool `json:"enabled"`
	} `json:"mux"`
}

func xrayText(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}
func xrayObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]json.RawMessage{}, true
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}
func xrayBool(m map[string]json.RawMessage, key string) (bool, bool) {
	if len(m[key]) == 0 {
		return false, true
	}
	var value bool
	e := json.Unmarshal(m[key], &value)
	return value, e == nil
}
func xrayNonempty(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "{}" && s != "[]" && s != `""` && s != "false" && s != "0"
}
func xrayStreamServer(s *Server, stream map[string]json.RawMessage) bool {
	for _, key := range []string{"network", "security"} {
		if len(stream[key]) > 0 {
			var value string
			if json.Unmarshal(stream[key], &value) != nil {
				return false
			}
		}
	}
	security := xrayText(stream, "security")
	if security == "" {
		security = "none"
	}
	s.Security = security
	switch security {
	case "none":
	case "tls", "reality":
		key := "tlsSettings"
		if security == "reality" {
			key = "realitySettings"
		}
		tls, ok := xrayObject(stream[key])
		if !ok {
			return false
		}
		s.SNI = xrayText(tls, "serverName")
		s.Fingerprint = xrayText(tls, "fingerprint")
		s.Insecure, ok = xrayBool(tls, "allowInsecure")
		if !ok {
			return false
		}
		for _, k := range []string{"pinnedPeerCertificateChainSha256", "pinnedPeerCertificatePublicKeySha256", "verifyPeerCertInNames", "certificates", "echConfigList", "disableSystemRoot", "pinnedPeerCertSha256"} {
			if xrayNonempty(tls[k]) {
				return false
			}
		}
		if security == "reality" {
			s.PublicKey = xrayText(tls, "publicKey")
			if s.PublicKey == "" {
				s.PublicKey = xrayText(tls, "password")
			}
			s.ShortID = xrayText(tls, "shortId")
			if xrayNonempty(tls["mldsa65Verify"]) {
				return false
			}
		}
		var alpn []string
		if len(tls["alpn"]) > 0 {
			if json.Unmarshal(tls["alpn"], &alpn) != nil {
				return false
			}
			s.ALPN = strings.Join(alpn, ",")
		}
	default:
		return false
	}
	network := xrayText(stream, "network")
	if network == "" || network == "raw" {
		network = "tcp"
	}
	if network == "splithttp" {
		network = "xhttp"
	}
	s.Transport = network
	if sock, ok := xrayObject(stream["sockopt"]); !ok || xrayNonempty(sock["dialerProxy"]) {
		return false
	}
	switch network {
	case "tcp":
		tcp, ok := xrayObject(stream["tcpSettings"])
		if !ok {
			return false
		}
		header, ok := xrayObject(tcp["header"])
		if !ok {
			return false
		}
		kind := xrayText(header, "type")
		if kind != "" && kind != "none" {
			return false
		}
	case "ws", "httpupgrade":
		field := "wsSettings"
		if network == "httpupgrade" {
			field = "httpupgradeSettings"
		}
		tr, ok := xrayObject(stream[field])
		if !ok {
			return false
		}
		s.Path = xrayText(tr, "path")
		s.HostHeader = xrayText(tr, "host")
		headers, ok := xrayObject(tr["headers"])
		if !ok {
			return false
		}
		for k, v := range headers {
			if strings.EqualFold(k, "Host") {
				var host string
				if json.Unmarshal(v, &host) != nil {
					return false
				}
				s.HostHeader = host
			} else if xrayNonempty(v) {
				return false
			}
		}
		if xrayNonempty(tr["maxEarlyData"]) || xrayNonempty(tr["earlyDataHeaderName"]) {
			return false
		}
	case "grpc":
		tr, ok := xrayObject(stream["grpcSettings"])
		if !ok {
			return false
		}
		s.ServiceName = xrayText(tr, "serviceName")
		if xrayNonempty(tr["authority"]) || xrayNonempty(tr["multiMode"]) {
			return false
		}
	case "xhttp":
		if s.Type != "vless" {
			return false
		}
		raw := stream["xhttpSettings"]
		if len(raw) == 0 {
			raw = stream["splithttpSettings"]
		}
		tr, ok := xrayObject(raw)
		if !ok {
			return false
		}
		s.Path = xrayText(tr, "path")
		s.HostHeader = xrayText(tr, "host")
		s.XMode = xrayText(tr, "mode")
		switch s.XMode {
		case "", "auto", "packet-up", "stream-up", "stream-one":
		default:
			return false
		}
		if xrayNonempty(tr["extra"]) {
			extra, ok := xrayObject(tr["extra"])
			if !ok {
				return false
			}
			b, _ := json.Marshal(extra)
			s.XExtra = string(b)
		}
	default:
		return false
	}
	if s.Type == "shadowsocks" && (security != "none" || network != "tcp") {
		return false
	}
	return true
}
func xrayNodeValid(s *Server) bool {
	s.Host = strings.TrimSpace(s.Host)
	if strings.ContainsAny(s.Host, " \r\n\t/?#@") {
		return false
	}
	if s.Name == "" {
		s.Name = s.Host
	}
	s.ID = "validate"
	settings := DefaultSettings()
	settings.Servers = []Server{*s}
	settings.normalize()
	if settings.Validate() != nil {
		return false
	}
	*s = settings.Servers[0]
	s.ID = ""
	return true
}
func parseXrayOutbound(raw json.RawMessage, name string) ([]Server, int) {
	var node subscriptionXrayOutbound
	if json.Unmarshal(raw, &node) != nil {
		return nil, 1
	}
	switch node.Protocol {
	case "freedom", "blackhole", "dns":
		return nil, 0
	case "vless", "trojan", "shadowsocks":
	default:
		return nil, 1
	}
	if xrayNonempty(node.Proxy) {
		return nil, 1
	}
	if name == "" {
		name = node.Tag
	}
	prototype := Server{Type: node.Protocol, Name: name}
	if !xrayStreamServer(&prototype, node.Stream) {
		return nil, 1
	}
	var out []Server
	skipped := 0
	if node.Protocol == "vless" {
		for _, target := range node.Settings.Vnext {
			for _, user := range target.Users {
				if (user.Encryption != "" && user.Encryption != "none") || (user.Flow != "" && user.Flow != "xtls-rprx-vision") {
					skipped++
					continue
				}
				s := prototype
				s.Host = target.Address
				s.Port = target.Port
				s.UUID = user.ID
				s.Flow = user.Flow
				if xrayNodeValid(&s) {
					out = append(out, s)
					if len(out) > 500 {
						return nil, 1
					}
				} else {
					skipped++
				}
			}
		}
	} else {
		for _, target := range node.Settings.Servers {
			s := prototype
			s.Host = target.Address
			s.Port = target.Port
			s.Password = target.Password
			s.Method = target.Method
			if xrayNodeValid(&s) {
				out = append(out, s)
				if len(out) > 500 {
					return nil, 1
				}
			} else {
				skipped++
			}
		}
	}
	if len(out) == 0 && skipped == 0 {
		skipped = 1
	}
	return out, skipped
}
func parseXraySubscription(text string) ([]Server, int) {
	raw := json.RawMessage(text)
	var roots []json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(text), "[") {
		if json.Unmarshal(raw, &roots) != nil {
			return nil, 1
		}
	} else {
		roots = []json.RawMessage{raw}
	}
	if len(roots) > 500 {
		return nil, 1
	}
	var out []Server
	skipped := 0
	total := 0
	for _, root := range roots {
		var cfg struct {
			Remarks   string            `json:"remarks"`
			Name      string            `json:"name"`
			Outbounds []json.RawMessage `json:"outbounds"`
			Protocol  string            `json:"protocol"`
		}
		if json.Unmarshal(root, &cfg) != nil {
			skipped++
			continue
		}
		name := cfg.Remarks
		if name == "" {
			name = cfg.Name
		}
		if cfg.Protocol != "" && len(cfg.Outbounds) == 0 {
			cfg.Outbounds = []json.RawMessage{root}
		}
		if len(cfg.Outbounds) == 0 {
			skipped++
			continue
		}
		total += len(cfg.Outbounds)
		if total > 2000 {
			return nil, 1
		}
		for _, ob := range cfg.Outbounds {
			nodes, bad := parseXrayOutbound(ob, name)
			out = append(out, nodes...)
			skipped += bad
			if len(out) > 500 {
				return nil, 1
			}
		}
	}
	return out, skipped
}
