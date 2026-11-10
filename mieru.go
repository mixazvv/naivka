package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Only remote endpoint settings are imported. Local listeners, RPC, upstream
// dialers and routing from a subscription must never control Naivka's host.
type MieruBinding struct {
	Port      int    `json:"port,omitempty"`
	PortRange string `json:"portRange,omitempty"`
	Protocol  string `json:"protocol"`
}
type MieruOptions struct {
	Bindings     []MieruBinding `json:"portBindings"`
	MTU          int            `json:"mtu,omitempty"`
	Multiplexing string         `json:"multiplexing,omitempty"`
	Handshake    string         `json:"handshakeMode,omitempty"`
}

func validateMieru(s Server) error {
	if s.Host == "" || strings.ContainsAny(s.Host, " /\\\r\n\t") || s.Username == "" || s.Password == "" {
		return errors.New("Mieru: нужны адрес, имя пользователя и пароль")
	}
	if s.Mieru == nil || len(s.Mieru.Bindings) == 0 || len(s.Mieru.Bindings) > 64 {
		return errors.New("Mieru: укажите порты и транспорт TCP или UDP")
	}
	for _, b := range s.Mieru.Bindings {
		if b.Protocol != "TCP" && b.Protocol != "UDP" {
			return errors.New("Mieru: транспорт должен быть TCP или UDP")
		}
		if b.PortRange != "" {
			parts := strings.Split(b.PortRange, "-")
			if len(parts) != 2 || b.Port != 0 {
				return errors.New("Mieru: неверный диапазон портов")
			}
			a, e1 := strconv.Atoi(parts[0])
			z, e2 := strconv.Atoi(parts[1])
			if e1 != nil || e2 != nil || a < 1 || z > 65535 || a > z {
				return errors.New("Mieru: неверный диапазон портов")
			}
		} else if b.Port < 1 || b.Port > 65535 {
			return errors.New("Mieru: неверный порт")
		}
	}
	m := s.Mieru
	if m.MTU != 0 && (m.MTU < 1280 || m.MTU > 1400) {
		return errors.New("Mieru: MTU должен быть от 1280 до 1400")
	}
	switch m.Multiplexing {
	case "", "MULTIPLEXING_DEFAULT", "MULTIPLEXING_OFF", "MULTIPLEXING_LOW", "MULTIPLEXING_MIDDLE", "MULTIPLEXING_HIGH":
	default:
		return errors.New("Mieru: неизвестный режим мультиплексирования")
	}
	switch m.Handshake {
	case "", "HANDSHAKE_DEFAULT", "HANDSHAKE_STANDARD", "HANDSHAKE_NO_WAIT":
	default:
		return errors.New("Mieru: неизвестный режим handshake")
	}
	return nil
}

func parseMieru(u *url.URL) (Server, error) {
	s := Server{ID: newID(), Type: "mieru", Host: u.Hostname(), Mieru: &MieruOptions{}}
	if u.User != nil {
		s.Username = u.User.Username()
		s.Password, _ = u.User.Password()
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return s, errors.New("Mieru: повреждены параметры ссылки")
	}
	for k := range q {
		switch k {
		case "profile", "mtu", "multiplexing", "handshake-mode", "port", "protocol":
		default:
			return s, fmt.Errorf("Mieru: параметр %q пока не поддерживается", k)
		}
	}
	s.Name = orDefault(q.Get("profile"), s.Host)
	if u.Port() != "" {
		return s, errors.New("Mieru: порт должен быть в параметре port")
	}
	if q.Get("mtu") != "" {
		s.Mieru.MTU, err = strconv.Atoi(q.Get("mtu"))
		if err != nil {
			return s, errors.New("Mieru: неверный MTU")
		}
	}
	s.Mieru.Multiplexing, s.Mieru.Handshake = q.Get("multiplexing"), q.Get("handshake-mode")
	if len(q["port"]) != len(q["protocol"]) {
		return s, errors.New("Mieru: каждому порту нужен транспорт")
	}
	for i, p := range q["port"] {
		b := MieruBinding{Protocol: q["protocol"][i]}
		if strings.Contains(p, "-") {
			b.PortRange = p
		} else {
			b.Port, err = strconv.Atoi(p)
			if err != nil {
				return s, errors.New("Mieru: неверный порт")
			}
		}
		s.Mieru.Bindings = append(s.Mieru.Bindings, b)
	}
	if len(s.Mieru.Bindings) > 0 {
		s.Port = s.Mieru.Bindings[0].Port
		if s.Port == 0 {
			s.Port, _ = strconv.Atoi(strings.Split(s.Mieru.Bindings[0].PortRange, "-")[0])
		}
	}
	return s, validateMieru(s)
}

func shareMieru(s Server) string {
	if validateMieru(s) != nil {
		return ""
	}
	host := s.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u := url.URL{Scheme: "mierus", Host: host, User: url.UserPassword(s.Username, s.Password)}
	q := url.Values{"profile": {s.Name}}
	m := s.Mieru
	if m.MTU != 0 {
		q.Set("mtu", strconv.Itoa(m.MTU))
	}
	if m.Multiplexing != "" {
		q.Set("multiplexing", m.Multiplexing)
	}
	if m.Handshake != "" {
		q.Set("handshake-mode", m.Handshake)
	}
	for _, b := range m.Bindings {
		p := b.PortRange
		if p == "" {
			p = strconv.Itoa(b.Port)
		}
		q.Add("port", p)
		q.Add("protocol", b.Protocol)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func buildMieruConfig(s Server, port int) (M, error) {
	if err := validateMieru(s); err != nil {
		return nil, err
	}
	endpoint := M{"portBindings": s.Mieru.Bindings}
	if net.ParseIP(s.Host) != nil {
		endpoint["ipAddress"] = s.Host
	} else {
		endpoint["domainName"] = s.Host
	}
	p := M{"profileName": "naivka", "user": M{"name": s.Username, "password": s.Password}, "servers": []any{endpoint}}
	if s.Mieru.MTU != 0 {
		p["mtu"] = s.Mieru.MTU
	}
	if s.Mieru.Multiplexing != "" {
		p["multiplexing"] = M{"level": s.Mieru.Multiplexing}
	}
	if s.Mieru.Handshake != "" {
		p["handshakeMode"] = s.Mieru.Handshake
	}
	return M{"profiles": []any{p}, "activeProfile": "naivka", "socks5Port": port, "rpcPort": 0, "socks5ListenLAN": false, "loggingLevel": "ERROR", "advancedSettings": M{"noCheckUpdate": true}}, nil
}

// Recognize JSON without interpreting unrelated Xray subscriptions as Mieru.
func parseMieruJSON(text string) ([]Server, int, bool) {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &root) != nil || root["profiles"] == nil {
		return nil, 0, false
	}
	var profiles []struct {
		Name string `json:"profileName"`
		User struct {
			Name     string `json:"name"`
			Password string `json:"password"`
			Hash     string `json:"hashedPassword"`
		} `json:"user"`
		Servers []struct {
			IP       string         `json:"ipAddress"`
			Domain   string         `json:"domainName"`
			Bindings []MieruBinding `json:"portBindings"`
		} `json:"servers"`
		MTU          int `json:"mtu"`
		Multiplexing struct {
			Level string `json:"level"`
		} `json:"multiplexing"`
		Handshake string          `json:"handshakeMode"`
		Pattern   json.RawMessage `json:"trafficPattern"`
		Dialer    json.RawMessage `json:"dialer"`
	}
	if json.Unmarshal(root["profiles"], &profiles) != nil || len(profiles) > 2000 {
		return nil, 1, true
	}
	var out []Server
	skipped := 0
	for _, p := range profiles {
		if xrayNonempty(p.Pattern) || xrayNonempty(p.Dialer) || p.User.Hash != "" {
			skipped++
			continue
		}
		for _, e := range p.Servers {
			s := Server{ID: newID(), Type: "mieru", Name: p.Name, Host: orDefault(e.Domain, e.IP), Username: p.User.Name, Password: p.User.Password, Mieru: &MieruOptions{Bindings: e.Bindings, MTU: p.MTU, Multiplexing: p.Multiplexing.Level, Handshake: p.Handshake}}
			if validateMieru(s) != nil || len(out) >= 2000 {
				skipped++
				continue
			}
			s.Port = e.Bindings[0].Port
			if s.Port == 0 {
				s.Port, _ = strconv.Atoi(strings.Split(e.Bindings[0].PortRange, "-")[0])
			}
			out = append(out, s)
		}
	}
	return out, skipped, true
}
