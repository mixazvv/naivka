//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
)

type procEntry struct {
	PID        int
	Name, Path string
}

func listProcesses() []procEntry {
	var out []procEntry
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		pid, _ := strconv.Atoi(filepath.Base(d))
		exe, err := os.Readlink(d + "/exe")
		if err != nil {
			continue
		}
		out = append(out, procEntry{PID: pid, Name: filepath.Base(exe), Path: exe})
	}
	return out
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
