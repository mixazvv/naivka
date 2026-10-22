package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// RuleList — внешний список сайтов по ссылке, который целиком идёт одним действием.
//
//	srs  — двоичный rule-set sing-box (.srs): скачивает и обновляет само ядро;
//	json — исходный rule-set sing-box (.json): тоже ядро;
//	text — обычный список доменов и IP по строке (.lst, .txt, hosts): скачивает Naivka
//	       и вставляет в конфиг (так он работает и в профиле телефона).
type RuleList struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Format  string `json:"format"` // srs | json | text
	Action  string `json:"action"` // proxy | direct | block
	Enabled bool   `json:"enabled"`
}

const (
	maxRuleLists   = 30
	listMaxBytes   = 8 << 20
	listRefreshAge = 24 * time.Hour
)

func listFormatFromURL(u string) string {
	p := strings.ToLower(u)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	switch {
	case strings.HasSuffix(p, ".srs"):
		return "srs"
	case strings.HasSuffix(p, ".json"):
		return "json"
	}
	return "text"
}

func (l *RuleList) normalize() {
	l.Name = strings.TrimSpace(l.Name)
	l.URL = strings.TrimSpace(l.URL)
	// id — имя файла в папке lists и часть тега в конфиге: только восемь hex-знаков,
	// иначе id из чужой копии вроде «..\\..\\x» записал бы скачанный список вне папки.
	if !listIDRe.MatchString(l.ID) {
		l.ID = newID()
	}
	if l.Format != "srs" && l.Format != "json" && l.Format != "text" {
		l.Format = listFormatFromURL(l.URL)
	}
	if l.Action != "direct" && l.Action != "block" {
		l.Action = "proxy"
	}
	if l.Name == "" {
		if u, err := url.Parse(l.URL); err == nil {
			l.Name = filepath.Base(u.Path)
		}
	}
}

var listIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

func (l RuleList) validate() error {
	u, err := url.Parse(l.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("Список «%s»: нужна ссылка https://…", l.Name)
	}
	return nil
}

// inlineList — разобранный текстовый список.
type inlineList struct {
	Domains []string
	IPs     []string
}

// listCache хранит скачанные текстовые списки в <данные>\lists, чтобы без сети работала прошлая версия.
type listCache struct {
	dir string
	mu  sync.Mutex
	err map[string]string // id → последняя ошибка загрузки
}

func newListCache(dataDir string) *listCache {
	return &listCache{dir: filepath.Join(dataDir, "lists"), err: map[string]string{}}
}

func (c *listCache) path(id string) string { return filepath.Join(c.dir, id+".txt") }

// get отдаёт список из кэша; fetch — сначала обновить, если копии нет или она старше суток (force — всегда).
func (c *listCache) get(l RuleList, fetch, force bool) (*inlineList, error) {
	p := c.path(l.ID)
	st, statErr := os.Stat(p)
	if fetch && (force || statErr != nil || time.Since(st.ModTime()) > listRefreshAge) {
		err := c.download(l.URL, p)
		c.mu.Lock()
		if err != nil {
			c.err[l.ID] = err.Error()
		} else {
			delete(c.err, l.ID)
		}
		c.mu.Unlock()
		if err != nil && statErr != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errors.New("список ещё не скачан")
	}
	return parseInlineList(string(b)), nil
}

func (c *listCache) lastError(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err[id]
}

func (c *listCache) download(u, dst string) error {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Naivka")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("не скачался: %s", humanErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("сервер ответил %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, listMaxBytes+1))
	if err != nil {
		return err
	}
	if len(b) > listMaxBytes {
		return fmt.Errorf("список больше %d МБ", listMaxBytes>>20)
	}
	il := parseInlineList(string(b))
	if len(il.Domains)+len(il.IPs) == 0 {
		return errors.New("в файле нет ни одного домена или IP — это точно список?")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// parseInlineList: домены и IP из текста — по одному в строке, через запятую, строки hosts,
// префиксы domain:/full:. Программы (.exe) и мусор пропускаются.
func parseInlineList(text string) *inlineList {
	res := ParseRules(text, "proxy")
	out := &inlineList{}
	for _, r := range res.Rules {
		switch r.Kind {
		case "domain":
			// Домен верхнего уровня («com», «ru») из чужого списка забрал бы себе полинтернета.
			if d := strings.TrimPrefix(r.Value, "."); strings.Contains(d, ".") {
				out.Domains = append(out.Domains, d)
			}
		case "ip":
			v := r.Value
			if !strings.Contains(v, "/") {
				if strings.Contains(v, ":") {
					v += "/128"
				} else {
					v += "/32"
				}
			}
			// Слишком широкие сети (0.0.0.0/0, /4…) из чужого списка увели бы мимо сервера или
			// заблокировали бы весь трафик — такие строки пропускаем.
			if _, n, err := net.ParseCIDR(v); err != nil || !narrowEnough(n) {
				continue
			}
			out.IPs = append(out.IPs, v)
		}
	}
	return out
}

// narrowEnough — сеть не шире /8 для IPv4 и /16 для IPv6.
func narrowEnough(n *net.IPNet) bool {
	ones, bits := n.Mask.Size()
	if bits == 32 {
		return ones >= 8
	}
	return ones >= 16
}

// fillLists — текстовые списки для конфига. fetch: скачать свежие (при подключении).
func (a *App) fillLists(s *Settings, env *ConfigEnv, fetch bool) {
	for _, l := range s.RuleLists {
		if !l.Enabled || l.Format != "text" {
			continue
		}
		il, err := a.lists.get(l, fetch, false)
		if err != nil {
			if fetch {
				a.mon.Event("warn", fmt.Sprintf("Список «%s» не подключён: %s", l.Name, err), false)
			}
			continue
		}
		if env.InlineLists == nil {
			env.InlineLists = map[string]*inlineList{}
		}
		env.InlineLists[l.ID] = il
	}
}

// RuleListView — состояние списка для интерфейса.
type RuleListView struct {
	ID      string `json:"id"`
	Count   int    `json:"count"`   // доменов и IP (только текстовые)
	Updated int64  `json:"updated"` // когда скачан (только текстовые)
	Error   string `json:"error"`
}

func (a *App) listsView() []RuleListView {
	s := a.settingsCopy()
	out := []RuleListView{}
	for _, l := range s.RuleLists {
		v := RuleListView{ID: l.ID, Error: a.lists.lastError(l.ID)}
		if l.Format == "text" {
			if st, err := os.Stat(a.lists.path(l.ID)); err == nil {
				v.Updated = st.ModTime().Unix()
				if il, err := a.lists.get(l, false, false); err == nil {
					v.Count = len(il.Domains) + len(il.IPs)
				}
			}
		}
		out = append(out, v)
	}
	return out
}

// refreshLists скачивает текстовые списки заново; если ядро работает — перезапускает его с ними.
func (a *App) refreshLists() error {
	s := a.settingsCopy()
	var errs []string
	for _, l := range s.RuleLists {
		if !l.Enabled || l.Format != "text" {
			continue
		}
		if _, err := a.lists.get(l, true, true); err != nil {
			errs = append(errs, l.Name+": "+err.Error())
		}
	}
	if a.core.Running() {
		go a.restartCore()
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}
