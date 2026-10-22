//go:build !legacy

package main

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Друзья на своём сервере («Свой сервер» → «Друзья»): у каждого свой вход — отдельные ключи
// Reality, Hysteria2 и NaiveProxy, — поэтому любого можно приостановить или убрать, не трогая остальных.
// Всё делает server/friends.sh на самом сервере; Naivka только передаёт команду по SSH.
//
// Пароль SSH не сохраняется: соединение держится в памяти 20 минут после последнего действия,
// чтобы не спрашивать пароль на каждый щелчок, и подключается только к серверу с подтверждённым ключом.

//go:embed server/friends.sh
var friendsScript string

const friendsIdle = 20 * time.Minute

var friendIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

// Friend — друг, как его описал сервер.
type Friend struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Created int64    `json:"created"`
	Until   int64    `json:"until"` // 0 — бессрочно
	Paused  bool     `json:"paused"`
	Expired bool     `json:"expired"`
	Links   []string `json:"links"`
}

type friendsConn struct {
	cl    *ssh.Client
	timer *time.Timer
}

type friendsSessions struct {
	mu    sync.Mutex
	conns map[string]*friendsConn // id своего сервера → соединение
	ops   sync.Mutex              // команды по одной: скрипт и так не даёт править список одновременно
}

func (f *friendsSessions) get(id string) *ssh.Client {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.conns[id]
	if c == nil {
		return nil
	}
	c.timer.Reset(friendsIdle)
	return c.cl
}

func (f *friendsSessions) put(id string, cl *ssh.Client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conns == nil {
		f.conns = map[string]*friendsConn{}
	}
	if old := f.conns[id]; old != nil {
		old.timer.Stop()
		old.cl.Close()
	}
	c := &friendsConn{cl: cl}
	c.timer = time.AfterFunc(friendsIdle, func() { f.drop(id, cl) })
	f.conns[id] = c
}

// drop закрывает соединение, если оно всё ещё то же самое (cl == nil — любое).
func (f *friendsSessions) drop(id string, cl *ssh.Client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.conns[id]
	if c == nil || (cl != nil && c.cl != cl) {
		return
	}
	c.timer.Stop()
	c.cl.Close()
	delete(f.conns, id)
}

type friendsReq struct {
	Host       string `json:"host"` // id своего сервера
	Action     string `json:"action"`
	Name       string `json:"name"`
	Days       int    `json:"days"`
	FID        string `json:"fid"`
	Password   string `json:"password"`
	Key        string `json:"key"`
	Passphrase string `json:"passphrase"`
}

// errNeedAuth — соединения нет, окно должно спросить пароль или ключ SSH.
var errNeedAuth = errors.New("need_auth")

func (r *friendsReq) check() error {
	switch r.Action {
	case "list":
	case "add":
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" || len([]rune(r.Name)) > 40 || strings.ContainsFunc(r.Name, func(c rune) bool { return c < 32 || c == 127 }) {
			return errors.New("Имя — от 1 до 40 символов")
		}
		fallthrough
	case "renew":
		if r.Days < 0 || r.Days > 3650 {
			return errors.New("Срок — от 0 (бессрочно) до 3650 дней")
		}
	case "remove", "pause", "resume":
	default:
		return errors.New("неизвестное действие")
	}
	if r.Action != "list" && r.Action != "add" && !friendIDRe.MatchString(r.FID) {
		return errors.New("неверный id друга")
	}
	return nil
}

func (a *App) ownHost(id string) (OwnHost, bool) {
	for _, h := range a.settingsCopy().OwnHosts {
		if h.ID == id {
			return h, true
		}
	}
	return OwnHost{}, false
}

// addOwnHost запоминает свой сервер; повторно тот же адрес и порт SSH — обновляет запись.
func (a *App) addOwnHost(h OwnHost) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ns := a.settings.clone()
	h.ID, h.Added = newID(), time.Now().Unix()
	replaced := false
	for i, old := range ns.OwnHosts {
		if old.Host == h.Host && old.SSHPort == h.SSHPort {
			h.ID, h.Added = old.ID, old.Added
			if h.Name == "" {
				h.Name = old.Name
			}
			ns.OwnHosts[i] = h
			replaced = true
		}
	}
	if !replaced {
		ns.OwnHosts = append(ns.OwnHosts, h)
	}
	a.settings = ns
	return h.ID, SaveSettings(a.dir, ns)
}

// friendsPath — куда скрипт кладёт себя на сервере (для часовой проверки сроков).
func friendsPath() string {
	if d := installTestEnv["CONF_DIR"]; d != "" {
		return d + "/naivka-friends"
	}
	return "/usr/local/sbin/naivka-friends"
}

func (a *App) runFriends(req friendsReq) ([]Friend, error) {
	if err := req.check(); err != nil {
		return nil, err
	}
	host, ok := a.ownHost(req.Host)
	if !ok {
		return nil, errors.New("Сервер не найден")
	}
	a.friends.ops.Lock()
	defer a.friends.ops.Unlock()

	cl := a.friends.get(host.ID)
	if cl == nil {
		if req.Password == "" && strings.TrimSpace(req.Key) == "" {
			return nil, errNeedAuth
		}
		var err error
		if cl, err = sshDial(host.Host, host.SSHPort, host.SSHUser, req.Password, req.Key, req.Passphrase, host.HostKey); err != nil {
			return nil, err
		}
		a.friends.put(host.ID, cl)
	}
	env := map[string]string{"ACTION": req.Action, "SERVER_ADDR": host.Host}
	if req.Action == "add" {
		env["NAME"] = req.Name
	}
	if req.Action == "add" || req.Action == "renew" {
		env["DAYS"] = strconv.Itoa(req.Days)
	}
	if req.FID != "" {
		env["FID"] = req.FID
	}
	for k, v := range installTestEnv {
		env[k] = v
	}
	var lines []string
	res, err := a.runScriptOpts(cl, host.SSHUser, friendsScript, env,
		[]string{"ACTION", "NAME", "DAYS", "FID", "SERVER_ADDR", "NAIVKA_TEST", "CONF_DIR"},
		scriptOpts{persist: friendsPath(), logf: func(l string) { lines = append(lines, l) }})
	if err != nil {
		var nerr net.Error
		if errors.As(err, &nerr) || strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "closed") {
			a.friends.drop(host.ID, cl) // соединение порвалось — в следующий раз подключимся заново
		}
		// Скрипт сам пишет причину строкой «[x] …» — она понятнее общего «ошибка на сервере».
		for i := len(lines) - 1; i >= 0; i-- {
			if l, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "[x] "); ok {
				return nil, errors.New(l)
			}
		}
		return nil, err
	}
	if len(res.friends) == 0 {
		return nil, errors.New("сервер не прислал список друзей")
	}
	return parseFriendsLine(res.friends[len(res.friends)-1])
}

func parseFriendsLine(b64 string) ([]Friend, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("список друзей не разобрался: %v", err)
	}
	var v struct {
		Friends []Friend `json:"friends"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("список друзей не разобрался: %v", err)
	}
	out := make([]Friend, 0, len(v.Friends))
	for _, f := range v.Friends {
		if !friendIDRe.MatchString(f.ID) {
			continue
		}
		// Показываем только ссылки, которые Naivka сама понимает: в окне они становятся QR-кодом.
		var links []string
		for _, l := range f.Links {
			if _, errs := ParseShareLinks(l); len(errs) == 0 && len(links) < 5 {
				links = append(links, l)
			}
		}
		f.Links = links
		out = append(out, f)
	}
	return out, nil
}

func (a *App) friendsRoutes(api func(method, path string, f apiFunc)) {
	api("POST", "/api/friends", func(r *http.Request) (any, error) {
		var req friendsReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		list, err := a.runFriends(req)
		if errors.Is(err, errNeedAuth) {
			return map[string]any{"needAuth": true}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"friends": list}, nil
	})
	// Отключиться от сервера сразу, не дожидаясь 20 минут.
	api("POST", "/api/friends/close", func(r *http.Request) (any, error) {
		var req friendsReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		a.friends.drop(req.Host, nil)
		return nil, nil
	})
	// Добавить в список сервер, поставленный раньше, чем Naivka начала их запоминать.
	api("POST", "/api/ownhost", func(r *http.Request) (any, error) {
		var req serverInstallReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if err := req.checkTarget(); err != nil {
			return nil, err
		}
		req.User = strings.TrimSpace(req.User)
		if req.User == "" {
			req.User = "root"
		}
		if !sshUserRe.MatchString(req.User) {
			return nil, errors.New("Неверное имя пользователя SSH")
		}
		if !strings.HasPrefix(req.Fingerprint, "SHA256:") {
			return nil, errors.New("Сначала проверьте ключ сервера")
		}
		if len([]rune(req.Name)) > 40 {
			return nil, errors.New("Название — до 40 символов")
		}
		id, err := a.addOwnHost(OwnHost{Name: strings.TrimSpace(req.Name), Host: req.Host, SSHPort: req.Port, SSHUser: req.User, HostKey: req.Fingerprint})
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "settings": a.settingsCopy()}, nil
	})
}
