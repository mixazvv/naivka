//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
)

type processEntry32 struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

type procEntry struct {
	PID        int
	Name, Path string
}

// listProcesses — запущенные программы (снимок процессов Windows, без консольных утилит).
func listProcesses() []procEntry {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(2 /* TH32CS_SNAPPROCESS */, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return nil
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	var out []procEntry
	var e processEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e))); r != 0; r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e))) {
		pid := int(e.ProcessID)
		if pid > 4 {
			out = append(out, procEntry{PID: pid, Name: syscall.UTF16ToString(e.ExeFile[:]), Path: processPath(pid)})
		}
	}
	return out
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
