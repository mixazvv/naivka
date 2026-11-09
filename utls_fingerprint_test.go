package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fingerprintTestLink = "vless://6f1c2e1a-1b2c-4d3e-8f9a-0b1c2d3e4f50@node.example.invalid:443?security=reality&sni=camouflage.example.invalid&fp=hellofirefox_120&pbk=WJYfp1uxWwd2NXm6VQKrxMM-JymW4SvmpJAx1pk8RnY&sid=abcd&flow=xtls-rprx-vision&type=tcp#Fixture"

func TestSingboxFingerprintAliases(t *testing.T) {
	for in, want := range map[string]string{
		"": "chrome", "  Firefox  ": "firefox", "HelloFirefox_120": "firefox",
		"hellofirefox_120": "firefox", "firefox120": "firefox", "hellofirefox_auto": "firefox",
		"HelloChrome_Auto": "chrome", "HelloEdge_Auto": "edge", "HelloSafari_Auto": "safari",
		"HelloIOS_Auto": "ios", "HelloAndroid_11_OkHttp": "android", "Hello360_Auto": "360",
		"HelloQQ_Auto": "qq", "HelloRandomized": "randomized",
		"chrome_pq": "chrome_pq", "chrome_psk": "chrome_psk", "random": "random", "randomized": "randomized",
		// Unknown/different profiles must not be guessed or silently replaced by Chrome.
		"hellofirefox_148": "hellofirefox_148", "HelloRandomizedNoALPN": "HelloRandomizedNoALPN",
		"hellochrome_120_pq": "hellochrome_120_pq", "custom-profile": "custom-profile", "none": "none",
	} {
		if got := singboxFingerprint(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestFingerprintImportAndExistingSettings(t *testing.T) {
	for name, raw := range map[string]string{
		"links":     fingerprintTestLink,
		"base64":    base64.StdEncoding.EncodeToString([]byte(fingerprintTestLink)),
		"xray-json": strings.Replace(xraySubscriptionFixture, `"fingerprint":"chrome"`, `"fingerprint":"hellofirefox_120"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			servers, skipped, err := parseSubscription(raw)
			if err != nil || skipped != 0 || len(servers) != 1 {
				t.Fatal("import failed", skipped, err)
			}
			s := DefaultSettings()
			servers[0].ID = "saved-server"
			s.Servers, s.Selected = servers, servers[0].ID
			s.Servers[0].Fav = true
			dir := t.TempDir()
			if err := SaveSettings(dir, s); err != nil {
				t.Fatal(err)
			}
			s, err = LoadSettings(dir)
			if err != nil {
				t.Fatal(err)
			}
			sv := s.Servers[0]
			if sv.Fingerprint != "hellofirefox_120" || s.Selected != servers[0].ID || !sv.Fav {
				t.Fatal("saved profile/selection changed")
			}
			before := sv
			cfg, err := BuildConfig(s, ConfigEnv{})
			if err != nil {
				t.Fatal(err)
			}
			var out M
			for _, value := range cfg["outbounds"].([]any) {
				if value.(M)["tag"] == serverTag(sv) {
					out = value.(M)
				}
			}
			if out == nil || out["tls"].(M)["utls"].(M)["fingerprint"] != "firefox" {
				t.Fatal("generated config retained unsupported fingerprint")
			}
			if s.Servers[0] != before || out["uuid"] != sv.UUID || out["tls"].(M)["server_name"] != sv.SNI {
				t.Fatal("conversion changed source or credentials")
			}
			back, errs := ParseShareLinks(ShareLink(sv))
			if len(errs) != 0 || len(back) != 1 || back[0].Fingerprint != sv.Fingerprint {
				t.Fatal("export lost original fingerprint", errs)
			}
		})
	}
}

func TestFingerprintConversionPreservesTLSAndReality(t *testing.T) {
	servers, errs := ParseShareLinks(fingerprintTestLink)
	if len(errs) != 0 || len(servers) != 1 {
		t.Fatal(errs)
	}
	sv := servers[0]
	for _, security := range []string{"reality", "tls"} {
		sv.Security = security
		sv.ALPN = "h2,http/1.1"
		out := vlessOutbound(sv, "test")
		tls := out["tls"].(M)
		if tls["utls"].(M)["fingerprint"] != "firefox" || tls["insecure"] == true || out["flow"] != sv.Flow {
			t.Fatal("conversion changed TLS verification or flow")
		}
		if security == "reality" {
			reality := tls["reality"].(M)
			if reality["public_key"] != sv.PublicKey || reality["short_id"] != sv.ShortID || reality["enabled"] != true {
				t.Fatal("Reality authentication changed")
			}
		}
		// xHTTP still uses Xray, which accepts the original detailed name.
		xrayTLS := xrayOutbound(sv, "test")["streamSettings"].(map[string]any)[security+"Settings"].(map[string]any)
		if xrayTLS["fingerprint"] != sv.Fingerprint {
			t.Fatal("Xray fingerprint changed")
		}
	}
	for _, typ := range []string{"trojan", "anytls"} {
		sv.Type = typ
		sv.PinSHA256 = "fixture-key-pin"
		tls := protoTLS(sv, true, nil)
		if tls["utls"].(M)["fingerprint"] != "firefox" || tls["insecure"] == true || tls["certificate_public_key_sha256"].([]any)[0] != sv.PinSHA256 {
			t.Fatal("TLS pin changed")
		}
	}
	if tls := protoTLS(Server{Type: "hysteria2", Fingerprint: "hellofirefox_120"}, false, nil); tls["utls"] != nil {
		t.Fatal("uTLS added to QUIC")
	}
}

// SINGBOX=/path/to/sing-box go test -run TestFingerprintConfigWithSingbox .
// Uses check only: no VPN startup, network requests or real subscription data.
func TestFingerprintConfigWithSingbox(t *testing.T) {
	sb := os.Getenv("SINGBOX")
	if sb == "" {
		t.Skip("SINGBOX не задан")
	}
	servers, errs := ParseShareLinks(fingerprintTestLink)
	if len(errs) != 0 || len(servers) != 1 {
		t.Fatal(errs)
	}
	for _, typ := range []string{"vless", "trojan", "anytls"} {
		sv := servers[0]
		sv.Type, sv.Password = typ, "fixture-password"
		var out M
		if typ == "vless" {
			out = vlessOutbound(sv, "test")
		} else {
			out = protoOutbound(sv, "test")
		}
		cfg := M{"outbounds": []any{out}}
		file := filepath.Join(t.TempDir(), "config.json")
		check := func() ([]byte, error) {
			b, _ := json.Marshal(cfg)
			if err := os.WriteFile(file, b, 0600); err != nil {
				t.Fatal(err)
			}
			return exec.Command(sb, "check", "-c", file).CombinedOutput()
		}
		if b, err := check(); err != nil {
			t.Fatalf("%s corrected config rejected: %s", typ, b)
		}
		out["tls"].(M)["utls"].(M)["fingerprint"] = sv.Fingerprint
		if b, err := check(); err == nil || !strings.Contains(string(b), "unknown uTLS fingerprint") {
			t.Fatalf("%s original error not reproduced: %s", typ, b)
		}
	}
}
