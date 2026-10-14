package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Десять одновременных просьб о перезапуске: один идущий + один повтор, а не десять.
func TestSerialRunner(t *testing.T) {
	var r serialRunner
	var runs, active, overlap atomic.Int32
	job := func() {
		if active.Add(1) > 1 {
			overlap.Add(1)
		}
		runs.Add(1)
		time.Sleep(50 * time.Millisecond)
		active.Add(-1)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.Run(job) }()
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 9; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.Run(job) }()
	}
	wg.Wait()
	if runs.Load() != 2 || overlap.Load() != 0 || r.Busy() {
		t.Fatalf("перезапусков %d (ожидалось 2), одновременно %d, занят после конца %v", runs.Load(), overlap.Load(), r.Busy())
	}
}

// Обычные строки запуска winws из настоящего журнала не попадают в журнал Naivka, а ошибки — попадают.
func TestRoutineZapretLines(t *testing.T) {
	routine := []string{
		"github version v72.9 (c849e55ef0f1c244206f5a05ff7b1ab41a3824ee)",
		"",
		"we have 9 user defined desync profile(s) and default low priority profile 0",
		`Loading hostlist C:\Program Files\Naivka\zapret\1.10.3\lists\list-google.txt`,
		"loading plain text list",
		`Loaded 22 hosts from C:\Program Files\Naivka\zapret\1.10.3\lists\list-google.txt`,
		`Loading ipset C:\Program Files\Naivka\zapret\1.10.3\lists\ipset-all.txt`,
		`Loaded 1 ip/subnets from C:\Program Files\Naivka\zapret\1.10.3\lists\ipset-all.txt`,
		"windivert initialized. capture is started.",
	}
	for _, l := range routine {
		if !routineZapretLine(l) {
			t.Errorf("обычная строка попадёт в журнал: %q", l)
		}
	}
	for _, l := range []string{"windivert: error opening filter: Access is denied", "could not read hostlist", "bad value for --dpi-desync"} {
		if routineZapretLine(l) {
			t.Errorf("ошибка спрячется: %q", l)
		}
	}
	// полный вывод для сообщения «сразу завершился»: без шума, но с ошибкой
	b := &tailBuf{max: 40}
	for _, l := range append(routine, "windivert: error opening filter: Access is denied") {
		b.add(l)
	}
	if b.String() != "windivert: error opening filter: Access is denied" {
		t.Errorf("сообщение о сбое: %q", b.String())
	}
}

// Защита путей в режиме администратора не трогает path у WebSocket/HTTPUpgrade, но убирает настоящие файлы.
func TestSanitizeKeepsTransportPath(t *testing.T) {
	cfg := M{
		"log": M{"output": `C:\Windows\evil.log`},
		"outbounds": []any{
			M{"type": "vless", "tag": "w-1", "transport": M{"type": "ws", "path": "/api/v1/ws"}},
			M{"type": "vless", "tag": "w-2", "transport": M{"type": "httpupgrade", "path": "/up"}},
			M{"type": "vless", "tag": "w-3", "tls": M{"certificate_path": `C:\x.pem`}},
		},
		"experimental": M{"cache_file": M{"path": filepath.Join(os.TempDir(), "Naivka", "run", "cache.db")}},
	}
	removed := sanitizePaths(cfg, filepath.Join(os.TempDir(), "Naivka", "run"))
	obs := cfg["outbounds"].([]any)
	if obs[0].(M)["transport"].(M)["path"] != "/api/v1/ws" || obs[1].(M)["transport"].(M)["path"] != "/up" {
		t.Fatalf("path транспорта удалён: %v", removed)
	}
	if _, ok := cfg["log"].(M)["output"]; ok {
		t.Error("log.output не удалён")
	}
	if _, ok := obs[2].(M)["tls"].(M)["certificate_path"]; ok {
		t.Error("certificate_path не удалён")
	}
	if len(removed) != 2 {
		t.Errorf("удалено %d, ожидалось 2: %v", len(removed), removed)
	}
}

// Включение и выключение zapret и tg-ws-proxy применяется к ядру сразу, остальное — нет.
func TestAddonsNeedRestart(t *testing.T) {
	a := DefaultSettings()
	b := a.clone()
	b.Zapret.Strategy = "other"
	if addonsNeedRestart(a, b) {
		t.Fatal("strategy change must not restart the core")
	}
	for _, f := range []func(*Settings){
		func(s *Settings) { s.Zapret.Enabled = !s.Zapret.Enabled },
		func(s *Settings) { s.Zapret.RouteDirect = !s.Zapret.RouteDirect },
		func(s *Settings) { s.Telegram.Enabled = !s.Telegram.Enabled },
	} {
		c := a.clone()
		f(c)
		if !addonsNeedRestart(a, c) {
			t.Fatal("addon toggle must restart the core")
		}
		if a.fingerprint() == c.fingerprint() {
			t.Fatal("toggle must change the fingerprint")
		}
	}
}

// Результаты проверки серверов видны по одному, не дожидаясь самого медленного.
func TestTestBoard(t *testing.T) {
	b := &testBoard{res: map[string]*testResult{}}
	b.begin([]Server{{ID: "a"}, {ID: "b"}})
	b.put(testResult{ID: "a", Ms: 40})
	v := b.view()
	done, pending := v["done"].([]testResult), v["pending"].([]string)
	if len(done) != 1 || done[0].ID != "a" || len(pending) != 1 || pending[0] != "b" {
		t.Fatalf("view = %+v", v)
	}
	b.begin([]Server{{ID: "a"}})
	if v := b.view(); len(v["done"].([]testResult)) != 0 {
		t.Fatal("a new check must hide the old result")
	}
}

// Подбор zapret начинает с выбранной стратегии и лучших по прошлому разу.
func TestPickOrder(t *testing.T) {
	strats := []string{"general", "general (ALT)", "general (ALT2)", "general (FAKE TLS)"}
	prev := []PickResult{{Strategy: "general", OK: 1}, {Strategy: "general (ALT2)", OK: 4, Ms: 90}, {Strategy: "gone", OK: 4}}
	got := pickOrder(strats, "general (FAKE TLS)", prev)
	want := []string{"general (FAKE TLS)", "general (ALT2)", "general", "general (ALT)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
