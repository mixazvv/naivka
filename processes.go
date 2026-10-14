package main

import (
	"path/filepath"
	"sort"
	"strings"
)

type ProcInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Net  bool   `json:"net"` // выходила в сеть за этот сеанс
}

// Processes — программы для правил: сначала те, что выходили в сеть, потом остальные запущенные.
// Системные программы из папки Windows показываются, только если сами выходили в сеть.
func (a *App) Processes() []ProcInfo {
	seen := map[string]*ProcInfo{}
	a.mon.mu.Lock()
	for name, path := range a.mon.procSeen {
		seen[strings.ToLower(name)] = &ProcInfo{Name: name, Path: path, Net: true}
	}
	a.mon.mu.Unlock()
	winDir := strings.ToLower(filepath.Join(envOr("SystemRoot", `C:\Windows`)) + `\`)
	for _, p := range listProcesses() {
		key := strings.ToLower(p.Name)
		if e, ok := seen[key]; ok {
			if e.Path == "" {
				e.Path = p.Path
			}
			continue
		}
		lp := strings.ToLower(p.Path)
		if p.Name == "" || strings.HasPrefix(lp, winDir) || p.Path == "" {
			continue
		}
		seen[key] = &ProcInfo{Name: p.Name, Path: p.Path}
	}
	skip := map[string]bool{"naivka.exe": true, "sing-box.exe": true, "winws.exe": true, "tgwsproxy.exe": true, "msedgewebview2.exe": true}
	var out []ProcInfo
	for k, v := range seen {
		if !skip[k] {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Net != out[j].Net {
			return out[i].Net
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
