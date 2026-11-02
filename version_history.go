package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var versionAPIBase = "https://api.github.com/repos/" + appRepo
var releaseTagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+(?:\.[0-9]+){0,2}$`)

type versionRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}
type versionOption struct {
	Tag       string `json:"tag"`
	Available bool   `json:"available"`
	Current   bool   `json:"current"`
}

func versionRequest(ctx context.Context, path string, out any) error {
	req, e := http.NewRequestWithContext(ctx, "GET", versionAPIBase+path, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Naivka")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("Не удалось загрузить список версий GitHub")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub: HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if e != nil || len(b) > 4*1024*1024 {
		return errors.New("Слишком большой ответ GitHub")
	}
	return json.Unmarshal(b, out)
}
func versionAvailable(r versionRelease) bool {
	if r.Draft || r.Prerelease || !releaseTagPattern.MatchString(r.Tag) || parseVer(r.Tag) == nil {
		return false
	}
	need := map[string]bool{releaseAsset(r.Tag): false, "SHA256SUMS.txt": false, "SHA256SUMS.txt.sig": false}
	for _, a := range r.Assets {
		if _, ok := need[a.Name]; ok {
			need[a.Name] = true
		}
	}
	for _, yes := range need {
		if !yes {
			return false
		}
	}
	return true
}

type versionPage struct {
	Versions []versionOption `json:"versions"`
	HasMore  bool            `json:"hasMore"`
}

func versionList(ctx context.Context, page int) (versionPage, error) {
	if page < 1 || page > 10000 {
		return versionPage{}, errors.New("Неверная страница")
	}
	var all []versionRelease
	if e := versionRequest(ctx, "/releases?per_page=50&page="+strconv.Itoa(page), &all); e != nil {
		return versionPage{}, e
	}
	out := make([]versionOption, 0, len(all))
	for _, r := range all {
		if r.Draft || !releaseTagPattern.MatchString(r.Tag) {
			continue
		}
		out = append(out, versionOption{r.Tag, versionAvailable(r), !versionNewer(r.Tag, appVersion) && !versionNewer(appVersion, r.Tag)})
	}
	return versionPage{out, len(all) == 50}, nil
}
func (u *selfUpdater) installVersion(a *App, tag string) error {
	if !releaseTagPattern.MatchString(tag) || parseVer(tag) == nil {
		return errors.New("Неверный номер версии")
	}
	if !versionNewer(tag, appVersion) && !versionNewer(appVersion, tag) {
		return errors.New("Эта версия уже установлена")
	}
	if !u.setBusy("download") {
		return errors.New("Установка уже выполняется")
	}
	go func() {
		var release versionRelease
		e := versionRequest(context.Background(), "/releases/tags/"+tag, &release)
		if e == nil && (release.Tag != tag || !versionAvailable(release)) {
			e = errors.New("Для версии нет подписанного архива этой платформы")
		}
		if e == nil {
			e = u.run(a, tag)
		}
		if e != nil {
			a.core.logs.Add("[naivka] смена версии: " + e.Error())
			a.mon.Event("error", "Смена версии: "+firstLine(e.Error()), false)
		}
		u.finish(e)
	}()
	return nil
}

// Backups contain sealed settings, never decrypted credentials. They are retained
// outside cleanupUpdate's temporary paths and can also be restored manually.
func createVersionBackup(exe, dir, current, target string) (string, error) {
	root := filepath.Join(dir, "version-backups")
	if e := os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	backup, e := os.MkdirTemp(root, time.Now().UTC().Format("20060102-150405")+"-"+strings.TrimPrefix(current, "v")+"-")
	if e != nil {
		return "", e
	}
	body, e := os.ReadFile(exe)
	if e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(backup, "Naivka.exe"), body, 0700); e != nil {
		return "", e
	}
	if body, e = os.ReadFile(settingsPath(dir)); e == nil {
		e = os.WriteFile(filepath.Join(backup, "settings.json"), body, 0600)
	} else if errors.Is(e, os.ErrNotExist) {
		e = nil
	}
	if e != nil {
		return "", e
	}
	manifest, _ := json.Marshal(map[string]string{"from": current, "to": target, "executable": exe, "note": "settings.json is sealed for the current Windows user; stop Naivka before restoring both files"})
	if e = os.WriteFile(filepath.Join(backup, "manifest.json"), manifest, 0600); e != nil {
		return "", e
	}
	return backup, nil
}
func prepareVersionSettings(cfg updateSupervisorConfig) error {
	if cfg.Backup == "" {
		return nil
	}
	b, e := os.ReadFile(settingsPath(cfg.Dir))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	// Take the final settings after the old process has exited.
	if e = writeVersionSettings(cfg.Backup, b); e != nil {
		return e
	}
	if !cfg.Downgrade {
		return nil
	}
	var raw map[string]json.RawMessage
	if e = json.Unmarshal(b, &raw); e != nil {
		return e
	}
	if raw == nil {
		return errors.New("Некорректные настройки")
	}
	raw["autoConnect"] = json.RawMessage("false")
	b, e = json.MarshalIndent(raw, "", "  ")
	if e != nil {
		return e
	}
	return writeVersionSettings(cfg.Dir, b)
}
func writeVersionSettings(dir string, b []byte) error {
	tmp := settingsPath(dir) + ".version.tmp"
	if e := os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, settingsPath(dir))
}
func restoreVersionSettings(cfg updateSupervisorConfig) error {
	if cfg.Backup == "" {
		return nil
	}
	b, e := os.ReadFile(filepath.Join(cfg.Backup, "settings.json"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return writeVersionSettings(cfg.Dir, b)
}
