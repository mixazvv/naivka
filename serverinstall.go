//go:build !legacy

package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
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

// Установка своего сервера по SSH («Серверы» → «Свой сервер»): протоколы (Reality, Hysteria2,
// NaiveProxy) и, по желанию, веб-панель (3X-UI или s-ui).
//
// Безопасность:
//   - пароль и ключ SSH нигде не сохраняются: живут в памяти только на время установки (и открытия панели);
//   - сначала Naivka узнаёт ключ сервера, НЕ отправляя пароль, и показывает его отпечаток —
//     человек сверяет его с панелью хостинга; потом подключение идёт только к серверу с этим ключом
//     (иначе пароль от root можно было бы отдать подставному серверу);
//   - на сервер уходят server/install-server.sh и server/install-panel.sh из самой Naivka —
//     их можно прочитать заранее;
//   - панель по умолчанию слушает только 127.0.0.1 сервера, Naivka открывает её через SSH-туннель.

//go:embed server/install-server.sh
var installScript string

//go:embed server/install-panel.sh
var panelScript string

const serverInstallSupported = true

type serverInstallReq struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Password    string `json:"password"`
	Key         string `json:"key"`        // закрытый ключ OpenSSH/PEM
	Passphrase  string `json:"passphrase"` // пароль ключа
	Fingerprint string `json:"fingerprint"`
	Confirm     bool   `json:"confirm"` // человек прочитал предупреждение

	Protocols bool   `json:"protocols"` // ставить протоколы (Reality и др.)
	SNI       string `json:"sni"`
	Hy2       bool   `json:"hy2"`
	Naive     bool   `json:"naive"`
	Domain    string `json:"domain"` // для NaiveProxy
	BBR       bool   `json:"bbr"`
	Name      string `json:"name"`

	Panel        string `json:"panel"`        // "" | 3x-ui | s-ui
	PanelAccess  string `json:"panelAccess"`  // tunnel | public
	PanelConfirm bool   `json:"panelConfirm"` // человек прочитал предупреждение о панели
}

// ServerInstallView — ход установки для интерфейса.
type ServerInstallView struct {
	Active bool     `json:"active"`
	Done   bool     `json:"done"`
	Error  string   `json:"error"`
	Log    []string `json:"log"`
	Added  []string `json:"added"` // имена добавленных серверов
	Panel  string   `json:"panel"` // ID добавленной панели
}

type serverInstaller struct {
	mu      sync.Mutex
	state   ServerInstallView
	tunMu   sync.Mutex
	tunnels map[string]*panelTunnel // ID панели → открытый туннель
}

var hostRe = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$`)
var ansiRe2 = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
var sshUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)

func (r *serverInstallReq) checkTarget() error {
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
	return nil
}

func (r *serverInstallReq) checkAuth() error {
	r.User = strings.TrimSpace(r.User)
	if r.User == "" {
		r.User = "root"
	}
	if !sshUserRe.MatchString(r.User) {
		return errors.New("Неверное имя пользователя SSH")
	}
	if r.Password == "" && strings.TrimSpace(r.Key) == "" {
		return errors.New("Нужен пароль или ключ SSH")
	}
	if !strings.HasPrefix(r.Fingerprint, "SHA256:") {
		return errors.New("Сначала проверьте ключ сервера")
	}
	return nil
}

func (r *serverInstallReq) check() error {
	if err := r.checkTarget(); err != nil {
		return err
	}
	if err := r.checkAuth(); err != nil {
		return err
	}
	if !r.Confirm {
		return errors.New("Подтвердите, что понимаете, что делаете")
	}
	if !r.Protocols && r.Panel == "" {
		return errors.New("Выберите, что ставить: протоколы, панель или и то и другое")
	}
	if r.Protocols {
		r.Domain = strings.ToLower(strings.TrimSpace(r.Domain))
		if r.Naive && !hostRe.MatchString(r.Domain) {
			return errors.New("Для NaiveProxy нужен домен, например proxy.example.com")
		}
		r.SNI = strings.ToLower(strings.TrimSpace(r.SNI))
		if !r.Naive && !hostRe.MatchString(r.SNI) {
			return errors.New("Укажите сайт для маскировки Reality, например www.example.com")
		}
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" {
			r.Name = r.Host
		}
		if len([]rune(r.Name)) > 40 || strings.ContainsAny(r.Name, "\n\r\x00") {
			return errors.New("Название — до 40 символов")
		}
	}
	switch r.Panel {
	case "":
	case "3x-ui", "s-ui":
		if r.PanelAccess != "public" {
			r.PanelAccess = "tunnel"
		}
		if r.Panel == "s-ui" {
			r.PanelAccess = "tunnel" // у s-ui нет HTTPS из коробки
		}
		if !r.PanelConfirm {
			return errors.New("Подтвердите, что понимаете риски установки панели")
		}
	default:
		return errors.New("Неизвестная панель")
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

// sshDial подключается только к серверу с подтверждённым отпечатком ключа.
func sshDial(host string, port int, user, password, key, passphrase, fingerprint string) (*ssh.Client, error) {
	var auth []ssh.AuthMethod
	if k := strings.TrimSpace(key); k != "" {
		var signer ssh.Signer
		var err error
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(k), []byte(passphrase))
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
	if password != "" {
		pw := password
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
		User: user,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if ssh.FingerprintSHA256(k) != fingerprint {
				keyErr = fmt.Errorf("Ключ сервера не тот, что вы подтвердили (%s вместо %s). Пароль не отправлен, подключение прервано: возможно, это не ваш сервер", ssh.FingerprintSHA256(k), fingerprint)
				return keyErr
			}
			return nil
		},
		HostKeyAlgorithms: hostKeyAlgos,
		Timeout:           20 * time.Second,
	}
	cl, err := ssh.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)), cfg)
	if err != nil {
		if keyErr != nil {
			return nil, keyErr
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errors.New("Сервер не принял пароль или ключ")
		}
		return nil, fmt.Errorf("SSH: %s", humanErr(err))
	}
	return cl, nil
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
	if err := req.check(); err != nil {
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
		added, panelID, err := a.runServerInstall(req)
		si.mu.Lock()
		si.state.Active, si.state.Done, si.state.Added, si.state.Panel = false, true, added, panelID
		if err != nil {
			si.state.Error = err.Error()
		}
		si.mu.Unlock()
		if err != nil {
			a.mon.Event("error", "Установка на сервер "+req.Host+": "+firstLine(err.Error()), false)
		} else {
			what := strings.Join(added, ", ")
			if panelID != "" {
				what = strings.TrimPrefix(what+", панель "+req.Panel, ", ")
			}
			a.mon.Event("ok", fmt.Sprintf("Сервер %s готов: %s", req.Host, what), true)
		}
	}()
	return nil
}

// scriptResult — что скрипт сообщил строками NAIVKA-…: в журнал они не попадают (там ключи и пароли).
type scriptResult struct {
	links   []string
	panels  []string // base64 JSON
	friends []string // base64 JSON
}

// scriptOpts — необязательное для runScriptOpts.
type scriptOpts struct {
	persist string            // путь на сервере: скрипт сначала кладётся туда (нужен потом таймеру), потом запускается
	logf    func(line string) // куда писать вывод; по умолчанию — журнал установки
}

// runScript выполняет встроенный скрипт на сервере, передавая его через stdin.
func (a *App) runScript(cl *ssh.Client, user, script string, env map[string]string, order []string) (scriptResult, error) {
	return a.runScriptOpts(cl, user, script, env, order, scriptOpts{})
}

func (a *App) runScriptOpts(cl *ssh.Client, user, script string, env map[string]string, order []string, opt scriptOpts) (scriptResult, error) {
	si := a.installer
	logf := opt.logf
	if logf == nil {
		logf = func(l string) { si.logf("%s", l) }
	}
	var res scriptResult
	sess, err := cl.NewSession()
	if err != nil {
		return res, err
	}
	defer sess.Close()
	var parts []string
	for _, k := range order {
		if v, ok := env[k]; ok {
			parts = append(parts, k+"="+shellQuote(v))
		}
	}
	cmd := "env " + strings.Join(parts, " ") + " bash -s"
	if opt.persist != "" {
		p := shellQuote(opt.persist)
		cmd = "env " + strings.Join(parts, " ") + " bash -c " + shellQuote(`set -e; f="$(mktemp)"; cat > "$f"; install -m 700 "$f" `+p+`; rm -f "$f"; exec `+p)
	}
	if user != "root" {
		cmd = "sudo -n " + cmd // sudo с паролем по SSH без терминала не спросить — нужен NOPASSWD
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		return res, err
	}
	pr, pw := io.Pipe()
	sess.Stdout, sess.Stderr = pw, pw
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := ansiRe2.ReplaceAllString(sc.Text(), "")
			if l, ok := strings.CutPrefix(line, "NAIVKA-LINK "); ok {
				res.links = append(res.links, strings.TrimSpace(l))
				continue
			}
			if l, ok := strings.CutPrefix(line, "NAIVKA-PANEL "); ok {
				res.panels = append(res.panels, strings.TrimSpace(l))
				continue
			}
			if l, ok := strings.CutPrefix(line, "NAIVKA-FRIENDS "); ok {
				res.friends = append(res.friends, strings.TrimSpace(l))
				continue
			}
			logf(line)
		}
	}()
	if err := sess.Start(cmd); err != nil {
		pw.Close()
		return res, err
	}
	_, _ = io.WriteString(stdin, script)
	stdin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	waitErr := make(chan error, 1)
	go func() { waitErr <- sess.Wait() }()
	select {
	case err = <-waitErr:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGTERM)
		err = errors.New("скрипт работает дольше 30 минут — прерван")
	}
	pw.Close()
	<-readDone
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) && user != "root" && sudoFailed(si) {
			return res, errors.New("sudo требует пароль. Войдите как root или разрешите этому пользователю sudo без пароля")
		}
		if ee == nil {
			return res, err
		}
		return res, errors.New("скрипт на сервере завершился с ошибкой — подробности в журнале выше")
	}
	return res, nil
}

func (a *App) runServerInstall(req serverInstallReq) ([]string, string, error) {
	si := a.installer
	si.logf("Подключаюсь к %s как %s…", net.JoinHostPort(req.Host, strconv.Itoa(req.Port)), req.User)
	cl, err := sshDial(req.Host, req.Port, req.User, req.Password, req.Key, req.Passphrase, req.Fingerprint)
	if err != nil {
		return nil, "", err
	}
	defer cl.Close()

	var added []string
	if req.Protocols {
		env := map[string]string{
			"SERVER_ADDR": req.Host, "REALITY_SNI": req.SNI, "NAME": req.Name,
			"HY2": boolEnv(req.Hy2), "BBR": boolEnv(req.BBR), "NAIVE": boolEnv(req.Naive), "DOMAIN": req.Domain,
		}
		for k, v := range installTestEnv {
			env[k] = v
		}
		res, err := a.runScript(cl, req.User, installScript, env,
			[]string{"SERVER_ADDR", "REALITY_SNI", "NAME", "HY2", "BBR", "NAIVE", "DOMAIN", "NAIVKA_TEST", "CONF_DIR", "NAIVKA_SELFTEST_URL"})
		if err != nil {
			return nil, "", err
		}
		if len(res.links) == 0 {
			return nil, "", errors.New("скрипт не выдал ссылок на серверы")
		}
		servers, errs := ParseShareLinks(strings.Join(res.links, "\n"))
		if len(errs) > 0 {
			return nil, "", fmt.Errorf("ссылки от сервера не разобрались: %s", strings.Join(errs, "; "))
		}
		if added, err = a.addInstalledServers(servers); err != nil {
			return nil, "", err
		}
		// Запоминаем сервер: через него потом заводятся друзья («Свой сервер» → «Друзья»).
		if _, err := a.addOwnHost(OwnHost{Name: req.Name, Host: req.Host, SSHPort: req.Port, SSHUser: req.User, HostKey: req.Fingerprint}); err != nil {
			si.logf("Не удалось запомнить сервер для друзей: %v", err)
		}
	}

	panelID := ""
	if req.Panel != "" {
		si.logf("")
		si.logf("Ставлю панель %s…", req.Panel)
		res, err := a.runScript(cl, req.User, panelScript, map[string]string{
			"PANEL": req.Panel, "ACCESS": req.PanelAccess, "SERVER_ADDR": req.Host,
		}, []string{"PANEL", "ACCESS", "SERVER_ADDR"})
		if err != nil {
			return added, "", fmt.Errorf("панель: %w", err)
		}
		if len(res.panels) == 0 {
			return added, "", errors.New("скрипт панели не выдал данных для входа")
		}
		p, err := parsePanelLine(res.panels[len(res.panels)-1])
		if err != nil {
			return added, "", err
		}
		p.Host, p.SSHPort, p.SSHUser, p.HostKey = req.Host, req.Port, req.User, req.Fingerprint
		if panelID, err = a.addPanel(p); err != nil {
			return added, "", err
		}
	}
	return added, panelID, nil
}

func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func parsePanelLine(b64 string) (Panel, error) {
	var p Panel
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return p, errors.New("данные панели не читаются")
	}
	var v struct {
		Type, Version, Access, Path, User, Pass, URL string
		Port                                         int
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return p, errors.New("данные панели не читаются")
	}
	if (v.Type != "3x-ui" && v.Type != "s-ui") || v.Port < 1 || v.Port > 65535 || v.User == "" || v.Pass == "" {
		return p, errors.New("скрипт панели выдал неполные данные")
	}
	if v.Access != "public" {
		v.Access, v.URL = "tunnel", ""
	}
	if !strings.HasPrefix(v.Path, "/") {
		v.Path = "/" + v.Path
	}
	return Panel{Type: v.Type, Version: v.Version, Access: v.Access, Port: v.Port, Path: v.Path, User: v.User, Pass: v.Pass, URL: v.URL}, nil
}

// addPanel сохраняет панель; повторная установка на тот же сервер заменяет прежнюю запись.
func (a *App) addPanel(p Panel) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ns := a.settings.clone()
	p.ID, p.Added = newID(), time.Now().Unix()
	replaced := false
	for i, old := range ns.Panels {
		if old.Host == p.Host && old.Type == p.Type {
			p.ID = old.ID
			ns.Panels[i] = p
			replaced = true
		}
	}
	if !replaced {
		ns.Panels = append(ns.Panels, p)
	}
	a.settings = ns
	return p.ID, SaveSettings(a.dir, ns)
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

// ---------- туннель к панели ----------

// panelTunnel — локальный порт на 127.0.0.1, проброшенный по SSH к панели на сервере.
type panelTunnel struct {
	ln     net.Listener
	cl     *ssh.Client
	url    string
	closed chan struct{}
}

func (t *panelTunnel) close() {
	select {
	case <-t.closed:
	default:
		close(t.closed)
		t.ln.Close()
		t.cl.Close()
	}
}

const tunnelTTL = 3 * time.Hour

// openPanelTunnel подключается к серверу (только с ключом, подтверждённым при установке)
// и пробрасывает 127.0.0.1:<случайный порт> на 127.0.0.1:<порт панели> сервера.
func (a *App) openPanelTunnel(id, password, key, passphrase string) (string, error) {
	var p *Panel
	for _, x := range a.settingsCopy().Panels {
		if x.ID == id {
			x := x
			p = &x
		}
	}
	if p == nil {
		return "", errors.New("Панель не найдена")
	}
	if p.Access == "public" && p.URL != "" {
		return p.URL, nil
	}
	if password == "" && strings.TrimSpace(key) == "" {
		return "", errors.New("Нужен пароль или ключ SSH")
	}
	si := a.installer
	si.tunMu.Lock()
	if t := si.tunnels[id]; t != nil {
		t.close()
		delete(si.tunnels, id)
	}
	si.tunMu.Unlock()

	cl, err := sshDial(p.Host, p.SSHPort, p.SSHUser, password, key, passphrase, p.HostKey)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cl.Close()
		return "", err
	}
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Port))
	// Проверяем сразу, что панель на сервере отвечает.
	if c, err := cl.Dial("tcp", target); err != nil {
		ln.Close()
		cl.Close()
		return "", fmt.Errorf("панель на сервере не отвечает (порт %d): %v", p.Port, err)
	} else {
		c.Close()
	}
	t := &panelTunnel{ln: ln, cl: cl, closed: make(chan struct{}),
		url: fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, p.Path)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r, err := cl.Dial("tcp", target)
				if err != nil {
					return
				}
				defer r.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(r, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, r); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	go func() {
		select {
		case <-time.After(tunnelTTL):
		case <-t.closed:
		}
		t.close()
	}()
	si.tunMu.Lock()
	if si.tunnels == nil {
		si.tunnels = map[string]*panelTunnel{}
	}
	si.tunnels[id] = t
	si.tunMu.Unlock()
	return t.url, nil
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
		if err := req.checkTarget(); err != nil {
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
	a.friendsRoutes(api)
	api("GET", "/api/server/script", func(r *http.Request) (any, error) {
		if r.URL.Query().Get("panel") == "1" {
			return map[string]string{"script": panelScript}, nil
		}
		return map[string]string{"script": installScript}, nil
	})
	api("POST", "/api/panel/open", func(r *http.Request) (any, error) {
		var req struct {
			ID         string `json:"id"`
			Password   string `json:"password"`
			Key        string `json:"key"`
			Passphrase string `json:"passphrase"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		u, err := a.openPanelTunnel(req.ID, req.Password, req.Key, req.Passphrase)
		if err != nil {
			return nil, err
		}
		openURL(u)
		return map[string]string{"url": u}, nil
	})
}
