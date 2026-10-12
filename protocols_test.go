package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

const testSS2022Key = "AAAAAAAAAAAAAAAAAAAAAA==" // 16 байт для 2022-blake3-aes-128-gcm

func TestProtocolLinks(t *testing.T) {
	ssLegacy := "ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pa$$@ss.example.com:8388")) + "#Old"
	ssSIP002 := "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret")) + "@ss.example.com:8388#SIP002"
	ss2022 := "ss://2022-blake3-aes-128-gcm:" + "AAAAAAAAAAAAAAAAAAAAAA%3D%3D" + "@ss.example.com:8388#SS2022"
	links := []string{
		"hysteria2://letmein@hy.example.com:8443/?sni=real.example.com&obfs=salamander&obfs-password=gawr&insecure=1&upmbps=50#Hy2",
		"hy2://u:p@hy.example.com#Hy2user",
		"tuic://d39351d5-2ec2-4ac7-aab4-8ca53a5816fc:pass@tuic.example.com:443?congestion_control=cubic&udp_relay_mode=quic&alpn=h3&sni=t.example.com#TUIC",
		"trojan://p%40ss@tr.example.com:443?security=tls&type=ws&path=%2Fws&host=cdn.example.com&sni=cdn.example.com#TrojanWS",
		"trojan://pass@tr.example.com#TrojanTCP",
		"anytls://pw@any.example.com:443/?sni=any.example.com&insecure=0#AnyTLS",
		ssLegacy, ssSIP002, ss2022,
	}
	text := ""
	for _, l := range links {
		text += l + "\n"
	}
	servers, errs := ParseShareLinks(text)
	if len(errs) > 0 || len(servers) != len(links) {
		t.Fatalf("разбор: %d из %d, ошибки %v", len(servers), len(links), errs)
	}
	hy := servers[0]
	if hy.Type != "hysteria2" || hy.Password != "letmein" || hy.Port != 8443 || hy.SNI != "real.example.com" || hy.Obfs != "salamander" || hy.ObfsPassword != "gawr" || !hy.Insecure || hy.UpMbps != 50 {
		t.Errorf("hysteria2: %+v", hy)
	}
	if servers[1].Password != "u:p" || servers[1].Port != 443 {
		t.Errorf("hy2 логин:пароль: %+v", servers[1])
	}
	if tu := servers[2]; tu.Type != "tuic" || tu.Password != "pass" || tu.Congestion != "cubic" || tu.UDPRelay != "quic" {
		t.Errorf("tuic: %+v", tu)
	}
	if tr := servers[3]; tr.Password != "p@ss" || tr.Transport != "ws" || tr.Path != "/ws" || tr.HostHeader != "cdn.example.com" {
		t.Errorf("trojan ws: %+v", tr)
	}
	if ss := servers[6]; ss.Method != "chacha20-ietf-poly1305" || ss.Password != "pa$$" || ss.Host != "ss.example.com" || ss.Port != 8388 || ss.Name != "Old" {
		t.Errorf("ss старый: %+v", ss)
	}
	if ss := servers[7]; ss.Method != "aes-256-gcm" || ss.Password != "secret" {
		t.Errorf("ss SIP002: %+v", ss)
	}
	if ss := servers[8]; ss.Method != "2022-blake3-aes-128-gcm" || ss.Password != testSS2022Key {
		t.Errorf("ss 2022: %+v", ss)
	}

	// ссылка → сервер → ссылка → сервер: ничего не теряется
	s := DefaultSettings()
	s.Servers = servers
	s.normalize()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, sv := range s.Servers {
		back, errs := ParseShareLinks(ShareLink(sv))
		if len(errs) > 0 || len(back) != 1 {
			t.Errorf("%s: обратно не разобралась %s: %v", sv.Name, ShareLink(sv), errs)
			continue
		}
		b := back[0]
		b.ID = sv.ID
		x, _ := json.Marshal(b)
		y, _ := json.Marshal(sv)
		if string(x) != string(y) {
			t.Errorf("%s: туда-обратно\n%s\n%s", sv.Name, x, y)
		}
	}
}

func TestProtocolValidation(t *testing.T) {
	for _, l := range []string{
		"hysteria2://@hy.example.com",                                                                               // нет пароля
		"hysteria2://p@hy.example.com?obfs=foo",                                                                     // неизвестная маскировка
		"hysteria2://p@hy.example.com?obfs=salamander",                                                              // нет пароля маскировки
		"tuic://not-a-uuid:p@t.example.com",                                                                         // UUID
		"trojan://p@t.example.com?security=reality",                                                                 // только TLS
		"ss://" + base64.StdEncoding.EncodeToString([]byte("rc4-md5:p")) + "@s.example.com:1#x",                     // старый шифр
		"ss://2022-blake3-aes-256-gcm:short@s.example.com:1",                                                        // ключ не той длины
		"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:p")) + "@s.example.com:1?plugin=obfs-local", // плагин
	} {
		if sv, errs := ParseShareLinks(l); len(errs) == 0 {
			t.Errorf("принято: %s → %+v", l, sv)
		}
	}
}

func TestProtocolOutbounds(t *testing.T) {
	servers, _ := ParseShareLinks("hysteria2://p@hy.example.com:8443?obfs=salamander&obfs-password=o#h\n" +
		"tuic://d39351d5-2ec2-4ac7-aab4-8ca53a5816fc:p@t.example.com#t\n" +
		"trojan://p@tr.example.com?type=grpc&serviceName=svc#tr\n" +
		"ss://2022-blake3-aes-128-gcm:" + "AAAAAAAAAAAAAAAAAAAAAA%3D%3D@s.example.com:8388#ss\n" +
		"anytls://p@a.example.com#a\n" +
		"naive+https://u:p@n.example.com#n")
	s := DefaultSettings()
	s.Servers = servers
	s.UDPProxy = true
	s.normalize()
	cfg, err := BuildConfig(s, ConfigEnv{APIPort: 9090})
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]M{}
	var udp []any
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(M)
		types[m["type"].(string)] = m
		if m["tag"] == "udp-auto" {
			udp = m["outbounds"].([]any)
		}
	}
	for _, typ := range []string{"hysteria2", "tuic", "trojan", "shadowsocks", "anytls", "naive"} {
		if types[typ] == nil {
			t.Errorf("нет outbound %s", typ)
		}
	}
	if types["hysteria2"]["obfs"].(M)["type"] != "salamander" {
		t.Error("hysteria2 без obfs")
	}
	if types["trojan"]["transport"].(M)["service_name"] != "svc" {
		t.Error("trojan без gRPC")
	}
	if alpn := types["tuic"]["tls"].(M)["alpn"]; alpn == nil {
		t.Error("tuic без alpn h3")
	}
	if _, ok := types["hysteria2"]["tls"].(M)["utls"]; ok {
		t.Error("uTLS у QUIC-протокола")
	}
	// UDP через сервер: все, кто умеет UDP, кроме Naive и AnyTLS
	if len(udp) != 4 {
		t.Errorf("группа UDP: %v", udp)
	}
}
