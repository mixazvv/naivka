package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Обновление самой Naivka: проверка релизов на GitHub и установка по кнопке.
// Скачивается архив из релиза этого же репозитория, сверяется с SHA256SUMS.txt того же
// релиза, затем заменяется только запущенный exe (настройки и данные не трогаются)
// и Naivka перезапускается.

const appRepo = "mixazvv/naivka"

type selfUpdater struct {
	addonBase
	checked int64
}

// AppUpdateView — состояние для интерфейса.
type AppUpdateView struct {
	AddonState
	Newer   bool  `json:"newer"`
	Checked int64 `json:"checked"`
}

// parseVer: "v2.3.4" → [2 3 4]; не номер версии (сборка "dev") — nil.
func parseVer(v string) []int {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) < 2 || len(parts) > 4 {
		return nil
	}
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

// versionNewer — latest новее current. Для сборки разработчика всегда false.
func versionNewer(latest, current string) bool {
	l, c := parseVer(latest), parseVer(current)
	if l == nil || c == nil {
		return false
	}
	for i := 0; i < len(l) || i < len(c); i++ {
		var x, y int
		if i < len(l) {
			x = l[i]
		}
		if i < len(c) {
			y = c[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func (u *selfUpdater) view() AppUpdateView {
	st := u.snapshot()
	st.Installed = appVersion
	u.mu.Lock()
	at := u.checked
	u.mu.Unlock()
	return AppUpdateView{AddonState: st, Newer: versionNewer(st.Latest, appVersion), Checked: at}
}

// check узнаёт последнюю версию. notify — сообщить о новой один раз (событие и уведомление Windows).
func (u *selfUpdater) check(a *App, notify bool) error {
	if parseVer(appVersion) == nil {
		return nil
	}
	tag, err := latestTag(appRepo)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.state.Latest, u.checked = tag, time.Now().Unix()
	u.mu.Unlock()
	if notify && versionNewer(tag, appVersion) && !notified["Naivka"+tag] {
		notified["Naivka"+tag] = true
		a.mon.Event("info", fmt.Sprintf("Вышла новая версия Naivka %s — обновить можно на главном экране одной кнопкой", tag), true)
	}
	return nil
}

// releaseAsset — какой архив из релиза подходит этой сборке.
func releaseAsset(tag string) string {
	if legacyBuild {
		return "Naivka-" + tag + "-windows7.zip"
	}
	return "Naivka-" + tag + "-windows.zip"
}

// sumFor ищет контрольную сумму файла в SHA256SUMS.txt («<hex>  <имя>»).
func sumFor(sums io.Reader, name string) string {
	sc := bufio.NewScanner(sums)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// findExe — новый exe в распакованном архиве: с тем же именем, что запущенный, иначе основной этой сборки.
func findExe(root, current string) string {
	names := []string{current}
	if legacyBuild {
		if strings.Contains(current, "32") {
			names = append(names, "Naivka-win7-32.exe")
		}
		names = append(names, "Naivka-win7-64.exe")
	} else {
		names = append(names, "Naivka.exe")
	}
	for _, n := range names {
		p := filepath.Join(root, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && isWindowsExe(p) {
			return p
		}
	}
	return ""
}

func isWindowsExe(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	_, err = io.ReadFull(f, b)
	return err == nil && string(b) == "MZ"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var errManualUpdate = errors.New("скачайте новую версию со страницы релизов и распакуйте поверх старой папки")

// install скачивает, проверяет и ставит последнюю версию, затем перезапускает Naivka.
func (u *selfUpdater) install(a *App) error {
	st := u.snapshot()
	if !versionNewer(st.Latest, appVersion) {
		return errors.New("Установлена последняя версия")
	}
	if !u.setBusy("download") {
		return errors.New("Обновление уже идёт")
	}
	go func() {
		err := u.run(a, st.Latest)
		if err != nil {
			log.Println("обновление Naivka:", err)
			a.mon.Event("error", "Не удалось обновить Naivka: "+err.Error(), false)
		}
		u.finish(err)
	}()
	return nil
}

// releaseBase — откуда скачивать релизы; в тестах подменяется на локальный сервер.
var releaseBase = "https://github.com/"

// fetch скачивает архив релиза в work, сверяет контрольные суммы, распаковывает
// и возвращает путь к новому exe.
func (u *selfUpdater) fetch(work, tag, exeName string) (string, error) {
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return "", err
	}
	base := releaseBase + appRepo + "/releases/download/" + tag + "/"
	asset := releaseAsset(tag)
	sumsPath := filepath.Join(work, "SHA256SUMS.txt")
	if err := downloadTo(base+"SHA256SUMS.txt", sumsPath, nil); err != nil {
		return "", fmt.Errorf("не скачались контрольные суммы: %v", err)
	}
	zipPath := filepath.Join(work, asset)
	if err := downloadTo(base+asset, zipPath, u.progress); err != nil {
		return "", fmt.Errorf("не скачался архив: %v", err)
	}
	sf, err := os.Open(sumsPath)
	if err != nil {
		return "", err
	}
	want := sumFor(sf, asset)
	sf.Close()
	got, err := fileSHA256(zipPath)
	if err != nil {
		return "", err
	}
	if want == "" || got != want {
		return "", errors.New("архив не совпал с контрольной суммой из релиза — ничего не заменено")
	}
	// И с хешем, который GitHub посчитал сам при загрузке файла (если API его отдало).
	if _, err := verifyAsset(appRepo, tag, asset, zipPath); err != nil {
		return "", err
	}

	u.mu.Lock()
	u.state.Busy = "install"
	u.mu.Unlock()
	unpacked := filepath.Join(work, "x")
	if err := extractZip(zipPath, unpacked); err != nil {
		return "", err
	}
	fresh := findExe(unpacked, exeName)
	if fresh == "" {
		return "", errors.New("в архиве нет программы для этой системы")
	}
	return fresh, nil

}

func (u *selfUpdater) run(a *App, tag string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	work := filepath.Join(a.dir, "update")
	fresh, err := u.fetch(work, tag, filepath.Base(exe))
	if err != nil {
		return err
	}
	unpacked := filepath.Dir(fresh)

	// Запущенный exe Windows не даёт перезаписать, но даёт переименовать.
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("нет доступа к папке программы (%v) — %w", err, errManualUpdate)
	}
	if err := copyFile(fresh, exe); err != nil {
		os.Remove(exe)
		os.Rename(old, exe)
		return fmt.Errorf("не удалось записать новую версию (%v) — %w", err, errManualUpdate)
	}
	// Отладочная копия рядом (если есть) обновляется тоже: её никто не держит.
	if !legacyBuild && filepath.Base(exe) != "Naivka-debug.exe" {
		dbg := filepath.Join(filepath.Dir(exe), "Naivka-debug.exe")
		if fileExists(dbg) && fileExists(filepath.Join(unpacked, "Naivka-debug.exe")) {
			_ = copyFile(filepath.Join(unpacked, "Naivka-debug.exe"), dbg)
		}
	}

	wasRunning := a.core.Running()
	a.pause()
	args := []string{"--replace"}
	if wasRunning {
		args = append(args, "--connect")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		os.Remove(exe)
		os.Rename(old, exe)
		if wasRunning {
			go a.connect()
		}
		return fmt.Errorf("новая версия не запустилась (%v), оставлена прежняя", err)
	}
	log.Println("обновление Naivka до", tag, "— перезапуск")
	go func() { time.Sleep(400 * time.Millisecond); a.Quit() }()
	return nil
}

// cleanupUpdate убирает следы прошлого обновления: старый exe и скачанный архив.
func cleanupUpdate(dir string) {
	os.RemoveAll(filepath.Join(dir, "update"))
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	old := exe + ".old"
	if !fileExists(old) {
		return
	}
	// Прежний процесс мог ещё не отпустить файл.
	for i := 0; i < 10; i++ {
		if os.Remove(old) == nil {
			return
		}
		time.Sleep(time.Second)
	}
}
