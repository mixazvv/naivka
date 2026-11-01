package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Diagnostic reports deliberately contain codes only: no endpoints, credentials,
// raw errors, configuration, or process command lines.
type repairCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
type repairView struct {
	OperationID string        `json:"operationId"`
	Phase       string        `json:"phase"`
	Diagnosis   string        `json:"diagnosis"`
	Result      string        `json:"result"`
	Checks      []repairCheck `json:"checks"`
}
type repairState struct {
	mu     sync.Mutex
	view   repairView
	cancel context.CancelFunc
}

func (r *repairState) snapshot() repairView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.view
	v.Checks = append([]repairCheck{}, v.Checks...)
	return v
}
func (r *repairState) stop(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != "" && id != r.view.OperationID {
		return errors.New("UNKNOWN_OPERATION")
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
		r.view.Phase = "CANCELLED"
		r.view.Result = "NO_ACTION"
	}
	return nil
}
func (r *repairState) begin() (context.Context, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return nil, "", errors.New("REPAIR_BUSY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	id := randHex(16)
	r.cancel = cancel
	r.view = repairView{OperationID: id, Phase: "CAPTURING", Result: "NO_ACTION", Checks: []repairCheck{}}
	return ctx, id, nil
}
func (r *repairState) check(id, name, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.OperationID != id || r.cancel == nil {
		return
	}
	r.view.Phase = name
	r.view.Checks = append(r.view.Checks, repairCheck{name, status})
}
func (r *repairState) finish(id, diagnosis string, cancelled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.OperationID != id || r.cancel == nil {
		return
	}
	r.cancel()
	r.cancel = nil
	r.view.Diagnosis = diagnosis
	r.view.Phase = "PLAN_READY"
	if cancelled {
		r.view.Phase = "CANCELLED"
	}
}
func (a *App) startRepair() (repairView, error) {
	// Serialize the snapshot with shutdown/disconnect intent. Diagnostics never
	// take life.mu or alter wantConnected, settings, selectors or proxy settings.
	a.life.intentMu.Lock()
	defer a.life.intentMu.Unlock()
	if a.life.closing {
		return repairView{}, errors.New("APP_CLOSING")
	}
	ctx, id, err := a.repair.begin()
	if err != nil {
		return repairView{}, err
	}
	gen := a.life.gen.Load()
	s := a.settingsCopy()
	from, to := a.fo.get()
	go func() {
		diagnosis := "UNKNOWN"
		defer func() {
			f, t := a.fo.get()
			stale := a.life.gen.Load() != gen || a.settingsCopy().fingerprint() != s.fingerprint() || f != from || t != to
			if stale {
				diagnosis = "STALE_STATE"
			}
			a.repair.finish(id, diagnosis, ctx.Err() != nil || stale)
		}()
		status := "UNKNOWN"
		if networkUp() {
			status = "OK"
		}
		// Adapter presence alone cannot establish Internet access or a captive portal.
		a.repair.check(id, "LOCAL_ADAPTER", status)
		a.repair.check(id, "DIRECT_INTERNET", "UNKNOWN")
		a.repair.check(id, "DNS", "UNKNOWN")
		if ctx.Err() != nil {
			return
		}
		if s.ZapretOnly {
			diagnosis = "VPN_NOT_APPLICABLE"
			return
		}
		if !a.core.Running() {
			a.repair.check(id, "CORE", "DOWN")
			diagnosis = "CORE_NOT_RUNNING"
			return
		}
		cl := a.core.Clash()
		if cl == nil || cl.pingContext(ctx) != nil {
			a.repair.check(id, "CORE", "UNKNOWN")
			return
		}
		a.repair.check(id, "CORE", "OK")
		// Probe only the actual configured local proxy. Never fall back to a direct
		// client: it could bypass Kill Switch. HTTP redirects are not proof of success.
		if s.MixedPort <= 0 || s.MixedPort > 65535 {
			return
		}
		client := geoClient(s.MixedPort)
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		good := 0
		for i, u := range []string{"https://www.gstatic.com/generate_204", "https://cp.cloudflare.com/generate_204"} {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			req, _ := http.NewRequestWithContext(probeCtx, "GET", u, nil)
			resp, err := client.Do(req)
			ok := err == nil && resp.StatusCode == http.StatusNoContent
			if resp != nil {
				resp.Body.Close()
			}
			cancel()
			label := "HTTPS_A"
			if i == 1 {
				label = "HTTPS_B"
			}
			st := "UNKNOWN"
			if ok {
				good++
				st = "OK"
			}
			a.repair.check(id, label, st)
			if ctx.Err() != nil {
				return
			}
		}
		if good == 2 {
			diagnosis = "PATH_REACHABLE"
		} else if good == 1 {
			diagnosis = "PARTIAL_REACHABILITY"
		}
		// This initial phase does not distinguish DNS failure from outbound failure,
		// probe backup servers, or claim that routing and leak protection are correct.
	}()
	return a.repair.snapshot(), nil
}
