package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const xraySubscriptionFixture = `[{"remarks":"Тестовый профиль","outbounds":[{"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"node.example.invalid","port":443,"users":[{"id":"6f1c2e1a-1b2c-4d3e-8f9a-0b1c2d3e4f50","encryption":"none","flow":""}]}]},"streamSettings":{"security":"tls","network":"ws","tlsSettings":{"serverName":"sni.example.invalid","fingerprint":"chrome","alpn":["http/1.1"]},"wsSettings":{"path":"/path","headers":{"Host":"ws.example.invalid"}}}},{"protocol":"freedom","tag":"direct"},{"protocol":"blackhole","tag":"block"}],"routing":{"rules":[{"outboundTag":"block"}]},"dns":{"servers":["malicious-config.invalid"]},"inbounds":[{"port":9999}]}]`

func TestSubscriptionXrayJSONAndBase64(t *testing.T) {
	for _, body := range []string{xraySubscriptionFixture, base64.StdEncoding.EncodeToString([]byte(xraySubscriptionFixture))} {
		list, skipped, e := parseSubscription(body)
		if e != nil || len(list) != 1 || skipped != 0 {
			t.Fatal(e, len(list), skipped)
		}
		sv := list[0]
		if sv.Type != "vless" || sv.Name != "Тестовый профиль" || sv.Host != "node.example.invalid" || sv.Port != 443 || sv.Transport != "ws" || sv.Path != "/path" || sv.HostHeader != "ws.example.invalid" || sv.SNI != "sni.example.invalid" || sv.Insecure || sv.ALPN != "http/1.1" {
			t.Fatal("lost server settings", sv.Type, sv.Transport, sv.SNI)
		}
		s := DefaultSettings()
		s.Servers = list
		s.normalize()
		cfg, e := BuildConfig(s, ConfigEnv{})
		if e != nil {
			t.Fatal(e)
		}
		data, _ := json.Marshal(cfg)
		if strings.Contains(string(data), "malicious-config.invalid") || strings.Contains(string(data), `"port":9999`) {
			t.Fatal("remote client policy imported")
		}
	}
}
func TestSubscriptionXrayRealityAndXHTTP(t *testing.T) {
	raw := `{"remarks":"Reality","outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"reality.example.invalid","port":443,"users":[{"id":"6f1c2e1a-1b2c-4d3e-8f9a-0b1c2d3e4f50","encryption":"none","flow":"xtls-rprx-vision"}]}]},"streamSettings":{"security":"reality","network":"xhttp","realitySettings":{"serverName":"example.invalid","fingerprint":"firefox","publicKey":"WJYfp1uxWwd2NXm6VQKrxMM-JymW4SvmpJAx1pk8RnY","shortId":"abcd"},"xhttpSettings":{"path":"/x","host":"cdn.example.invalid","mode":"packet-up","extra":{"noGRPCHeader":true}}}}]}`
	list, skipped, e := parseSubscription(raw)
	if e != nil || len(list) != 1 || skipped != 0 {
		t.Fatal(e, len(list), skipped)
	}
	sv := list[0]
	if sv.Security != "reality" || sv.ShortID != "abcd" || sv.Transport != "xhttp" || sv.XMode != "packet-up" || sv.XExtra != `{"noGRPCHeader":true}` || sv.Flow != "xtls-rprx-vision" {
		t.Fatal("lost Reality/xHTTP settings")
	}
	generated := xrayOutbound(sv, "proxy")
	b, _ := json.Marshal(generated)
	for _, part := range []string{`"security":"reality"`, `"path":"/x"`, `"shortId":"abcd"`, `"noGRPCHeader":true`} {
		if !strings.Contains(string(b), part) {
			t.Fatal("generated config missing", part)
		}
	}
}
func TestSubscriptionXrayTrojanAndShadowsocks(t *testing.T) {
	raw := `{"outbounds":[{"protocol":"trojan","settings":{"servers":[{"address":"trojan.example.invalid","port":443,"password":"fixture-pass"}]},"streamSettings":{"security":"tls","network":"grpc","tlsSettings":{"serverName":"example.invalid"},"grpcSettings":{"serviceName":"vpn"}}},{"protocol":"shadowsocks","settings":{"servers":[{"address":"ss.example.invalid","port":8388,"method":"aes-128-gcm","password":"fixture-pass"}]}},{"protocol":"vmess","settings":{}}]}`
	list, skipped, e := parseSubscription(raw)
	if e != nil || len(list) != 2 || skipped != 1 {
		t.Fatal(e, len(list), skipped)
	}
	if list[0].Type != "trojan" || list[0].ServiceName != "vpn" || list[1].Type != "shadowsocks" || list[1].Method != "aes-128-gcm" {
		t.Fatal("wrong protocol conversion")
	}
}
func TestSubscriptionXrayRejectsUnsupportedAndBrokenFields(t *testing.T) {
	for _, alter := range []func(map[string]any){
		func(ob map[string]any) { ob["protocol"] = "vmess" },
		func(ob map[string]any) { ob["proxySettings"] = map[string]any{"tag": "another"} },
		func(ob map[string]any) { ob["streamSettings"].(map[string]any)["network"] = "kcp" },
		func(ob map[string]any) { ob["streamSettings"].(map[string]any)["security"] = "xtls" },
		func(ob map[string]any) { ob["streamSettings"].(map[string]any)["security"] = 123 },
		func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["pinnedPeerCertificateChainSha256"] = []any{"fixture-pin"}
		},
		func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["wsSettings"].(map[string]any)["headers"].(map[string]any)["Authorization"] = "secret-header"
		},
		func(ob map[string]any) {
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["encryption"] = "unsupported"
		},
		func(ob map[string]any) {
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["id"] = "not-an-uuid"
		},
	} {
		var root []map[string]any
		_ = json.Unmarshal([]byte(xraySubscriptionFixture), &root)
		ob := root[0]["outbounds"].([]any)[0].(map[string]any)
		alter(ob)
		b, _ := json.Marshal(root)
		list, skipped, e := parseSubscription(string(b))
		if e == nil || len(list) != 0 || skipped < 1 {
			t.Fatal("unsupported profile was silently weakened", e, len(list), skipped)
		}
		if strings.Contains(e.Error(), "fixture-pin") || strings.Contains(e.Error(), "secret-header") {
			t.Fatal("error leaks profile secrets")
		}
	}
}
