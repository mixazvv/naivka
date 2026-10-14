package main

import "sync"

// Сайты zapret идут через переключатель zapret-out: напрямую, пока zapret справляется,
// или через сервер, если он перестал справляться или упал. Раньше каждое такое переключение
// перезапускало ядро — рвались все соединения, в том числе через сервер. Теперь переключается
// только zapret-out, и переподключаются только соединения с сайтами zapret.
type zapretRouteState struct {
	mu      sync.Mutex
	routed  bool   // в работающем конфиге есть zapret-out
	applied string // куда он сейчас направлен: direct или proxy
}

func (z *zapretRouteState) set(routed bool, applied string) {
	z.mu.Lock()
	z.routed, z.applied = routed, applied
	z.mu.Unlock()
}

func (z *zapretRouteState) get() (bool, string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.routed, z.applied
}

// zapretRouteTarget — куда направить сайты zapret сейчас.
func zapretRouteTarget(active bool) string {
	if active {
		return tagDirect
	}
	return tagProxy
}

// applyZapretRoute приводит маршрут сайтов zapret к состоянию zapret. Если переключателя
// в конфиге нет, а нужен (или наоборот) — перезапускает ядро; иначе переключает на ходу.
func (a *App) applyZapretRoute() {
	if !a.core.Running() {
		return // при подключении маршрут возьмётся из состояния zapret
	}
	s := a.settingsCopy()
	routed, applied := a.zr.get()
	if routed != a.zapret.routed(s) {
		a.restartCore()
		return
	}
	if !routed {
		return
	}
	want := zapretRouteTarget(a.zapret.active(s))
	if want == applied {
		return
	}
	cl := a.core.Clash()
	if cl == nil {
		return
	}
	if err := cl.SelectIn(tagZapretOut, want); err != nil {
		a.core.logs.Add("[naivka] не удалось переключить маршрут zapret: " + err.Error())
		a.restartCore()
		return
	}
	a.zr.set(true, want)
	if want == tagDirect {
		a.core.logs.Add("[naivka] сайты zapret — снова напрямую")
	} else {
		a.core.logs.Add("[naivka] сайты zapret — через сервер")
	}
}
