package main

import (
	"strings"
	"testing"
	"time"
)

func TestPeriodStart(t *testing.T) {
	loc := time.UTC
	for _, c := range []struct {
		now  string
		day  int
		want string
	}{
		{"2026-10-12", 1, "2026-10-01"}, {"2026-10-12", 15, "2026-09-15"}, {"2026-10-15", 15, "2026-10-15"},
		{"2026-01-03", 5, "2025-12-05"}, {"2026-03-01", 31, "2026-03-01"},
	} {
		now, _ := time.ParseInLocation("2006-01-02", c.now, loc)
		if got := dayKey(periodStart(now.Add(13*time.Hour), c.day)); got != c.want {
			t.Errorf("%s день %d: %s, нужно %s", c.now, c.day, got, c.want)
		}
	}
}

func TestTrafficStore(t *testing.T) {
	dir := t.TempDir()
	st := newTrafficStore(dir)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.Local)
	st.add(now, "proxy", 600<<20, 100<<20)
	st.add(now, "s-a", 600<<20, 100<<20)
	st.add(now.AddDate(0, 0, -1), "direct", 5, 6)
	st.add(now.AddDate(0, -1, 0), "proxy", 1<<30, 0) // прошлый месяц не считается
	st.save(true)

	st2 := newTrafficStore(dir) // с диска
	s := DefaultSettings()
	s.TrafficLimitGB = 1
	v := st2.view(now, 31, s)
	if len(v.Days) != 31 || v.Days[30].Date != "2026-10-12" || v.Days[30].Keys["s-a"][0] != 600<<20 || v.Days[29].Keys["direct"][1] != 6 {
		t.Fatalf("дни: %+v", v.Days[29:])
	}
	if v.PeriodDown != 600<<20 || v.PeriodUp != 100<<20 || v.Period != "2026-10-01" {
		t.Errorf("месяц: %+v", v)
	}

	var got []string
	ev := func(kind, text string) { got = append(got, kind+": "+text) }
	st2.checkLimit(now, s, ev) // 700 МБ из 1 ГБ — меньше 80%
	if len(got) != 0 {
		t.Fatalf("рано: %v", got)
	}
	st2.add(now, "proxy", 200<<20, 0) // 900 МБ — больше 80%
	st2.checkLimit(now, s, ev)
	st2.checkLimit(now, s, ev) // второй раз не повторяет
	if len(got) != 1 || !strings.HasPrefix(got[0], "warn:") {
		t.Fatalf("80%%: %v", got)
	}
	st2.add(now, "proxy", 200<<20, 0) // больше 100%
	st2.checkLimit(now, s, ev)
	if len(got) != 2 || !strings.HasPrefix(got[1], "error:") {
		t.Fatalf("100%%: %v", got)
	}
	s.TrafficLimitGB = 0
	st2.checkLimit(now, s, ev)
	if len(got) != 2 {
		t.Error("без лимита предупреждает")
	}
}
