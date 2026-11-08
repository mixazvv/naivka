//go:build !legacy

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func transactionFunctions(t *testing.T) string {
	t.Helper()
	start := strings.Index(installScript, "BACKUP=\"\";")
	end := strings.Index(installScript, "\ntrap finish_transaction EXIT")
	if start < 0 || end < start {
		t.Fatal("missing transaction helpers")
	}
	return installScript[start:end]
}

func TestServerInstallTransactionRestoresFiles(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			dir := t.TempDir()
			conf := filepath.Join(dir, "sing-box")
			if err := os.Mkdir(conf, 0700); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"config.json", "naivka.env", "friend.json"} {
				if err := os.WriteFile(filepath.Join(conf, file), []byte("original-"+file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			code := "0"
			if commit {
				code = "1"
			}
			script := "set -Eeuo pipefail\nCONF_DIR=" + shellQuote(conf) + "\nTEST=1\nlog(){ :; }; warn(){ :; };\n" + transactionFunctions(t) + "\ntrap finish_transaction EXIT\nbegin_transaction\nprintf broken > \"$CONF_DIR/config.json\"\nprintf new > \"$CONF_DIR/naivka.env\"\nmv \"$CONF_DIR/friend.json\" \"$CONF_DIR/friend.json.disabled\"\nCOMMITTED=" + code + "\n"
			out, err := exec.Command("bash", "-c", script).CombinedOutput()
			if commit && err != nil || !commit && err == nil {
				t.Fatal(string(out), err)
			}
			body, _ := os.ReadFile(filepath.Join(conf, "config.json"))
			if commit {
				if string(body) != "broken" {
					t.Fatal("commit undone")
				}
				return
			}
			for _, file := range []string{"config.json", "naivka.env", "friend.json"} {
				b, e := os.ReadFile(filepath.Join(conf, file))
				if e != nil || string(b) != "original-"+file {
					t.Fatal("rollback failed", file, e, string(b))
				}
			}
			if _, e := os.Stat(filepath.Join(conf, "friend.json.disabled")); !os.IsNotExist(e) {
				t.Fatal("new file survived rollback")
			}
			backups, _ := filepath.Glob(conf + ".backup.*")
			if len(backups) != 1 {
				t.Fatal("backup not retained")
			}
			info, e := os.Stat(backups[0])
			if e != nil || info.Mode().Perm() != 0700 {
				t.Fatal("backup must be private", e)
			}
		})
	}
}

func TestServerInstallRollbackServiceState(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "config")
	script := "set -Eeuo pipefail\nCONF_DIR=" + shellQuote(conf) + "\nTEST=0\nlog(){ :; }; warn(){ :; };\n" + transactionFunctions(t) + "\n"
	for _, variable := range []string{"DROPIN_DIR", "CADDY_DIR", "CADDY_UNIT", "CADDY_BIN", "SYSCTL_FILE"} {
		script += variable + "=" + shellQuote(filepath.Join(dir, variable)) + "\n"
	}
	logPath := filepath.Join(dir, "service.log")
	script += "systemctl(){ printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "; case \"$*\" in 'is-active --quiet sing-box'|'is-enabled --quiet sing-box') return 0;; 'is-active --quiet naivka-caddy'|'is-enabled --quiet naivka-caddy') return 1;; esac; }\ntrap finish_transaction EXIT\nbegin_transaction\nmkdir -p \"$CONF_DIR\"\nprintf bad > \"$CONF_DIR/config.json\"\nSB_APPLIED=1;CADDY_APPLIED=1\nexit 1\n"
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err == nil {
		t.Fatal(string(out))
	}
	logs, e := os.ReadFile(logPath)
	if e != nil {
		t.Fatal(e)
	}
	text := string(logs)
	if !strings.Contains(text, "start sing-box\n") || strings.Contains(text, "start naivka-caddy\n") || !strings.Contains(text, "disable naivka-caddy\n") {
		t.Fatal("incorrect service restore", text)
	}
	if _, e := os.Stat(conf); !os.IsNotExist(e) {
		t.Fatal("new install files survived rollback")
	}
}

func TestServerInstallValidationPrecedesServiceStop(t *testing.T) {
	check := strings.Index(installScript, "sing-box check -c \"$NEW_CONFIG\"")
	stop := strings.Index(installScript, "systemctl stop sing-box\n")
	if check < 0 || stop < check {
		t.Fatal("existing service stopped before validation")
	}
	state := strings.Index(installScript, "mv -f \"$NEW_STATE\" \"$STATE_FILE\"")
	if state < check {
		t.Fatal("state published before validation")
	}
	commit := strings.Index(installScript, "\nCOMMITTED=1\n")
	selftest := strings.Index(installScript, "\nselftest Reality")
	if selftest < 0 || commit < selftest {
		t.Fatal("commit before traffic verification")
	}
}

func TestServerInstallFullScriptRollbackAndCommit(t *testing.T) {
	for _, code := range []string{"000", "204", "check-fail"} {
		t.Run(code, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			conf := filepath.Join(dir, "conf")
			os.Mkdir(bin, 0700)
			os.Mkdir(conf, 0700)
			original := `{"inbounds":[{"tag":"naivka-reality"}],"old":true}`
			os.WriteFile(filepath.Join(conf, "config.json"), []byte(original), 0600)
			state := "UUID=11111111-1111-4111-8111-111111111111\nPRIVATE_KEY=private\nPUBLIC_KEY=public\nSHORT_ID=0123456789abcdef\nREALITY_PORT=8443\nDOMAIN=\n"
			os.WriteFile(filepath.Join(conf, "naivka.env"), []byte(state), 0600)
			mocks := map[string]string{
				"sing-box": "case \"$1\" in version) echo 'sing-box version 1.14.4';; check) [ \"$MOCK_HTTP\" != check-fail ];; run) exit 0;; *) exit 1;; esac",
				"curl":     "case \" $* \" in *' -x '*) printf '%s' \"$MOCK_HTTP\";; esac",
				"ss":       "exit 0", "pkill": "exit 0", "sleep": "exit 0",
			}
			for name, body := range mocks {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			script := strings.Replace(installScript, `[[ "${EUID}" -eq 0 ]] || die`, `[[ "$TEST" == 1 ]] || die`, 1)
			path := filepath.Join(dir, "install.sh")
			os.WriteFile(path, []byte(script), 0700)
			cmd := exec.Command("bash", path)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "NAIVKA_TEST=1", "CONF_DIR="+conf, "SERVER_ADDR=203.0.113.10", "REALITY_SNI=www.example.com", "HY2=0", "NAIVE=0", "BBR=0", "MOCK_HTTP="+code)
			out, err := cmd.CombinedOutput()
			if code == "204" {
				if err != nil || !strings.Contains(string(out), "NAIVKA-LINK vless://") {
					t.Fatal(string(out), err)
				}
			} else {
				if err == nil || strings.Contains(string(out), "NAIVKA-LINK") {
					t.Fatal("failure published links", string(out), err)
				}
				cfg, _ := os.ReadFile(filepath.Join(conf, "config.json"))
				env, _ := os.ReadFile(filepath.Join(conf, "naivka.env"))
				if string(cfg) != original || string(env) != state {
					t.Fatal("full install did not restore original files", string(out))
				}
			}
		})
	}
}

func TestServerInstallIncompleteBackupNeverOverwritesOriginal(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "config")
	os.Mkdir(conf, 0700)
	file := filepath.Join(conf, "config.json")
	os.WriteFile(file, []byte("working"), 0600)
	script := "set -Eeuo pipefail\nCONF_DIR=" + shellQuote(conf) + "\nTEST=1\nlog(){ :; };warn(){ :; };die(){ exit 1; };\n" + transactionFunctions(t) + "\ntrap finish_transaction EXIT\ncp(){ return 1; }\nbegin_transaction\nprintf changed > \"$CONF_DIR/config.json\"\n"
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err == nil {
		t.Fatal("incomplete backup accepted", string(out))
	}
	body, e := os.ReadFile(file)
	if e != nil || string(body) != "working" {
		t.Fatal("original changed after backup failure", e, string(body))
	}
}
