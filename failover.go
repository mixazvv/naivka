package main

import (
	"sync"
	"time"
)

// Переход на другой сервер: выбранный вручную сервер перестал отвечать — временно работаем
// через самый быстрый из живых своих серверов, а когда выбранный оживёт, возвращаемся.
// Выбор в настройках не меняется: это временная подмена, её видно в ленте событий.
type failoverState struct {
	mu      sync.Mutex
	from    string // тег выбранного сервера, который не отвечает
	to      string // тег, через который работаем сейчас
	restore string
	auto    bool
	repair  bool
	retryAt time.Time
	oks     int // сколько проверок подряд выбранный сервер снова отвечает
}

func (f *failoverState) get() (from, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.from, f.to
}

func (f *failoverState) set(from, to string) {
	f.mu.Lock()
	f.from, f.to, f.oks = from, to, 0
	f.retryAt = time.Time{}
	f.restore = from
	f.auto = false
	f.repair = false
	f.mu.Unlock()
}

func (f *failoverState) setRepair(from, to, restore string, auto bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.from, f.to, f.restore, f.auto, f.oks = from, to, restore, auto, 0
	f.repair = true
	f.retryAt = time.Time{}
}
func (f *failoverState) restoreTag() string { f.mu.Lock(); defer f.mu.Unlock(); return f.restore }
func (f *failoverState) autoRoute() bool    { f.mu.Lock(); defer f.mu.Unlock(); return f.auto }
func (f *failoverState) isRepair() bool     { f.mu.Lock(); defer f.mu.Unlock(); return f.repair }
func (f *failoverState) clear()             { f.set("", "") }

// selectedServerTag — тег выбранного вручную своего сервера; "" — автовыбор, подписка или ничего.
func selectedServerTag(s *Settings) string {
	for _, sv := range s.Servers {
		if sv.ID == s.Selected {
			return serverTag(sv)
		}
	}
	return ""
}

// pickFailover — самый быстрый живой сервер, кроме недоступных (delays: тег → мс, только ответившие).
func pickFailover(delays map[string]int, skip ...string) string {
	best, bestMs := "", 0
	for tag, ms := range delays {
		bad := ms <= 0
		for _, s := range skip {
			if tag == s {
				bad = true
			}
		}
		if bad {
			continue
		}
		if best == "" || ms < bestMs || (ms == bestMs && tag < best) {
			best, bestMs = tag, ms
		}
	}
	return best
}

// failover — связь через сервер пропала. true — перешли на другой сервер.
// The monitor's existing failover permission now uses the same verified
// transaction as the manual wizard. It never reports success on a TLS-only probe.
func (a *App) failover(s *Settings) bool {
	if !s.Failover || a.wl.Active() || a.repair.busy() || (selectedServerTag(s) == "" && !(s.Selected == "auto" && (s.SmartSelect || a.fo.autoRoute()))) {
		return false
	}
	ctx, id, err := a.repair.begin()
	if err != nil {
		return false
	}
	diagnosis := "NO_VERIFIED_BACKUP"
	performed := false
	defer func() {
		if !performed {
			phase := "PLAN_READY"
			if ctx.Err() != nil {
				phase = "CANCELLED"
				diagnosis = "CANCELLED"
			}
			a.repair.outcome(id, phase, "NO_ACTION", diagnosis)
		}
	}()
	a.scores.work.Lock()
	defer a.scores.work.Unlock()
	cl := a.core.Clash()
	snap, err := a.captureRepair(ctx, cl)
	if err != nil {
		diagnosis = "CORE_API_UNAVAILABLE"
		return false
	}
	snap.operationID = id
	if snap.choice.settings.Selected != s.Selected || snap.choice.settings.fingerprint() != s.fingerprint() || !(appRepairIO{a}).fresh(ctx, snap, snap.selector) {
		diagnosis = "STALE_STATE"
		return false
	}
	delays, err := cl.groupDelayContext(ctx, tagAuto, s.TestURL, 5000)
	if err != nil {
		return false
	}
	allowed := map[string]int{}
	for _, tag := range serverScoreTags(s) {
		if tag != snap.effective && tag != selectedServerTag(s) && snap.ports[tag] > 0 && repairCompatible(s, tag) && !a.scores.quarantined(tag, time.Now()) {
			if ms := delays[tag]; ms > 0 {
				allowed[tag] = ms
			}
		}
	}
	if s.SmartSelect {
		a.scores.observe(scoreEpoch(s, cl), serverScoreTags(s), delays, time.Now())
	}
	type backupResult struct {
		tag  string
		path repairPath
	}
	results := make(chan backupResult, 3)
	ordered := []string{}
	for len(ordered) < 3 {
		tag := pickFailover(allowed)
		if tag == "" {
			break
		}
		delete(allowed, tag)
		ordered = append(ordered, tag)
		go func(tag string) { results <- backupResult{tag, probeRepairPath(ctx, snap.ports[tag], snap.doh)} }(tag)
	}
	verified := map[string]int{}
	for range ordered {
		r := <-results
		if r.path.verified() {
			verified[r.tag] = delays[r.tag]
		}
	}
	best := pickFailover(verified)
	why := "две HTTPS-проверки и DNS через резерв"
	if s.SmartSelect {
		candidate, reason := a.scores.decide(snap.effective, time.Now(), true, snap.effective, selectedServerTag(s))
		if _, ok := verified[candidate]; ok {
			best, why = candidate, reason
		}
	}
	if best == "" || ctx.Err() != nil {
		return false
	}
	a.repair.mu.Lock()
	if a.repair.cancel == nil {
		a.repair.mu.Unlock()
		return false
	}
	a.repair.switching = true
	a.repair.view.Phase = "RECHECKING"
	a.repair.mu.Unlock()
	performed = true
	a.performRepair(ctx, id, &repairPlan{snap: snap, candidates: map[string]int{best: snap.ports[best]}}, best, appRepairIO{a})
	v := a.repair.snapshot()
	ok := v.OperationID == id && v.Result == "SWITCHED"
	if ok {
		a.mon.Event("warn", "Сервер «"+a.serverName(snap.effective)+"» не отвечает — временно работаю через «"+a.serverName(best)+"»: "+why, true)
	}
	return ok
}

// Return after two successful probes; publish the state only after Select succeeds.
func (a *App) failback(cl *Clash) {
	if a.repair.busy() {
		return
	}
	snap := a.choiceSnapshot()
	from, to := a.fo.get()
	if from == "" {
		return
	}
	if (!a.fo.autoRoute() && selectedServerTag(snap.settings) != from) || (a.fo.autoRoute() && snap.settings.Selected != "auto") || (!snap.settings.Failover && !a.fo.isRepair()) {
		return
	}
	a.fo.mu.Lock()
	retry := a.fo.retryAt
	a.fo.mu.Unlock()
	if time.Now().Before(retry) || (snap.settings.SmartSelect && a.scores.quarantined(from, time.Now())) {
		return
	}
	ms, err := cl.Delay(from, snap.settings.TestURL)
	a.fo.mu.Lock()
	if a.fo.from != from || a.fo.to != to {
		a.fo.mu.Unlock()
		return
	}
	if err != nil || ms <= 0 {
		a.fo.oks = 0
		a.fo.mu.Unlock()
		return
	}
	a.fo.oks++
	ready := a.fo.oks >= 2
	if ready {
		a.fo.oks = 0
		a.fo.retryAt = time.Now().Add(2 * time.Minute)
	}
	a.fo.mu.Unlock()
	if ready {
		a.startVerifiedFailback(cl, from, to, snap)
	}
}
