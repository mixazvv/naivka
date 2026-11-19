//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A local stand-in stalls either config validation or API readiness. No VPN
// server, system proxy, TUN interface or external network is used.
func TestCoreCancelDuringStartup(t *testing.T) {
	for _, phase := range []string{"check", "run"} {
		t.Run(phase, func(t *testing.T) {
			c := NewCore(t.TempDir())
			marker := filepath.Join(c.coreDir(), "entered")
			script := "#!/bin/sh\ncase \"$1\" in\n" + phase + ") touch entered; exec sleep 60;;\n*) exit 0;;\nesac\n"
			if err := os.WriteFile(c.exePath(), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			s := DefaultSettings()
			s.MixedPort = 0
			s.Servers = []Server{{ID: "test", Type: "trojan", Host: "example.invalid", Port: 443, Password: "test"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- c.StartContext(ctx, s) }()
			deadline := time.After(3 * time.Second)
			for !fileExists(marker) {
				select {
				case err := <-result:
					t.Fatalf("startup ended before %s: %v", phase, err)
				case <-deadline:
					t.Fatal("startup did not reach target phase")
				case <-time.After(10 * time.Millisecond):
				}
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("startup ignored cancellation")
			}
			if c.Status().Starting || c.Running() {
				t.Fatal("core remains active")
			}
			c.mu.Lock()
			alive := c.cmd != nil
			c.mu.Unlock()
			if alive {
				t.Fatal("child process was not reaped")
			}
			if fileExists(filepath.Join(c.runDir(), "config.json")) {
				t.Fatal("plaintext config remains")
			}
		})
	}
}

func TestStartupVersionProbeDoesNotBlockWindow(t *testing.T) {
	dir := t.TempDir()
	coreDir := filepath.Join(dir, "core")
	if err := os.MkdirAll(coreDir, 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nwhile [ ! -f release ]; do sleep 0.02; done\necho 'sing-box version fixture'\necho 'Tags: with_naive_outbound'\n"
	if err := os.WriteFile(filepath.Join(coreDir, exeName()), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c := NewCore(dir)
	if info := c.Status().Info; !info.Present || !info.Checking {
		t.Fatal("expected available core with pending capabilities", info)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.StartContext(ctx, DefaultSettings()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("start did not wait for capabilities", err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.initialInfo:
	case <-time.After(3 * time.Second):
		t.Fatal("version probe did not finish")
	}
	if info := c.Status().Info; !info.Present || info.Checking || info.Version != "fixture" || !info.Naive {
		t.Fatal(info)
	}
}
