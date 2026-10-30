package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Посредник Telegram: данные проходят в обе стороны без искажений, счётчики верные.
func TestTgRelay(t *testing.T) {
	// «tg-ws-proxy»: отвечает тем же, что получил, удвоенным
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				b, _ := io.ReadAll(c)
				c.Write(append(b, b...))
			}(c)
		}
	}()
	tg := &TgWs{inner: up.Addr().(*net.TCPAddr).Port}
	port, _ := freePort()
	if err := tg.startRelay(port); err != nil {
		t.Fatal(err)
	}
	defer tg.stopRelay()

	payload := make([]byte, 300_000)
	rand.Read(payload)
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, _, conns := tg.Traffic(); conns != 1 {
		t.Errorf("открытых соединений %d, ожидалось 1", conns)
	}
	c.Write(payload)
	c.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(c)
	c.Close()
	if !bytes.Equal(got, append(payload, payload...)) {
		t.Fatalf("данные искажены: получено %d байт", len(got))
	}
	time.Sleep(50 * time.Millisecond)
	down, upB, conns := tg.Traffic()
	if upB != int64(len(payload)) || down != int64(2*len(payload)) || conns != 0 {
		t.Errorf("счётчики: отдача %d, загрузка %d, соединений %d", upB, down, conns)
	}
}

// portOwner находит процесс, который слушает порт.
func TestPortOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	pid, path := portOwner(port)
	if pid != os.Getpid() || path == "" {
		t.Fatalf("portOwner(%d) = %d %q, ожидался наш процесс %d", port, pid, path, os.Getpid())
	}
	if portFree(port) {
		t.Error("занятый порт считается свободным")
	}
	if p := pickFreePort(port); p == port || !portFree(p) {
		t.Errorf("pickFreePort вернул %d", p)
	}
}

func TestReplaceExe(t *testing.T) {
	dir := t.TempDir()
	exe, tmp := filepath.Join(dir, "TgWsProxy.exe"), filepath.Join(dir, "TgWsProxy.exe.new")
	os.WriteFile(exe, []byte("old"), 0o755)
	os.WriteFile(exe+".old", []byte("stale"), 0o755)
	os.WriteFile(tmp, []byte("new"), 0o755)
	if err := replaceExe(tmp, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe = %q", b)
	}
	if fileExists(tmp) || fileExists(exe+".old") {
		t.Fatal("остались временные файлы")
	}
}
