package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

const scoreWindow = 15 * time.Minute
const scoreFresh = 90 * time.Second
const scoreDwell = 3 * time.Minute
const scoreQuarantine = 2 * time.Minute

type scoreSample struct {
	at time.Time
	ms int
}
type scoreHistory struct {
	samples    []scoreSample
	quarantine time.Time
}
type serverRating struct {
	Tag             string  `json:"tag"`
	Samples         int     `json:"samples"`
	Successes       int     `json:"successes"`
	Median          int     `json:"median"`
	P95             int     `json:"p95"`
	Score           float64 `json:"score"`
	LastAt          int64   `json:"lastAt"`
	QuarantineUntil int64   `json:"quarantineUntil,omitempty"`
	Eligible        bool    `json:"eligible"`
	good, bad       int
}
type serverScores struct {
	mu        sync.Mutex
	work      sync.Mutex // one fresh batch at a time, never count cached /proxies history again
	epoch     string
	rows      map[string]*scoreHistory
	hold      string
	switched  time.Time
	contender string
	wins      int
}

func (q *serverScores) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.epoch = ""
	q.rows = nil
	q.hold = ""
	q.switched = time.Time{}
	q.contender = ""
	q.wins = 0
}
func serverScoreTags(s *Settings) []string {
	out := make([]string, 0, len(s.Servers))
	for _, sv := range s.Servers {
		out = append(out, serverTag(sv))
	}
	return out
}
func scoreEpoch(s *Settings, cl *Clash) string { return cl.secret + "|" + s.fingerprint() }
func (q *serverScores) observe(epoch string, tags []string, delays map[string]int, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.epoch != epoch {
		q.epoch = epoch
		q.rows = map[string]*scoreHistory{}
		q.hold = ""
		q.switched = time.Time{}
		q.contender = ""
		q.wins = 0
	}
	allowed := map[string]bool{}
	for _, tag := range tags {
		allowed[tag] = true
		h := q.rows[tag]
		if h == nil {
			h = &scoreHistory{}
			q.rows[tag] = h
		}
		kept := h.samples[:0]
		for _, p := range h.samples {
			if !p.at.Before(now.Add(-scoreWindow)) {
				kept = append(kept, p)
			}
		}
		h.samples = kept
		ms := delays[tag]
		if ms < 1 || ms > 60000 {
			ms = 0
		}
		h.samples = append(h.samples, scoreSample{now, ms})
		if len(h.samples) > 20 {
			h.samples = h.samples[len(h.samples)-20:]
		}
		bad := 0
		for i := len(h.samples) - 1; i >= 0 && h.samples[i].ms == 0; i-- {
			bad++
		}
		if bad >= 3 {
			h.quarantine = now.Add(scoreQuarantine)
		}
	}
	for tag := range q.rows {
		if !allowed[tag] {
			delete(q.rows, tag)
		}
	}
}
func rating(tag string, h *scoreHistory, now time.Time) serverRating {
	r := serverRating{Tag: tag}
	var good []int
	recent, fail := 0, 0
	for _, p := range h.samples {
		if p.at.Before(now.Add(-scoreWindow)) {
			continue
		}
		r.Samples++
		r.LastAt = p.at.Unix()
		if p.ms > 0 {
			good = append(good, p.ms)
			r.good++
			r.bad = 0
		} else {
			r.bad++
			r.good = 0
		}
		if now.Sub(p.at) <= 4*time.Minute {
			recent++
			if p.ms == 0 {
				fail++
			}
		}
	}
	r.Successes = len(good)
	if len(good) > 0 {
		sort.Ints(good)
		r.Median = good[len(good)/2]
		if len(good)%2 == 0 {
			r.Median = (good[len(good)/2-1] + r.Median) / 2
		}
		r.P95 = good[int(math.Ceil(float64(len(good))*.95))-1]
		stability := float64(len(good)) / float64(r.Samples)
		latency := 1 / (1 + (float64(r.Median)*.7+math.Min(float64(r.P95), float64(r.Median)*3)*.3)/200)
		reliability := 1.0
		if recent > 0 {
			reliability = 1 - float64(fail)/float64(recent)
		}
		// Speed is unknown: renormalize 40/30/10 to 50/37.5/12.5; never invent throughput.
		r.Score = math.Round((50*stability+37.5*latency+12.5*reliability)*10) / 10
	}
	if now.Before(h.quarantine) {
		r.QuarantineUntil = h.quarantine.Unix()
	}
	r.Eligible = r.Samples > 0 && r.good > 0 && now.Sub(time.Unix(r.LastAt, 0)) <= scoreFresh && r.QuarantineUntil == 0
	return r
}
func (q *serverScores) view(now time.Time) []serverRating {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.viewLocked(now)
}
func (q *serverScores) viewFor(epoch string, now time.Time) []serverRating {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.epoch != epoch {
		return []serverRating{}
	}
	return q.viewLocked(now)
}
func (q *serverScores) viewLocked(now time.Time) []serverRating {
	out := make([]serverRating, 0, len(q.rows))
	for tag, h := range q.rows {
		out = append(out, rating(tag, h, now))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}
func (q *serverScores) held(epoch string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.epoch == epoch {
		return q.hold
	}
	return ""
}
func (q *serverScores) commit(epoch, tag string, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.epoch != epoch {
		return
	}
	if q.hold != tag {
		if h := q.rows[q.hold]; h != nil && rating(q.hold, h, now).bad >= 2 {
			h.quarantine = now.Add(scoreQuarantine)
		}
	}
	q.hold = tag
	q.switched = now
	q.contender = ""
	q.wins = 0
}
func (q *serverScores) quarantined(tag string, now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	h := q.rows[tag]
	return h != nil && now.Before(h.quarantine)
}
func (q *serverScores) decide(current string, now time.Time, emergency bool, skip ...string) (string, string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var best, cur serverRating
	for tag, h := range q.rows {
		r := rating(tag, h, now)
		if tag == current {
			cur = r
		}
		excluded := false
		for _, s := range skip {
			if tag == s {
				excluded = true
			}
		}
		if excluded || !r.Eligible {
			continue
		}
		if best.Tag == "" || r.Score > best.Score || (r.Score == best.Score && tag < best.Tag) {
			best = r
		}
	}
	// Reconcile a successfully observed direct choice after an ambiguous API reply.
	// This does not write the selector or overwrite any manual Settings.Selected.
	if q.hold != "" && cur.Tag != "" && current != q.hold {
		q.hold = current
		q.switched = now
		q.contender = ""
		q.wins = 0
	}
	if best.Tag == "" {
		q.contender = ""
		q.wins = 0
		return "", "нет свежего подходящего резерва"
	}
	if emergency {
		return best.Tag, fmt.Sprintf("оценка %.1f; запросы %d/%d, медиана %d мс, p95 %d мс", best.Score, best.Successes, best.Samples, best.Median, best.P95)
	}
	if q.hold == "" {
		// Freeze a currently healthy native choice before starting the dwell period.
		if cur.Eligible {
			return current, "первичное закрепление текущего сервера"
		}
		return best.Tag, "первый доступный сервер по свежей проверке"
	}
	if best.Tag == current {
		q.contender = ""
		q.wins = 0
		return "", "текущий сервер имеет лучшую оценку"
	}
	if cur.bad >= 2 && best.good >= 2 {
		return best.Tag, "две неудачные проверки текущего сервера; свежий устойчивый резерв"
	}
	if !cur.Eligible || best.good < 2 || best.Score < cur.Score+12 || now.Sub(q.switched) < scoreDwell {
		q.contender = ""
		q.wins = 0
		return "", "порог улучшения или выдержка не достигнуты"
	}
	if q.contender == best.Tag {
		q.wins++
	} else {
		q.contender = best.Tag
		q.wins = 1
	}
	if q.wins < 2 {
		return "", "ожидаю повторного подтверждения улучшения"
	}
	return best.Tag, fmt.Sprintf("оценка %.1f → %.1f; медиана %d мс, p95 %d мс; улучшение подтверждено дважды", cur.Score, best.Score, best.Median, best.P95)
}

type selectionSnapshot struct {
	rev      uint64
	gen      int64
	settings *Settings
}

func (a *App) choiceSnapshot() selectionSnapshot {
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	return selectionSnapshot{a.selectionRev, a.life.gen.Load(), a.settingsCopy()}
}

// Lifecycle lock precedes selectionMu. Disconnect cancels the HTTP operation;
// a manual selection queued during it always writes last. Probes hold neither lock.
func (a *App) commitChoice(snap selectionSnapshot, cl *Clash, fn func(context.Context, *Settings) bool) bool {
	if a.repair.busy() {
		return false
	}
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	ctx, cancel, err := a.life.operation(snap.gen, true)
	if err != nil {
		return false
	}
	defer cancel()
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	if a.repair.busy() {
		return false
	}
	current := a.settingsCopy()
	a.mu.Lock()
	applied := a.applied
	a.mu.Unlock()
	live := a.core.Clash()
	if (applied != "" && applied != current.fingerprint()) || a.selectionRev != snap.rev || live == nil || live.secret != cl.secret || current.Selected != snap.settings.Selected || current.SmartSelect != snap.settings.SmartSelect || current.Failover != snap.settings.Failover || current.fingerprint() != snap.settings.fingerprint() || a.wl.Active() {
		return false
	}
	return fn(ctx, current)
}
func (a *App) scoreTick(cl *Clash) {
	if !a.scores.work.TryLock() {
		return
	}
	defer a.scores.work.Unlock()
	snap := a.choiceSnapshot()
	s := snap.settings
	if !s.SmartSelect || len(s.Servers) == 0 || !a.life.want.Load() || a.wl.Active() {
		return
	}
	delays, err := cl.GroupDelay(tagAuto, s.TestURL, 5000)
	if err != nil {
		return
	} // batch failure is UNKNOWN, not N server failures
	px, err := cl.Proxies()
	if err != nil {
		return
	}
	a.commitChoice(snap, cl, func(ctx context.Context, current *Settings) bool {
		now := time.Now()
		epoch := scoreEpoch(current, cl)
		a.scores.observe(epoch, serverScoreTags(current), delays, now)
		if from, _ := a.fo.get(); from != "" {
			return false
		}
		if current.Selected != "auto" {
			return false
		} // explicit server remains the user's choice
		actual := px.Selected
		if actual == tagAuto {
			actual = px.AutoNow
		}
		next, why := a.scores.decide(actual, now, false)
		if next == "" {
			return false
		}
		if next == actual && px.Selected != tagAuto {
			a.scores.commit(epoch, next, now)
			return true
		}
		if err := cl.SelectContext(ctx, next); err != nil || ctx.Err() != nil {
			return false
		}
		a.selectionRev++
		a.scores.commit(epoch, next, now)
		a.mon.Event("info", "Умный выбор: «"+a.serverName(actual)+"» → «"+a.serverName(next)+"»: "+why, false)
		return true
	})
}
