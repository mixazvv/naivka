//go:build windows

package main

import "syscall"

func processCPUSeconds() float64 {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return -1
	}
	var creation, exit, kernel, user syscall.Filetime
	if syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user) != nil {
		return -1
	}
	return float64((uint64(kernel.HighDateTime)<<32|uint64(kernel.LowDateTime))+(uint64(user.HighDateTime)<<32|uint64(user.LowDateTime))) / 1e7
}
