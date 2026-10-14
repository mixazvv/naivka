package main

import (
	"sync"
)

// Переход на другой сервер: выбранный вручную сервер перестал отвечать — временно работаем
// через самый быстрый из живых своих серверов, а когда выбранный оживёт, возвращаемся.
// Выбор в настройках не меняется: это временная подмена, её видно в ленте событий.
type failoverState struct {
	mu   sync.Mutex
	from string // тег выбранного сервера, который не отвечает
	to   string // тег, через который работаем сейчас
	oks  int    // сколько проверок подряд выбранный сервер снова отвечает
}

func (f *failoverState) get() (from, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.from, f.to
}

func (f *failoverState) set(from, to string) {
	f.mu.Lock()
	f.from, f.to, f.oks = from, to, 0
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
	chosen := selectedServerTag(s)
	if chosen == "" {
		return false // автовыбор сам уходит с мёртвого сервера
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
	best := pickFailover(delays, cur, chosen)
	if best == "" {
		return false
	}
	if err := cl.Select(best); err != nil {
		return false
	}
	a.fo.set(chosen, best)
	a.mon.Event("warn", "Сервер «"+a.serverName(cur)+"» не отвечает — временно работаю через «"+a.serverName(best)+"». Вернусь, когда он оживёт", true)
	_ = cl.CloseAll()
	return true
}

// failback — раз в минуту, пока работаем через замену: отвечает ли снова выбранный сервер.
// Возвращаемся после двух удачных проверок подряд, чтобы не прыгать туда-сюда.
func (a *App) failback(cl *Clash) {
	from, _ := a.fo.get()
	if from == "" {
		return
	}
	s := a.settingsCopy()
	if selectedServerTag(s) != from || !s.Failover {
		a.fo.clear() // выбор сменили — подмена больше не нужна
		return
	}
	ms, err := cl.Delay(from, s.TestURL)
	a.fo.mu.Lock()
	if a.fo.from != from {
		a.fo.mu.Unlock()
		return
	}
	if err != nil || ms <= 0 {
		a.fo.oks = 0
		a.fo.mu.Unlock()
		return
	}
	a.fo.oks++
	back := a.fo.oks >= 2
	if back {
		a.fo.from, a.fo.to, a.fo.oks = "", "", 0
	}
	a.fo.mu.Unlock()
	if !back {
		return
	}
	if err := cl.Select(from); err != nil {
		return
	}
	a.mon.Event("ok", "Сервер «"+a.serverName(from)+"» снова отвечает — вернулся на него", true)
	_ = cl.CloseAll()
}
