package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// Подписка zieng2/wl — общедоступные серверы VLESS для обхода белых списков.
// Серверы чужие, поэтому Naivka держит их запасным вариантом: включает, когда свои не отвечают.
var wlMirrors = []string{
	"https://raw.githubusercontent.com/zieng2/wl/main/vless_universal.txt",
	"https://codeberg.org/zieng2/wl/raw/branch/main/vless_universal.txt",
	"https://gitlab.com/zieng2/wl/raw/main/vless_universal.txt",
	// «экспериментальные зеркала, доступные при ограничениях связи»
	"https://hub.mos.ru/zieng2/wl/raw/main/list_universal.txt",
	"https://gitverse.ru/api/repos/zieng2/wl/raw/branch/master/list_universal.txt",
}

const ruFlag = "\U0001F1F7\U0001F1FA"

type WLData struct {
	Fetched int64    `json:"fetched"`
	Source  string   `json:"source"`
	Servers []Server `json:"servers"`
	Skipped int      `json:"skipped"` // xHTTP и прочее, чего не умеет sing-box
}

type WL struct {
	a *App

	mu      sync.Mutex
	data    WLData
	busy    bool
	err     string
	active  bool   // сейчас работаем через подписку вместо своих серверов
	applied string // какой набор серверов в работающем конфиге
}

func newWL(a *App) *WL {
	w := &WL{a: a}
	if b, err := os.ReadFile(w.path()); err == nil {
		_ = json.Unmarshal(b, &w.data)
	}
	return w
}

func (w *WL) path() string { return filepath.Join(w.a.dir, "wl.json") }

func wlID(sv Server) string {
	sv.ID, sv.Name = "", "" // имя у одного и того же сервера в подписке может меняться
	h := sha1.Sum([]byte(ShareLink(sv)))
	return hex.EncodeToString(h[:4])
}

// Servers — что пойдёт в конфиг: без российских (если не просили) и не больше Max.
func (w *WL) Servers(s *Settings) []Server {
	if !s.WL.Enabled {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Server
	for _, sv := range w.data.Servers {
		if !s.WL.IncludeRU && strings.HasPrefix(sv.Name, ruFlag) {
			continue
		}
		if sv.Transport == "xhttp" && (w.a.xray == nil || !w.a.xray.Present()) {
			continue
		}
		out = append(out, sv)
		if len(out) >= s.WL.Max {
			break
		}
	}
	return out
}

func setKey(list []Server) string {
	var b strings.Builder
	for _, sv := range list {
		b.WriteString(sv.ID)
	}
	return b.String()
}

func (w *WL) markApplied(list []Server) {
	w.mu.Lock()
	w.applied = setKey(list)
	w.mu.Unlock()
}

func (w *WL) stale(s *Settings) bool {
	cur := setKey(w.Servers(s))
	w.mu.Lock()
	defer w.mu.Unlock()
	return cur != w.applied
}

func (w *WL) Active() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active
}

func (w *WL) setActive(v bool) {
	w.mu.Lock()
	w.active = v
	w.mu.Unlock()
}

// Refresh скачивает подписку: сначала напрямую по всем зеркалам, потом — через свой сервер.
func (w *WL) Refresh() error {
	w.mu.Lock()
	if w.busy {
		w.mu.Unlock()
		return nil
	}
	w.busy, w.err = true, ""
	w.mu.Unlock()
	err := w.refresh()
	w.mu.Lock()
	w.busy = false
	if err != nil {
		w.err = err.Error()
	}
	w.mu.Unlock()
	return err
}

func (w *WL) refresh() error {
	directPort, proxyPort := w.a.core.ProbePorts()
	ports := []int{directPort}
	if proxyPort > 0 {
		ports = append(ports, proxyPort)
	}
	var lastErr error
	for _, port := range ports {
		for _, url := range wlMirrors {
			body, err := fetchText(url, port)
			if err != nil {
				lastErr = err
				continue
			}
			return w.load(body, url)
		}
	}
	return fmt.Errorf("ни одно зеркало подписки не ответило: %v", lastErr)
}

func fetchText(url string, port int) (string, error) {
	hc := geoClient(port)
	hc.Timeout = 20 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Naivka")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(b), err
}

func (w *WL) load(body, source string) error {
	text := strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	if !strings.Contains(text, "://") { // подписки часто отдают одним куском base64
		if dec, err := base64.StdEncoding.DecodeString(text); err == nil {
			text = string(dec)
		} else if dec, err := base64.RawStdEncoding.DecodeString(text); err == nil {
			text = string(dec)
		}
	}
	servers, errs := ParseShareLinks(text)
	var list []Server
	seen := map[string]bool{}
	for _, sv := range servers {
		if sv.Type != "vless" {
			continue
		}
		sv.ID = wlID(sv)
		if !seen[sv.ID] {
			seen[sv.ID] = true
			list = append(list, sv)
		}
	}
	if len(list) == 0 {
		return errors.New("в подписке не нашлось ни одного подходящего сервера")
	}
	w.mu.Lock()
	w.data = WLData{Fetched: time.Now().Unix(), Source: source, Servers: list, Skipped: len(errs)}
	b, _ := json.MarshalIndent(w.data, "", " ")
	w.mu.Unlock()
	return os.WriteFile(w.path(), b, 0o600)
}

// loop: пока подписка включена — обновлять раз в час. Новый список попадает в работу
// сразу, только если через подписку сейчас идёт трафик; иначе — при следующем подключении.
func (w *WL) loop() {
	time.Sleep(15 * time.Second)
	for {
		s := w.a.settingsCopy()
		w.mu.Lock()
		age := time.Since(time.Unix(w.data.Fetched, 0))
		w.mu.Unlock()
		if s.WL.Enabled && age > 55*time.Minute {
			if err := w.Refresh(); err == nil && w.a.core.Running() && w.inUse(s) && w.stale(s) {
				w.a.mon.Event("info", "Подписка «белые списки» обновлена — применяю новый список серверов", false)
				w.a.restartCore()
			}
		}
		time.Sleep(5 * time.Minute)
	}
}

func (w *WL) inUse(s *Settings) bool { return s.Selected == "wl" || w.Active() }

// engage: свои серверы не отвечают — переходим на подписку.
func (w *WL) engage() {
	s := w.a.settingsCopy()
	if !s.WL.Enabled || !s.WL.Fallback || w.Active() || s.Selected == "wl" || len(w.Servers(s)) == 0 || len(s.Servers) == 0 {
		return
	}
	w.setActive(true)
	w.a.mon.Event("warn", "Свои серверы не отвечают — временно работаю через подписку «белые списки». Вернусь, когда они оживут", true)
	if w.stale(s) {
		w.a.restartCore()
		return
	}
	w.a.applySelection()
	if cl := w.a.core.Clash(); cl != nil {
		_ = cl.CloseAll()
	}
}

// checkOwn: пока работаем через подписку, раз в 2 минуты проверяем свои серверы.
func (w *WL) checkOwn(cl *Clash) {
	if !w.Active() {
		return
	}
	s := w.a.settingsCopy()
	for i, sv := range s.Servers {
		if i >= 5 {
			break
		}
		if ms, err := cl.Delay(serverTag(sv), s.TestURL); err == nil && ms > 0 {
			w.setActive(false)
			w.a.applySelection()
			_ = cl.CloseAll()
			w.a.mon.Event("ok", "Свои серверы снова отвечают — вернулся с подписки на «"+sv.Name+"»", true)
			return
		}
	}
}

type WLView struct {
	Enabled bool              `json:"enabled"`
	Fetched int64             `json:"fetched"`
	Source  string            `json:"source"`
	Total   int               `json:"total"`
	Used    int               `json:"used"`
	RU      int               `json:"ru"`
	Skipped int               `json:"skipped"`
	Busy    bool              `json:"busy"`
	Error   string            `json:"error"`
	Active  bool              `json:"active"`
	Names   map[string]string `json:"names"` // w-<id> → имя (для статистики и списка)
	List    []WLItem          `json:"list"`
	XHTTP   int               `json:"xhttp"`
	Xray    XrayView          `json:"xray"`
}

type WLItem struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Security  string `json:"security"`
	Transport string `json:"transport"`
	RU        bool   `json:"ru"`
	Used      bool   `json:"used"`
	Why       string `json:"why,omitempty"` // почему не используется
}

func (w *WL) View() WLView {
	s := w.a.settingsCopy()
	used := w.Servers(s)
	w.mu.Lock()
	defer w.mu.Unlock()
	v := WLView{Enabled: s.WL.Enabled, Fetched: w.data.Fetched, Source: w.data.Source, Total: len(w.data.Servers),
		Used: len(used), Skipped: w.data.Skipped, Busy: w.busy, Error: w.err, Active: w.active, Names: map[string]string{}}
	for _, sv := range w.data.Servers {
		if strings.HasPrefix(sv.Name, ruFlag) {
			v.RU++
		}
	}
	inUse := map[string]bool{}
	for _, sv := range used {
		v.Names["w-"+sv.ID] = sv.Name
		inUse[sv.ID] = true
	}
	xrayOK := w.a.xray != nil && w.a.xray.Present()
	for _, sv := range w.data.Servers {
		it := WLItem{Tag: "w-" + sv.ID, Name: sv.Name, Security: sv.Security, Transport: sv.Transport,
			RU: strings.HasPrefix(sv.Name, ruFlag), Used: inUse[sv.ID]}
		if sv.Transport == "xhttp" {
			v.XHTTP++
		}
		switch {
		case it.Used:
		case it.RU && !s.WL.IncludeRU:
			it.Why = "российский"
		case sv.Transport == "xhttp" && !xrayOK:
			it.Why = "нужен Xray"
		default:
			it.Why = "сверх лимита"
		}
		v.List = append(v.List, it)
	}
	if w.a.xray != nil {
		v.Xray = w.a.xray.View()
	}
	return v
}
