package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Эти значения вшиваются при сборке релиза утилитой cmd/release (-ldflags -X …).
// В сборке для разработки (build.bat) обновление выключено.
var (
	appVersion = "dev"
	selfRepo   = ""                    // «имя/naivka» на GitHub
	updateKey  = ""                    // открытый ключ ed25519 (base64), которым подписаны релизы
	updateBase = "https://github.com/" // в тестах подменяется на локальный сервер
)

type SelfUpdateView struct {
	Version   string `json:"version"`
	Repo      string `json:"repo"`
	Enabled   bool   `json:"enabled"`
	Latest    string `json:"latest"`
	Notes     string `json:"notes"`
	Available bool   `json:"available"`
	Busy      string `json:"busy"` // check | download | ""
	Error     string `json:"error"`
	Checked   int64  `json:"checked"`
}

var (
	selfMu    sync.Mutex
	selfState SelfUpdateView
)

func selfEnabled() bool { return appVersion != "dev" && selfRepo != "" && updateKey != "" }

// selfAsset — какой файл релиза подходит этой сборке.
func selfAsset() string {
	if runtime.GOOS != "windows" {
		return "Naivka-" + runtime.GOOS + "-" + runtime.GOARCH
	}
	if legacyBuild {
		if runtime.GOARCH == "386" {
			return "Naivka-win7-32.exe"
		}
		return "Naivka-win7-64.exe"
	}
	return "Naivka.exe"
}

// parseVer: «v1.2.3» → [1 2 3]; всё, что не число, считается нулём.
func parseVer(v string) [3]int {
	var out [3]int
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	for i, p := range parts {
		if i > 2 {
			break
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

func newerVersion(a, b string) bool {
	x, y := parseVer(a), parseVer(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func selfView() SelfUpdateView {
	selfMu.Lock()
	defer selfMu.Unlock()
	v := selfState
	v.Version, v.Repo, v.Enabled = appVersion, selfRepo, selfEnabled()
	v.Available = v.Enabled && v.Latest != "" && newerVersion(v.Latest, appVersion)
	return v
}

func setSelf(f func(s *SelfUpdateView)) {
	selfMu.Lock()
	f(&selfState)
	selfMu.Unlock()
}

func releaseURL(tag, name string) string {
	return updateBase + selfRepo + "/releases/download/" + tag + "/" + name
}

func fetchSmall(url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", filepath.Base(url), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// checkSelf узнаёт последнюю версию в репозитории и что в ней изменилось.
func (a *App) checkSelf(notify bool) error {
	if !selfEnabled() {
		return errors.New("Это сборка для разработки: обновление включается в релизных сборках (утилита cmd/release)")
	}
	setSelf(func(s *SelfUpdateView) { s.Busy, s.Error = "check", "" })
	tag, err := latestTagFrom(updateBase, selfRepo)
	if err != nil {
		setSelf(func(s *SelfUpdateView) { s.Busy, s.Error = "", "не удалось проверить: "+err.Error() })
		return err
	}
	notes := ""
	if newerVersion(tag, appVersion) {
		if b, err := fetchSmall(releaseURL(tag, "CHANGES.txt"), 16<<10); err == nil {
			notes = strings.TrimSpace(string(b))
		}
	}
	prev := selfView().Latest
	setSelf(func(s *SelfUpdateView) { s.Busy, s.Latest, s.Notes, s.Checked = "", tag, notes, time.Now().Unix() })
	if notify && newerVersion(tag, appVersion) && prev != tag {
		a.mon.Event("info", fmt.Sprintf("Доступна новая версия MZ %s (сейчас %s) — «Настройки» → «О программе»", tag, appVersion), true)
	}
	return nil
}

// applySelf скачивает новую версию, проверяет подпись и хеш, подменяет exe и перезапускается.
func (a *App) applySelf() error {
	v := selfView()
	if !v.Available {
		return errors.New("Новой версии нет")
	}
	if v.Busy != "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	newPath := exe + ".new"
	if f, err := os.Create(newPath); err != nil {
		return fmt.Errorf("папка с программой защищена от записи (%s). Переложите её туда, где можно писать, или запустите с правами администратора", filepath.Dir(exe))
	} else {
		f.Close()
	}
	setSelf(func(s *SelfUpdateView) { s.Busy, s.Error = "download", "" })
	fail := func(err error) error {
		os.Remove(newPath)
		setSelf(func(s *SelfUpdateView) { s.Busy, s.Error = "", err.Error() })
		return err
	}
	tag := v.Latest
	sums, err := fetchSmall(releaseURL(tag, "SHA256SUMS"), 64<<10)
	if err != nil {
		return fail(err)
	}
	sig, err := fetchSmall(releaseURL(tag, "SHA256SUMS.sig"), 4<<10)
	if err != nil {
		return fail(err)
	}
	if err := verifySums(sums, sig); err != nil {
		return fail(err)
	}
	if err := checkSumsVersion(sums, tag); err != nil {
		return fail(err)
	}
	want, err := sumFor(sums, selfAsset())
	if err != nil {
		return fail(err)
	}
	if err := downloadTo(releaseURL(tag, selfAsset()), newPath, nil); err != nil {
		return fail(err)
	}
	if got, err := fileSHA256(newPath); err != nil || got != want {
		return fail(errors.New("скачанный файл не совпал с подписанной контрольной суммой — обновление отменено"))
	}
	_ = os.Chmod(newPath, 0o755)
	old := exe + ".old"
	_ = os.Remove(old)
	// Работающий exe в Windows нельзя перезаписать, но можно переименовать.
	if err := os.Rename(exe, old); err != nil {
		return fail(fmt.Errorf("не удалось заменить программу: %v", err))
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(old, exe)
		return fail(fmt.Errorf("не удалось заменить программу: %v", err))
	}
	a.mon.Event("ok", displayName+": установлена версия "+tag+", перезапускаюсь", false)
	cmd := exec.Command(exe, "--replace", "--updated")
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		_ = os.Rename(exe, newPath)
		_ = os.Rename(old, exe)
		return fail(fmt.Errorf("не удалось запустить новую версию: %v", err))
	}
	go func() { time.Sleep(300 * time.Millisecond); a.Quit() }()
	return nil
}

func verifySums(sums, sig []byte) error {
	pub, err := base64.StdEncoding.DecodeString(updateKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("в сборке нет ключа проверки обновлений")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), sums, s) {
		return errors.New("подпись релиза не прошла проверку — обновление отменено. Файлы в репозитории могли подменить")
	}
	return nil
}

// checkSumsVersion: версия, записанная в подписанном SHA256SUMS, должна совпадать с тегом релиза.
// Иначе это старый подписанный релиз, перевыложенный под новым номером (откат на версию с уязвимостью).
func checkSumsVersion(sums []byte, tag string) error {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 3 && f[0] == "#" && f[1] == "naivka" {
			if f[2] == tag {
				return nil
			}
			return fmt.Errorf("релиз %s подписан как %s — похоже на подмену старой версией, обновление отменено", tag, f[2])
		}
	}
	return fmt.Errorf("в подписанном списке файлов релиза %s нет номера версии — обновление отменено", tag)
}

func sumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("в релизе нет файла %s для этой версии Windows", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func latestTagFrom(base, repo string) (string, error) {
	if base == "https://github.com/" {
		return latestTag(repo)
	}
	hc := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Head(base + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", errors.New("нет релизов")
	}
	return loc[i+5:], nil
}

// cleanupOld убирает файл прошлой версии, оставшийся после обновления (он может быть ещё занят).
func cleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := exe + ".old"
	for i := 0; i < 30 && fileExists(old); i++ {
		if os.Remove(old) == nil {
			return
		}
		time.Sleep(time.Second)
	}
}

// waitOldInstance: новая версия ждёт, пока старая закроется и отпустит порты.
func waitOldInstance(dir string) {
	inst, ok := readInstance(dir)
	if !ok {
		return
	}
	for i := 0; i < 50 && pingInstance(inst); i++ {
		time.Sleep(200 * time.Millisecond)
	}
}
