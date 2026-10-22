package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Дополнения (zapret и tg-ws-proxy) и ядро обновляются из официальных релизов на GitHub.

func latestTag(repo string) (string, error) {
	hc := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := hc.Head("https://github.com/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("GitHub вернул неожиданный ответ (%s)", resp.Status)
	}
	return loc[i+5:], nil
}

type progressFunc func(done, total int64)

type countingWriter struct {
	n     int64
	total int64
	fn    progressFunc
}

func (w *countingWriter) Write(b []byte) (int, error) {
	w.n += int64(len(b))
	if w.fn != nil {
		w.fn(w.n, w.total)
	}
	return len(b), nil
}

func downloadTo(url, dst string, fn progressFunc) error {
	hc := &http.Client{Timeout: 15 * time.Minute}
	resp, err := hc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub ответил %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	left := int64(maxUnpacked)
	err = copyLimited(io.MultiWriter(f, &countingWriter{total: resp.ContentLength, fn: fn}), resp.Body, &left)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// maxUnpacked — сколько всего можно распаковать из одного архива: ядро, zapret и прочее весят
// десятки мегабайт, а «архив-бомба» иначе забила бы диск.
const maxUnpacked = 512 << 20

// copyLimited копирует, пока не кончится общий запас left.
func copyLimited(dst io.Writer, src io.Reader, left *int64) error {
	n, err := io.Copy(dst, io.LimitReader(src, *left+1))
	*left -= n
	if err == nil && *left < 0 {
		err = errors.New("архив распаковывается в слишком большой объём")
	}
	return err
}

// extractZip распаковывает архив в dst. Если всё лежит в одной общей папке — она снимается.
func extractZip(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	prefix := ""
	for i, f := range zr.File {
		top := strings.SplitN(strings.ReplaceAll(f.Name, `\`, "/"), "/", 2)[0] + "/"
		if i == 0 {
			prefix = top
		} else if top != prefix {
			prefix = ""
			break
		}
	}
	root := filepath.Clean(dst) + string(filepath.Separator)
	left := int64(maxUnpacked)
	for _, f := range zr.File {
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, "/"), prefix)
		if name == "" {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if !strings.HasPrefix(target, root) { // защита от «../» в архиве
			return fmt.Errorf("подозрительный путь в архиве: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		err = copyLimited(out, rc, &left)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// AddonState — состояние установки и обновления для интерфейса.
type AddonState struct {
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Busy      string `json:"busy"` // download | check | ""
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Error     string `json:"error"`
}

type addonBase struct {
	mu    sync.Mutex
	state AddonState
}

func (b *addonBase) setBusy(kind string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state.Busy != "" {
		return false
	}
	b.state.Busy, b.state.Error, b.state.Done, b.state.Total = kind, "", 0, 0
	return true
}

func (b *addonBase) finish(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state.Busy = ""
	if err != nil {
		b.state.Error = err.Error()
	}
}

func (b *addonBase) progress(done, total int64) {
	b.mu.Lock()
	b.state.Done, b.state.Total = done, total
	b.mu.Unlock()
}

func (b *addonBase) snapshot() AddonState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// addonDir: дополнения запускаются с правами администратора (zapret — всегда),
// поэтому при них они лежат в Program Files, куда не может писать обычная программа.
func (a *App) addonDir(name string) string {
	if pb := protectedBase(); pb != "" {
		return filepath.Join(pb, name)
	}
	return filepath.Join(a.dir, "addons", name)
}

func readVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "version.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ---------- проверка обновлений ----------

type UpdatesView struct {
	Core     AddonState `json:"core"`
	Zapret   AddonState `json:"zapret"`
	Telegram AddonState `json:"telegram"`
	Xray     AddonState `json:"xray"`
	Checked  int64      `json:"checked"`
}

var (
	coreLatestMu sync.Mutex
	coreLatest   string
	updatesAt    int64
	notified     = map[string]bool{}
)

func (a *App) checkUpdates(notify bool) {
	type item struct {
		name, repo string
		installed  func() string
		set        func(string)
	}
	items := []item{
		{"Ядро sing-box", "SagerNet/sing-box", func() string { return "v" + a.core.Status().Info.Version },
			func(t string) { coreLatestMu.Lock(); coreLatest = t; coreLatestMu.Unlock() }},
		{"zapret", zapretRepo, func() string { return a.zapret.snapshot().Installed }, a.zapret.setLatest},
		{"tg-ws-proxy", tgwsRepo, func() string { return a.tgws.snapshot().Installed }, a.tgws.setLatest},
		{"Xray", xrayRepo, func() string { return a.xray.snapshot().Installed }, a.xray.setLatest},
	}
	for _, it := range items {
		tag, err := latestTag(it.repo)
		if err != nil {
			continue
		}
		it.set(tag)
		inst := it.installed()
		if notify && inst != "" && inst != "v" && inst != tag && !notified[it.name+tag] {
			notified[it.name+tag] = true
			a.mon.Event("info", fmt.Sprintf("Доступно обновление: %s %s (сейчас %s)", it.name, tag, strings.TrimPrefix(inst, "v")), true)
		}
	}
	coreLatestMu.Lock()
	updatesAt = time.Now().Unix()
	coreLatestMu.Unlock()
}

func (a *App) updatesLoop() {
	time.Sleep(20 * time.Second)
	for {
		if a.settingsCopy().UpdateCheck {
			a.checkUpdates(true)
			_ = a.self.check(a, true)
		}
		time.Sleep(12 * time.Hour)
	}
}

func (a *App) updatesView() UpdatesView {
	st := a.core.Status()
	coreLatestMu.Lock()
	cl, at := coreLatest, updatesAt
	coreLatestMu.Unlock()
	core := AddonState{Latest: cl}
	if st.Info.Version != "" {
		core.Installed = "v" + st.Info.Version
	}
	if st.Download.Active {
		core.Busy, core.Done, core.Total = "download", st.Download.Done, st.Download.Total
	}
	return UpdatesView{Core: core, Zapret: a.zapret.snapshot(), Telegram: a.tgws.snapshot(), Xray: a.xray.snapshot(), Checked: at}
}

var errNeedAdmin = errors.New("Нужны права администратора: перезапустите Naivka с правами (кнопка на этой странице)")
