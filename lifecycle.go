package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// Включение, выключение и перезапуск ядра идут через одну очередь (lifeState.mu): кнопка,
// трей, сторож, пробуждение, проверка связи и подбор стратегии zapret не перебивают друг друга —
// остановка из одного места не попадёт в середину подключения из другого.
// want — чего хочет пользователь: сторож и пробуждение подключают снова только если он
// не нажимал «Отключить»; gen растёт при каждом отключении, чтобы старые попытки сторожа прекратились.
type lifeState struct {
	mu     sync.Mutex
	want   atomic.Bool
	gen    atomic.Int64
	trying atomic.Int32 // сколько сейчас идёт попыток восстановить соединение (для окна)
}

// connect — подключиться по просьбе пользователя (или автоподключение при запуске).
func (a *App) connect() error {
	a.life.want.Store(true)
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	return a.connectLocked()
}

// reconnect — переподключиться заново с текущими настройками.
func (a *App) reconnect() error {
	a.life.want.Store(true)
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	a.core.Stop()
	return a.connectLocked()
}

// disconnect — пользователь отключился: сторож и пробуждение больше не подключают.
func (a *App) disconnect() {
	a.life.want.Store(false)
	a.life.gen.Add(1)
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	a.core.Stop()
	a.fo.clear()
	a.mu.Lock()
	a.applied = ""
	a.mu.Unlock()
}

// pause — остановить ядро, не меняя желания пользователя (сон, перезапуск с правами).
func (a *App) pause() {
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	a.core.Stop()
}

// resume — подключиться снова от имени Naivka (сторож, пробуждение). Ничего не делает,
// если пользователь за это время отключился (gen сменился) или ядро уже работает.
func (a *App) resume(gen int64) (done bool, err error) {
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	if !a.life.want.Load() || a.life.gen.Load() != gen {
		return true, nil
	}
	if a.core.Running() {
		return true, nil
	}
	if err := a.connectLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// restartCore перезапускает ядро, если пользователь хочет быть подключённым. Если перезапуск
// уже идёт, второй не начинается — идущий просто повторится один раз в конце с самыми свежими настройками.
func (a *App) restartCore() {
	a.restarter.Run(func() {
		a.life.mu.Lock()
		defer a.life.mu.Unlock()
		if !a.life.want.Load() {
			return
		}
		a.core.Stop()
		if err := a.connectLocked(); err != nil {
			a.mon.Event("error", "Перезапуск не удался: "+firstLine(err.Error()), true)
		}
	})
	if a.ui != nil {
		a.ui.Refresh()
	}
}

// watchdogDelays — паузы между попытками поднять упавшее ядро: сначала часто, потом раз в минуту.
var watchdogDelays = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, time.Minute}

func watchdogDelay(i int) time.Duration {
	if i >= len(watchdogDelays) {
		return watchdogDelays[len(watchdogDelays)-1]
	}
	return watchdogDelays[i]
}

// sleepUnless ждёт d, но прерывается, если пользователь отключился. false — ждать больше незачем.
func (a *App) sleepUnless(d time.Duration, gen int64) bool {
	for end := time.Now().Add(d); time.Now().Before(end); {
		if !a.life.want.Load() || a.life.gen.Load() != gen {
			return false
		}
		step := time.Until(end)
		if step > 500*time.Millisecond {
			step = 500 * time.Millisecond
		}
		time.Sleep(step)
	}
	return a.life.want.Load() && a.life.gen.Load() == gen
}

// onCrash: ядро упало. Защита от утечек уже удерживает трафик (если включена), а сторож
// поднимает ядро снова — пока не получится или пока пользователь не нажмёт «Отключить».
// Нет сети — ждём её, не тратя попытки; падает раз за разом — пробуем реже.
func (a *App) onCrash(msg string) {
	s := a.settingsCopy()
	if s.KillSwitch && s.Mode == "proxy" {
		a.mon.Event("error", "Ядро упало. Трафик заблокирован защитой от утечек, пока соединение не восстановится", true)
	} else {
		a.mon.Event("error", "Ядро упало: "+msg, true)
	}
	if a.ui != nil {
		a.ui.Refresh()
	}
	if !s.Watchdog {
		return
	}
	a.keepTrying(a.life.gen.Load(), 0)
}

// keepTrying подключает снова, пока не получится или пока пользователь не отключится.
// first — с какой по счёту паузы начать (после пробуждения первая попытка уже была).
func (a *App) keepTrying(gen int64, first int) {
	a.life.trying.Add(1)
	defer a.life.trying.Add(-1)
	warned := false
	for i := first; ; i++ {
		if !a.sleepUnless(watchdogDelay(i), gen) || !a.settingsCopy().Watchdog {
			return
		}
		if !networkUp() {
			i-- // сети нет — попытку не считаем, ждём её
			continue
		}
		if !a.mon.allowRestart() {
			// Ядро падает слишком часто — даём передышку. Попытки считаем только при живой сети,
			// иначе время без сети съедало бы их, и после её возвращения пришлось бы ждать.
			if !a.sleepUnless(2*time.Minute, gen) {
				return
			}
			continue
		}
		done, err := a.resume(gen)
		if done {
			if err == nil && a.core.Running() {
				a.mon.Event("ok", "Соединение восстановлено", true)
			}
			if a.ui != nil {
				a.ui.Refresh()
			}
			return
		}
		if i >= 2 && !warned {
			warned = true
			a.mon.Event("error", "Пока не удаётся восстановить соединение ("+firstLine(err.Error())+"). Продолжаю пробовать", true)
		}
	}
}
