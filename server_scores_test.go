package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServerScoresStatisticsQuarantineAndExpiry(t *testing.T) {
	var q serverScores
	now := time.Unix(1800000000, 0)
	tags := []string{"s-a", "s-b"}
	for i, ms := range []int{100, 200, 300, 400} {
		q.observe("one", tags, map[string]int{"s-a": ms, "s-b": 10}, now.Add(time.Duration(i)*time.Minute))
	}
	rows := q.view(now.Add(3 * time.Minute))
	if len(rows) != 2 || rows[1].Median != 250 || rows[1].P95 != 400 || rows[1].Successes != 4 {
		t.Fatal(rows)
	}
	for i := 4; i < 7; i++ {
		q.observe("one", tags, map[string]int{"s-a": 100}, now.Add(time.Duration(i)*time.Minute))
	}
	rows = q.view(now.Add(6 * time.Minute))
	for _, r := range rows {
		if r.Tag == "s-b" && (r.Eligible || r.QuarantineUntil == 0) {
			t.Fatal(r)
		}
	}
	q.observe("one", tags, map[string]int{"s-a": 100, "s-b": 10}, now.Add(7*time.Minute))
	for _, r := range q.view(now.Add(7 * time.Minute)) {
		if r.Tag == "s-b" && r.Eligible {
			t.Fatal("quarantine ended on first recovery", r)
		}
	}
	q.observe("one", tags, map[string]int{"s-a": 100, "s-b": 10}, now.Add(8*time.Minute+time.Second))
	for _, r := range q.view(now.Add(8*time.Minute + time.Second)) {
		if r.Tag == "s-b" && !r.Eligible {
			t.Fatal("quarantine never expires", r)
		}
	}
	for _, r := range q.view(now.Add(10 * time.Minute)) {
		if r.Eligible {
			t.Fatal("stale answer eligible", r)
		}
	}
	for _, r := range q.view(now.Add(time.Hour)) {
		if r.Samples != 0 || r.Eligible {
			t.Fatal("history did not expire", r)
		}
	}
}
func TestServerScoresSpikeDwellAndConfirmedImprovement(t *testing.T) {
	var q serverScores
	now := time.Unix(1800000000, 0)
	tags := []string{"s-a", "s-b"}
	q.observe("one", tags, map[string]int{"s-a": 100, "s-b": 100}, now)
	next, _ := q.decide("s-a", now, false)
	if next != "s-a" {
		t.Fatal(next)
	}
	q.commit("one", next, now)
	for i := 1; i <= 5; i++ {
		ms := 100
		if i == 1 {
			ms = 5000
		}
		at := now.Add(time.Duration(i) * time.Minute)
		q.observe("one", tags, map[string]int{"s-a": ms, "s-b": 100}, at)
		if next, why := q.decide("s-a", at, false); next != "" {
			t.Fatal("single latency spike caused switch", next, why)
		}
	}
	q.reset()
	for i := 0; i <= 4; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		q.observe("two", tags, map[string]int{"s-a": 1200, "s-b": 50}, at)
		if i == 0 {
			q.commit("two", "s-a", at)
			continue
		}
		next, _ := q.decide("s-a", at, false)
		if i < 4 && next != "" {
			t.Fatal("switch before dwell/two confirmations", i, next)
		}
		if i == 4 && next != "s-b" {
			t.Fatal("sustained improvement not used", next)
		}
	}
}
func TestServerScoresRepeatedFailureBypassesDwell(t *testing.T) {
	var q serverScores
	now := time.Unix(1800000000, 0)
	tags := []string{"s-a", "s-b"}
	q.observe("one", tags, map[string]int{"s-a": 100, "s-b": 200}, now)
	q.commit("one", "s-a", now)
	for i := 1; i <= 2; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		q.observe("one", tags, map[string]int{"s-b": 200}, at)
		next, _ := q.decide("s-a", at, false)
		if i == 1 && next != "" {
			t.Fatal("single failed control request caused switch")
		}
		if i == 2 {
			if next != "s-b" {
				t.Fatal(next)
			}
			q.commit("one", next, at)
		}
	}
	if !q.quarantined("s-a", now.Add(2*time.Minute)) {
		t.Fatal("failed current server was not quarantined")
	}
}
func TestServerScoresResetBoundsAndAllowlist(t *testing.T) {
	var q serverScores
	now := time.Now()
	for i := 0; i < 100; i++ {
		q.observe("one", []string{"s-a"}, map[string]int{"s-a": 10, "evil": 1}, now.Add(time.Duration(i)*time.Second))
	}
	rows := q.view(now.Add(100 * time.Second))
	if len(rows) != 1 || rows[0].Samples != 20 {
		t.Fatal(rows)
	}
	q.commit("one", "s-a", now)
	q.observe("new-core", []string{"s-b"}, map[string]int{"s-b": 20}, now)
	if q.held("one") != "" || q.held("new-core") != "" || len(q.view(now)) != 1 {
		t.Fatal("session/allowlist not reset")
	}
	q.reset()
	if len(q.view(now)) != 0 {
		t.Fatal("manual choice reset failed")
	}
	s := DefaultSettings()
	before := s.fingerprint()
	s.SmartSelect = true
	if s.fingerprint() != before {
		t.Fatal("mode toggle unnecessarily restarts core")
	}
}

type scoreTransport func(*http.Request) (*http.Response, error)

func (f scoreTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func scoreResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func qualityApp(t *testing.T) (*App, *Clash) {
	t.Helper()
	a := &App{dir: t.TempDir(), settings: DefaultSettings()}
	a.settings.SmartSelect = true
	a.settings.Selected = "auto"
	a.settings.Servers = []Server{{ID: "a", Type: "trojan", Host: "a.invalid", Port: 443, Password: "x"}, {ID: "b", Type: "trojan", Host: "b.invalid", Port: 443, Password: "y"}}
	a.core = NewCore(a.dir)
	a.core.running = true
	a.core.secret = "one"
	a.core.apiPort = 1234
	a.wl = &WL{}
	a.mon = newMonitor(a)
	_, _ = a.life.requestConnect()
	cl := a.core.Clash()
	return a, cl
}
func TestServerChoiceRejectsStaleIntentAndCore(t *testing.T) {
	for _, kind := range []string{"manual", "disconnect", "core", "settings", "toggle", "pending"} {
		t.Run(kind, func(t *testing.T) {
			a, cl := qualityApp(t)
			snap := a.choiceSnapshot()
			switch kind {
			case "manual":
				// Real manual API offline: no sockets or core calls are needed to publish intent.
				a.core.running = false
				_, err := a.apiSelect(httptest.NewRequest("POST", "/api/select", strings.NewReader(`{"id":"b"}`)))
				if err != nil {
					t.Fatal(err)
				}
				a.core.running = true
			case "disconnect":
				a.life.stopIntent(false)
			case "core":
				a.core.secret = "new-core"
			case "settings":
				a.settings.Servers[0].Password = "changed"
			case "toggle":
				a.settings.SmartSelect = false
			case "pending":
				a.applied = "different-config"
			}
			called := false
			if a.commitChoice(snap, cl, func(context.Context, *Settings) bool { called = true; return true }) || called {
				t.Fatal("stale result applied")
			}
		})
	}
}
func TestServerChoiceDisconnectCancelsHTTP(t *testing.T) {
	a, cl := qualityApp(t)
	snap := a.choiceSnapshot()
	entered := make(chan struct{})
	cl.hc = &http.Client{Transport: scoreTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	done := make(chan bool, 1)
	go func() {
		done <- a.commitChoice(snap, cl, func(ctx context.Context, _ *Settings) bool { return cl.SelectContext(ctx, "s-b") == nil })
	}()
	<-entered
	a.life.stopIntent(false)
	select {
	case ok := <-done:
		if ok {
			t.Fatal("cancelled selection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel selection")
	}
}
func TestServerScoreTickManualAndBatchFailure(t *testing.T) {
	for _, manual := range []bool{true, false} {
		a, cl := qualityApp(t)
		if manual {
			a.settings.Selected = "a"
		}
		puts := 0
		cl.hc = &http.Client{Transport: scoreTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == "PUT" {
				puts++
				return scoreResponse("{}"), nil
			}
			if strings.HasPrefix(r.URL.Path, "/group/") {
				return scoreResponse(`{"s-a":100,"s-b":20,"evil":1}`), nil
			}
			return scoreResponse(`{"proxies":{"proxy":{"now":"auto"},"auto":{"now":"s-a"}}}`), nil
		})}
		a.scoreTick(cl)
		if manual && puts != 0 {
			t.Fatal("manual choice overridden")
		}
		if !manual && puts != 1 {
			t.Fatal("auto choice not pinned", puts)
		}
		if len(a.scores.view(time.Now())) != 2 {
			t.Fatal("unconfigured server scored")
		}
		old := a.scores.view(time.Now())[0].Samples
		cl.hc.Transport = scoreTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("batch unavailable") })
		a.scoreTick(cl)
		if a.scores.view(time.Now())[0].Samples != old {
			t.Fatal("batch error counted as individual server failures")
		}
	}
}
func TestServerScoresConcurrent(t *testing.T) {
	var q serverScores
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				q.observe("one", []string{"s-a"}, map[string]int{"s-a": 10}, time.Now())
				_ = q.view(time.Now())
				q.decide("s-a", time.Now(), false)
				q.reset()
			}
		}()
	}
	wg.Wait()
}
func TestServerFailbackKeepsReserveOnSelectFailure(t *testing.T) {
	a, cl := qualityApp(t)
	a.settings.Selected = "a"
	a.settings.SmartSelect = false
	a.fo.set("s-a", "s-b")
	cl.hc = &http.Client{Transport: scoreTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "PUT" {
			return nil, errors.New("select rejected")
		}
		return scoreResponse(`{"delay":100}`), nil
	})}
	a.failback(cl)
	a.failback(cl)
	if from, to := a.fo.get(); from != "s-a" || to != "s-b" {
		t.Fatal("reserve state lost", from, to)
	}
	if len(a.mon.events) != 0 {
		b, _ := json.Marshal(a.mon.events)
		t.Fatal("false return event", string(b))
	}
}

func TestServerScoresReconcileObservedChoice(t *testing.T) {
	var q serverScores
	now := time.Now()
	tags := []string{"s-a", "s-b"}
	q.observe("one", tags, map[string]int{"s-a": 100, "s-b": 50}, now)
	q.commit("one", "s-a", now)
	if next, _ := q.decide("s-b", now.Add(time.Second), false); next != "" {
		t.Fatal("observed choice was unnecessarily replaced", next)
	}
	if q.held("one") != "s-b" {
		t.Fatal("old hold would restore an outdated route")
	}
}
