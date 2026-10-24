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
	// tagZapretOut — куда идут сайты zapret: напрямую (обход делает zapret) или через сервер,
	// если zapret не справляется. Переключается на ходу, без перезапуска ядра, и рвёт только
	// соединения с этими сайтами — соединения через сервер не трогает.
	tagZapretOut = "zapret-out"
)

// zapretDNS — через какой DNS узнавать адреса сайтов zapret: напрямую, пока zapret справляется.
const (
	tagDNSYouTube = "dns-youtube"
	googleDNSIP   = "8.8.8.8"
)

// youtubeDomains — сайты YouTube, чьи адреса узнаём у DNS Google (ConfigEnv.YouTubeDNS).
var youtubeDomains = []any{"googlevideo.com", "youtube.com", "ytimg.com", "youtu.be", "ggpht.com", "youtube-nocookie.com", "youtubei.googleapis.com"}

func zapretDNS(env ConfigEnv) string {
	if env.ZapretActive {
		return tagDNSDirect
	}
	return tagDNSRemote
}

// Наборы правил: SagerNet (гео-базы) и runetfreedom (списки блокировок РФ).
var ruleSetURL = map[string]string{
	"geoip-ru":            "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-ru.srs",
	"geosite-category-ru": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ru.srs",
	"ru-blocked":          "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geosite/geosite-ru-blocked.srs",
	"ru-blocked-ip":       "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geoip/geoip-ru-blocked.srs",
	"ru-inside":           "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geosite/geosite-ru-available-only-inside.srs",
	"ads":                 "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs",
	// Сервисы, которые сами не пускают пользователей из России (ИИ, магазины, облака).
	"geoblock": "https://github.com/itdoginfo/allow-domains/releases/latest/download/geoblock.srs",
}

// Российские доменные зоны: .ru, .рф, .su, .рус
var ruZones = []any{"ru", "xn--p1ai", "su", "xn--p1acf"}

// downloadDomains — серверы загрузки игр и обновлений, открытые для России. Через сервер
// большая закачка занимает весь канал до него: пинг растёт, остальной интернет тормозит.
// Поэтому в режиме «Россия напрямую» они сразу идут напрямую — без первой проверки через сервер.
var downloadDomains = []any{
	// Steam: контент, CDN, картинки магазина
	"steamcontent.com", "steamserver.net", "steampipe.akamaized.net", "steamcdn-a.akamaihd.net", "steamstatic.com",
	// Epic Games
	"download.epicgames.com", "download2.epicgames.com", "download3.epicgames.com", "download4.epicgames.com",
	"fastly-download.epicgames.com", "epicgames-download1.akamaized.net",
	// обновления Windows и загрузки Microsoft Store
	"windowsupdate.com", "delivery.mp.microsoft.com",
}

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
	// ZapretActive — zapret запущен и справляется: правила «через zapret» идут напрямую
	// (обход DPI делает zapret), иначе — через сервер.
	ZapretActive bool
	// ZapretRoute — сайты zapret идут через переключатель zapret-out (zapret включён).
	ZapretRoute bool
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
	// InlineLists — скачанные текстовые списки по ссылке: id → домены и IP.
	InlineLists map[string]*inlineList
	// DNSFallback — прямой DoH/DoT перестал отвечать: вместо него обычный DNS (dnsguard.go).
	DNSFallback bool
	// YouTubeDNS — адреса YouTube узнавать у DNS Google напрямую (dnsguard.go): DNS Яндекса
	// отвечает «нет такого адреса» на часть видеосерверов, и видео подвисает.
	YouTubeDNS bool
	// SmartDir — папка со списками автоопределения (smart.go); "" — автоопределение выключено.
	SmartDir string
	// UserRulesDir — папка со списками своих правил (userrules.go). "" — правила прямо в конфиге
	// (превью конфига и профиль для телефона).
	UserRulesDir string
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
		if ob := protoOutbound(sv, serverTag(sv)); ob != nil {
			outbounds = append(outbounds, ob)
			serverTags = append(serverTags, serverTag(sv))
			if sv.ID == s.Selected {
				selected = serverTag(sv)
			}
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
		return nil, errors.New("Это ядро sing-box не поддерживает Naive. Добавьте сервер другого протокола, например VLESS+Reality")
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
	zapretRoute := env.ZapretRoute
	if zapretRoute {
		def := tagProxy
		if env.ZapretActive {
			def = tagDirect
		}
		outbounds = append(outbounds, M{"type": "selector", "tag": tagZapretOut, "outbounds": []any{tagDirect, tagProxy},
			"default": def, "interrupt_exist_connections": true})
	}

	// UDP (голос Discord, игры): Naive его не передаёт, VLESS, Hysteria2, TUIC, Trojan и Shadowsocks — да.
	udpTarget := ""
	if s.UDPProxy {
		var vl []any
		for _, sv := range s.Servers {
			if serverTypes[sv.Type].udp && (sv.Transport != "xhttp" || env.XrayPorts[serverTag(sv)] > 0) {
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
	if s.Mode == "tun" {
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
	if s.Share.Enabled {
		in := M{"type": "mixed", "tag": "lan-in", "listen": "0.0.0.0", "listen_port": s.Share.Port}
		if s.Share.Auth {
			in["users"] = []any{M{"username": s.Share.Username, "password": s.Share.Password}}
		}
		inbounds = append(inbounds, in)
	}

	// ---------- route ----------
	rules := []any{M{"action": "sniff"}}
	if s.Share.Enabled {
		// раздача — только для устройств домашней сети
		// (в sing-box «source_ip_is_private: false» значит «условие не задано» — правило отклоняло бы
		// вообще всё; нужное «адрес не из частной сети» записывается через invert)
		rules = append(rules, M{"inbound": []any{"lan-in"}, "source_ip_is_private": true, "invert": true, "action": "reject"})
		// …и только в интернет и локальную сеть, но не к службам самого компьютера (127.0.0.1):
		// они рассчитаны на то, что к ним ходят только с этого компьютера. Имя сначала узнаём,
		// чтобы не пройти через имя, которое указывает на 127.0.0.1.
		rules = append(rules,
			M{"inbound": []any{"lan-in"}, "domain_suffix": []any{"localhost"}, "action": "reject"},
			M{"inbound": []any{"lan-in"}, "action": "resolve"},
			M{"inbound": []any{"lan-in"}, "ip_cidr": []any{"127.0.0.0/8", "0.0.0.0/8", "::1/128", "::/128"}, "action": "reject"})
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
	var dnsRules []any

	// Пользовательские правила: сначала блокировки, потом «напрямую», потом «через сервер».
	if env.TgWsProxy {
		// tg-ws-proxy сам ходит к серверам Telegram. Напрямую — если так выбрано: у многих провайдеров
		// эти соединения режутся, поэтому по умолчанию, пока Naivka подключена, — через сервер.
		out := tagDirect
		if s.Telegram.ViaServer {
			out = tagProxy
		}
		rules = append(rules, M{"process_name": []any{"TgWsProxy.exe", "TgWsProxy_windows.exe"}, "outbound": out})
	}
	var userSets []any
	for _, action := range userRuleActions {
		domains, ips, procs := userRuleParts(s, action)
		target := func(m M) M {
			switch action {
			case "block":
				m["action"] = "reject"
			case "direct":
				m["outbound"] = tagDirect
			case "zapret":
				switch {
				case zapretRoute:
					m["outbound"] = tagZapretOut
				case env.ZapretActive:
					m["outbound"] = tagDirect
				default:
					m["outbound"] = tagProxy
				}
			default:
				m["outbound"] = tagProxy
			}
			return m
		}
		dnsTarget := func(m M) M {
			switch action {
			case "block":
				m["action"] = "reject"
			case "direct":
				m["server"] = tagDNSDirect
			case "zapret":
				m["server"] = zapretDNS(env)
			default:
				m["server"] = tagDNSRemote
			}
			return m
		}
		if env.UserRulesDir != "" {
			// Списки в файлах: правила ссылаются на них, даже пустые, — меняются без перезапуска ядра.
			dTag, oTag := userRuleTag(action, true), userRuleTag(action, false)
			rules = append(rules, target(M{"rule_set": []any{oTag}}), target(M{"rule_set": []any{dTag}}))
			dnsRules = append(dnsRules, dnsTarget(M{"rule_set": []any{dTag}}))
			for _, tag := range []string{dTag, oTag} {
				userSets = append(userSets, M{"type": "local", "tag": tag, "format": "source", "path": userRuleSetPath(env.UserRulesDir, tag)})
			}
			continue
		}
		if len(procs) > 0 {
			rules = append(rules, target(M{"process_name": strs(procs)}))
		}
		if len(domains) > 0 {
			rules = append(rules, target(M{"domain_suffix": strs(domains)}))
			dnsRules = append(dnsRules, dnsTarget(M{"domain_suffix": strs(domains)}))
		}
		if len(ips) > 0 {
			rules = append(rules, target(M{"ip_cidr": strs(ips)}))
		}
	}

	// Списки по ссылке — после ваших правил: отдельное правило сильнее целого списка.
	var listSets []any
	for _, action := range []string{"block", "direct", "proxy"} {
		for _, l := range s.RuleLists {
			if !l.Enabled || l.Action != action {
				continue
			}
			tag := "list-" + l.ID
			switch l.Format {
			case "srs", "json":
				format := "binary"
				if l.Format == "json" {
					format = "source"
				}
				listSets = append(listSets, M{"type": "remote", "tag": tag, "format": format, "url": l.URL, "update_interval": "1d"})
			default:
				il := env.InlineLists[l.ID]
				if il == nil || len(il.Domains)+len(il.IPs) == 0 {
					continue // ещё не скачан
				}
				var inline []any
				if len(il.Domains) > 0 {
					inline = append(inline, M{"domain_suffix": strs(il.Domains)})
				}
				if len(il.IPs) > 0 {
					inline = append(inline, M{"ip_cidr": strs(il.IPs)})
				}
				listSets = append(listSets, M{"type": "inline", "tag": tag, "rules": inline})
			}
			r := M{"rule_set": []any{tag}}
			switch action {
			case "block":
				r["action"] = "reject"
				dnsRules = append(dnsRules, M{"rule_set": []any{tag}, "action": "reject"})
			case "direct":
				r["outbound"] = tagDirect
				dnsRules = append(dnsRules, M{"rule_set": []any{tag}, "server": tagDNSDirect})
			default:
				r["outbound"] = tagProxy
				dnsRules = append(dnsRules, M{"rule_set": []any{tag}, "server": tagDNSRemote})
			}
			rules = append(rules, r)
		}
	}

	if s.AdBlock {
		rules = append(rules, M{"rule_set": rs("ads"), "action": "reject"})
		dnsRules = append([]any{M{"rule_set": rs("ads"), "action": "reject"}}, dnsRules...)
	}
	if len(env.ZapretDomains) > 0 && (zapretRoute || env.ZapretActive) {
		out := tagDirect
		if zapretRoute {
			out = tagZapretOut
		}
		rules = append(rules, M{"domain_suffix": strs(env.ZapretDomains), "outbound": out})
		// Адреса этих сайтов узнаём там же, откуда к ним пойдём: иначе DNS через сервер
		// отдаёт зарубежный узел (YouTube, Discord), и напрямую до него далеко и медленно.
		if env.YouTubeDNS && zapretDNS(env) == tagDNSDirect {
			dnsRules = append(dnsRules, M{"domain_suffix": youtubeDomains, "server": tagDNSYouTube})
		}
		dnsRules = append(dnsRules, M{"domain_suffix": strs(env.ZapretDomains), "server": zapretDNS(env)})
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
		)
		if env.SmartDir != "" {
			rules = append(rules, M{"rule_set": rs("geoblock"), "outbound": tagProxy})
			dnsRules = append(dnsRules, M{"rule_set": rs("geoblock"), "server": tagDNSRemote})
		}
		if s.DownloadsDirect {
			rules = append(rules, M{"domain_suffix": downloadDomains, "outbound": tagDirect})
			// Адрес — у прямого DNS: он отдаёт ближайший к вам узел CDN, а не к серверу.
			dnsRules = append(dnsRules, M{"domain_suffix": downloadDomains, "server": tagDNSDirect})
		}
		if env.SmartDir != "" {
			// Автоопределение: после заблокированного и закрытого для России, до российских сайтов.
			rules = append(rules,
				M{"rule_set": []any{tagSmartProxy}, "outbound": tagProxy},
				M{"rule_set": []any{tagSmartDirect}, "outbound": tagDirect},
			)
			dnsRules = append(dnsRules, M{"rule_set": []any{tagSmartDirect}, "server": tagDNSDirect})
		}
		rules = append(rules,
			M{"domain_suffix": ruZones, "outbound": tagDirect},
			M{"rule_set": rs("geosite-category-ru"), "outbound": tagDirect},
			M{"rule_set": rs("geoip-ru"), "outbound": tagDirect},
		)
		dnsRules = append(dnsRules,
			M{"rule_set": rs("ru-blocked"), "server": tagDNSRemote},
			M{"domain_suffix": ruZones, "server": tagDNSDirect},
			M{"rule_set": rs("geosite-category-ru"), "server": tagDNSDirect},
		)
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

	ruleSets = append(ruleSets, listSets...)
	ruleSets = append(ruleSets, userSets...)
	if env.SmartDir != "" && s.Routing == "ru-direct" {
		for _, tag := range []string{tagSmartDirect, tagSmartProxy} {
			// Ядро само перечитывает эти файлы, когда Naivka их меняет, — без перезапуска.
			ruleSets = append(ruleSets, M{"type": "local", "tag": tag, "format": "source", "path": smartRuleSetPath(env.SmartDir, tag)})
		}
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
	if env.UserRulesDir != "" {
		// Программу соединения ядро ищет само, только если правила по программам были при запуске;
		// свои правила теперь меняются на ходу — ищем всегда.
		route["find_process"] = true
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
	dd, err := parseDirectDNS(s.DNSDirect)
	if err != nil {
		return nil, err
	}
	if env.DNSFallback && dd.Encrypted() {
		dd = directDNS{Kind: "udp", Host: dnsFallbackAddr(dd)}
	}
	dnsServers := []any{}
	dnsDirect := M{"type": dd.Kind, "tag": tagDNSDirect}
	if dd.Kind != "local" {
		dnsDirect["server"] = dd.Host
	}
	if dd.Port > 0 {
		dnsDirect["server_port"] = dd.Port
	}
	if dd.Path != "" && dd.Path != "/dns-query" {
		dnsDirect["path"] = dd.Path
	}
	if dd.Encrypted() && !isIP(dd.Host) {
		// Адрес самого DoH/DoT-сервера узнаём у системного DNS — провайдер увидит только его имя.
		dnsServers = append(dnsServers, M{"type": "local", "tag": "dns-bootstrap"})
		dnsDirect["domain_resolver"] = "dns-bootstrap"
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
	if env.YouTubeDNS {
		// DoH Google по IP: сертификат выписан и на 8.8.8.8, отдельный DNS для его имени не нужен.
		dnsServers = append(dnsServers, M{"type": "https", "tag": tagDNSYouTube, "server": googleDNSIP})
	}
	dns := M{
		"servers": append([]any{dnsDirect, dnsRemote}, dnsServers...),
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

	if t := strings.TrimSpace(s.Override); t != "" {
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
		ob["tls"] = withFragment(M{"enabled": true, "server_name": sni,
			"utls":    M{"enabled": true, "fingerprint": orDefault(sv.Fingerprint, "chrome")},
			"reality": M{"enabled": true, "public_key": sv.PublicKey, "short_id": sv.ShortID}}, sv)
	case "tls":
		t := M{"enabled": true, "server_name": sni, "utls": M{"enabled": true, "fingerprint": orDefault(sv.Fingerprint, "chrome")}}
		if sv.ALPN != "" {
			t["alpn"] = strs(strings.Split(sv.ALPN, ","))
		}
		if sv.Insecure {
			t["insecure"] = true
		}
		ob["tls"] = withFragment(t, sv)
	}
	if tr := transportFor(sv); tr != nil {
		ob["transport"] = tr
	}
	return ob
}

// transportFor — транспорт V2Ray (WebSocket, HTTPUpgrade, gRPC) для VLESS и Trojan; nil — обычный TCP.
func transportFor(sv Server) M {
	switch sv.Transport {
	case "ws":
		tr := M{"type": "ws", "path": orDefault(sv.Path, "/")}
		if sv.HostHeader != "" {
			tr["headers"] = M{"Host": sv.HostHeader}
		}
		return tr
	case "httpupgrade":
		tr := M{"type": "httpupgrade", "path": orDefault(sv.Path, "/")}
		if sv.HostHeader != "" {
			tr["host"] = sv.HostHeader
		}
		return tr
	case "grpc":
		return M{"type": "grpc", "service_name": sv.ServiceName}
	}
	return nil
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
