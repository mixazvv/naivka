package main

import (
	"net"
	"sync"
	"time"
)

// Сон и пробуждение: перед сном Naivka отключается (ядро не держит мёртвые соединения,
// системный прокси не указывает в никуда), после пробуждения ждёт сеть и подключается снова.
type sleepState struct {
	mu     sync.Mutex
	asleep bool
	wasOn  bool
}

func (a *App) onSuspend() {
	if !a.settingsCopy().SleepReconnect {
		return
	}
	a.sleep.mu.Lock()
	if a.sleep.asleep {
		a.sleep.mu.Unlock()
		return
	}
	st := a.core.Status()
	a.sleep.asleep, a.sleep.wasOn = true, st.Running || st.Blocked || st.Starting
	was := a.sleep.wasOn
	a.sleep.mu.Unlock()
	if !was {
		return
	}
	a.pause()
	a.mon.Event("info", "Компьютер засыпает — отключено, после пробуждения подключусь снова", false)
	if a.ui != nil {
		a.ui.Refresh()
	}
}

func (a *App) onResume() {
	a.sleep.mu.Lock()
	if !a.sleep.asleep {
		a.sleep.mu.Unlock()
		return
	}
	was := a.sleep.wasOn
	a.sleep.asleep, a.sleep.wasOn = false, false
	a.sleep.mu.Unlock()
	if !was || !a.settingsCopy().SleepReconnect {
		return
	}
	gen := a.life.gen.Load()
	// Сеть (особенно Wi-Fi) поднимается не сразу — ждём до полуминуты.
	time.Sleep(2 * time.Second)
	for i := 0; i < 14 && !networkUp(); i++ {
		time.Sleep(2 * time.Second)
	}
	if a.core.Running() {
		return
	}
	if _, err := a.resume(gen); err != nil {
		a.mon.Event("error", "После пробуждения не удалось подключиться: "+firstLine(err.Error()), true)
		if a.settingsCopy().Watchdog {
			go a.keepTrying(gen, 1)
		}
	} else if a.core.Running() {
		a.mon.Event("ok", "Компьютер проснулся — снова подключено", false)
	}
	if a.ui != nil {
		a.ui.Refresh()
	}
}

// networkUp: есть ли работающий сетевой интерфейс с адресом (кроме петлевого).
func networkUp() bool {
	ifs, err := net.Interfaces()
	if err != nil {
		return true
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() && !ipn.IP.IsLoopback() {
				return true
			}
		}
	}
	return false
}
