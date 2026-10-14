package main

import (
	"sync"
	"time"
)

// Пока Naivka держит системный прокси, другая программа (VPN-клиент, расширение браузера,
// корпоративная настройка) может его переписать — тогда трафик тихо пойдёт мимо сервера.
// Раз в 5 секунд проверяем и возвращаем; если его переписывают раз за разом — не воюем,
// а говорим, что делать.
type proxyGuard struct {
	mu     sync.Mutex
	fixes  crashBudget
	gaveUp bool
}

func (g *proxyGuard) reset() {
	g.mu.Lock()
	g.gaveUp, g.fixes = false, crashBudget{}
	g.mu.Unlock()
}

func (a *App) guardSystemProxy() {
	// Под общей очередью: не вмешиваемся в подключение или отключение, которое идёт сейчас.
	if !a.life.mu.TryLock() {
		return
	}
	defer a.life.mu.Unlock()
	port := a.core.SystemProxyPort()
	if port == 0 {
		return
	}
	ours, cur := systemProxyOurs(port)
	if ours {
		return
	}
	g := &a.proxyGuard
	g.mu.Lock()
	if g.gaveUp {
		g.mu.Unlock()
		return
	}
	if !g.fixes.allow(3, 5*time.Minute) {
		g.gaveUp = true
		g.mu.Unlock()
		a.mon.Event("warn", "Другая программа раз за разом меняет системный прокси — трафик может идти мимо сервера. "+
			"Закройте её (например, другой VPN-клиент) или включите режим «Весь трафик (TUN)»", true)
		return
	}
	g.mu.Unlock()
	if err := setSystemProxy(a.dir, port); err != nil {
		return
	}
	if a.core.SystemProxyPort() == 0 {
		restoreSystemProxy(a.dir) // ядро как раз остановилось — не оставляем прокси в никуда
		return
	}
	if cur == "" {
		a.mon.Event("warn", "Другая программа выключила системный прокси — включил снова", true)
	} else {
		a.mon.Event("warn", "Другая программа сменила системный прокси на «"+cur+"» — вернул на Naivka", true)
	}
}
