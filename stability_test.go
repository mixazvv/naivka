package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPickFailover(t *testing.T) {
	d := map[string]int{"s-a": 300, "s-b": 120, "s-c": 0, "s-d": 120}
	if got := pickFailover(d, "s-b"); got != "s-d" {
		t.Fatalf("got %q, want s-d (самый быстрый, кроме пропущенного)", got)
	}
	if got := pickFailover(d); got != "s-b" {
		t.Fatalf("got %q, want s-b (при равенстве — по имени)", got)
	}
	if got := pickFailover(map[string]int{"s-a": 0}, ""); got != "" {
		t.Fatalf("мёртвый сервер не годится, got %q", got)
	}
}

func TestSelectedServerTag(t *testing.T) {
	s := &Settings{Servers: []Server{{ID: "x"}}, Selected: "x"}
	if selectedServerTag(s) != "s-x" {
		t.Fatal("ожидался тег выбранного сервера")
	}
	s.Selected = "auto"
	if selectedServerTag(s) != "" {
		t.Fatal("автовыбор — не конкретный сервер")
	}
}

func TestFailoverState(t *testing.T) {
	var f failoverState
	f.set("s-a", "s-b")
	if from, to := f.get(); from != "s-a" || to != "s-b" {
		t.Fatal(from, to)
	}
	f.clear()
	if from, _ := f.get(); from != "" {
		t.Fatal("clear не сработал")
	}
}

func TestCrashBudget(t *testing.T) {
	var b crashBudget
	for i := 0; i < 3; i++ {
		if !b.allow(3, time.Minute) {
			t.Fatalf("попытка %d должна быть разрешена", i)
		}
	}
	if b.allow(3, time.Minute) {
		t.Fatal("четвёртая попытка за окно должна быть запрещена")
	}
	b.at = []time.Time{time.Now().Add(-2 * time.Minute)}
	if !b.allow(1, time.Minute) {
		t.Fatal("старые попытки за окном не считаются")
	}
}

func TestWatchdogDelay(t *testing.T) {
	if watchdogDelay(0) != 2*time.Second || watchdogDelay(100) != time.Minute {
		t.Fatal(watchdogDelay(0), watchdogDelay(100))
	}
	for i := 1; i < 10; i++ {
		if watchdogDelay(i) < watchdogDelay(i-1) {
			t.Fatal("паузы не должны уменьшаться")
		}
	}
}

func TestSpareTestURL(t *testing.T) {
	if u := spareTestURL("https://www.gstatic.com/generate_204"); !strings.Contains(u, "cloudflare") {
		t.Fatal(u)
	}
	if u := spareTestURL("https://cp.cloudflare.com/generate_204"); strings.Contains(u, "cloudflare") {
		t.Fatal("запасной адрес должен отличаться от основного:", u)
	}
}

func TestFindConflicts(t *testing.T) {
	procs := []procEntry{
		{Name: "v2rayN.exe"},
		{Name: "goodbyedpi.exe"},
		{Name: "sing-box.exe", Path: `/own/sing-box.exe`},
		{Name: "sing-box.exe", Path: `/other/app/sing-box.exe`},
		{Name: "sing-box.exe"}, // путь не прочитался — не тревожим
		{Name: "explorer.exe"},
	}
	got := findConflicts(procs, []string{"/own/sing-box.exe"}, false)
	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
	}
	j := strings.Join(names, ",")
	if !strings.Contains(j, "v2rayN") || !strings.Contains(j, "sing-box (не из Naivka)") {
		t.Fatal(j)
	}
	if strings.Contains(j, "GoodbyeDPI") {
		t.Fatal("GoodbyeDPI мешает только при включённом zapret:", j)
	}
	if len(got) != 2 {
		t.Fatal("лишние находки:", j)
	}
	if got := findConflicts(procs[1:2], nil, true); len(got) != 1 || got[0].Name != "GoodbyeDPI" {
		t.Fatal(got)
	}
}

func TestRedactSettings(t *testing.T) {
	s := DefaultSettings()
	s.Mode = "tun"
	s.Servers = []Server{{ID: "a", Host: "secret.example", Password: "hunter2"}}
	s.Rules = []Rule{{Kind: "domain", Value: "private-site.example", Action: "proxy"}}
	var raw any
	b, _ := json.Marshal(s)
	_ = json.Unmarshal(b, &raw)
	out, _ := json.Marshal(redactSettings(raw, ""))
	txt := string(out)
	for _, bad := range []string{"secret.example", "hunter2", "private-site.example"} {
		if strings.Contains(txt, bad) {
			t.Fatalf("в отчёт попало %q", bad)
		}
	}
	if !strings.Contains(txt, `"mode":"tun"`) || !strings.Contains(txt, `"action":"proxy"`) {
		t.Fatal("безопасные поля должны остаться:", txt)
	}
}

func TestMaskHosts(t *testing.T) {
	got := maskHosts("dial tcp 1.2.3.4:443 via vpn.example.com", []string{"vpn.example.com", "1.2.3.4", ""})
	if strings.Contains(got, "vpn.example.com") || strings.Contains(got, "1.2.3.4") {
		t.Fatal(got)
	}
}

func TestFailoverNotInFingerprint(t *testing.T) {
	a, b := DefaultSettings(), DefaultSettings()
	b.Failover = !a.Failover
	if a.fingerprint() != b.fingerprint() {
		t.Fatal("переход на рабочий сервер не должен требовать переподключения")
	}
	if !DefaultSettings().Failover {
		t.Fatal("по умолчанию включено")
	}
}

// Под -race: бюджет перезапусков безопасен при одновременных вызовах.
func TestCrashBudgetConcurrent(t *testing.T) {
	var b crashBudget
	done := make(chan bool)
	ok := make(chan bool, 40)
	for i := 0; i < 40; i++ {
		go func() { ok <- b.allow(5, time.Minute); done <- true }()
	}
	for i := 0; i < 40; i++ {
		<-done
	}
	close(ok)
	n := 0
	for v := range ok {
		if v {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("разрешено %d попыток, ожидалось 5", n)
	}
}
