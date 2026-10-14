package main

import (
	"sync"
	"time"
)

// crashBudget — сколько раз можно поднять упавшую программу (Xray, zapret, tg-ws-proxy):
// не больше n раз за окно, чтобы падающая раз за разом не перезапускалась бесконечно.
type crashBudget struct {
	mu sync.Mutex
	at []time.Time
}

func (b *crashBudget) allow(n int, window time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	cut := time.Now().Add(-window)
	keep := b.at[:0]
	for _, t := range b.at {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	b.at = keep
	if len(b.at) >= n {
		return false
	}
	b.at = append(b.at, time.Now())
	return true
}
