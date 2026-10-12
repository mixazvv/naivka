package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PortIssue — порт, который нужен Naivka, занят другой программой.
type PortIssue struct {
	Port int    `json:"port"`
	PID  int    `json:"pid"`
	Name string `json:"name"` // имя программы, если удалось узнать
	Path string `json:"path"`
	Kind string `json:"kind"` // naivka — вторая копия Naivka; other — чужая программа
}

func (p *PortIssue) Error() string {
	who := "другая программа"
	if p.Name != "" {
		who = "программа " + p.Name
	}
	if p.Kind == "naivka" {
		who = "другая копия Naivka"
	}
	return fmt.Sprintf("Порт %d занят: его держит %s. Закройте её или возьмите свободный порт.", p.Port, who)
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ownCoreExes — где может лежать наше ядро (обычная папка и Program Files для режима TUN).
func (c *Core) ownCoreExes() []string {
	out := []string{filepath.Join(c.dir, "core", exeName())}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		out = append(out, filepath.Join(pf, "Naivka", "core", exeName()))
	}
	return out
}

// freePortFor освобождает порт перед запуском ядра: зависшее ядро от прошлого запуска
// закрывает само, про остальных — возвращает понятную ошибку с именем программы.
func (c *Core) freePortFor(port int) error {
	if portFree(port) {
		return nil
	}
	pid, path := portOwner(port)
	issue := &PortIssue{Port: port, PID: pid, Path: path, Kind: "other"}
	if path != "" {
		issue.Name = filepath.Base(path)
	}
	if pid == os.Getpid() {
		return fmt.Errorf("порт %d уже занят самой Naivka (например, совпадает с портом Telegram) — выберите другой", port)
	}
	if pid > 0 && path != "" {
		for _, own := range c.ownCoreExes() {
			if samePath(path, own) {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
				for i := 0; i < 20 && !portFree(port); i++ {
					time.Sleep(100 * time.Millisecond)
				}
				if portFree(port) {
					c.logs.Add(fmt.Sprintf("[naivka] закрыто зависшее ядро от прошлого запуска (PID %d), порт %d освобождён", pid, port))
					return nil
				}
				issue.Name = "зависшее ядро Naivka (sing-box.exe)"
				return fmt.Errorf("Порт %d держит зависшее ядро Naivka от прошлого запуска, и закрыть его не вышло — скорее всего, оно запущено с правами администратора. Перезапустите Naivka с правами или закройте sing-box.exe в Диспетчере задач", port)
			}
		}
		if strings.EqualFold(issue.Name, "Naivka.exe") || strings.EqualFold(issue.Name, "naivka") {
			issue.Kind = "naivka"
		}
	}
	return issue
}

// pickFreePort — ближайший свободный порт после занятого (2081, 2082…), иначе любой свободный.
func pickFreePort(from int, avoid ...int) int {
	skip := map[int]bool{}
	for _, a := range avoid {
		skip[a] = true
	}
	for p := from + 1; p < from+50 && p < 65536; p++ {
		if !skip[p] && portFree(p) {
			return p
		}
	}
	p, _ := freePort()
	return p
}

var errNoPortInfo = errors.New("нет данных о порте")
