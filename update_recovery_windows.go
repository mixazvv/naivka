//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// Legacy supervisors execute the renamed .exe.old image. A parent that has
// not yet renamed its executable must also be counted. Never terminate either.
func legacyUpdateIdle(exe string) bool {
	own, err := os.Executable()
	if err != nil || !sameUpdateFile(own, exe) {
		return false
	}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snap)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = syscall.Process32First(snap, &entry); err == nil; err = syscall.Process32Next(snap, &entry) {
		name := strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))
		if int(entry.ProcessID) == os.Getpid() {
			continue
		}
		if !strings.EqualFold(name, filepath.Base(exe)) && !strings.HasSuffix(name, ".exe.old") && !(strings.HasPrefix(name, "naivka") && strings.HasSuffix(name, ".exe")) {
			continue
		}
		path := processPath(int(entry.ProcessID))
		if path == "" || sameUpdateFile(filepath.Dir(path), filepath.Dir(exe)) || sameUpdateFile(path, exe) || sameUpdateFile(path, exe+".old") {
			return false
		}
	}
	return err == syscall.ERROR_NO_MORE_FILES
}

func recoveryProcessAlive(pid int) bool {
	if pid <= 0 {
		return true
	}
	h, err := syscall.OpenProcess(0x1000, false, uint32(pid))
	if err != nil {
		return err != syscall.Errno(87)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if syscall.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == 259
}
