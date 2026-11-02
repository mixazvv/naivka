package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const updateHandshakeEnv = "NAIVKA_UPDATE_HANDSHAKE"

type updateIntent struct {
	Commit bool `json:"commit"`
	Resume bool `json:"resume"`
}

// Owned by the original app, guarded by life.intentMu. The pipe remains open
// until process exit; its EOF lets the supervisor distinguish exit from a hung API.
type updateTransfer struct {
	backup  string
	path    string
	gen     int64
	resume  bool
	aborted bool
	intent  updateIntent
	pipe    io.WriteCloser
}
type updateSupervisorConfig struct {
	Backup     string `json:"backup,omitempty"`
	Downgrade  bool   `json:"downgrade,omitempty"`
	Historical bool   `json:"historical,omitempty"`
	Exe        string `json:"exe"`
	Dir        string `json:"dir"`
	Intent     string `json:"intent"`
	Version    string `json:"version"`
	Token      string `json:"token"`
	Debug      bool   `json:"debug"`
}
type updateHandshake struct {
	URL    string `json:"url"`
	Token  string `json:"token"`
	Intent string `json:"intent"`
}
type updateAck struct {
	Resume bool `json:"resume"`
}

func writeUpdateIntent(path string, v updateIntent) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Atomic replacement also allows the candidate to cancel resume without a
	// reader mistaking a partially written record for permission to reconnect.
	if !fileExists(path) {
		return errors.New("UPDATE_INTENT_MISSING")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".update-intent-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	return os.Rename(tmp, path)
}
func (a *App) beginUpdateTransfer() (*updateTransfer, error) {
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	a.life.intentMu.Lock()
	if a.life.closing || a.life.updating {
		a.life.intentMu.Unlock()
		return nil, errors.New("APP_BUSY")
	}
	f, err := os.CreateTemp(a.dir, "update-intent-*.json")
	if err != nil {
		a.life.intentMu.Unlock()
		return nil, err
	}
	path := f.Name()
	f.Close()
	t := &updateTransfer{path: path, gen: a.life.gen.Load(), resume: a.life.want.Load()}
	if err = writeUpdateIntent(path, updateIntent{}); err != nil {
		os.Remove(path)
		a.life.intentMu.Unlock()
		return nil, err
	}
	a.transfer = t
	a.life.updating = true
	a.life.want.Store(false)
	a.life.intentMu.Unlock()
	a.core.Stop()
	return t, nil
}
func (a *App) cancelCandidateIntent(exit bool) {
	if a.candidate == nil {
		return
	}
	path := a.candidate.Intent
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var v updateIntent
	if json.Unmarshal(b, &v) != nil {
		return
	}
	v.Resume = false
	if exit {
		v.Commit = false
	}
	if writeUpdateIntent(path, v) != nil {
		_ = os.Remove(path)
	}
}
func (a *App) cancelUpdateResume() {
	a.life.intentMu.Lock()
	defer a.life.intentMu.Unlock()
	a.cancelCandidateIntent(false)
	if t := a.transfer; t != nil {
		t.resume = false
		t.intent.Resume = false
		if writeUpdateIntent(t.path, t.intent) != nil {
			_ = os.Remove(t.path)
		}
	}
}
func (a *App) cancelUpdateExit() {
	a.life.intentMu.Lock()
	defer a.life.intentMu.Unlock()
	a.cancelCandidateIntent(true)
	if t := a.transfer; t != nil {
		t.aborted = true
		t.resume = false
		t.intent = updateIntent{}
		if writeUpdateIntent(t.path, t.intent) != nil {
			_ = os.Remove(t.path)
		}
	}
}
func (a *App) flushUpdateIntent() {
	a.life.intentMu.Lock()
	defer a.life.intentMu.Unlock()
	if t := a.transfer; t != nil {
		// Quit itself increments gen once. Any additional increment is a manual
		// disconnect, even if its subsequent file write has not run yet.
		if a.life.closing && a.life.gen.Load() != t.gen+1 {
			t.intent.Resume = false
		}
		if writeUpdateIntent(t.path, t.intent) != nil {
			_ = os.Remove(t.path)
		}
	}
}
func (a *App) prepareUpdateExit(forUpdate bool) {
	a.life.intentMu.Lock()
	defer a.life.intentMu.Unlock()
	if t := a.transfer; t != nil {
		t.intent = updateIntent{Commit: forUpdate && !t.aborted, Resume: forUpdate && !t.aborted && t.resume && a.life.gen.Load() == t.gen && !a.life.closing}
		if writeUpdateIntent(t.path, t.intent) != nil {
			t.intent = updateIntent{}
			_ = os.Remove(t.path)
		}
	}
}
func (a *App) abortUpdateTransfer(t *updateTransfer) {
	a.life.intentMu.Lock()
	resume := a.transfer == t && t.resume && a.life.gen.Load() == t.gen && !a.life.closing
	if a.transfer == t {
		a.transfer = nil
		a.life.updating = false
		if resume {
			a.life.want.Store(true)
		}
	}
	a.life.intentMu.Unlock()
	if t.pipe != nil {
		t.pipe.Close()
	}
	os.Remove(t.path)
	if resume {
		go a.resume(t.gen)
	}
}

// This is a compensating file operation, not proof that a restarted app is healthy.
func rollbackUpdateExe(exe string) error {
	if !fileExists(exe + ".old") {
		return errors.New("UPDATE_BACKUP_MISSING")
	}
	failed := exe + ".failed"
	if fileExists(failed) {
		return errors.New("UPDATE_FAILED_FILE_EXISTS")
	}
	moved := false
	if fileExists(exe) {
		if err := os.Rename(exe, failed); err != nil {
			return fmt.Errorf("UPDATE_REMOVE_FAILED: %w", err)
		}
		moved = true
	}
	if err := os.Rename(exe+".old", exe); err != nil {
		if moved {
			_ = os.Rename(failed, exe)
		}
		return fmt.Errorf("UPDATE_RESTORE_FAILED: %w", err)
	}
	if moved {
		_ = os.Remove(failed)
	}
	return nil
}
func (a *App) launchUpdateSupervisor(exe, tag string, debug bool, t *updateTransfer) error {
	cmd := exec.Command(exe+".old", "--update-supervisor")
	cmd.Dir = filepath.Dir(exe)
	hideWindow(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	abort := func() { _ = cmd.Process.Kill(); <-done; input.Close() }
	cfg := updateSupervisorConfig{Exe: exe, Dir: a.dir, Intent: t.path, Version: tag, Token: randHex(24), Debug: debug, Backup: t.backup, Downgrade: versionNewer(appVersion, tag), Historical: versionNewer("v2.25.0", tag)}
	if err = json.NewEncoder(input).Encode(cfg); err != nil {
		abort()
		return err
	}
	ack := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ack <- strings.TrimSpace(line) }()
	select {
	case line := <-ack:
		if line != "READY" {
			abort()
			return errors.New("UPDATE_SUPERVISOR_NOT_READY")
		}
	case <-time.After(8 * time.Second):
		abort()
		return errors.New("UPDATE_SUPERVISOR_TIMEOUT")
	}
	a.life.intentMu.Lock()
	t.pipe = input
	a.life.intentMu.Unlock()
	// Do not close input: EOF must mean that the original process has exited.
	return nil
}

// Internal helper mode runs before normal bootstrap and never opens the UI or VPN.
func runUpdateSupervisor(input io.Reader, output io.Writer) error {
	dec := json.NewDecoder(io.LimitReader(input, 64<<10))
	var cfg updateSupervisorConfig
	if err := dec.Decode(&cfg); err != nil {
		return err
	}
	own, err := os.Executable()
	if err != nil {
		return err
	}
	if p, e := filepath.EvalSymlinks(own); e == nil {
		own = p
	}
	if !filepath.IsAbs(cfg.Exe) || !sameUpdateFile(own, cfg.Exe+".old") {
		return errors.New("UPDATE_INVALID_EXECUTABLE")
	}
	if !filepath.IsAbs(cfg.Dir) || !sameUpdateFile(filepath.Dir(cfg.Intent), cfg.Dir) {
		return errors.New("UPDATE_INVALID_INTENT_DIRECTORY")
	}
	if !strings.HasPrefix(filepath.Base(cfg.Intent), "update-intent-") || len(cfg.Token) != 48 || parseVer(cfg.Version) == nil {
		return errors.New("UPDATE_INVALID_HANDOFF")
	}
	if cfg.Backup != "" && (!filepath.IsAbs(cfg.Backup) || !sameUpdateFile(filepath.Dir(cfg.Backup), filepath.Join(cfg.Dir, "version-backups"))) {
		return errors.New("UPDATE_INVALID_BACKUP")
	}
	defer os.Remove(cfg.Exe + ".debug.new")
	if _, err = io.WriteString(output, "READY\n"); err != nil {
		return err
	}
	eof := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, input); close(eof) }()
	select {
	case <-eof:
	case <-time.After(2 * time.Minute):
		return errors.New("UPDATE_PARENT_TIMEOUT")
	}
	removeIntent := true
	defer func() {
		if removeIntent {
			os.Remove(cfg.Intent)
		}
	}()
	var intent updateIntent
	b, err := os.ReadFile(cfg.Intent)
	if err != nil || json.Unmarshal(b, &intent) != nil || !intent.Commit {
		_ = os.Remove(cfg.Exe + ".debug.new")
		return rollbackUpdateExe(cfg.Exe)
	}
	runner := updateRunner{timeout: 60 * time.Second, stable: time.Second}
	if cfg.Downgrade {
		intent.Resume = false
		_ = writeUpdateIntent(cfg.Intent, intent)
	}
	code := ""
	if err = prepareVersionSettings(cfg); err != nil {
		code = runner.rollback(cfg, intent, "UPDATE_SETTINGS_FAILED")
	} else if cfg.Historical {
		code = runner.runHistorical(cfg, intent)
	} else {
		code = runner.run(cfg, intent)
	}
	if code == "UPDATE_COMMIT_UNKNOWN" || code == "UPDATE_CHILD_STOP_FAILED" || code == "UPDATE_ROLLBACK_FAILED" {
		removeIntent = false
	}
	return nil
}

type updateRunner struct {
	timeout, stable time.Duration
	command         func(string, []string, []string) *exec.Cmd
}

func (r updateRunner) newCommand(exe string, args, env []string) *exec.Cmd {
	if r.command != nil {
		return r.command(exe, args, env)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Dir = filepath.Dir(exe)
	hideWindow(cmd)
	return cmd
}
func (r updateRunner) run(cfg updateSupervisorConfig, intent updateIntent) (result string) {
	defer func() {
		if !strings.HasSuffix(result, "_ROLLED_BACK") {
			recordUpdateResult(cfg.Dir, result)
		}
	}()
	// A single private callback binds readiness to the launched PID, version and
	// random token. The response is withheld until a short stability window passes.
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return r.rollback(cfg, intent, "UPDATE_LISTEN_FAILED")
	}
	var pid atomic.Int64
	pidReady := make(chan struct{})
	ready := make(chan struct{}, 1)
	commit := make(chan struct{})
	flushed := make(chan struct{}, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method != "POST" || q.URL.Path != "/ready" || subtle.ConstantTimeCompare([]byte(q.Header.Get("X-Naivka-Update")), []byte(cfg.Token)) != 1 {
			http.Error(w, "forbidden", 403)
			return
		}
		var v struct {
			PID     int    `json:"pid"`
			Version string `json:"version"`
		}
		select {
		case <-pidReady:
		case <-ctx.Done():
			http.Error(w, "not ready", 409)
			return
		}
		if json.NewDecoder(io.LimitReader(q.Body, 1024)).Decode(&v) != nil || int64(v.PID) != pid.Load() || v.PID <= 0 || v.Version != cfg.Version {
			http.Error(w, "invalid readiness", 400)
			return
		}
		select {
		case ready <- struct{}{}:
		default:
		}
		select {
		case <-commit:
			latest := updateIntent{}
			if b, err := os.ReadFile(cfg.Intent); err == nil {
				_ = json.Unmarshal(b, &latest)
			}
			if !latest.Commit {
				http.Error(w, "cancelled", 409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(updateAck{Resume: latest.Resume})
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case flushed <- struct{}{}:
			default:
			}
		case <-ctx.Done():
			http.Error(w, "not committed", 409)
		}
	})}
	go server.Serve(ln)
	defer server.Close()
	hand, _ := json.Marshal(updateHandshake{URL: "http://" + ln.Addr().String() + "/ready", Token: cfg.Token, Intent: cfg.Intent})
	env := withoutUpdateHandshake(os.Environ())
	env = append(env, updateHandshakeEnv+"="+string(hand))
	cmd := r.newCommand(cfg.Exe, []string{"--replace", "--no-elevate", "--update-candidate"}, env)
	if err = cmd.Start(); err != nil {
		return r.rollback(cfg, intent, "UPDATE_START_FAILED")
	}
	pid.Store(int64(cmd.Process.Pid))
	close(pidReady)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	failure := ""
	select {
	case <-ready:
	case <-done:
		return r.rollback(cfg, intent, "UPDATE_EXIT_BEFORE_READY")
	case <-ctx.Done():
		failure = "UPDATE_READY_TIMEOUT"
	}
	if failure == "" {
		timer := time.NewTimer(r.stable)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-done:
			return r.rollback(cfg, intent, "UPDATE_EXIT_BEFORE_COMMIT")
		case <-ctx.Done():
			failure = "UPDATE_READY_TIMEOUT"
		}
	}
	if failure != "" {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return "UPDATE_CHILD_STOP_FAILED"
		}
		return r.rollback(cfg, intent, failure)
	}
	var finalIntent updateIntent
	if b, e := os.ReadFile(cfg.Intent); e != nil || json.Unmarshal(b, &finalIntent) != nil || !finalIntent.Commit {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return "UPDATE_CHILD_STOP_FAILED"
		}
		return r.rollback(cfg, intent, "UPDATE_USER_EXIT")
	}
	close(commit)
	select {
	case <-flushed:
	case <-done:
		return "UPDATE_COMMIT_UNKNOWN"
	case <-ctx.Done():
		return "UPDATE_COMMIT_UNKNOWN"
	}
	if cfg.Debug {
		_ = os.Rename(cfg.Exe+".debug.new", filepath.Join(filepath.Dir(cfg.Exe), "Naivka-debug.exe"))
	}
	// No rollback after authorizing the commit response: the candidate may now own the
	// system proxy. Backups are cleaned only by that committed copy.
	return "UPDATED"
}
func (r updateRunner) rollback(cfg updateSupervisorConfig, intent updateIntent, cause string) string {
	b, err := os.ReadFile(cfg.Intent)
	var latest updateIntent
	if err != nil || json.Unmarshal(b, &latest) != nil {
		latest = updateIntent{}
	}
	intent = latest
	_ = os.Remove(cfg.Exe + ".debug.new")
	if cfg.Backup != "" && !fileExists(cfg.Exe+".old") {
		if b, e := os.ReadFile(filepath.Join(cfg.Backup, "Naivka.exe")); e == nil {
			_ = os.WriteFile(cfg.Exe+".old", b, 0755)
		}
	}
	if err := rollbackUpdateExe(cfg.Exe); err != nil {
		recordUpdateResult(cfg.Dir, "UPDATE_ROLLBACK_FAILED")
		return "UPDATE_ROLLBACK_FAILED"
	}
	if err := restoreVersionSettings(cfg); err != nil {
		return "UPDATE_SETTINGS_ROLLBACK_FAILED"
	}
	if !intent.Commit {
		recordUpdateResult(cfg.Dir, "UPDATE_USER_EXIT_ROLLED_BACK")
		return "UPDATE_USER_EXIT_ROLLED_BACK"
	}
	recordUpdateResult(cfg.Dir, cause+"_ROLLED_BACK")
	args := []string{"--replace", "--no-elevate", "--update-rollback"}
	if intent.Resume {
		args = append(args, "--connect")
	}
	cmd := r.newCommand(cfg.Exe, args, withoutUpdateHandshake(os.Environ()))
	if err := cmd.Start(); err != nil {
		return "UPDATE_ROLLBACK_START_FAILED"
	}
	go cmd.Wait()
	return cause + "_ROLLED_BACK"
}
func withoutUpdateHandshake(env []string) []string {
	out := make([]string, 0, len(env))
	for _, v := range env {
		if !strings.HasPrefix(strings.ToUpper(v), updateHandshakeEnv+"=") {
			out = append(out, v)
		}
	}
	return out
}
func readUpdateHandshake() (updateHandshake, error) {
	raw := os.Getenv(updateHandshakeEnv)
	_ = os.Unsetenv(updateHandshakeEnv)
	var h updateHandshake
	if json.Unmarshal([]byte(raw), &h) != nil {
		return h, errors.New("UPDATE_HANDSHAKE_MISSING")
	}
	u, err := url.Parse(h.URL)
	if !filepath.IsAbs(h.Intent) || !strings.HasPrefix(filepath.Base(h.Intent), "update-intent-") {
		return h, errors.New("UPDATE_HANDSHAKE_INVALID")
	}
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "/ready" || u.RawQuery != "" || u.Fragment != "" || len(h.Token) != 48 {
		return h, errors.New("UPDATE_HANDSHAKE_INVALID")
	}
	return h, nil
}
func reportUpdateReady(h updateHandshake) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "version": appVersion})
	q, err := http.NewRequestWithContext(ctx, "POST", h.URL, strings.NewReader(string(b)))
	if err != nil {
		return false, err
	}
	q.Header.Set("X-Naivka-Update", h.Token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(q)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, errors.New("UPDATE_NOT_COMMITTED")
	}
	var ack updateAck
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&ack); err != nil {
		return false, err
	}
	return ack.Resume, nil
}
func (a *App) resumeAfterUpdate(want bool, gen int64) {
	a.life.intentMu.Lock()
	a.life.updating = false
	if !want || a.life.gen.Load() != gen || a.life.closing {
		a.life.intentMu.Unlock()
		return
	}
	a.life.want.Store(true)
	a.life.intentMu.Unlock()
	go a.resume(gen)
}

func recordUpdateResult(dir, code string) {
	b, _ := json.Marshal(map[string]string{"code": code})
	_ = os.WriteFile(filepath.Join(dir, "update-result.json"), b, 0o600)
}
func takeUpdateResult(dir string) string {
	path := filepath.Join(dir, "update-result.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = os.Remove(path)
	var v struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	switch v.Code {
	case "UPDATE_USER_EXIT_ROLLED_BACK", "UPDATED", "UPDATE_LISTEN_FAILED_ROLLED_BACK", "UPDATE_START_FAILED_ROLLED_BACK", "UPDATE_EXIT_BEFORE_READY_ROLLED_BACK", "UPDATE_EXIT_BEFORE_COMMIT_ROLLED_BACK", "UPDATE_READY_TIMEOUT_ROLLED_BACK", "UPDATE_CHILD_STOP_FAILED", "UPDATE_ROLLBACK_FAILED", "UPDATE_ROLLBACK_START_FAILED", "UPDATE_COMMIT_UNKNOWN":
		return v.Code
	}
	return ""
}

func updateResultMessage(code string) string {
	switch code {
	case "UPDATE_USER_EXIT_ROLLED_BACK":
		return "Обновление отменено. Восстановлена прежняя версия."
	case "UPDATE_ROLLBACK_START_FAILED":
		return "Прежняя версия восстановлена, но её не удалось запустить автоматически."
	case "UPDATE_ROLLBACK_FAILED":
		return "Откат обновления не завершён. Закройте Naivka и восстановите программу из проверенного архива релиза."
	case "UPDATE_CHILD_STOP_FAILED":
		return "Не удалось остановить новую копию. Закройте Naivka перед ручным обновлением."
	case "UPDATE_COMMIT_UNKNOWN":
		return "Не удалось подтвердить завершение обновления. Проверьте текущую версию перед повторной установкой."
	}
	return "Новая версия не подтвердила запуск. Восстановлена прежняя версия."
}

func (a *App) startupReady() bool {
	if a.ui == nil || !pingInstance(instanceInfo{Port: a.port, Token: a.token}) {
		return false
	}
	if host, ok := a.ui.(interface{ Ready() bool }); ok {
		return host.Ready()
	}
	return true
}

// Windows can spell the same path with an 8.3 name or a long directory name.
// File identity also handles symlink aliases without accepting unrelated targets.
func sameUpdateFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
