package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Свои правила — в файлах-списках (local rule-set), а не в самом конфиге: ядро перечитывает эти файлы
// само, поэтому перенос сайта или программы на другую ветку работает сразу, без переподключения.
// На каждое действие два списка: адреса сайтов (они нужны ещё и правилам DNS) и остальное — IP и программы.

var userRuleActions = []string{"block", "direct", "zapret", "proxy"}

func userRuleTag(action string, domains bool) string {
	if domains {
		return "user-" + action + "-d"
	}
	return "user-" + action + "-o"
}

func userRuleSetPath(dir, tag string) string { return filepath.Join(dir, tag+".json") }

// userRuleParts раскладывает правила одного действия: домены, IP-сети, программы.
func userRuleParts(s *Settings, action string, mobile bool) (domains, ips, procs []string) {
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
			if !mobile { // на телефоне правил по программам нет
				procs = append(procs, r.Value)
			}
		}
	}
	return
}

// userRuleSetFiles — содержимое всех списков. Пустой список тоже пишется: ядро ссылается на каждый.
func userRuleSetFiles(s *Settings) map[string][]byte {
	out := map[string][]byte{}
	for _, action := range userRuleActions {
		domains, ips, procs := userRuleParts(s, action, false)
		d, o := []any{}, []any{}
		if len(domains) > 0 {
			d = append(d, M{"domain_suffix": strs(domains)})
		}
		if len(ips) > 0 {
			o = append(o, M{"ip_cidr": strs(ips)})
		}
		if len(procs) > 0 {
			o = append(o, M{"process_name": strs(procs)})
		}
		out[userRuleTag(action, true)], _ = json.Marshal(M{"version": 2, "rules": d})
		out[userRuleTag(action, false)], _ = json.Marshal(M{"version": 2, "rules": o})
	}
	return out
}

// writeUserRuleSets записывает списки через временный файл (ядро не прочитает половину);
// неизменившиеся не трогает, чтобы ядро не перечитывало их зря.
func writeUserRuleSets(dir string, s *Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for tag, b := range userRuleSetFiles(s) {
		p := userRuleSetPath(dir, tag)
		if old, err := os.ReadFile(p); err == nil && string(old) == string(b) {
			continue
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return nil
}

// fingerprintNoRules — отпечаток настроек без своих правил: если отличаются только правила,
// ядро перезапускать не нужно.
func (s *Settings) fingerprintNoRules() string {
	c := s.clone()
	c.Rules = nil
	return c.fingerprint()
}

func ruleKeys(rules []Rule) map[string]Rule {
	m := map[string]Rule{}
	for _, r := range rules {
		m[r.Kind+"|"+strings.ToLower(r.Value)+"|"+r.Action] = r
	}
	return m
}

// changedRules — правила, которые добавили, убрали или перевели на другой путь.
func changedRules(prev, next []Rule) []Rule {
	a, b := ruleKeys(prev), ruleKeys(next)
	var out []Rule
	for k, r := range a {
		if _, ok := b[k]; !ok {
			out = append(out, r)
		}
	}
	for k, r := range b {
		if _, ok := a[k]; !ok {
			out = append(out, r)
		}
	}
	return out
}

func zapretRulesChanged(changed []Rule) bool {
	for _, r := range changed {
		if r.Action == "zapret" {
			return true
		}
	}
	return false
}

// connMatchesRule — относится ли открытое соединение к правилу.
func connMatchesRule(c Conn, r Rule) bool {
	switch r.Kind {
	case "domain":
		d := strings.TrimPrefix(strings.ToLower(r.Value), ".")
		h := strings.ToLower(c.Metadata.Host)
		return h != "" && (h == d || strings.HasSuffix(h, "."+d))
	case "process":
		p := c.Metadata.ProcessPath
		if i := strings.LastIndexAny(p, `\/`); i >= 0 {
			p = p[i+1:]
		}
		return p != "" && strings.EqualFold(p, r.Value)
	case "ip":
		ip := net.ParseIP(c.Metadata.DestIP)
		if ip == nil {
			return false
		}
		v := r.Value
		if !strings.Contains(v, "/") {
			return ip.Equal(net.ParseIP(v))
		}
		_, n, err := net.ParseCIDR(v)
		return err == nil && n.Contains(ip)
	}
	return false
}

// applyRulesLive — правила изменились, остальные настройки нет: переписать списки и закрыть
// соединения, которых это касается, — они откроются заново уже новым путём.
func (a *App) applyRulesLive(prev, ns *Settings, changed []Rule) {
	if err := writeUserRuleSets(a.core.runDir(), ns); err != nil {
		a.core.logs.Add("[naivka] свои правила: " + err.Error())
		a.restartCore()
		return
	}
	// «Применено» сдвигаем, только если до этого ничего не ждало переподключения:
	// иначе подсказка «Переподключить» о прежних изменениях пропала бы.
	a.mu.Lock()
	if a.applied == prev.fingerprint() {
		a.applied = ns.fingerprint()
	}
	a.mu.Unlock()
	go func() {
		time.Sleep(400 * time.Millisecond) // ядро перечитывает список чуть позже записи
		cl := a.core.Clash()
		if cl == nil {
			return
		}
		v, err := cl.ConnectionsRaw()
		if err != nil {
			return
		}
		closed := 0
		for _, c := range v.Connections {
			for _, r := range changed {
				if connMatchesRule(c, r) {
					if cl.CloseConn(c.ID) == nil {
						closed++
					}
					break
				}
			}
		}
		if closed > 0 {
			a.core.logs.Add("[naivka] правила применены сразу; переоткрыто соединений: " + strconv.Itoa(closed))
		}
	}()
}
