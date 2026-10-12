//go:build !legacy

package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Установка своего сервера по SSH («Серверы» → «Свой сервер»).
//
// Безопасность:
//   - пароль и ключ SSH нигде не сохраняются: живут в памяти только на время установки;
//   - сначала Naivka узнаёт ключ сервера, НЕ отправляя пароль, и показывает его отпечаток —
//     человек сверяет его с панелью хостинга; потом подключение идёт только к серверу с этим ключом
//     (иначе пароль от root можно было бы отдать подставному серверу);
//   - на сервер уходит server/install-server.sh из самой Naivka — его можно прочитать заранее.

//go:embed server/install-server.sh
var installScript string

const serverInstallSupported = true

type serverInstallReq struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Password    string `json:"password"`
	Key         string `json:"key"`        // закрытый ключ OpenSSH/PEM
	Passphrase  string `json:"passphrase"` // пароль ключа
	Fingerprint string `json:"fingerprint"`
	SNI         string `json:"sni"`
	Hy2         bool   `json:"hy2"`
	BBR         bool   `json:"bbr"`
	Name        string `json:"name"`
	Confirm     bool   `json:"confirm"` // человек прочитал предупреждение
}

// ServerInstallView — ход установки для интерфейса.
type ServerInstallView struct {
	Active bool     `json:"active"`
	Done   bool     `json:"done"`
	Error  string   `json:"error"`
	Log    []string `json:"log"`
	Added  []string `json:"added"` // имена добавленных серверов
}

type serverInstaller struct {
	mu    sync.Mutex
	state ServerInstallView
}

var hostRe = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$`)
var ansiRe2 = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (r *serverInstallReq) check(needAuth bool) error {
	r.Host = strings.TrimSpace(strings.Trim(strings.TrimSpace(r.Host), "[]"))
	if net.ParseIP(r.Host) == nil && !hostRe.MatchString(r.Host) {
		return errors.New("Укажите IP или домен сервера")
	}
	if r.Port == 0 {
		r.Port = 22
	}
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("Неверный порт SSH")
	}
	if !needAuth {
		return nil
	}
	r.User = strings.TrimSpace(r.User)
	if r.User == "" {
		r.User = "root"
	}
	if !regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`).MatchString(r.User) {
		return errors.New("Неверное имя пользователя SSH")
	}
	if r.Password == "" && strings.TrimSpace(r.Key) == "" {
		return errors.New("Нужен пароль или ключ SSH")
	}
	if !strings.HasPrefix(r.Fingerprint, "SHA256:") {
		return errors.New("Сначала проверьте ключ сервера")
	}
	if !r.Confirm {
		return errors.New("Подтвердите, что понимаете, что делаете")
	}
	r.SNI = strings.ToLower(strings.TrimSpace(r.SNI))
	if !hostRe.MatchString(r.SNI) {
		return errors.New("Укажите сайт для маскировки Reality, например www.example.com")
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		r.Name = r.Host
	}
	if len([]rune(r.Name)) > 40 || strings.ContainsAny(r.Name, "\n\r\x00") {
		return errors.New("Название — до 40 символов")
	}
	return nil
}

// hostKeyAlgos — какой ключ сервера спрашивать: ed25519 первым, его отпечаток проще всего сверить
// (ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub). Порядок одинаковый при проверке и при установке,
// иначе сервер мог бы показать другой ключ и отпечаток «не совпал» бы.
var hostKeyAlgos = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}

// hostKey узнаёт ключ сервера, не отправляя ни пароля, ни ключа: соединение рвётся сразу после обмена ключами.
func hostKey(host string, port int) (fingerprint, keyType string, err error) {
	var got ssh.PublicKey
	errGot := errors.New("ключ получен")
	cfg := &ssh.ClientConfig{
		User: "naivka-probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errGot
		},
		HostKeyAlgorithms: hostKeyAlgos,
		Timeout:           15 * time.Second,
	}
	_, err = ssh.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)), cfg)
	if got == nil {
		if err == nil {
			err = errors.New("сервер не прислал ключ")
		}
		return "", "", fmt.Errorf("не удалось подключиться по SSH: %s", humanErr(err))
	}
	return ssh.FingerprintSHA256(got), got.Type(), nil
}

// shellQuote — значение для командной строки sh в одинарных кавычках.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (si *serverInstaller) view() ServerInstallView {
	si.mu.Lock()
	defer si.mu.Unlock()
	v := si.state
	v.Log = append([]string(nil), si.state.Log...)
	v.Added = append([]string(nil), si.state.Added...)
	return v
}

func (si *serverInstaller) logf(format string, args ...any) {
	si.mu.Lock()
	si.state.Log = append(si.state.Log, fmt.Sprintf(format, args...))
	if len(si.state.Log) > 2000 {
		si.state.Log = si.state.Log[len(si.state.Log)-2000:]
	}
	si.mu.Unlock()
}

func (a *App) startServerInstall(req serverInstallReq) error {
	if err := req.check(true); err != nil {
		return err
	}
	si := a.installer
	si.mu.Lock()
	if si.state.Active {
		si.mu.Unlock()
		return errors.New("Установка уже идёт")
	}
	si.state = ServerInstallView{Active: true}
	si.mu.Unlock()
	go func() {
		added, err := a.runServerInstall(req)
		si.mu.Lock()
		si.state.Active, si.state.Done, si.state.Added = false, true, added
		if err != nil {
			si.state.Error = err.Error()
		}
		si.mu.Unlock()
		if err != nil {
			a.mon.Event("error", "Установка сервера "+req.Host+": "+firstLine(err.Error()), false)
		} else {
			a.mon.Event("ok", fmt.Sprintf("Сервер %s установлен, добавлено: %s", req.Host, strings.Join(added, ", ")), true)
		}
	}()
	return nil
}

func (a *App) runServerInstall(req serverInstallReq) ([]string, error) {
	si := a.installer
	var auth []ssh.AuthMethod
	if k := strings.TrimSpace(req.Key); k != "" {
		var signer ssh.Signer
		var err error
		if req.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(k), []byte(req.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(k))
		}
		if err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, errors.New("Ключ SSH защищён паролем — укажите его")
			}
			return nil, fmt.Errorf("Ключ SSH не читается: %v", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if req.Password != "" {
		pw := req.Password
		auth = append(auth, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range qs {
				ans[i] = pw
			}
			return ans, nil
		}))
	}
	var keyErr error
	cfg := &ssh.ClientConfig{
		User: req.User,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if ssh.FingerprintSHA256(key) != req.Fingerprint {
				keyErr = fmt.Errorf("Ключ сервера не тот, что вы подтвердили (%s вместо %s). Пароль не отправлен, установка прервана: возможно, это не ваш сервер", ssh.FingerprintSHA256(key), req.Fingerprint)
				return keyErr
			}
			return nil
		},
		HostKeyAlgorithms: hostKeyAlgos,
		Timeout:           20 * time.Second,
	}
	addr := net.JoinHostPort(req.Host, strconv.Itoa(req.Port))
	si.logf("Подключаюсь к %s как %s…", addr, req.User)
	cl, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		if keyErr != nil {
			return nil, keyErr
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errors.New("Сервер не принял пароль или ключ")
		}
		return nil, fmt.Errorf("SSH: %s", humanErr(err))
	}
	defer cl.Close()
	sess, err := cl.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	env := map[string]string{
		"SERVER_ADDR": req.Host, "REALITY_SNI": req.SNI, "NAME": req.Name,
		"HY2": map[bool]string{true: "1", false: "0"}[req.Hy2], "BBR": map[bool]string{true: "1", false: "0"}[req.BBR],
	}
	for k, v := range installTestEnv {
		env[k] = v
	}
	var parts []string
	for _, k := range []string{"SERVER_ADDR", "REALITY_SNI", "NAME", "HY2", "BBR", "NAIVKA_TEST", "CONF_DIR", "NAIVKA_SELFTEST_URL"} {
		if v, ok := env[k]; ok {
			parts = append(parts, k+"="+shellQuote(v))
		}
	}
	cmd := "env " + strings.Join(parts, " ") + " bash -s"
	if req.User != "root" {
		cmd = "sudo -n " + cmd // sudo с паролем по SSH без терминала не спросить — нужен NOPASSWD
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	sess.Stdout, sess.Stderr = pw, pw
	var links []string
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := ansiRe2.ReplaceAllString(sc.Text(), "")
			if l, ok := strings.CutPrefix(line, "NAIVKA-LINK "); ok {
				links = append(links, strings.TrimSpace(l))
				continue // ссылки с ключами в журнал не пишем
			}
			si.logf("%s", line)
		}
	}()
	if err := sess.Start(cmd); err != nil {
		pw.Close()
		return nil, err
	}
	_, _ = io.WriteString(stdin, installScript)
	stdin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	waitErr := make(chan error, 1)
	go func() { waitErr <- sess.Wait() }()
	select {
	case err = <-waitErr:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGTERM)
		err = errors.New("установка идёт дольше 20 минут — прервана")
	}
	pw.Close()
	<-readDone
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) && req.User != "root" && ee.ExitStatus() == 1 && len(links) == 0 && sudoFailed(si) {
			return nil, errors.New("sudo требует пароль. Войдите как root или разрешите этому пользователю sudo без пароля")
		}
		return nil, errors.New("скрипт установки завершился с ошибкой — подробности в журнале выше")
	}
	if len(links) == 0 {
		return nil, errors.New("скрипт не выдал ссылок на серверы")
	}

	servers, errs := ParseShareLinks(strings.Join(links, "\n"))
	if len(errs) > 0 {
		return nil, fmt.Errorf("ссылки от сервера не разобрались: %s", strings.Join(errs, "; "))
	}
	return a.addInstalledServers(servers)
}

func sudoFailed(si *serverInstaller) bool {
	v := si.view()
	for _, l := range v.Log {
		if strings.Contains(l, "sudo") && (strings.Contains(l, "password") || strings.Contains(l, "пароль")) {
			return true
		}
	}
	return false
}

// addInstalledServers добавляет серверы; повторная установка на тот же адрес и порт заменяет прежние.
func (a *App) addInstalledServers(servers []Server) ([]string, error) {
	a.mu.Lock()
	ns := a.settings.clone()
	var names []string
	for _, sv := range servers {
		replaced := false
		for i, old := range ns.Servers {
			if old.Type == sv.Type && old.Host == sv.Host && old.Port == sv.Port {
				sv.ID = old.ID
				ns.Servers[i] = sv
				replaced = true
			}
		}
		if !replaced {
			ns.Servers = append(ns.Servers, sv)
		}
		names = append(names, sv.Name)
	}
	ns.normalize()
	if err := ns.Validate(); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	a.settings = ns
	err := SaveSettings(a.dir, ns)
	a.mu.Unlock()
	return names, err
}

// installTestEnv — проверка установки без настоящего VPS: NAIVKA_INSTALL_TEST="CONF_DIR=/tmp/x;NAIVKA_SELFTEST_URL=http://…"
// включает тестовый режим скрипта (без apt и systemd). Обычному человеку не нужно.
var installTestEnv = func() map[string]string {
	out := map[string]string{}
	v := os.Getenv("NAIVKA_INSTALL_TEST")
	if v == "" {
		return out
	}
	out["NAIVKA_TEST"] = "1"
	for _, kv := range strings.Split(v, ";") {
		if k, val, ok := strings.Cut(kv, "="); ok && (k == "CONF_DIR" || k == "NAIVKA_SELFTEST_URL") {
			out[k] = val
		}
	}
	return out
}()

func (a *App) serverInstallRoutes(api func(method, path string, f apiFunc)) {
	api("POST", "/api/server/hostkey", func(r *http.Request) (any, error) {
		var req serverInstallReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if err := req.check(false); err != nil {
			return nil, err
		}
		fp, typ, err := hostKey(req.Host, req.Port)
		if err != nil {
			return nil, err
		}
		return map[string]string{"fingerprint": fp, "type": typ}, nil
	})
	api("POST", "/api/server/install", func(r *http.Request) (any, error) {
		var req serverInstallReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if err := a.startServerInstall(req); err != nil {
			return nil, err
		}
		return a.installer.view(), nil
	})
	api("GET", "/api/server/install", func(*http.Request) (any, error) { return a.installer.view(), nil })
	api("GET", "/api/server/script", func(*http.Request) (any, error) { return map[string]string{"script": installScript}, nil })
}
