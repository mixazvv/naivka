//go:build !legacy

package main

import (
	"context"
	_ "embed"
	"errors"
	"io"
	"math"
	"net/http"
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
type vpsReport struct {
	CheckedAt int64              `json:"checkedAt"`
	Metrics   map[string]float64 `json:"metrics"`
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
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && len(k) < 40 && len(v) < 80 {
			values[k] = v
		}
	}
	if values["protocol"] != "1" {
		return r, errors.New("Отчёт не поддерживается: требуется Linux с /proc")
	}
	for _, k := range []string{"cpu_usage", "uptime", "load", "cpus", "memory_total", "memory_used", "disk_total", "disk_used", "network_rx", "network_tx"} {
		if v, err := strconv.ParseFloat(values[k], 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e20 {
			if k != "cpu_usage" || v <= 100 {
				r.Metrics[k] = v
			}
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
	add("firewall", map[string]string{"active": "info", "inactive": "warning"})
	for _, service := range []string{"sing-box", "xray", "naivka-caddy", "x-ui"} {
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
	// These need distro-specific update metadata and external reachability probes.
	r.Checks = append(r.Checks, vpsCheck{"os_updates", "unknown", "unknown"}, vpsCheck{"external_ports", "unknown", "unknown"})
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
		a.friends.ops.Lock()
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
