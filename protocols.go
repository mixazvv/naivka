package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Ссылки новых протоколов: hysteria2:// (hy2://), tuic://, trojan://, ss://, anytls://.
// Форматы — как у их официальных клиентов и v2rayN/NekoBox/Hiddify.

func linkPort(u *url.URL) (int, error) {
	p := u.Port()
	if p == "" {
		return 443, nil
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("неверный порт")
	}
	return n, nil
}

func linkName(u *url.URL, host string) string {
	if n := strings.TrimSpace(u.Fragment); n != "" {
		return n
	}
	return host
}

func truthy(v string) bool { return v == "1" || strings.EqualFold(v, "true") }

// userSecret — секрет из «пароль@хост» или «логин:пароль@хост».
func userSecret(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	if p, ok := u.User.Password(); ok {
		return u.User.Username() + ":" + p
	}
	return u.User.Username()
}

func parseHysteria2(u *url.URL) (Server, error) {
	if u.Hostname() == "" {
		return Server{}, errors.New("нет адреса сервера")
	}
	port, err := linkPort(u)
	if err != nil {
		return Server{}, err
	}
	q := u.Query()
	sv := Server{ID: newID(), Type: "hysteria2", Host: u.Hostname(), Port: port, Password: userSecret(u),
		SNI: q.Get("sni"), Insecure: truthy(q.Get("insecure")), Obfs: q.Get("obfs"), ObfsPassword: q.Get("obfs-password")}
	if v, err := strconv.Atoi(q.Get("upmbps")); err == nil {
		sv.UpMbps = v
	}
	if v, err := strconv.Atoi(q.Get("downmbps")); err == nil {
		sv.DownMbps = v
	}
	sv.Name = linkName(u, sv.Host)
	return sv, validateServerProto(sv)
}

func parseTUIC(u *url.URL) (Server, error) {
	if u.Hostname() == "" || u.User == nil {
		return Server{}, errors.New("нет UUID или адреса сервера")
	}
	port, err := linkPort(u)
	if err != nil {
		return Server{}, err
	}
	q := u.Query()
	pass, _ := u.User.Password()
	sv := Server{ID: newID(), Type: "tuic", Host: u.Hostname(), Port: port, UUID: u.User.Username(), Password: pass,
		SNI: q.Get("sni"), ALPN: q.Get("alpn"), Insecure: truthy(q.Get("allow_insecure")) || truthy(q.Get("insecure")),
		Congestion: orDefault(q.Get("congestion_control"), "bbr"), UDPRelay: orDefault(q.Get("udp_relay_mode"), "native")}
	sv.Name = linkName(u, sv.Host)
	return sv, validateServerProto(sv)
}

func parseTrojan(u *url.URL) (Server, error) {
	if u.Hostname() == "" || u.User == nil {
		return Server{}, errors.New("нет пароля или адреса сервера")
	}
	port, err := linkPort(u)
	if err != nil {
		return Server{}, err
	}
	q := u.Query()
	sec := orDefault(q.Get("security"), "tls")
	if sec != "tls" {
		return Server{}, fmt.Errorf("Trojan с шифрованием %q не поддерживается — только TLS", sec)
	}
	tr := orDefault(q.Get("type"), "tcp")
	if tr == "raw" {
		tr = "tcp"
	}
	sv := Server{ID: newID(), Type: "trojan", Host: u.Hostname(), Port: port, Password: userSecret(u), Security: "tls",
		SNI: orDefault(q.Get("sni"), q.Get("peer")), Fingerprint: orDefault(q.Get("fp"), "chrome"), ALPN: q.Get("alpn"),
		Transport: tr, Path: q.Get("path"), HostHeader: q.Get("host"), ServiceName: q.Get("serviceName"),
		Insecure: truthy(q.Get("allowInsecure")) || truthy(q.Get("insecure"))}
	sv.Name = linkName(u, sv.Host)
	return sv, validateServerProto(sv)
}

func parseAnyTLS(u *url.URL) (Server, error) {
	if u.Hostname() == "" || u.User == nil {
		return Server{}, errors.New("нет пароля или адреса сервера")
	}
	port, err := linkPort(u)
	if err != nil {
		return Server{}, err
	}
	q := u.Query()
	sv := Server{ID: newID(), Type: "anytls", Host: u.Hostname(), Port: port, Password: userSecret(u),
		SNI: q.Get("sni"), Fingerprint: orDefault(q.Get("fp"), "chrome"), ALPN: q.Get("alpn"), Insecure: truthy(q.Get("insecure"))}
	sv.Name = linkName(u, sv.Host)
	return sv, validateServerProto(sv)
}

func b64any(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("не base64")
}

// parseSS: SIP002 ss://base64(шифр:пароль)@хост:порт#имя, ss://шифр:пароль@хост:порт (для 2022)
// и старый ss://base64(шифр:пароль@хост:порт)#имя.
func parseSS(line string) (Server, error) {
	rest := line[len("ss://"):]
	name := ""
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		name, _ = url.PathUnescape(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		q, _ := url.ParseQuery(rest[i+1:])
		if q.Get("plugin") != "" {
			return Server{}, errors.New("плагины Shadowsocks (obfs, v2ray-plugin) не поддерживаются")
		}
		rest = rest[:i]
	}
	rest = strings.TrimSuffix(rest, "/")
	if !strings.Contains(rest, "@") { // старый формат: всё в base64
		b, err := b64any(rest)
		if err != nil {
			return Server{}, errors.New("ссылка ss:// не читается")
		}
		rest = string(b)
	}
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return Server{}, errors.New("ссылка ss:// не читается")
	}
	user, hostport := rest[:at], rest[at+1:]
	if dec, err := url.PathUnescape(user); err == nil {
		user = dec
	}
	if !strings.Contains(user, ":") {
		b, err := b64any(user)
		if err != nil {
			return Server{}, errors.New("не удалось прочитать шифр и пароль")
		}
		user = string(b)
	}
	method, pass, ok := strings.Cut(user, ":")
	if !ok {
		return Server{}, errors.New("не удалось прочитать шифр и пароль")
	}
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return Server{}, errors.New("нет адреса или порта сервера")
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return Server{}, errors.New("неверный порт")
	}
	sv := Server{ID: newID(), Type: "shadowsocks", Host: host, Port: port, Method: strings.ToLower(method), Password: pass, Name: strings.TrimSpace(name)}
	if sv.Name == "" {
		sv.Name = host
	}
	return sv, validateServerProto(sv)
}

// shareLinkProto — ссылка для новых протоколов; "" — протокол не из их числа.
func shareLinkProto(sv Server) string {
	name := url.PathEscape(sv.Name)
	host := net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port))
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	enc := func() string {
		if len(q) == 0 {
			return ""
		}
		return "?" + q.Encode()
	}
	secret := func(s string) string { return url.PathEscape(s) }
	switch sv.Type {
	case "hysteria2":
		set("sni", sv.SNI)
		set("obfs", sv.Obfs)
		set("obfs-password", sv.ObfsPassword)
		if sv.Insecure {
			q.Set("insecure", "1")
		}
		if sv.UpMbps > 0 {
			q.Set("upmbps", strconv.Itoa(sv.UpMbps))
		}
		if sv.DownMbps > 0 {
			q.Set("downmbps", strconv.Itoa(sv.DownMbps))
		}
		return "hysteria2://" + secret(sv.Password) + "@" + host + "/" + enc() + "#" + name
	case "tuic":
		set("sni", sv.SNI)
		set("alpn", sv.ALPN)
		q.Set("congestion_control", sv.Congestion)
		q.Set("udp_relay_mode", sv.UDPRelay)
		if sv.Insecure {
			q.Set("allow_insecure", "1")
		}
		return "tuic://" + url.UserPassword(sv.UUID, sv.Password).String() + "@" + host + enc() + "#" + name
	case "trojan":
		q.Set("security", "tls")
		set("sni", sv.SNI)
		set("fp", sv.Fingerprint)
		set("alpn", sv.ALPN)
		q.Set("type", sv.Transport)
		set("path", sv.Path)
		set("host", sv.HostHeader)
		set("serviceName", sv.ServiceName)
		if sv.Insecure {
			q.Set("allowInsecure", "1")
		}
		return "trojan://" + secret(sv.Password) + "@" + host + enc() + "#" + name
	case "shadowsocks":
		if strings.HasPrefix(sv.Method, "2022-") { // SIP002 для 2022: шифр и ключ открытым текстом, с кодированием
			return "ss://" + url.PathEscape(sv.Method) + ":" + url.PathEscape(sv.Password) + "@" + host + "#" + name
		}
		return "ss://" + base64.RawURLEncoding.EncodeToString([]byte(sv.Method+":"+sv.Password)) + "@" + host + "#" + name
	case "anytls":
		set("sni", sv.SNI)
		set("fp", sv.Fingerprint)
		if sv.Insecure {
			q.Set("insecure", "1")
		}
		return "anytls://" + secret(sv.Password) + "@" + host + "/" + enc() + "#" + name
	}
	return ""
}

// protoTLS — TLS для новых протоколов (у Hysteria2 и TUIC это TLS внутри QUIC: без uTLS).
func protoTLS(sv Server, utls bool, defaultALPN []string) M {
	t := M{"enabled": true, "server_name": orDefault(sv.SNI, sv.Host)}
	if utls {
		t["utls"] = M{"enabled": true, "fingerprint": orDefault(sv.Fingerprint, "chrome")}
	}
	if sv.ALPN != "" {
		t["alpn"] = strs(strings.Split(sv.ALPN, ","))
	} else if len(defaultALPN) > 0 {
		t["alpn"] = strs(defaultALPN)
	}
	if sv.Insecure {
		t["insecure"] = true
	}
	return t
}

// protoOutbound — outbound sing-box для новых протоколов; nil — протокол не из их числа.
func protoOutbound(sv Server, tag string) M {
	ob := M{"type": sv.Type, "tag": tag, "server": sv.Host, "server_port": sv.Port}
	switch sv.Type {
	case "hysteria2":
		ob["password"] = sv.Password
		if sv.UpMbps > 0 {
			ob["up_mbps"] = sv.UpMbps
		}
		if sv.DownMbps > 0 {
			ob["down_mbps"] = sv.DownMbps
		}
		if sv.Obfs == "salamander" {
			ob["obfs"] = M{"type": "salamander", "password": sv.ObfsPassword}
		}
		ob["tls"] = protoTLS(sv, false, nil)
	case "tuic":
		ob["uuid"] = sv.UUID
		ob["password"] = sv.Password
		ob["congestion_control"] = sv.Congestion
		ob["udp_relay_mode"] = sv.UDPRelay
		ob["tls"] = protoTLS(sv, false, []string{"h3"})
	case "trojan":
		ob["password"] = sv.Password
		ob["tls"] = protoTLS(sv, true, nil)
		if tr := transportFor(sv); tr != nil {
			ob["transport"] = tr
		}
	case "shadowsocks":
		ob["method"] = sv.Method
		ob["password"] = sv.Password
	case "anytls":
		ob["password"] = sv.Password
		ob["tls"] = protoTLS(sv, true, nil)
	default:
		return nil
	}
	return ob
}

// protoTitle — короткое имя протокола для меню в трее.
func protoTitle(sv Server) string {
	switch sv.Type {
	case "vless":
		if sv.Security == "reality" {
			return "Reality"
		}
		return "VLESS"
	case "hysteria2":
		return "Hysteria2"
	case "tuic":
		return "TUIC"
	case "trojan":
		return "Trojan"
	case "shadowsocks":
		return "Shadowsocks"
	case "anytls":
		return "AnyTLS"
	}
	return "Naive"
}
