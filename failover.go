package main

import (
	"context"
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
	f.mu.Unlock()
}

func (f *failoverState) clear() { f.set("", "") }

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
func (a *App) failover(s *Settings) bool {
	if !s.Failover || a.wl.Active() {
		return false
	}
	a.scores.work.Lock()
	defer a.scores.work.Unlock()
	snap := a.choiceSnapshot()
	if s.Selected != snap.settings.Selected || s.fingerprint() != snap.settings.fingerprint() {
		return false
	}
	chosen := selectedServerTag(snap.settings)
	if chosen == "" {
		return false
	}
	cl := a.core.Clash()
	if cl == nil {
		return false
	}
	_, cur := a.fo.get()
	if cur == "" {
		cur = chosen
	}
	delays, err := cl.GroupDelay(tagAuto, s.TestURL, 5000)
	if err != nil {
		return false
	}
	return a.commitChoice(snap, cl, func(ctx context.Context, current *Settings) bool {
		from, to := a.fo.get()
		live := to
		if live == "" {
			live = chosen
		}
		if live != cur || (from != "" && from != chosen) {
			return false
		}
		allowedDelays := map[string]int{}
		for _, tag := range serverScoreTags(current) {
			if ms, ok := delays[tag]; ok {
				allowedDelays[tag] = ms
			}
		}
		best := pickFailover(allowedDelays, cur, chosen)
		why := "самый быстрый ответивший резерв"
		if current.SmartSelect {
			a.scores.observe(scoreEpoch(current, cl), serverScoreTags(current), delays, time.Now())
			best, why = a.scores.decide(cur, time.Now(), true, cur, chosen)
		}
		// Even without scoring, never select an injected/unconfigured group tag.
		allowed := false
		for _, tag := range serverScoreTags(current) {
			if best == tag {
				allowed = true
			}
		}
		if best == "" || !allowed {
			return false
		}
		if err := cl.SelectContext(ctx, best); err != nil || ctx.Err() != nil {
			return false
		}
		a.fo.set(chosen, best)
		a.mon.Event("warn", "Сервер «"+a.serverName(cur)+"» не отвечает — временно работаю через «"+a.serverName(best)+"»: "+why+". Вернусь, когда он оживёт", true)
		_ = cl.doContext(ctx, "DELETE", "/connections", nil, nil)
		return true
	})
}

// Return after two successful probes; publish the state only after Select succeeds.
func (a *App) failback(cl *Clash) {
	snap := a.choiceSnapshot()
	from, to := a.fo.get()
	if from == "" {
		return
	}
	if selectedServerTag(snap.settings) != from || !snap.settings.Failover {
		return
	}
	a.fo.mu.Lock()
	retryAt := a.fo.retryAt
	a.fo.mu.Unlock()
	if time.Now().Before(retryAt) || (snap.settings.SmartSelect && a.scores.quarantined(from, time.Now())) {
		return
	}
	ms, err := cl.Delay(from, snap.settings.TestURL)
	a.commitChoice(snap, cl, func(ctx context.Context, current *Settings) bool {
		a.fo.mu.Lock()
		if a.fo.from != from || a.fo.to != to {
			a.fo.mu.Unlock()
			return false
		}
		if err != nil || ms <= 0 {
			a.fo.oks = 0
			a.fo.mu.Unlock()
			return false
		}
		a.fo.oks++
		back := a.fo.oks >= 2
		a.fo.mu.Unlock()
		if !back {
			return false
		}
		if err := cl.SelectContext(ctx, from); err != nil || ctx.Err() != nil {
			a.fo.mu.Lock()
			a.fo.oks = 0
			a.fo.retryAt = time.Now().Add(scoreQuarantine)
			a.fo.mu.Unlock()
			return false
		}
		a.fo.clear()
		a.mon.Event("ok", "Сервер «"+a.serverName(from)+"» снова отвечает — вернулся на него", true)
		_ = cl.doContext(ctx, "DELETE", "/connections", nil, nil)
		return true
	})
}
