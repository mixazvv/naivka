//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// portOwner для Linux (нужен для тестов): /proc/net/tcp → номер сокета → процесс.
func portOwner(port int) (pid int, path string) {
	inodes := map[string]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) < 10 || fs[3] != "0A" { // 0A — LISTEN
				continue
			}
			i := strings.LastIndexByte(fs[1], ':')
			if p, err := strconv.ParseInt(fs[1][i+1:], 16, 32); err == nil && int(p) == port {
				inodes[fs[9]] = true
			}
		}
		fh.Close()
	}
	if len(inodes) == 0 {
		return 0, ""
	}
	procs, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range procs {
		link, err := os.Readlink(fd)
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		if inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
			parts := strings.Split(fd, "/")
			pid, _ = strconv.Atoi(parts[2])
			exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			return pid, exe
		}
	}
	return 0, ""
}
