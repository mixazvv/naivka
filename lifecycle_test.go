package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLifecycleDisconnectCancelsActiveAndQueuedConnect(t *testing.T) {
	var l lifeState
	gen, err := l.requestConnect()
	if err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	ctx, finish, err := l.operation(gen, true)
	if err != nil {
		t.Fatal(err)
	}
	l.stopIntent(false)
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("active operation not cancelled")
	}
	finish()
	l.mu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, _, err := l.operation(gen, true); err == nil {
		t.Fatal("queued old connect accepted")
	}
	newGen, err := l.requestConnect()
	if err != nil || newGen == gen {
		t.Fatal("explicit reconnect must get current generation", err)
	}
	_, finish, err = l.operation(newGen, true)
	if err != nil {
		t.Fatal(err)
	}
	finish()
}

func TestLifecycleShutdownIsTerminal(t *testing.T) {
	var l lifeState
	gen, _ := l.requestConnect()
	ctx, finish, err := l.operation(gen, true)
	if err != nil {
		t.Fatal(err)
	}
	l.stopIntent(true)
	finish()
	if ctx.Err() == nil {
		t.Fatal("shutdown did not cancel operation")
	}
	// A later disconnect must not undo the terminal shutdown state.
	l.stopIntent(false)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.requestConnect(); err == nil {
				t.Error("connect accepted during shutdown")
			}
		}()
	}
	wg.Wait()
	if l.want.Load() {
		t.Fatal("shutdown intent overwritten")
	}
	if _, _, err := l.operation(l.gen.Load(), false); err == nil {
		t.Fatal("zapret start accepted during shutdown")
	}
	// Check real entry points reject work before touching core/addons.
	a := &App{settings: &Settings{}}
	a.life.stopIntent(true)
	if err := a.connect(); err == nil {
		t.Fatal("connect accepted")
	}
	if err := a.reconnect(); err == nil {
		t.Fatal("reconnect accepted")
	}
	a.settings.ZapretOnly = true
	if err := a.connect(); err == nil {
		t.Fatal("zapret connect accepted")
	}
	if done, err := a.resume(a.life.gen.Load()); !done || err != nil {
		t.Fatal(done, err)
	}
}

func TestClashStartupProbeCancelsInFlightRequest(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer srv.Close()
	cl := &Clash{base: srv.URL, hc: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- cl.pingContext(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe not started")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe ignored cancellation")
	}
}

func TestCoreCancelledStartDoesNotTouchCore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&Core{}).StartContext(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
