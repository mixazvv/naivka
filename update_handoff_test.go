package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Real subprocess, but no app/VPN startup. This exercises PID-bound IPC and exit
// races independently of Windows GUI, registry and file-lock integration tests.
func TestUpdateChildProcess(t *testing.T) {
	mode := os.Getenv("NAIVKA_TEST_UPDATE_MODE")
	if mode == "" {
		return
	}
	if mode == "exit" {
		os.Exit(2)
	}
	if mode == "hang" || mode == "rollback" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	h, err := readUpdateHandshake()
	if err != nil {
		os.Exit(3)
	}
	if mode == "disconnect" {
		_ = writeUpdateIntent(h.Intent, updateIntent{Commit: true, Resume: false})
	}
	if mode == "user-exit" {
		_ = writeUpdateIntent(h.Intent, updateIntent{})
		os.Exit(7)
	}
	if mode == "late-disconnect" || mode == "late-exit" {
		go func() {
			time.Sleep(40 * time.Millisecond)
			_ = writeUpdateIntent(h.Intent, updateIntent{Commit: mode != "late-exit", Resume: false})
			if mode == "late-exit" {
				os.Exit(8)
			}
		}()
	}
	version := "v9.9.9"
	pid := os.Getpid()
	token := h.Token
	switch mode {
	case "wrong-version":
		version = "v0.0.0"
	case "wrong-pid":
		pid++
	case "wrong-token":
		token = "nope"
	}
	b, _ := json.Marshal(map[string]any{"pid": pid, "version": version})
	if mode == "ready-then-exit" {
		go func() { time.Sleep(40 * time.Millisecond); os.Exit(5) }()
	}
	req, _ := http.NewRequest("POST", h.URL, bytes.NewReader(b))
	req.Header.Set("X-Naivka-Update", token)
	client := http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		result, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			writeTestAck(os.Getenv("NAIVKA_TEST_UPDATE_ACK"), result)
		}
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestUpdateRunnerProcesses(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		resume     bool
	}{
		{"success", "UPDATED", true}, {"success", "UPDATED", false},
		{"exit", "UPDATE_EXIT_BEFORE_READY_ROLLED_BACK", true},
		{"hang", "UPDATE_READY_TIMEOUT_ROLLED_BACK", false},
		{"wrong-version", "UPDATE_READY_TIMEOUT_ROLLED_BACK", true},
		{"wrong-pid", "UPDATE_READY_TIMEOUT_ROLLED_BACK", false},
		{"wrong-token", "UPDATE_READY_TIMEOUT_ROLLED_BACK", false},
		{"ready-then-exit", "UPDATE_EXIT_BEFORE_COMMIT_ROLLED_BACK", true},
		{"start-error", "UPDATE_START_FAILED_ROLLED_BACK", false},
		{"disconnect", "UPDATED", true}, {"late-disconnect", "UPDATED", true},
		{"user-exit", "UPDATE_USER_EXIT_ROLLED_BACK", true}, {"late-exit", "UPDATE_USER_EXIT_ROLLED_BACK", true},
	} {
		t.Run(tc.mode+map[bool]string{true: "-resume", false: "-offline"}[tc.resume], func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "Naivka.exe")
			os.WriteFile(exe, []byte("new"), 0o755)
			os.WriteFile(exe+".old", []byte("old"), 0o755)
			f, _ := os.CreateTemp(dir, "update-intent-*.json")
			f.Close()
			writeUpdateIntent(f.Name(), updateIntent{Commit: true, Resume: tc.resume})
			cfg := updateSupervisorConfig{Exe: exe, Dir: dir, Intent: f.Name(), Version: "v9.9.9", Token: strings.Repeat("a", 48)}
			ack := filepath.Join(dir, "ack.json")
			var mu sync.Mutex
			var children []*exec.Cmd
			var rollbackArgs []string
			runner := updateRunner{timeout: 1500 * time.Millisecond, stable: 150 * time.Millisecond, command: func(target string, args, env []string) *exec.Cmd {
				mode := tc.mode
				for _, a := range args {
					if a == "--update-rollback" {
						mode = "rollback"
						rollbackArgs = append([]string{}, args...)
					}
				}
				var cmd *exec.Cmd
				if mode == "start-error" {
					cmd = exec.Command(filepath.Join(dir, "does-not-exist"))
				} else {
					cmd = exec.Command(os.Args[0], "-test.run=^TestUpdateChildProcess$")
				}
				cmd.Env = append(env, "NAIVKA_TEST_UPDATE_MODE="+mode, "NAIVKA_TEST_UPDATE_ACK="+ack)
				mu.Lock()
				children = append(children, cmd)
				mu.Unlock()
				return cmd
			}}
			defer func() {
				mu.Lock()
				defer mu.Unlock()
				for _, c := range children {
					if c.Process != nil {
						_ = c.Process.Kill()
					}
				}
			}()
			got := runner.run(cfg, updateIntent{Commit: true, Resume: tc.resume})
			if got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
			body, _ := os.ReadFile(exe)
			if got == "UPDATED" {
				if string(body) != "new" || !fileExists(exe+".old") {
					t.Fatal("backup deleted before committed copy cleanup")
				}
				deadline := time.Now().Add(time.Second)
				for !fileExists(ack) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				b, _ := os.ReadFile(ack)
				var v updateAck
				if json.Unmarshal(b, &v) != nil || v.Resume != (tc.resume && !strings.Contains(tc.mode, "disconnect")) {
					t.Fatalf("wrong resume decision: %s", b)
				}
			} else {
				if string(body) != "old" {
					t.Fatal("old exe not restored")
				}
				connected := false
				for _, a := range rollbackArgs {
					if a == "--connect" {
						connected = true
					}
				}
				if connected != (tc.resume && tc.want != "UPDATE_USER_EXIT_ROLLED_BACK") {
					t.Fatal("rollback changed connection intent", rollbackArgs)
				}
				if tc.want == "UPDATE_USER_EXIT_ROLLED_BACK" && len(rollbackArgs) > 0 {
					t.Fatal("manual exit restarted old UI")
				}
			}
		})
	}
}
func TestUpdateIntentRespectsManualActions(t *testing.T) {
	for _, kind := range []string{"none", "disconnect", "exit", "late-disconnect", "late-exit"} {
		t.Run(kind, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "update-intent-*.json")
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			a := &App{transfer: &updateTransfer{path: f.Name(), resume: true}}
			if kind == "disconnect" {
				a.life.stopIntent(false)
				a.cancelUpdateResume()
			}
			if kind == "exit" {
				a.cancelUpdateExit()
			}
			a.prepareUpdateExit(true)
			if kind == "late-disconnect" {
				a.cancelUpdateResume()
			}
			if kind == "late-exit" {
				a.cancelUpdateExit()
			}
			a.flushUpdateIntent()
			b, _ := os.ReadFile(f.Name())
			var v updateIntent
			if json.Unmarshal(b, &v) != nil {
				t.Fatal("invalid intent")
			}
			if v.Resume != (kind == "none") {
				t.Fatal("manual action ignored", kind, v)
			}
			if v.Commit != (!strings.Contains(kind, "exit")) {
				t.Fatal("manual exit ignored", kind, v)
			}
		})
	}
}
func TestUpdateRollbackFailureKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app")
	os.WriteFile(exe, []byte("new"), 0o755)
	os.WriteFile(exe+".old", []byte("old"), 0o755)
	os.WriteFile(exe+".failed", []byte("unrelated"), 0o600)
	if err := rollbackUpdateExe(exe); err == nil {
		t.Fatal("clobbered existing failed path")
	}
	b, _ := os.ReadFile(exe + ".old")
	if string(b) != "old" {
		t.Fatal("backup lost")
	}
	if err := writeExe(exe, []byte("another")); err == nil {
		t.Fatal("overwrote pending backup")
	}
}
func TestUpdateHandshakeValidationAndEnvironment(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1:123/ready", "http://example.com:123/ready", "http://127.0.0.1:123/ready?token=secret", "http://127.0.0.1/ready"} {
		b, _ := json.Marshal(updateHandshake{URL: address, Token: strings.Repeat("a", 48), Intent: filepath.Join(t.TempDir(), "update-intent-test.json")})
		t.Setenv(updateHandshakeEnv, string(b))
		if _, err := readUpdateHandshake(); err == nil {
			t.Fatal("accepted", address)
		}
		if os.Getenv(updateHandshakeEnv) != "" {
			t.Fatal("handshake inherited by children")
		}
	}
	env := withoutUpdateHandshake([]string{"A=1", updateHandshakeEnv + "=secret", "B=2"})
	if strings.Contains(strings.Join(env, " "), "secret") {
		t.Fatal(env)
	}
}
func TestUpdateResumeRejectsStaleGeneration(t *testing.T) {
	a := &App{}
	a.life.gen.Store(2)
	a.life.updating = true
	a.resumeAfterUpdate(true, 1)
	if a.life.want.Load() || a.life.updating {
		t.Fatal("stale connection resumed")
	}
	a.life.updating = true
	if _, err := a.life.requestConnect(); err == nil {
		t.Fatal("connect accepted during transfer")
	}
	if _, _, err := a.life.operation(2, false); err == nil {
		t.Fatal("zapret accepted during transfer")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("NAIVKA_TEST_ACTUAL_UPDATE") == "1" {
		for _, arg := range os.Args[1:] {
			if arg == "--update-supervisor" {
				if runUpdateSupervisor(os.Stdin, os.Stdout) != nil {
					os.Exit(2)
				}
				os.Exit(0)
			}
			if arg == "--update-candidate" {
				h, err := readUpdateHandshake()
				if err != nil {
					os.Exit(3)
				}
				appVersion = "v9.9.9"
				resume, err := reportUpdateReady(h)
				if err != nil {
					os.Exit(4)
				}
				b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "resume": resume})
				writeTestAck(os.Getenv("NAIVKA_TEST_UPDATE_ACK"), b)
				time.Sleep(time.Minute)
				os.Exit(0)
			}
			if arg == "--update-rollback" {
				os.Exit(0)
			}
		}
	}
	os.Exit(m.Run())
}
func TestSupervisorWaitsForParentPipe(t *testing.T) {
	body, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{true, false} {
		t.Run(map[bool]string{true: "commit", false: "user-exit"}[commit], func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "Naivka.exe")
			backup := exe + ".old"
			if err := os.WriteFile(backup, body, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, body, 0o755); err != nil {
				t.Fatal(err)
			}
			file, err := os.CreateTemp(dir, "update-intent-*.json")
			if err != nil {
				t.Fatal(err)
			}
			file.Close()
			if err := writeUpdateIntent(file.Name(), updateIntent{}); err != nil {
				t.Fatal(err)
			}
			ack := filepath.Join(dir, "ack.json")
			cmd := exec.Command(backup, "--update-supervisor")
			cmd.Env = append(os.Environ(), "NAIVKA_TEST_ACTUAL_UPDATE=1", "NAIVKA_TEST_UPDATE_ACK="+ack)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			if err = json.NewEncoder(input).Encode(updateSupervisorConfig{Exe: exe, Dir: dir, Intent: file.Name(), Version: "v9.9.9", Token: strings.Repeat("a", 48)}); err != nil {
				t.Fatal(err)
			}
			line, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "READY" {
				t.Fatal(line, err)
			}
			time.Sleep(50 * time.Millisecond)
			if fileExists(ack) {
				t.Fatal("candidate started before parent exit")
			}
			if err = writeUpdateIntent(file.Name(), updateIntent{Commit: commit, Resume: false}); err != nil {
				t.Fatal(err)
			}
			input.Close()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("supervisor stuck after parent exit")
			}
			if commit {
				deadline := time.Now().Add(time.Second)
				for !fileExists(ack) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				b, err := os.ReadFile(ack)
				if err != nil {
					t.Fatal(err)
				}
				var v struct {
					PID    int
					Resume bool
				}
				if err = json.Unmarshal(b, &v); err != nil {
					t.Fatal(err)
				}
				if v.PID <= 0 {
					t.Fatal("invalid child pid")
				}
				if proc, err := os.FindProcess(v.PID); err == nil {
					proc.Kill()
					proc.Release()
				}
				if v.Resume {
					t.Fatal("offline update auto-connected")
				}
			} else if fileExists(backup) {
				t.Fatal("backup not restored on user exit")
			}
		})
	}
}

func TestFinalUpdateIntentSeesDisconnectBeforeFileWrite(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "update-intent-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	a := &App{transfer: &updateTransfer{path: f.Name(), resume: true}}
	a.prepareUpdateExit(true)
	a.life.stopIntent(true)  // update shutdown
	a.life.stopIntent(false) // manual disconnect won, file callback still pending
	a.flushUpdateIntent()
	b, _ := os.ReadFile(f.Name())
	var v updateIntent
	json.Unmarshal(b, &v)
	if v.Resume {
		t.Fatal("late manual disconnect overwritten")
	}
}
func TestUpdateResultContainsOnlyKnownCodes(t *testing.T) {
	dir := t.TempDir()
	recordUpdateResult(dir, "secret token: do-not-show")
	if got := takeUpdateResult(dir); got != "" {
		t.Fatal(got)
	}
	recordUpdateResult(dir, "UPDATE_START_FAILED_ROLLED_BACK")
	if got := takeUpdateResult(dir); got != "UPDATE_START_FAILED_ROLLED_BACK" {
		t.Fatal(got)
	}
	if got := takeUpdateResult(dir); got != "" {
		t.Fatal("reported twice")
	}
}

func writeTestAck(path string, b []byte) {
	if os.WriteFile(path+".tmp", b, 0o600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func TestUpdateRollbackRestartFailure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app.exe")
	os.WriteFile(exe, []byte("new"), 0o755)
	os.WriteFile(exe+".old", []byte("old"), 0o755)
	f, _ := os.CreateTemp(dir, "update-intent-*.json")
	f.Close()
	writeUpdateIntent(f.Name(), updateIntent{Commit: true})
	runner := updateRunner{timeout: time.Second, stable: time.Millisecond, command: func(string, []string, []string) *exec.Cmd { return exec.Command(filepath.Join(dir, "missing")) }}
	if code := runner.run(updateSupervisorConfig{Exe: exe, Dir: dir, Intent: f.Name()}, updateIntent{Commit: true}); code != "UPDATE_ROLLBACK_START_FAILED" {
		t.Fatal(code)
	}
	b, _ := os.ReadFile(exe)
	if string(b) != "old" {
		t.Fatal("working exe not restored despite launch failure")
	}
}
