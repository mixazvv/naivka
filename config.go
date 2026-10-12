package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
)

type M = map[string]any

const (
	tagDirect    = "direct"
	tagProxy     = "proxy"
	tagAuto      = "auto"
	tagDNSDirect = "dns-direct"
	tagDNSRemote = "dns-remote"
)

// Наборы правил: SagerNet (гео-базы) и runetfreedom (списки блокировок РФ).
var ruleSetURL = map[string]string{
	"geoip-ru":            "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-ru.srs",
	"geosite-category-ru": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ru.srs",
	"ru-blocked":          "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geosite/geosite-ru-blocked.srs",
	"ru-blocked-ip":       "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geoip/geoip-ru-blocked.srs",
	"ru-inside":           "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geosite/geosite-ru-available-only-inside.srs",
	"ads":                 "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs",
}

// Twitch без рекламы. Twitch вставляет рекламу прямо в плейлист трансляции, и решает это
// по стране, из которой пришёл запрос плейлиста. Поэтому запросы плейлистов (usher и
// video-weaver; сами куски видео идут как обычно) уходят через прокси проекта TTV LOL PRO
// (github.com/younesaassila/ttv-lol-pro) — они стоят там, где Twitch рекламу не показывает.
// Прокси видит только имя сайта: соединение до Twitch зашифровано насквозь.
const (
	tagTwitch     = "twitch-adfree"
	twitchProxyPt = 2023
)

var twitchProxies = []string{"chromium.api.cdn-perfprod.com", "firefox.api.cdn-perfprod.com"}

var twitchPlaylistRule = M{
	"domain": []any{"usher.ttvnw.net"},
	"domain_regex": []any{
		`^[a-z0-9-]+\.playlist\.(live-video|ttvnw)\.net$`,
		`^video-weaver\.[a-z0-9-]+\.hls\.ttvnw\.net$`,
	},
}

// Российские доменные зоны: .ru, .рф, .su, .рус
var ruZones = []any{"ru", "xn--p1ai", "su", "xn--p1acf"}

type ConfigEnv struct {
	APIPort   int
	Secret    string
	CachePath string
	// Служебные входы для проверки IP: всё, что пришло в них, идёт строго
	// через сервер или строго напрямую — независимо от правил.
	ProbeDirectPort int
	ProbeProxyPort  int
	// NoNaive — ядро собрано без Naive (сборка sing-box для Windows 7):
	// Naive-серверы пропускаются, работают только VLESS+Reality.
	NoNaive bool
	// Mobile — профиль для телефона (sing-box для iOS/Android, Karing):
	// только TUN, без локального API, служебных входов и правил по программам.
	Mobile bool
	// ZapretActive — zapret запущен и справляется: правила «через zapret» идут напрямую
	// (обход DPI делает zapret), иначе — через сервер.
	ZapretActive bool
	// ZapretDomains — домены из списков zapret, которые пускаем напрямую.
	ZapretDomains []string
	// TgWsProxy — tg-ws-proxy включён: его соединения всегда напрямую.
	TgWsProxy bool
	// WLServers — серверы запасной подписки (группа «wl»).
	WLServers []Server
	// ServerProbes — служебные входы для теста скорости: тег сервера или группы → порт.
	ServerProbes map[string]int
	// XrayPorts — серверы xHTTP: тег → локальный вход Xray. Без входа сервер пропускается.
	XrayPorts map[string]int
}

func serverTag(sv Server) string { return "s-" + sv.ID }

func isIP(h string) bool {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return net.ParseIP(strings.Trim(h, "[]")) != nil
}

func strs(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

func BuildConfig(s *Settings, env ConfigEnv) (M, error) {
	if len(s.Servers) == 0 {
		return nil, errors.New("Добавьте хотя бы один сервер")
	}
	used := map[string]bool{}
	rs := func(tags ...string) []any {
		for _, t := range tags {
			used[t] = true
		}
		return strs(tags)
	}

	// ---------- outbounds ----------
	outbounds := []any{M{"type": "direct", "tag": tagDirect}}
	var serverTags []any
	selected := tagAuto
	for _, sv := range s.Servers {
		if sv.Type == "naive" && env.NoNaive {
			continue
		}
		sni := sv.SNI
		if sni == "" {
			sni = sv.Host
		}
		if sv.Type == "vless" {
			if sv.Transport == "xhttp" {
				p := env.XrayPorts[serverTag(sv)]
				if p == 0 {
					continue // нет Xray — сервер xHTTP не может работать
				}
				outbounds = append(outbounds, xrayBridge(serverTag(sv), p))
			} else {
				outbounds = append(outbounds, vlessOutbound(sv, serverTag(sv)))
			}
			serverTags = append(serverTags, serverTag(sv))
			if sv.ID == s.Selected {
				selected = serverTag(sv)
			}
			continue
		}
		ob := M{
			"type":        "naive",
			"tag":         serverTag(sv),
			"server":      sv.Host,
			"server_port": sv.Port,
			"tls":         M{"enabled": true, "server_name": sni},
		}
		if sv.Username != "" {
			ob["username"] = sv.Username
		}
		if sv.Password != "" {
			ob["password"] = sv.Password
		}
		if sv.QUIC {
			ob["quic"] = true
		}
		if sv.UoT {
			ob["udp_over_tcp"] = true
		}
		outbounds = append(outbounds, ob)
		serverTags = append(serverTags, serverTag(sv))
		if sv.ID == s.Selected {
			selected = serverTag(sv)
		}
	}
	var wlTags []any
	for _, sv := range env.WLServers {
		tag := "w-" + sv.ID
		if sv.Transport == "xhttp" {
			p := env.XrayPorts[tag]
			if p == 0 {
				continue
			}
			outbounds = append(outbounds, xrayBridge(tag, p))
		} else {
			outbounds = append(outbounds, vlessOutbound(sv, tag))
		}
		wlTags = append(wlTags, tag)
	}
	if len(serverTags) == 0 && len(wlTags) == 0 {
		return nil, errors.New("Это ядро sing-box не поддерживает Naive. Добавьте сервер VLESS+Reality")
	}
	selOut := []any{}
	if len(serverTags) > 0 {
		outbounds = append(outbounds, M{"type": "urltest", "tag": tagAuto, "outbounds": serverTags,
			"url": s.TestURL, "interval": s.TestInterval, "tolerance": 100})
		selOut = append(append(selOut, tagAuto), serverTags...)
	}
	if len(wlTags) > 0 {
		outbounds = append(outbounds, M{"type": "urltest", "tag": "wl", "outbounds": wlTags,
			"url": s.TestURL, "interval": "5m", "tolerance": 150, "idle_timeout": "30m"})
		selOut = append(append(selOut, "wl"), wlTags...) // можно выбрать и конкретный сервер подписки
		if s.Selected == "wl" || len(serverTags) == 0 {
			selected = "wl"
		}
		for _, t := range wlTags {
			if t == s.Selected {
				selected = s.Selected
			}
		}
	} else if selected == "wl" {
		selected = tagAuto
	}
	outbounds = append(outbounds, M{"type": "selector", "tag": tagProxy, "outbounds": selOut,
		"default": selected, "interrupt_exist_connections": true})

	// UDP (голос Discord, игры): Naive его не передаёт, VLESS — да.
	udpTarget := ""
	if s.UDPProxy && !env.Mobile {
		var vl []any
		for _, sv := range s.Servers {
			if sv.Type == "vless" {
				vl = append(vl, serverTag(sv))
			}
		}
		switch {
		case len(vl) > 0:
			outbounds = append(outbounds, M{"type": "urltest", "tag": "udp-auto", "outbounds": vl,
				"url": s.TestURL, "interval": s.TestInterval, "tolerance": 100})
			udpTarget = "udp-auto"
		case len(wlTags) > 0:
			udpTarget = "wl"
		}
	}
	if s.TwitchAdFree {
		var tw []any
		for i, host := range twitchProxies {
			tag := fmt.Sprintf("%s-%d", tagTwitch, i+1)
			outbounds = append(outbounds, M{"type": "http", "tag": tag, "server": host, "server_port": twitchProxyPt})
			tw = append(tw, tag)
		}
		// Проверяем прокси запросом к самому Twitch: на чужие сайты они могут и не пускать.
		outbounds = append(outbounds, M{"type": "urltest", "tag": tagTwitch, "outbounds": tw,
			"url": "https://usher.ttvnw.net/", "interval": "10m", "tolerance": 150, "idle_timeout": "1h"})
	}
	if t := strings.TrimSpace(s.ExtraOutbounds); t != "" {
		var extra []any
		if err := json.Unmarshal([]byte(t), &extra); err != nil {
			return nil, fmt.Errorf("Дополнительные outbounds: %v", err)
		}
		outbounds = append(outbounds, extra...)
	}

	// ---------- inbounds ----------
	inbounds := []any{M{
		"type":        "mixed",
		"tag":         "mixed-in",
		"listen":      "127.0.0.1",
		"listen_port": s.MixedPort,
	}}
	if env.ProbeDirectPort > 0 && env.ProbeProxyPort > 0 {
		inbounds = append(inbounds,
			M{"type": "mixed", "tag": "probe-direct", "listen": "127.0.0.1", "listen_port": env.ProbeDirectPort},
			M{"type": "mixed", "tag": "probe-proxy", "listen": "127.0.0.1", "listen_port": env.ProbeProxyPort},
		)
	}
	if env.Mobile {
		inbounds = []any{M{
			"type":         "tun",
			"tag":          "tun-in",
			"address":      []any{"172.19.0.1/30"},
			"auto_route":   true,
			"strict_route": true,
		}}
	} else if s.Mode == "tun" {
		inbounds = append([]any{M{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "Naivka",
			"address":        []any{"172.19.0.1/30"},
			"mtu":            9000,
			"auto_route":     true,
			"strict_route":   true,
			"stack":          "mixed",
		}}, inbounds...)
	}
	for tag, port := range env.ServerProbes {
		inbounds = append(inbounds, M{"type": "mixed", "tag": "probe-" + tag, "listen": "127.0.0.1", "listen_port": port})
	}
	if s.Share.Enabled && !env.Mobile {
		in := M{"type": "mixed", "tag": "lan-in", "listen": "0.0.0.0", "listen_port": s.Share.Port}
		if s.Share.Auth {
			in["users"] = []any{M{"username": s.Share.Username, "password": s.Share.Password}}
		}
		inbounds = append(inbounds, in)
	}

	// ---------- route ----------
	rules := []any{M{"action": "sniff"}}
	if s.Share.Enabled && !env.Mobile {
		// раздача — только для устройств домашней сети
		rules = append(rules, M{"inbound": []any{"lan-in"}, "source_ip_is_private": false, "action": "reject"})
	}
	probeTags := make([]string, 0, len(env.ServerProbes))
	for tag := range env.ServerProbes {
		probeTags = append(probeTags, tag)
	}
	sort.Strings(probeTags)
	for _, tag := range probeTags {
		rules = append(rules, M{"inbound": []any{"probe-" + tag}, "outbound": tag})
	}
	if env.ProbeDirectPort > 0 && env.ProbeProxyPort > 0 {
		rules = append(rules,
			M{"inbound": []any{"probe-proxy"}, "outbound": tagProxy},
			M{"inbound": []any{"probe-direct"}, "outbound": tagDirect},
		)
	}
	rules = append(rules,
		M{"protocol": "dns", "action": "hijack-dns"},
		M{"ip_is_private": true, "outbound": tagDirect},
	)
	if s.TwitchAdFree {
		// HTTP-прокси передаёт только TCP: QUIC к плейлистам отклоняем, браузер повторит по TCP.
		udp, tcp := M{"network": "udp", "action": "reject"}, M{"outbound": tagTwitch}
		for k, v := range twitchPlaylistRule {
			udp[k], tcp[k] = v, v
		}
		rules = append(rules, udp, tcp)
	}
	var dnsRules []any

	// Пользовательские правила: сначала блокировки, потом «напрямую», потом «через сервер».
	if env.TgWsProxy && !env.Mobile {
		// tg-ws-proxy сам ходит к серверам Telegram — через наш сервер ему не нужно.
		rules = append(rules, M{"process_name": []any{"TgWsProxy.exe", "TgWsProxy_windows.exe"}, "outbound": tagDirect})
	}
	for _, action := range []string{"block", "direct", "zapret", "proxy"} {
		var domains, ips, procs []string
		for _, r := range s.Rules {
			if r.Action != action {
				continue
			}
			switch r.Kind {
			case "domain":
				domains = append(domains, strings.TrimPrefix(strings.ToLower(r.Value), "."))
			case "ip":
				v := r.Value
				if !strings.Contains(v, "/") {
					if strings.Contains(v, ":") {
						v += "/128"
					} else {
						v += "/32"
					}
				}
				ips = append(ips, v)
			case "process":
				if !env.Mobile { // на телефоне правил по программам нет
					procs = append(procs, r.Value)
				}
			}
		}
		target := func(m M) M {
			switch action {
			case "block":
				m["action"] = "reject"
			case "direct":
				m["outbound"] = tagDirect
			case "zapret":
				if env.ZapretActive {
					m["outbound"] = tagDirect
				} else {
					m["outbound"] = tagProxy
				}
			default:
				m["outbound"] = tagProxy
			}
			return m
		}
		if len(procs) > 0 {
			rules = append(rules, target(M{"process_name": strs(procs)}))
		}
		if len(domains) > 0 {
			rules = append(rules, target(M{"domain_suffix": strs(domains)}))
			switch action {
			case "block":
				dnsRules = append(dnsRules, M{"domain_suffix": strs(domains), "action": "reject"})
			case "direct":
				dnsRules = append(dnsRules, M{"domain_suffix": strs(domains), "server": tagDNSDirect})
			default:
				dnsRules = append(dnsRules, M{"domain_suffix": strs(domains), "server": tagDNSRemote})
			}
		}
		if len(ips) > 0 {
			rules = append(rules, target(M{"ip_cidr": strs(ips)}))
		}
	}

	if s.AdBlock {
		rules = append(rules, M{"rule_set": rs("ads"), "action": "reject"})
		dnsRules = append([]any{M{"rule_set": rs("ads"), "action": "reject"}}, dnsRules...)
	}
	if env.ZapretActive && len(env.ZapretDomains) > 0 && !env.Mobile {
		rules = append(rules, M{"domain_suffix": strs(env.ZapretDomains), "outbound": tagDirect})
	}

	if t := strings.TrimSpace(s.ExtraRules); t != "" {
		var extra []any
		if err := json.Unmarshal([]byte(t), &extra); err != nil {
			return nil, fmt.Errorf("Дополнительные правила: %v", err)
		}
		rules = append(rules, extra...)
	}

	// Naive не умеет UDP (кроме UoT на sing-box-сервере), поэтому QUIC отклоняем —
	// браузер сам перейдёт на обычный HTTPS поверх TCP, а он уже пойдёт через сервер.
	if s.BlockQUIC {
		rules = append(rules, M{"network": "udp", "port": 443, "action": "reject"})
	}
	if s.UDPDirect && udpTarget == "" {
		rules = append(rules, M{"network": "udp", "outbound": tagDirect})
	}

	if s.RuInsideDirect {
		rules = append(rules, M{"rule_set": rs("ru-inside"), "outbound": tagDirect})
		dnsRules = append(dnsRules, M{"rule_set": rs("ru-inside"), "server": tagDNSDirect})
	}

	final, dnsFinal := tagProxy, tagDNSRemote
	switch s.Routing {
	case "ru-direct":
		// Заблокированное — через сервер, даже если это .ru
		rules = append(rules,
			M{"rule_set": rs("ru-blocked"), "outbound": tagProxy},
			M{"rule_set": rs("ru-blocked-ip"), "outbound": tagProxy},
			M{"domain_suffix": ruZones, "outbound": tagDirect},
			M{"rule_set": rs("geosite-category-ru"), "outbound": tagDirect},
			M{"rule_set": rs("geoip-ru"), "outbound": tagDirect},
		)
		dnsRules = append(dnsRules,
			M{"rule_set": rs("ru-blocked"), "server": tagDNSRemote},
			M{"domain_suffix": ruZones, "server": tagDNSDirect},
			M{"rule_set": rs("geosite-category-ru"), "server": tagDNSDirect},
		)
	case "blocked-only":
		rules = append(rules,
			M{"rule_set": rs("ru-blocked"), "outbound": tagProxy},
			M{"rule_set": rs("ru-blocked-ip"), "outbound": tagProxy},
		)
		dnsRules = append(dnsRules, M{"rule_set": rs("ru-blocked"), "server": tagDNSRemote})
		final, dnsFinal = tagDirect, tagDNSDirect
	case "global":
		// всё через сервер
	}

	var ruleSets []any
	var names []string
	for t := range used {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		ruleSets = append(ruleSets, M{
			"type":            "remote",
			"tag":             t,
			"format":          "binary",
			"url":             ruleSetURL[t],
			"update_interval": "1d",
		})
	}

	if udpTarget != "" {
		var out []any
		for _, r := range rules {
			if m, ok := r.(M); ok && m["outbound"] == tagProxy && m["network"] == nil {
				c := M{}
				for k, v := range m {
					c[k] = v
				}
				c["network"] = "udp"
				c["outbound"] = udpTarget
				out = append(out, c)
			}
			out = append(out, r)
		}
		rules = out
		if final == tagProxy {
			rules = append(rules, M{"network": "udp", "outbound": udpTarget})
		} else if s.UDPDirect {
			rules = append(rules, M{"network": "udp", "outbound": tagDirect})
		}
	}

	route := M{
		"rules":                   rules,
		"final":                   final,
		"auto_detect_interface":   true,
		"default_domain_resolver": tagDNSDirect,
	}
	// Списки правил качаются HTTP-клиентом из http_clients (sing-box 1.14+):
	// напрямую или через сервер, если GitHub у провайдера открывается плохо.
	rulesClient := M{"tag": "rules"}
	if s.RuleSetViaProxy {
		rulesClient["detour"] = tagProxy
	}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
		route["default_http_client"] = "rules"
	}

	// ---------- dns ----------
	var dnsDirect M
	if s.DNSDirect == "local" {
		dnsDirect = M{"type": "local", "tag": tagDNSDirect}
	} else {
		dnsDirect = M{"type": "udp", "tag": tagDNSDirect, "server": s.DNSDirect}
	}
	host, path, err := parseDoH(s.DNSRemote)
	if err != nil {
		return nil, err
	}
	dnsRemote := M{"type": "https", "tag": tagDNSRemote, "detour": tagProxy}
	if h, p, err := net.SplitHostPort(host); err == nil {
		dnsRemote["server"] = h
		var port int
		fmt.Sscanf(p, "%d", &port)
		dnsRemote["server_port"] = port
	} else {
		dnsRemote["server"] = host
	}
	if path != "" && path != "/dns-query" {
		dnsRemote["path"] = path
	}
	if !isIP(host) {
		dnsRemote["domain_resolver"] = tagDNSDirect
	}
	dns := M{
		"servers": []any{dnsDirect, dnsRemote},
		"final":   dnsFinal,
	}
	if len(dnsRules) > 0 {
		dns["rules"] = dnsRules
	}
	if s.IPv4Only {
		dns["strategy"] = "ipv4_only"
	}

	cfg := M{
		"log":          M{"level": s.LogLevel, "timestamp": true},
		"http_clients": []any{rulesClient},
		"dns":          dns,
		"inbounds":     inbounds,
		"outbounds":    outbounds,
		"route":        route,
		"experimental": M{
			"clash_api": M{
				"external_controller": fmt.Sprintf("127.0.0.1:%d", env.APIPort),
				"secret":              env.Secret,
			},
			"cache_file": M{"enabled": true, "path": env.CachePath},
		},
	}

	if env.Mobile {
		cfg["experimental"] = M{"cache_file": M{"enabled": true}}
		cfg["log"] = M{"level": "warn"}
	}

	if t := strings.TrimSpace(s.Override); t != "" && !env.Mobile {
		var ov M
		if err := json.Unmarshal([]byte(t), &ov); err != nil {
			return nil, fmt.Errorf("Ручные правки конфига: %v", err)
		}
		deepMerge(cfg, ov)
	}
	return cfg, nil
}

// deepMerge: объекты сливаются рекурсивно, всё остальное (включая массивы) заменяется,
// null удаляет ключ.
func deepMerge(dst, src M) {
	for k, v := range src {
		if v == nil {
			delete(dst, k)
			continue
		}
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				deepMerge(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}

// vlessOutbound — VLESS с Reality, TLS или без шифрования, поверх TCP, WebSocket, gRPC или HTTPUpgrade.
func vlessOutbound(sv Server, tag string) M {
	ob := M{"type": "vless", "tag": tag, "server": sv.Host, "server_port": sv.Port, "uuid": sv.UUID}
	if sv.Flow != "" {
		ob["flow"] = sv.Flow
	}
	sni := sv.SNI
	if sni == "" {
		sni = sv.Host
	}
	switch sv.Security {
	case "reality", "":
		ob["tls"] = M{"enabled": true, "server_name": sni,
			"utls":    M{"enabled": true, "fingerprint": orDefault(sv.Fingerprint, "chrome")},
			"reality": M{"enabled": true, "public_key": sv.PublicKey, "short_id": sv.ShortID}}
	case "tls":
		t := M{"enabled": true, "server_name": sni, "utls": M{"enabled": true, "fingerprint": orDefault(sv.Fingerprint, "chrome")}}
		if sv.ALPN != "" {
			t["alpn"] = strs(strings.Split(sv.ALPN, ","))
		}
		if sv.Insecure {
			t["insecure"] = true
		}
		ob["tls"] = t
	}
	switch sv.Transport {
	case "ws":
		tr := M{"type": "ws", "path": orDefault(sv.Path, "/")}
		if sv.HostHeader != "" {
			tr["headers"] = M{"Host": sv.HostHeader}
		}
		ob["transport"] = tr
	case "httpupgrade":
		tr := M{"type": "httpupgrade", "path": orDefault(sv.Path, "/")}
		if sv.HostHeader != "" {
			tr["host"] = sv.HostHeader
		}
		ob["transport"] = tr
	case "grpc":
		ob["transport"] = M{"type": "grpc", "service_name": sv.ServiceName}
	}
	return ob
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// xrayBridge — сервер xHTTP: sing-box отдаёт трафик в локальный вход Xray, а тот — на сервер.
func xrayBridge(tag string, port int) M {
	return M{"type": "socks", "tag": tag, "server": "127.0.0.1", "server_port": port, "version": "5"}
}
