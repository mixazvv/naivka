//go:build windows

package main

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var (
	iphlpapi                   = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable    = iphlpapi.NewProc("GetExtendedTcpTable")
	procQueryFullProcessImageW = kernel32.NewProc("QueryFullProcessImageNameW")
)

// portOwner: какой процесс слушает порт на 127.0.0.1 / 0.0.0.0 / [::].
// Это та же таблица, что показывает netstat -ano, но без запуска консольной программы.
func portOwner(port int) (pid int, path string) {
	for _, af := range []uintptr{2, 23} { // AF_INET, AF_INET6
		var size uint32
		procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, af, 3 /* TCP_TABLE_OWNER_PID_LISTENER */, 0)
		if size == 0 {
			continue
		}
		buf := make([]byte, size+1024)
		size = uint32(len(buf))
		if r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, af, 3, 0); r != 0 {
			continue
		}
		n := int(binary.LittleEndian.Uint32(buf))
		rowSize, portOff, pidOff := 24, 8, 20 // MIB_TCPROW_OWNER_PID
		if af == 23 {
			rowSize, portOff, pidOff = 56, 20, 52 // MIB_TCP6ROW_OWNER_PID
		}
		for i := 0; i < n; i++ {
			row := buf[4+i*rowSize:]
			if len(row) < rowSize {
				break
			}
			// порт хранится в сетевом порядке байт в младших 16 битах
			p := int(row[portOff])<<8 | int(row[portOff+1])
			if p == port {
				pid = int(binary.LittleEndian.Uint32(row[pidOff:]))
				return pid, processPath(pid)
			}
		}
	}
	return 0, ""
}

func processPath(pid int) string {
	h, err := syscall.OpenProcess(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := procQueryFullProcessImageW.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
