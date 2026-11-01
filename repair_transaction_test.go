package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func healthyRepairPath() repairPath { return repairPath{HTTP: [2]string{"OK", "OK"}, DNS: "OK"} }

type fakeRepairIO struct {
	mu                         sync.Mutex
	route                      string
	owner                      bool
	primary, candidate, actual repairPath
	selects                    []string
	failSelect, failRollback   bool
	committed                  bool
	onSelect                   func()
}

func (f *fakeRepairIO) owned(*repairCapture) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.owner }
func (f *fakeRepairIO) fresh(ctx context.Context, _ *repairCapture, expected string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ctx.Err() == nil && f.owner && f.route == expected
}
func (f *fakeRepairIO) probe(_ context.Context, port int, _ string) repairPath {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch port {
	case 1:
		return f.primary
	case 2:
		return f.candidate
	default:
		if f.route == "s-a" {
			return f.primary
		}
		return f.actual
	}
}
func (f *fakeRepairIO) selectRoute(ctx context.Context, _ *repairCapture, expected, target string) (bool, error) {
	f.mu.Lock()
	if ctx.Err() != nil || !f.owner || f.route != expected {
		f.mu.Unlock()
		return false, errors.New("stale")
	}
	f.selects = append(f.selects, target)
	if target == "s-a" && f.failRollback {
		f.mu.Unlock()
		return true, errors.New("rollback rejected")
	}
	f.route = target
	hook := f.onSelect
	fail := f.failSelect && target == "s-b"
	f.mu.Unlock()
	if hook != nil && target == "s-b" {
		hook()
	}
	if fail {
		return true, errors.New("lost ACK")
	}
	return true, nil
}
func (f *fakeRepairIO) commit(ctx context.Context, _ *repairCapture, target string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil || !f.owner || f.route != target {
		return false
	}
	f.committed = true
	return true
}
func transactionFixture(t *testing.T) (*App, *repairPlan, *fakeRepairIO, string) {
	t.Helper()
	a := &App{}
	_, id, err := a.repair.begin()
	if err != nil {
		t.Fatal(err)
	}
	a.repair.mu.Lock()
	a.repair.switching = true
	a.repair.mu.Unlock()
	plan := &repairPlan{snap: &repairCapture{selector: "s-a", primaryPort: 1, mixedPort: 3, doh: "https://resolver.invalid/dns-query"}, candidates: map[string]int{"s-b": 2}, expires: time.Now().Add(time.Minute)}
	driver := &fakeRepairIO{route: "s-a", owner: true, candidate: healthyRepairPath(), actual: healthyRepairPath()}
	return a, plan, driver, id
}
func TestRepairTransactionScenarios(t *testing.T) {
	for _, tc := range []struct {
		name          string
		prepare       func(*fakeRepairIO, *repairPlan, context.CancelFunc)
		phase, result string
		puts          int
	}{
		{"T01 primary works", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) { f.primary = healthyRepairPath() }, "PLAN_READY", "NO_ACTION", 0},
		{"T02 verified reserve", func(*fakeRepairIO, *repairPlan, context.CancelFunc) {}, "COMMITTED", "SWITCHED", 1},
		{"T03 no reserve", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) { f.candidate = repairPath{} }, "PLAN_READY", "NO_ACTION", 0},
		{"T06 one endpoint", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) {
			f.primary = repairPath{HTTP: [2]string{"OK", "TIMEOUT"}}
		}, "PLAN_READY", "NO_ACTION", 0},
		{"T07 reserve cannot carry user traffic", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) { f.actual = repairPath{} }, "ROLLED_BACK", "ROLLED_BACK", 2},
		{"T08 cancel before select", func(_ *fakeRepairIO, _ *repairPlan, cancel context.CancelFunc) { cancel() }, "CANCELLED", "NO_ACTION", 0},
		{"T09 stale manual choice", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) { f.owner = false; f.route = "s-manual" }, "CANCELLED", "NO_ACTION", 0},
		{"T10 core lost after select", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) {
			f.onSelect = func() { f.mu.Lock(); f.owner = false; f.mu.Unlock() }
		}, "CANCELLED", "NO_ACTION", 1},
		{"T12 failed failback restores reserve", func(f *fakeRepairIO, p *repairPlan, _ context.CancelFunc) {
			p.restore = true
			f.primary = healthyRepairPath()
			f.actual = repairPath{}
		}, "ROLLED_BACK", "ROLLED_BACK", 2},
		{"T21 cancel after select", func(f *fakeRepairIO, _ *repairPlan, cancel context.CancelFunc) { f.onSelect = cancel }, "ROLLED_BACK", "ROLLED_BACK", 2},
		{"T22 rollback failure", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) {
			f.actual = repairPath{}
			f.failRollback = true
		}, "ROLLBACK_FAILED", "FAILED", 2},
		{"manual supersedes rollback", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) {
			f.onSelect = func() { f.mu.Lock(); f.owner = false; f.route = "s-manual"; f.mu.Unlock() }
		}, "CANCELLED", "NO_ACTION", 1},
		{"ambiguous PUT response rolls back", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) { f.failSelect = true }, "ROLLED_BACK", "ROLLED_BACK", 2},
		{"DNS proof missing", func(f *fakeRepairIO, _ *repairPlan, _ context.CancelFunc) { f.candidate.DNS = "NXDOMAIN" }, "PLAN_READY", "NO_ACTION", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, p, f, id := transactionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tc.prepare(f, p, cancel)
			a.performRepair(ctx, id, p, "s-b", f)
			v := a.repair.snapshot()
			if v.Phase != tc.phase || v.Result != tc.result || len(f.selects) != tc.puts {
				t.Fatalf("phase %s result %s puts %v", v.Phase, v.Result, f.selects)
			}
			if v.Result != "SWITCHED" && f.committed {
				t.Fatal("failed operation was committed")
			}
			if a.repair.busy() {
				t.Fatal("coordinator leaked")
			}
		})
	}
}
func TestRepairPlansExpiryRevisionAllowlistAndIdempotence(t *testing.T) {
	var r repairState
	_, id, _ := r.begin()
	snap := &repairCapture{choice: selectionSnapshot{rev: 7}}
	r.offer(id, &repairPlan{snap: snap, candidates: map[string]int{"s-b": 2}, expires: time.Now().Add(time.Minute)})
	r.finish(id, "PRIMARY_UNREACHABLE", false)
	if _, _, _, err := r.action(id, 6, "b"); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, _, _, err := r.action(id, 7, "unknown"); err == nil {
		t.Fatal("unverified candidate accepted")
	}
	ctx, plan, target, err := r.action(id, 7, "b")
	if err != nil || plan == nil || target != "s-b" {
		t.Fatal(plan, target, err)
	}
	if _, second, _, err := r.action(id, 7, "b"); err != nil || second != nil {
		t.Fatal("duplicate mutation started")
	}
	r.stop(id)
	if ctx.Err() == nil || !r.busy() {
		t.Fatal("cancel freed coordinator before rollback finished")
	}
	r.outcome(id, "ROLLED_BACK", "ROLLED_BACK", "ORIGINAL_CONFIGURATION_RESTORED")
	if r.busy() {
		t.Fatal("coordinator never freed")
	}
	_, next, _ := r.begin()
	r.offer(next, &repairPlan{snap: snap, candidates: map[string]int{"s-b": 2}, expires: time.Now().Add(-time.Second)})
	r.finish(next, "PRIMARY_UNREACHABLE", false)
	if _, _, _, err := r.action(next, 7, "b"); err == nil {
		t.Fatal("expired plan accepted")
	}
}
func TestRepairDNSValidation(t *testing.T) {
	packet := []byte{0x4e, 0x56, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 93, 184, 216, 34}
	if got := repairDNSReply(packet); got != "OK" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
		want   string
	}{
		{"nxdomain", func(p []byte) []byte { p[3] = 0x83; binary.BigEndian.PutUint16(p[6:8], 0); return p[:29] }, "NXDOMAIN"},
		{"wrong ID", func(p []byte) []byte { p[0] = 0; return p }, "INVALID_RESPONSE"},
		{"truncated", func(p []byte) []byte { return p[:len(p)-1] }, "INVALID_RESPONSE"},
		{"private interception", func(p []byte) []byte { copy(p[len(p)-4:], []byte{127, 0, 0, 1}); return p }, "UNEXPECTED_ANSWER"},
		{"zero answers", func(p []byte) []byte { binary.BigEndian.PutUint16(p[6:8], 0); return p[:29] }, "NO_ANSWER"},
		{"malformed compression", func(p []byte) []byte { p[30] = 255; return p }, "INVALID_RESPONSE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.change(append([]byte{}, packet...))
			if got := repairDNSReply(p); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
func TestRepairProbeNoDirectFallbackAndNoRedirect(t *testing.T) {
	if repairClient(0) != nil || repairClient(70000) != nil {
		t.Fatal("invalid proxy accepted")
	}
	client := repairClient(1234)
	tr := client.Transport.(*http.Transport)
	u, err := tr.Proxy(&http.Request{})
	if err != nil || u.Host != "127.0.0.1:1234" {
		t.Fatal(u, err)
	}
	if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect followed")
	}
	// The fake HTTP round trip never opens a socket or reads environment proxies.
	client.Transport = scoreTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("private body")), Header: make(http.Header)}, nil
	})
	if got := repairHTTP(context.Background(), client, "https://fixture.invalid"); got != "REDIRECT" {
		t.Fatal(got)
	}
}

func TestRepairCompatibleCandidates(t *testing.T) {
	s := &Settings{Mode: "tun", Servers: []Server{{ID: "naive", Type: "naive"}, {ID: "udp", Type: "vless"}}}
	if repairCompatible(s, "s-naive") || !repairCompatible(s, "s-udp") || repairCompatible(s, "outside") {
		t.Fatal("incompatible or unknown candidate allowed")
	}
	s.Mode = "proxy"
	if !repairCompatible(s, "s-naive") {
		t.Fatal("supported proxy candidate excluded")
	}
}
func TestRepairOpaqueServerIDs(t *testing.T) {
	var r repairState
	_, id, _ := r.begin()
	r.offer(id, &repairPlan{snap: &repairCapture{}, candidates: map[string]int{"s-s-b": 2, "s-b": 3}, expires: time.Now().Add(time.Minute)})
	r.finish(id, "PRIMARY_UNREACHABLE", false)
	ctx, plan, target, err := r.action(id, 0, "s-b")
	if err != nil || target != "s-s-b" || plan.candidates[target] != 2 {
		t.Fatal(target, err)
	}
	r.stop(id)
	if ctx.Err() == nil {
		t.Fatal("cancel failed")
	}
	r.outcome(id, "CANCELLED", "NO_ACTION", "CANCELLED")
}

func TestRepairTUNRouteCoverage(t *testing.T) {
	table := make([]byte, 8+4*104)
	binary.LittleEndian.PutUint32(table, 4)
	for i := 0; i < 4; i++ {
		row := table[8+i*104 : 8+(i+1)*104]
		binary.LittleEndian.PutUint32(row[8:12], 7)
		row[40] = 1
		if i < 2 {
			binary.LittleEndian.PutUint16(row[12:14], 2)
			if i == 1 {
				row[16] = 128
			}
		} else {
			binary.LittleEndian.PutUint16(row[12:14], 23)
			if i == 3 {
				row[20] = 128
			}
		}
	}
	v4, v6, known := repairRouteCoverage(table, 7)
	if !known || !v4 || !v6 {
		t.Fatal(v4, v6, known)
	}
	if a, b, k := repairRouteCoverage(table, 8); !k || a || b {
		t.Fatal("other interface accepted")
	}
	table[8+104+40] = 2
	v4, v6, known = repairRouteCoverage(table, 7)
	if !known || v4 || !v6 {
		t.Fatal("incomplete IPv4 coverage accepted")
	}
	if _, _, k := repairRouteCoverage(table[:10], 7); k {
		t.Fatal("truncated table accepted")
	}
}

func TestRepairLinkedCancellation(t *testing.T) {
	user, stopUser := context.WithCancel(context.Background())
	stopUser()
	ctx, finish := linkRepairContext(context.Background(), user)
	if ctx.Err() == nil {
		t.Fatal("already cancelled confirmation can still issue a selector request")
	}
	finish()
	life, stopLife := context.WithCancel(context.Background())
	ctx, finish = linkRepairContext(life, context.Background())
	defer finish()
	stopLife()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("lifecycle cancellation not propagated")
	}
}
