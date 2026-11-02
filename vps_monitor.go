//go:build !legacy

package main

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed server/vps-report.sh
var vpsReportScript string

const vpsOutputLimit = 16 * 1024

type vpsCheck struct {
	ID    string `json:"id"`
	Level string `json:"level"`
	Value string `json:"value"`
}
type vpsUpdates struct {
	Provider    string `json:"provider"`
	Count       int    `json:"count"`
	OldestIndex int64  `json:"oldestIndex"`
	NewestIndex int64  `json:"newestIndex"`
}

type vpsListener struct {
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Binding  string `json:"binding"`
}

type vpsReport struct {
	BootID    string             `json:"bootId,omitempty"`
	CheckedAt int64              `json:"checkedAt"`
	Metrics   map[string]float64 `json:"metrics"`
	Updates   *vpsUpdates        `json:"updates,omitempty"`
	Listeners []vpsListener      `json:"listeners"`
	Checks    []vpsCheck         `json:"checks"`
}

// Drain excess output without retaining it or blocking the SSH transport.
type vpsOutput struct {
	data     []byte
	overflow bool
}

func (b *vpsOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := vpsOutputLimit - len(b.data)
	if len(p) > left {
		p = p[:left]
		b.overflow = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

// waitVPS bounds channel setup as well as command execution. Closing the transport
// must unblock run; the buffered result lets it finish even after cancellation.
func waitVPS(ctx context.Context, closeTransport func(), run func() (vpsReport, error)) (vpsReport, error) {
	type result struct {
		report vpsReport
		err    error
	}
	done := make(chan result, 1)
	go func() { r, err := run(); done <- result{r, err} }()
	select {
	case r := <-done:
		return r.report, r.err
	case <-ctx.Done():
		closeTransport()
		return vpsReport{}, errors.New("Проверка VPS прервана или превысила 30 секунд; SSH-соединение закрыто")
	}
}

func collectVPS(ctx context.Context, cl *ssh.Client) (vpsReport, error) {
	return waitVPS(ctx, func() { cl.Close() }, func() (vpsReport, error) {
		session, err := cl.NewSession()
		if err != nil {
			return vpsReport{}, errors.New("Не удалось открыть канал SSH")
		}
		defer session.Close()
		var out vpsOutput
		session.Stdout = &out
		session.Stderr = io.Discard // never return server logs/configuration/credentials
		session.Stdin = strings.NewReader(vpsReportScript)
		if err := session.Run("sh -s"); err != nil {
			return vpsReport{}, errors.New("Не удалось получить отчёт Linux по SSH")
		}
		if out.overflow {
			return vpsReport{}, errors.New("Отчёт VPS превысил допустимый размер")
		}
		return parseVPSReport(string(out.data))
	})
}

func parseVPSReport(raw string) (vpsReport, error) {
	r := vpsReport{CheckedAt: time.Now().Unix(), Metrics: map[string]float64{}}
	if len(raw) > vpsOutputLimit {
		return r, errors.New("Слишком большой отчёт VPS")
	}
	values := map[string]string{}
	listeners := map[vpsListener]bool{}
	listenerInvalid, listenerOverflow := false, false
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == "listener" && len(v) >= 80 {
			listenerInvalid = true
			continue
		}
		if ok && len(k) < 40 && len(v) < 80 {
			if k == "listener" {
				parts := strings.Split(v, "|")
				valid := false
				if len(parts) == 3 && (parts[0] == "tcp" || parts[0] == "udp") && (parts[2] == "loopback" || parts[2] == "wildcard" || parts[2] == "specific") {
					port, err := strconv.Atoi(parts[1])
					if err == nil && port >= 1 && port <= 65535 && strconv.Itoa(port) == parts[1] {
						valid = true
						item := vpsListener{parts[0], port, parts[2]}
						if _, exists := listeners[item]; !exists {
							if len(listeners) < 128 {
								listeners[item] = true
							} else {
								listenerOverflow = true
							}
						}
					}
				}
				if !valid {
					listenerInvalid = true
				}
			} else {
				values[k] = v
			}
		}
	}
	if values["protocol"] != "1" {
		return r, errors.New("Отчёт не поддерживается: требуется Linux с /proc")
	}
	for _, k := range []string{"cpu_usage", "uptime", "load", "cpus", "memory_total", "memory_used", "disk_total", "disk_used", "network_rx", "network_tx", "server_time", "network_set", "load5", "load15", "swap_total", "swap_used", "inodes_total", "inodes_used", "processes", "pressure_cpu", "pressure_memory", "pressure_io"} {
		if v, err := strconv.ParseFloat(values[k], 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e20 {
			if k != "cpu_usage" || v <= 100 {
				r.Metrics[k] = v
			}
		}
	}
	if id := values["boot_id"]; len(id) == 36 && strings.Count(id, "-") == 4 {
		valid := true
		for _, c := range id {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c == '-') {
				valid = false
			}
		}
		if valid {
			r.BootID = id
		}
	}
	// Unknown values never turn into a successful check; raw server text is not exposed.
	add := func(id string, allowed map[string]string) {
		value := values[id]
		level, ok := allowed[value]
		if !ok {
			value = "unknown"
			level = "unknown"
		}
		r.Checks = append(r.Checks, vpsCheck{id, level, value})
	}
	add("root_login", map[string]string{"no": "ok", "yes": "warning", "prohibit-password": "warning", "without-password": "warning", "forced-commands-only": "warning"})
	add("password_auth", map[string]string{"no": "ok", "yes": "warning"})
	for _, key := range []string{"keyboard_auth", "empty_passwords", "x11_forwarding", "match_password", "match_keyboard"} {
		add(key, map[string]string{"yes": "warning", "no": "ok"})
	}
	add("match_root", map[string]string{"no": "ok", "yes": "warning", "prohibit-password": "warning", "without-password": "warning", "forced-commands-only": "warning"})
	add("pam", map[string]string{"yes": "info", "no": "info"})
	add("publickey_auth", map[string]string{"yes": "ok", "no": "warning"})
	if n, e := strconv.Atoi(values["auth_tries"]); e == nil && n >= 1 && n <= 100 {
		level := "info"
		v := "bounded"
		if n > 6 {
			level = "warning"
			v = "high"
		}
		r.Checks = append(r.Checks, vpsCheck{"auth_tries", level, v})
	} else {
		add("auth_tries", nil)
	}
	add("firewalld", map[string]string{"active": "info", "inactive": "warning"})
	add("nft_input", map[string]string{"drop_observed": "info", "hook_observed": "info", "no_hook": "warning"})
	add("iptables_input", map[string]string{"accept": "warning", "drop": "info"})
	for _, label := range []string{"singbox", "xray", "panel", "ssh"} {
		add("owner_"+label, map[string]string{"root": "info", "other": "warning"})
		add("access_"+label, map[string]string{"restricted": "info", "shared_access": "warning"})
		add("acl_"+label, map[string]string{"basic": "info", "extended": "warning"})
	}
	for _, label := range []string{"caddy", "panel"} {
		add("cert_"+label, map[string]string{"valid_30d": "info", "expires_30d": "warning", "expired": "warning"})
	}
	add("firewall", map[string]string{"active": "info", "inactive": "warning"})
	for _, service := range []string{"sing-box", "xray", "naivka-caddy", "x-ui", "marzban", "nginx", "caddy", "fail2ban", "ssh", "sshd"} {
		add("service_"+service, map[string]string{"active": "ok", "inactive": "info", "failed": "warning"})
		add("enabled_"+service, map[string]string{"enabled": "ok", "disabled": "info", "static": "info", "masked": "info"})
	}
	mode, err := strconv.ParseUint(values["config_mode"], 8, 9)
	c := vpsCheck{"config_mode", "unknown", "unknown"}
	if err == nil && len(values["config_mode"]) == 3 {
		c.Level = "ok"
		c.Value = "restricted"
		if mode&0077 != 0 {
			c.Level = "warning"
			c.Value = "shared_access"
		}
	}
	r.Checks = append(r.Checks, c)
	add("certificate", map[string]string{"valid_30d": "info", "expires_30d": "warning", "expired": "warning"})
	updatesCheck := vpsCheck{"os_updates", "unknown", "unknown"}
	if values["os_updates"] == "unsupported" {
		updatesCheck.Value = "unsupported"
	}
	if values["os_updates"] == "apt_cached" {
		count, errCount := strconv.Atoi(values["apt_updates"])
		oldest, errOld := strconv.ParseInt(values["apt_index_oldest"], 10, 64)
		newest, errNew := strconv.ParseInt(values["apt_index_newest"], 10, 64)
		if errCount == nil && count >= 0 && count <= 1000000 && errOld == nil && errNew == nil && oldest >= 946684800 && newest >= oldest && newest <= r.CheckedAt+300 {
			r.Updates = &vpsUpdates{"apt_cached", count, oldest, newest}
			updatesCheck = vpsCheck{"os_updates", "info", "none_in_cache"}
			if count > 0 {
				updatesCheck.Level = "warning"
				updatesCheck.Value = "available_in_cache"
			}
		}
	}
	r.Checks = append(r.Checks, updatesCheck)
	if listenerInvalid {
		values["local_listeners"] = "unknown"
	}
	if listenerOverflow && values["local_listeners"] == "observed" {
		values["local_listeners"] = "truncated"
	}
	add("local_listeners", map[string]string{"observed": "info", "truncated": "info"})
	if values["local_listeners"] == "observed" || values["local_listeners"] == "truncated" {
		r.Listeners = make([]vpsListener, 0, len(listeners))
		for listener := range listeners {
			r.Listeners = append(r.Listeners, listener)
		}
		sort.Slice(r.Listeners, func(i, j int) bool {
			a, b := r.Listeners[i], r.Listeners[j]
			if a.Port != b.Port {
				return a.Port < b.Port
			}
			if a.Protocol != b.Protocol {
				return a.Protocol < b.Protocol
			}
			return a.Binding < b.Binding
		})
	}
	// Local binding does not establish internet reachability through NAT/firewalls.
	r.Checks = append(r.Checks, vpsCheck{"external_ports", "unknown", "unknown"})

	return r, nil
}

func (a *App) vpsRoutes(api func(method, path string, f apiFunc)) {
	api("POST", "/api/vps/report", func(r *http.Request) (any, error) {
		var req friendsReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		host, ok := a.ownHost(req.Host)
		if !ok {
			return nil, errors.New("Сервер не найден")
		}
		// Reuse the same serialized, fingerprint-verified, idle-expiring SSH access.
		if !a.friends.ops.TryLock() {
			return nil, errors.New("SSH занят другой операцией. Повторите проверку позже")
		}
		defer a.friends.ops.Unlock()
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		cl, err := a.friendsClient(host, req)
		if errors.Is(err, errNeedAuth) {
			return map[string]bool{"needAuth": true}, nil
		}
		if err != nil {
			return nil, err
		}
		defer a.friends.release(host.ID, cl)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		report, err := collectVPS(ctx, cl)
		if err != nil {
			a.friends.drop(host.ID, cl)
			return nil, err
		}
		return report, nil
	})
}
