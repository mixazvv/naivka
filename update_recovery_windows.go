//go:build windows

package main

import (
	"encoding/hex"
	"syscall"
	"unsafe"
)

var querySystemInformation = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQuerySystemInformation")

// SystemBootEnvironmentInformation starts with the GUID for this Windows boot.
func recoveryBootID() string {
	if querySystemInformation.Find() != nil {
		return ""
	}
	var info struct {
		ID       [16]byte
		Firmware uint32
		_        uint32
		Flags    uint64
	}
	var size uint32
	status, _, _ := querySystemInformation.Call(90, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), uintptr(unsafe.Pointer(&size)))
	if status != 0 || size < 16 || info.ID == [16]byte{} {
		return ""
	}
	return hex.EncodeToString(info.ID[:])
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
