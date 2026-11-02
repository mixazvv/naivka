package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Releases before 2.25 have no commit handshake. They start offline with a
// durable backup outside their old cleanup paths. Readiness is authenticated
// against the launched PID and exact version; it confirms the API, not VPN/TUN.
func historicalInstance(ctx context.Context, dir, version string, pid int) bool {
	inst, ok := readInstance(dir)
	if !ok || inst.PID != pid || inst.Port < 1 || inst.Port > 65535 || len(inst.Token) < 16 {
		return false
	}
	req, e := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/api/self", inst.Port), nil)
	if e != nil {
		return false
	}
	req.Header.Set("X-Naivka-Token", inst.Token)
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	return resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&v) == nil && strings.TrimPrefix(v.Version, "v") == strings.TrimPrefix(version, "v")
}
func (r updateRunner) runHistorical(cfg updateSupervisorConfig, intent updateIntent) (result string) {
	defer func() {
		if !strings.HasSuffix(result, "_ROLLED_BACK") {
			recordUpdateResult(cfg.Dir, result)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmd := r.newCommand(cfg.Exe, []string{"--replace", "--no-elevate"}, withoutUpdateHandshake(os.Environ()))
	if e := cmd.Start(); e != nil {
		return r.rollback(cfg, intent, "UPDATE_START_FAILED")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var readyAt time.Time
	failure := "UPDATE_READY_TIMEOUT"
	for {
		select {
		case <-done:
			return r.rollback(cfg, intent, "UPDATE_EXIT_BEFORE_READY")
		case <-ctx.Done():
			goto failed
		case <-ticker.C:
			var latest updateIntent
			b, e := os.ReadFile(cfg.Intent)
			if e != nil || json.Unmarshal(b, &latest) != nil || !latest.Commit {
				failure = "UPDATE_USER_EXIT"
				goto failed
			}
			if !historicalInstance(ctx, cfg.Dir, cfg.Version, cmd.Process.Pid) {
				readyAt = time.Time{}
				continue
			}
			if readyAt.IsZero() {
				readyAt = time.Now()
				continue
			}
			if time.Since(readyAt) < r.stable {
				continue
			}
			// No automatic reconnect on a selected downgrade, even if the previous app
			// was connected. The user starts the VPN explicitly in the selected version.
			if cfg.Debug {
				_ = os.Rename(cfg.Exe+".debug.new", filepath.Join(filepath.Dir(cfg.Exe), "Naivka-debug.exe"))
			}
			return "UPDATED"
		}
	}
failed:
	// Ask a bootstrapped old app to clean up its own proxy/drivers before killing.
	if inst, ok := readInstance(cfg.Dir); ok && inst.PID == cmd.Process.Pid {
		_ = postInstance(inst, "/api/quit")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return "UPDATE_CHILD_STOP_FAILED"
		}
	}
	return r.rollback(cfg, intent, failure)
}
