package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestRepairCoordinator(t *testing.T) {
	var r repairState
	ctx, id, err := r.begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.begin(); err == nil {
		t.Fatal("concurrent operation accepted")
	}
	if err := r.stop("wrong"); err == nil {
		t.Fatal("wrong id accepted")
	}
	if ctx.Err() != nil {
		t.Fatal("wrong id cancelled operation")
	}
	r.check(id, "CORE", "OK")
	v := r.snapshot()
	v.Checks[0].Status = "modified"
	if r.snapshot().Checks[0].Status != "OK" {
		t.Fatal("snapshot aliases state")
	}
	if err := r.stop(id); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("cancel ignored")
	}
	r.finish(id, "PATH_REACHABLE", false)
	if r.snapshot().Phase != "CANCELLED" {
		t.Fatal("late completion overwrote cancellation")
	}
	_, next, err := r.begin()
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop(next)
	r.check(id, "SECRET", "bad")
	r.finish(id, "OLD_RESULT", false)
	if r.snapshot().Diagnosis != "" {
		t.Fatal("old result applied to new operation")
	}
}
func TestRepairConcurrentCancellation(t *testing.T) {
	var r repairState
	_, id, _ := r.begin()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.check(id, "CORE", "OK"); r.snapshot(); r.stop(id) }()
	}
	wg.Wait()
	b, _ := json.Marshal(r.snapshot())
	if strings.Contains(string(b), "token") {
		t.Fatal(string(b))
	}
}
func TestRepairShutdownRejectsStart(t *testing.T) {
	a := &App{}
	a.life.stopIntent(true)
	if _, err := a.startRepair(); err == nil {
		t.Fatal("accepted after shutdown")
	}
}
