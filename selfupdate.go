package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Обновление самой Naivka: проверка релизов на GitHub и установка по кнопке.
// Архив из релиза этого же репозитория скачивается в память, сверяется с SHA256SUMS.txt того же
// релиза и с хешем, который хранит GitHub, затем заменяется только запущенный exe (настройки
// и данные не трогаются) и Naivka перезапускается. На диск до проверки ничего не пишется,
// так что подменить проверенный файл до установки нельзя.

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

// downloadBytes скачивает файл в память (не больше limit байт).
func downloadBytes(url string, limit int64, fn progressFunc) ([]byte, error) {
	hc := &http.Client{Timeout: 15 * time.Minute}
	resp, err := hc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub ответил %s", resp.Status)
	}
	var buf bytes.Buffer
	n, err := io.Copy(io.MultiWriter(&buf, &countingWriter{total: resp.ContentLength, fn: fn}), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if n > limit {
		return nil, errors.New("файл слишком большой")
	}
	return buf.Bytes(), nil
}

// fetch скачивает архив релиза, сверяет его с контрольными суммами и возвращает новый exe.
// Всё в памяти: между проверкой и записью файл на диске никто не подменит.
func (u *selfUpdater) fetch(tag, exeName string) (exe []byte, debug []byte, err error) {
	base := releaseBase + appRepo + "/releases/download/" + tag + "/"
	asset := releaseAsset(tag)
	sums, err := downloadBytes(base+"SHA256SUMS.txt", 1<<20, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("не скачались контрольные суммы: %v", err)
	}
	archive, err := downloadBytes(base+asset, 300<<20, u.progress)
	if err != nil {
		return nil, nil, fmt.Errorf("не скачался архив: %v", err)
	}
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	if want := sumFor(bytes.NewReader(sums), asset); want == "" || got != want {
		return nil, nil, errors.New("архив не совпал с контрольной суммой из релиза — ничего не заменено")
	}
	// И с хешем, который GitHub посчитал сам при загрузке файла (если API его отдало).
	if want, derr := assetDigest(appRepo, tag, asset); derr == nil && want != "" && want != got {
		return nil, nil, errors.New("архив не совпал с контрольной суммой, которую хранит GitHub, — ничего не заменено")
	}

	u.mu.Lock()
	u.state.Busy = "install"
	u.mu.Unlock()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, nil, err
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() {
			files[path.Base(strings.ReplaceAll(f.Name, `\`, "/"))] = f
		}
	}
	read := func(name string) []byte {
		f := files[name]
		if f == nil || f.UncompressedSize64 > 300<<20 {
			return nil
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, 300<<20))
		if err != nil || len(b) < 2 || string(b[:2]) != "MZ" {
			return nil
		}
		return b
	}
	for _, n := range exeCandidates(exeName) {
		if exe = read(n); exe != nil {
			break
		}
	}
	if exe == nil {
		return nil, nil, errors.New("в архиве нет программы для этой системы")
	}
	if !legacyBuild {
		debug = read("Naivka-debug.exe")
	}
	return exe, debug, nil
}

// exeCandidates — какой exe из архива ставить: с тем же именем, что запущенный, иначе основной этой сборки.
func exeCandidates(current string) []string {
	names := []string{current}
	if legacyBuild {
		if strings.Contains(current, "32") {
			names = append(names, "Naivka-win7-32.exe")
		}
		return append(names, "Naivka-win7-64.exe")
	}
	return append(names, "Naivka.exe")
}

// writeExe пишет новый exe рядом со старым (в ту же папку, тот же диск) и подменяет его.
func writeExe(exe string, body []byte) error {
	tmp := exe + ".new"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, body, 0o755); err != nil {
		return fmt.Errorf("не удалось записать новую версию (%v) — %w", err, errManualUpdate)
	}
	// Запущенный exe Windows не даёт перезаписать, но даёт переименовать.
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("нет доступа к папке программы (%v) — %w", err, errManualUpdate)
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe)
		os.Remove(tmp)
		return fmt.Errorf("не удалось записать новую версию (%v) — %w", err, errManualUpdate)
	}
	return nil
}

func (u *selfUpdater) run(a *App, tag string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	body, debug, err := u.fetch(tag, filepath.Base(exe))
	if err != nil {
		return err
	}
	if err := writeExe(exe, body); err != nil {
		return err
	}
	// Отладочная копия рядом (если есть) обновляется тоже: её никто не держит.
	if dbg := filepath.Join(filepath.Dir(exe), "Naivka-debug.exe"); debug != nil && filepath.Base(exe) != "Naivka-debug.exe" && fileExists(dbg) {
		_ = os.WriteFile(dbg, debug, 0o755)
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
		os.Rename(exe+".old", exe)
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
