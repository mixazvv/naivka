package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type updateRecoveryRecord struct {
	Exe        string `json:"exe"`
	Parent     int    `json:"parent"`
	Supervisor int    `json:"supervisor"`
}

func readRecoveryFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("Слишком большой файл операции обновления")
	}
	return b, nil
}

func writeUpdateRecovery(path string, v updateRecoveryRecord) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".recovery-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path+".recovery")
}

type updateRecoveryPlan struct {
	Active   bool   `json:"active"`
	Pending  int    `json:"pending"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason"`
	Revision string `json:"revision"`
	files    []string
}

func inspectUpdateRecovery(dir, exe string, alive func(int) bool) (updateRecoveryPlan, error) {
	files, err := filepath.Glob(filepath.Join(dir, "update-intent-*.json"))
	p := updateRecoveryPlan{Pending: len(files), Ready: len(files) > 0, files: files}
	if err != nil {
		return p, err
	}
	hash := sha256.New()
	for _, file := range files {
		b, err := readRecoveryFile(file, 4096)
		if err != nil {
			return p, err
		}
		fmt.Fprint(hash, filepath.Base(file))
		hash.Write(b)
		b, err = readRecoveryFile(file+".recovery", 16384)
		if err != nil {
			if !os.IsNotExist(err) {
				return p, err
			}
			p.Ready = false
			p.Reason = "У операции старой версии нет сведений о процессах. Автоматическое завершение небезопасно; используйте проверенный архив релиза."
			continue
		}
		hash.Write(b)
		var v updateRecoveryRecord
		if json.Unmarshal(b, &v) != nil || v.Parent <= 0 || !sameUpdateFile(v.Exe, exe) {
			p.Ready = false
			p.Reason = "Не удалось подтвердить принадлежность операции этой программе."
			continue
		}
		if alive(v.Parent) || (v.Supervisor > 0 && alive(v.Supervisor)) {
			p.Active = true
			p.Ready = false
			p.Reason = "Обновление ещё выполняется или его процесс не удалось проверить. Дождитесь завершения и запустите проверку снова."
		}
	}
	for _, file := range []string{exe, exe + ".old"} {
		if info, err := os.Stat(file); err == nil {
			fmt.Fprintf(hash, "%s:%d:%d", file, info.Size(), info.ModTime().UnixNano())
		}
	}
	p.Revision = hex.EncodeToString(hash.Sum(nil))
	return p, nil
}
func (a *App) updateView() AppUpdateView {
	v := a.self.view()
	a.life.intentMu.Lock()
	active := a.life.updating || a.life.closing
	a.life.intentMu.Unlock()
	if v.Busy == "" && !active {
		if exe, err := os.Executable(); err == nil {
			if p, err := inspectUpdateRecovery(a.dir, exe, recoveryProcessAlive); err == nil && !p.Active {
				v.RecoveryPending = p.Pending
			}
		}
	}
	return v
}
func archiveInterruptedUpdate(dir, exe, revision string, alive func(int) bool) error {
	// Caller serializes lifecycle and file replacement before inspecting again.
	p, err := inspectUpdateRecovery(dir, exe, alive)
	if err != nil {
		return err
	}
	if !p.Ready || p.Revision != revision {
		return errors.New("Состояние обновления изменилось или операция ещё активна. Проверьте снова")
	}
	root := filepath.Join(dir, "version-backups")
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	archive, err := os.MkdirTemp(root, "recovered-")
	if err != nil {
		return err
	}
	// Preserve sealed settings before moving any transaction files.
	if b, e := os.ReadFile(settingsPath(dir)); e == nil {
		if err = os.WriteFile(filepath.Join(archive, "settings.json"), b, 0600); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	moves := [][2]string{}
	rollback := func(cause error) error {
		var restoreErr error
		for i := len(moves) - 1; i >= 0; i-- {
			if e := os.Rename(moves[i][1], moves[i][0]); e != nil {
				restoreErr = e
			}
		}
		if restoreErr != nil {
			return fmt.Errorf("Не удалось вернуть файлы операции; резервная копия сохранена: %w", restoreErr)
		}
		return cause
	}
	paths := []string{}
	for _, file := range p.files {
		paths = append(paths, file, file+".recovery")
	}
	if fileExists(exe + ".old") {
		paths = append(paths, exe+".old")
	}
	for _, from := range paths {
		to := filepath.Join(archive, filepath.Base(from))
		if err = os.Rename(from, to); err != nil {
			return rollback(err)
		}
		moves = append(moves, [2]string{from, to})
	}
	return nil
}
func (a *App) updateRecoveryRoutes(api func(string, string, apiFunc)) {
	api("GET", "/api/app/recovery", func(*http.Request) (any, error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		return inspectUpdateRecovery(a.dir, exe, recoveryProcessAlive)
	})
	api("POST", "/api/app/recovery", func(r *http.Request) (out any, resultErr error) {
		var req struct {
			Revision string `json:"revision"`
			Confirm  bool   `json:"confirm"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if !req.Confirm || req.Revision == "" {
			return nil, errors.New("Подтвердите завершение прерванной операции")
		}
		if !a.startupReady() {
			return nil, errors.New("Сначала дождитесь готовности текущей версии")
		}
		if !a.self.setBusy("recovery") {
			return nil, errors.New("Обновление ещё выполняется")
		}
		defer func() { a.self.finish(resultErr) }()
		a.life.mu.Lock()
		defer a.life.mu.Unlock()
		a.life.intentMu.Lock()
		defer a.life.intentMu.Unlock()
		active := a.life.updating || a.life.closing || a.transfer != nil
		if active {
			return nil, errors.New("Полностью закройте Naivka через трей и проверьте восстановление после обычного запуска")
		}
		updateFilesMu.Lock()
		defer updateFilesMu.Unlock()
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		if err = archiveInterruptedUpdate(a.dir, exe, req.Revision, recoveryProcessAlive); err != nil {
			return nil, err
		}
		a.mon.Event("ok", "Прерванная операция завершена. Текущая версия сохранена, файлы восстановления перенесены в version-backups", false)
		return map[string]bool{"ok": true}, nil
	})
}
