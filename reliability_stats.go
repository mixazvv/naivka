package main

import (
	"runtime"
	"time"
)

// Collected only on explicit request; no background profiler or history.
func reliabilityStats() any {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	cpu := processCPUSeconds()
	return struct {
		At           int64   `json:"at"`
		Heap         uint64  `json:"heapBytes"`
		Goroutines   int     `json:"goroutines"`
		CPU          float64 `json:"cpuSeconds"`
		CPUAvailable bool    `json:"cpuAvailable"`
	}{time.Now().UnixMilli(), m.HeapAlloc, runtime.NumGoroutine(), cpu, cpu >= 0}
}
