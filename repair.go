package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Only stable codes and opaque server IDs leave the coordinator. Never raw
// network errors, URLs, DNS replies, credentials or configuration snapshots.
type repairCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
type repairCandidate struct {
	ID        string `json:"id"`
	CheckedAt int64  `json:"checkedAt"`
}
type repairView struct {
	OperationID      string            `json:"operationId"`
	Phase            string            `json:"phase"`
	Diagnosis        string            `json:"diagnosis"`
	Result           string            `json:"result"`
	Checks           []repairCheck     `json:"checks"`
	Candidates       []repairCandidate `json:"candidates"`
	ExpectedRevision uint64            `json:"expectedRevision"`
	ExpiresAt        int64             `json:"expiresAt"`
	Proposal         string            `json:"proposal"`
}
type repairPlan struct {
	snap       *repairCapture
	candidates map[string]int
	expires    time.Time
	restore    bool
}
type repairState struct {
	mu        sync.Mutex
	view      repairView
	cancel    context.CancelFunc
	switching bool
	plan      *repairPlan
}

func (r *repairState) busy() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.cancel != nil }
func (r *repairState) snapshot() repairView {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.view
	v.Checks = append([]repairCheck{}, v.Checks...)
	v.Candidates = append([]repairCandidate{}, v.Candidates...)
	return v
}
func (r *repairState) stop(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != "" && id != r.view.OperationID {
		return errors.New("UNKNOWN_OPERATION")
	}
	r.plan = nil
	if r.cancel == nil && r.view.Phase == "PLAN_READY" {
		r.view.Phase = "CANCELLED"
		r.view.Proposal = ""
	}
	if r.cancel != nil {
		r.cancel()
		if r.switching {
			r.view.Phase = "CANCELLING"
		} else {
			r.cancel = nil
			r.view.Phase = "CANCELLED"
			r.view.Result = "NO_ACTION"
		}
	}
	return nil
}
func (r *repairState) begin() (context.Context, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return nil, "", errors.New("REPAIR_BUSY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	id := randHex(16)
	r.cancel = cancel
	r.switching = false
	r.plan = nil
	r.view = repairView{OperationID: id, Phase: "CAPTURING", Result: "NO_ACTION", Checks: []repairCheck{}, Candidates: []repairCandidate{}}
	return ctx, id, nil
}
func (r *repairState) check(id, name, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.OperationID != id || r.cancel == nil {
		return
	}
	r.view.Phase = name
	if len(r.view.Checks) < 64 {
		r.view.Checks = append(r.view.Checks, repairCheck{name, status})
	}
}
func (r *repairState) finish(id, diagnosis string, cancelled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.OperationID != id || r.cancel == nil {
		return
	}
	r.cancel()
	r.cancel = nil
	r.switching = false
	r.view.Diagnosis = diagnosis
	r.view.Phase = "PLAN_READY"
	if cancelled {
		r.view.Phase = "CANCELLED"
		r.plan = nil
	}
}
func (r *repairState) offer(id string, plan *repairPlan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.OperationID != id || r.cancel == nil {
		return
	}
	r.plan = plan
	r.view.ExpectedRevision = plan.snap.choice.rev
	r.view.ExpiresAt = plan.expires.Unix()
	r.view.Proposal = "TEMPORARY_FAILOVER"
	for server := range plan.candidates {
		r.view.Candidates = append(r.view.Candidates, repairCandidate{strings.TrimPrefix(server, "s-"), time.Now().Unix()})
	}
	sort.Slice(r.view.Candidates, func(i, j int) bool { return r.view.Candidates[i].ID < r.view.Candidates[j].ID })
}
func (r *repairState) action(id string, revision uint64, target string) (context.Context, *repairPlan, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != r.view.OperationID {
		return nil, nil, "", errors.New("UNKNOWN_OPERATION")
	}
	// Duplicate confirmation is idempotent: never start a second mutation.
	if r.switching || r.view.Phase == "COMMITTED" || r.view.Phase == "ROLLED_BACK" || r.view.Phase == "ROLLBACK_FAILED" {
		return nil, nil, "", nil
	}
	if r.cancel != nil {
		return nil, nil, "", errors.New("REPAIR_BUSY")
	}
	if r.plan == nil || r.view.Phase != "PLAN_READY" || time.Now().After(r.plan.expires) || revision != r.plan.snap.choice.rev {
		return nil, nil, "", errors.New("STALE_PLAN")
	}
	target = "s-" + target
	port, ok := r.plan.candidates[target]
	if !ok {
		return nil, nil, "", errors.New("UNVERIFIED_CANDIDATE")
	}
	// The action owns its private snapshot; status reads cannot mutate it.
	plan := r.plan
	r.plan = nil
	plan.candidates = map[string]int{target: port}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	r.cancel = cancel
	r.switching = true
	r.view.Phase = "RECHECKING"
	r.view.Proposal = ""
	return ctx, plan, target, nil
}
func (r *repairState) outcome(id, phase, result, diagnosis string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.OperationID != id {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel = nil
	r.switching = false
	r.plan = nil
	r.view.Phase = phase
	r.view.Result = result
	r.view.Diagnosis = diagnosis
	r.view.Proposal = ""
}
func (a *App) startRepair() (repairView, error) {
	a.life.intentMu.Lock()
	defer a.life.intentMu.Unlock()
	if a.life.closing || a.life.updating {
		return repairView{}, errors.New("APP_CLOSING")
	}
	ctx, id, err := a.repair.begin()
	if err != nil {
		return repairView{}, err
	}
	go a.diagnoseRepair(ctx, id)
	return a.repair.snapshot(), nil
}
func (a *App) confirmRepair(id string, revision uint64, target string) (repairView, error) {
	ctx, plan, target, err := a.repair.action(id, revision, target)
	if err != nil {
		return repairView{}, err
	}
	if plan != nil {
		go a.performRepair(ctx, id, plan, target, appRepairIO{a})
	}
	return a.repair.snapshot(), nil
}

type repairPath struct {
	HTTP [2]string
	DNS  string
}

func (p repairPath) good() bool     { return p.HTTP[0] == "OK" && p.HTTP[1] == "OK" }
func (p repairPath) verified() bool { return p.good() && p.DNS == "OK" }
func (p repairPath) partial() bool  { return p.HTTP[0] == "OK" || p.HTTP[1] == "OK" }

type repairIO interface {
	owned(*repairCapture) bool
	fresh(context.Context, *repairCapture, string) bool
	probe(context.Context, int, string) repairPath
	selectRoute(context.Context, *repairCapture, string, string) (bool, error)
	commit(context.Context, *repairCapture, string) bool
}

func (a *App) performRepair(ctx context.Context, id string, plan *repairPlan, target string, driver repairIO) {
	snap := plan.snap
	origin := snap.selector
	port := plan.candidates[target]
	resultPhase, result, diagnosis := "CANCELLED", "NO_ACTION", "STALE_STATE"
	attempted := false
	defer func() {
		a.repair.outcome(id, resultPhase, result, diagnosis)
		if a.mon != nil {
			kind := "info"
			if resultPhase == "ROLLBACK_FAILED" {
				kind = "error"
			}
			a.mon.Event(kind, "Восстановление ["+id+"]: "+resultPhase+" · "+diagnosis, false)
		}
	}()
	if !driver.fresh(ctx, snap, origin) {
		return
	}
	// Recheck origin and candidate without mutating the live selector. One healthy
	// control URL is enough to reject a site-specific/ambiguous failover proposal.
	var primary, candidate repairPath
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); primary = driver.probe(ctx, snap.primaryPort, snap.doh) }()
	go func() { defer wg.Done(); candidate = driver.probe(ctx, port, snap.doh) }()
	wg.Wait()
	if ctx.Err() != nil {
		diagnosis = "CANCELLED"
		return
	}
	if !plan.restore && primary.partial() {
		resultPhase = "PLAN_READY"
		diagnosis = "PRIMARY_RECOVERED"
		return
	}
	if !candidate.verified() {
		resultPhase = "PLAN_READY"
		diagnosis = "CANDIDATE_NO_LONGER_HEALTHY"
		return
	}
	if !driver.fresh(ctx, snap, origin) {
		return
	}
	a.repair.check(id, "SWITCHING", "RUNNING")
	var err error
	attempted, err = driver.selectRoute(ctx, snap, origin, target)
	if !attempted {
		return
	}
	rollback := func() {
		// Independent bounded context, but ownership/generation are checked again.
		// Cancellation never authorizes a reconnect or overriding a new manual choice.
		rb, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if !driver.fresh(rb, snap, target) {
			// A failed PUT may already have left the original route in place.
			if driver.fresh(rb, snap, origin) {
				resultPhase = "ROLLED_BACK"
				result = "ROLLED_BACK"
				diagnosis = "ORIGINAL_CONFIGURATION_PRESENT"
				return
			}
			if driver.owned(snap) {
				resultPhase = "ROLLBACK_FAILED"
				result = "FAILED"
				diagnosis = "OWNERSHIP_UNVERIFIED"
			} else {
				resultPhase = "CANCELLED"
				result = "NO_ACTION"
				diagnosis = "USER_OR_CORE_CHANGED"
			}
			return
		}
		a.repair.check(id, "ROLLING_BACK", "RUNNING")
		_, rollbackErr := driver.selectRoute(rb, snap, target, origin)
		if rollbackErr != nil || !driver.fresh(rb, snap, origin) {
			resultPhase = "ROLLBACK_FAILED"
			result = "FAILED"
			diagnosis = "ROLLBACK_FAILED"
			return
		}
		resultPhase = "ROLLED_BACK"
		result = "ROLLED_BACK"
		diagnosis = "ORIGINAL_CONFIGURATION_RESTORED"
		oldPath := driver.probe(rb, snap.primaryPort, snap.doh)
		userPath := driver.probe(rb, snap.mixedPort, snap.doh)
		a.repair.check(id, "ROLLBACK_USER_PATH", repairPathStatus(userPath))
		if oldPath.verified() && userPath.verified() {
			diagnosis = "RESERVE_PATH_RESTORED"
		}
	}
	if err != nil || ctx.Err() != nil {
		rollback()
		return
	}
	a.repair.check(id, "VERIFYING", "RUNNING")
	forced := driver.probe(ctx, port, snap.doh)
	actual := driver.probe(ctx, snap.mixedPort, snap.doh)
	a.repair.check(id, "TARGET_PATH", repairPathStatus(forced))
	a.repair.check(id, "USER_PATH", repairPathStatus(actual))
	if ctx.Err() != nil || !forced.verified() || !actual.verified() || !driver.fresh(ctx, snap, target) {
		rollback()
		return
	}
	if !driver.commit(ctx, snap, target) {
		rollback()
		return
	}
	resultPhase = "COMMITTED"
	result = "SWITCHED"
	if plan.restore {
		result = "RESTORED"
	}
	diagnosis = "CONTROL_PATH_CONFIRMED"
}
func repairPathStatus(p repairPath) string {
	if p.verified() {
		return "OK"
	}
	if p.partial() {
		return "PARTIAL"
	}
	return "UNKNOWN"
}
