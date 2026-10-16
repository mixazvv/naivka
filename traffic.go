package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// История трафика: сколько прошло за каждый день — через сервер (по каждому серверу),
// напрямую, через zapret и Telegram. Хранится в traffic.json в папке данных, 400 дней.
// Это счётчики Naivka: провайдер и хостинг считают чуть больше (заголовки, шифрование, повторы).

const trafficKeepDays = 400

// TrafficDay — один день: ключ → [загрузка, отдача] в байтах.
// Ключи: proxy (всё через сервер), direct, zapret, telegram, а ещё s-<id> (свой сервер),
// wl (подписка) — части proxy.
type TrafficDay map[string][2]int64

type trafficStore struct {
	path  string
	mu    sync.Mutex
	days  map[string]TrafficDay // «2026-10-12» → день
	dirty bool
	saved time.Time
	// предупреждения о лимите: «2026-10|80» — уже показано
	Warned map[string]bool
}

type trafficFile struct {
	Days   map[string]TrafficDay `json:"days"`
	Warned map[string]bool       `json:"warned,omitempty"`
}

func newTrafficStore(dir string) *trafficStore {
	t := &trafficStore{path: filepath.Join(dir, "traffic.json"), days: map[string]TrafficDay{}, Warned: map[string]bool{}}
	if b, err := os.ReadFile(t.path); err == nil {
		var f trafficFile
		if json.Unmarshal(b, &f) == nil {
			if f.Days != nil {
				t.days = f.Days
			}
			if f.Warned != nil {
				t.Warned = f.Warned
			}
		}
	}
	return t
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// add учитывает трафик за секунду.
func (t *trafficStore) add(now time.Time, key string, down, up int64) {
	if down <= 0 && up <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	k := dayKey(now)
	d := t.days[k]
	if d == nil {
		d = TrafficDay{}
		t.days[k] = d
	}
	v := d[key]
	v[0] += down
	v[1] += up
	d[key] = v
	t.dirty = true
}

// save пишет файл не чаще раза в минуту (force — сразу, при выходе).
func (t *trafficStore) save(force bool) {
	t.mu.Lock()
	if !t.dirty || (!force && time.Since(t.saved) < time.Minute) {
		t.mu.Unlock()
		return
	}
	cut := dayKey(time.Now().AddDate(0, 0, -trafficKeepDays))
	for k := range t.days {
		if k < cut {
			delete(t.days, k)
		}
	}
	b, err := json.Marshal(trafficFile{Days: t.days, Warned: t.Warned})
	t.dirty, t.saved = false, time.Now()
	t.mu.Unlock()
	if err != nil {
		return
	}
	tmp := t.path + ".part"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, t.path)
	}
}

// periodStart — начало текущего расчётного месяца: число resetDay (1–28) этого или прошлого месяца.
func periodStart(now time.Time, resetDay int) time.Time {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	y, m, d := now.Date()
	start := time.Date(y, m, resetDay, 0, 0, 0, 0, now.Location())
	if d < resetDay {
		start = start.AddDate(0, -1, 0)
	}
	return start
}

// sumSince — сумма по ключу за дни начиная с from.
func (t *trafficStore) sumSince(from time.Time, key string) (down, up int64) {
	f := dayKey(from)
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, d := range t.days {
		if k >= f {
			down += d[key][0]
			up += d[key][1]
		}
	}
	return
}

// TrafficView — история для интерфейса.
type TrafficView struct {
	Days       []TrafficDayView `json:"days"`       // последние дни, по порядку
	Period     string           `json:"period"`     // начало расчётного месяца
	PeriodDown int64            `json:"periodDown"` // через сервер с начала месяца
	PeriodUp   int64            `json:"periodUp"`
	PeriodKeys TrafficDay       `json:"periodKeys"` // все ключи за месяц
	LimitGB    int              `json:"limitGB"`
}

type TrafficDayView struct {
	Date string     `json:"date"`
	Keys TrafficDay `json:"keys"`
}

func (t *trafficStore) view(now time.Time, days int, s *Settings) TrafficView {
	v := TrafficView{LimitGB: s.TrafficLimitGB, PeriodKeys: TrafficDay{}}
	start := periodStart(now, s.TrafficResetDay)
	v.Period = dayKey(start)
	ps := dayKey(start)
	t.mu.Lock()
	for i := days - 1; i >= 0; i-- {
		k := dayKey(now.AddDate(0, 0, -i))
		keys := TrafficDay{}
		for kk, val := range t.days[k] {
			keys[kk] = val
		}
		v.Days = append(v.Days, TrafficDayView{Date: k, Keys: keys})
	}
	var dk []string
	for k := range t.days {
		if k >= ps {
			dk = append(dk, k)
		}
	}
	sort.Strings(dk)
	for _, k := range dk {
		for kk, val := range t.days[k] {
			cur := v.PeriodKeys[kk]
			cur[0] += val[0]
			cur[1] += val[1]
			v.PeriodKeys[kk] = cur
		}
	}
	t.mu.Unlock()
	v.PeriodDown, v.PeriodUp = v.PeriodKeys["proxy"][0], v.PeriodKeys["proxy"][1]
	return v
}

// checkLimit предупреждает, когда трафик через сервер за месяц дошёл до 80% и 100% лимита.
func (t *trafficStore) checkLimit(now time.Time, s *Settings, event func(kind, text string)) {
	if s.TrafficLimitGB <= 0 {
		return
	}
	start := periodStart(now, s.TrafficResetDay)
	down, up := t.sumSince(start, "proxy")
	used := float64(down+up) / (1 << 30)
	limit := float64(s.TrafficLimitGB)
	for _, step := range []int{100, 80} {
		if used < limit*float64(step)/100 {
			continue
		}
		key := fmt.Sprintf("%s|%d|%d", dayKey(start), s.TrafficLimitGB, step)
		t.mu.Lock()
		done := t.Warned[key]
		if !done {
			t.Warned[key] = true
			t.dirty = true
		}
		t.mu.Unlock()
		if !done {
			kind := "warn"
			if step == 100 {
				kind = "error"
			}
			event(kind, fmt.Sprintf("Через сервер за месяц прошло %.1f ГБ — это %d%% лимита %d ГБ (месяц считается с %s)",
				used, int(used/limit*100), s.TrafficLimitGB, start.Format("02.01")))
		}
		return // показываем только самый высокий порог
	}
}
