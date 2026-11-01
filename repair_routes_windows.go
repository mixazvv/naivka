//go:build windows

package main

import (
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"
)

// Read the OS route table through IP Helper; no route/firewall/registry writes.
func readRepairRoutes() ([]byte, error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	directory := make([]uint16, 32768)
	n, _, _ := kernel.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&directory[0])), uintptr(len(directory)))
	if n == 0 || n >= uintptr(len(directory)) {
		return nil, errors.New("ROUTES_UNKNOWN")
	}
	lib := syscall.NewLazyDLL(filepath.Join(syscall.UTF16ToString(directory[:n]), "iphlpapi.dll"))
	get := lib.NewProc("GetIpForwardTable2")
	free := lib.NewProc("FreeMibTable")
	if get.Find() != nil || free.Find() != nil {
		return nil, errors.New("ROUTES_UNKNOWN")
	}
	var pointer unsafe.Pointer
	result, _, _ := get.Call(0, uintptr(unsafe.Pointer(&pointer)))
	if result != 0 || pointer == nil {
		return nil, errors.New("ROUTES_UNKNOWN")
	}
	defer free.Call(uintptr(pointer))
	count := *(*uint32)(pointer)
	if count > 16384 {
		return nil, errors.New("ROUTES_UNKNOWN")
	}
	size := 8 + int(count)*104
	return append([]byte{}, unsafe.Slice((*byte)(pointer), size)...), nil
}
