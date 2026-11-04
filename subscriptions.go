package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const subscriptionLimit = 4 << 20
const subscriptionInterval = 6 * time.Hour

var errSubscriptionFormat = errors.New("Сервис не вернул список серверов. Поддерживаются списки ссылок и Base64; HTML, Clash YAML и sing-box JSON пока не поддерживаются")

type subscriptionNetworkError struct{ message string }

func (e *subscriptionNetworkError) Error() string {
	return e.message + "; прежние серверы сохранены"
}

type subscriptionHTTPError struct{ status int }

func (e *subscriptionHTTPError) Error() string {
	return fmt.Sprintf("Сервис подписки ответил HTTP %d; прежние серверы сохранены", e.status)
}

// Never expose url.Error: its string contains the complete private subscription URL.
func subscriptionNetworkFailure(err error) error {
	var dns *net.DNSError
	var timeout net.Error
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &dns):
		return &subscriptionNetworkError{"DNS не смог определить адрес сервиса подписки"}
	case errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid):
		return &subscriptionNetworkError{"Не удалось проверить TLS-сертификат сервиса подписки. Проверьте дату Windows и сертификаты системы"}
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		return &subscriptionNetworkError{"Сервис подписки не ответил вовремя"}
	case errors.Is(err, context.Canceled):
		return &subscriptionNetworkError{"Загрузка подписки отменена"}
	default:
		return &subscriptionNetworkError{"Не удалось установить соединение с сервисом подписки"}
	}
}

type Subscription struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Auto    bool   `json:"auto"`
	Updated int64  `json:"updated"`
	Attempt int64  `json:"attempt"`
	Error   string `json:"error,omitempty"`
}
type subscriptionPreview struct {
	sub     Subscription
	servers []Server
	expires time.Time
}
type subscriptionState struct {
	mu       sync.Mutex
	previews map[string]subscriptionPreview
}

func subscriptionURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || len(raw) > 8192 {
		return "", errors.New("Нужна ссылка http:// или https:// на подписку")
	}
	u.Fragment = ""
	return u.String(), nil
}

// Parser errors can contain credentials; only their count leaves this boundary.
func parseSubscription(body string) ([]Server, int, error) {
	text := strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	if !strings.Contains(text, "://") {
		compact := strings.Join(strings.Fields(text), "")
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, e := enc.DecodeString(compact); e == nil {
				text = string(b)
				break
			}
		}
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if len(lines) > 2000 {
		return nil, 0, errors.New("В подписке слишком много строк")
	}
	parsed, errs := ParseShareLinks(strings.Join(lines, "\n"))
	out := []Server{}
	seen := map[string]bool{}
	for _, sv := range parsed {
		sv.ID = ""
		sv.Fav = false
		sv.SubscriptionID = ""
		if name := []rune(sv.Name); len(name) > 120 {
			sv.Name = string(name[:120])
		}
		key := subscriptionKey(sv)
		if !seen[key] {
			seen[key] = true
			out = append(out, sv)
		}
	}
	if len(out) == 0 {
		return nil, len(errs), errSubscriptionFormat
	}
	if len(out) > 500 {
		return nil, 0, errors.New("В одной подписке допускается до 500 серверов")
	}
	return out, len(errs), nil
}
func subscriptionKey(s Server) string {
	s.ID = ""
	s.Name = ""
	s.Fav = false
	s.SubscriptionID = ""
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}
func subscriptionEndpoint(s Server) string {
	return strings.Join([]string{s.Type, strings.ToLower(s.Host), fmt.Sprint(s.Port), s.Transport, s.Path, s.ServiceName, s.SNI, s.Username}, "|")
}
func fetchSubscription(ctx context.Context, hc *http.Client, raw string) ([]Server, int, error) {
	u, e := subscriptionURL(raw)
	if e != nil {
		return nil, 0, e
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Some subscription panels return a landing page to unknown clients, and
	// URI/Base64 profiles to clients that advertise a compatible profile format.
	servers, skipped, e := requestSubscription(ctx, hc, u, "Naivka")
	if errors.Is(e, errSubscriptionFormat) && ctx.Err() == nil {
		return requestSubscription(ctx, hc, u, "v2rayNG/1.10.11 (Naivka)")
	}
	return servers, skipped, e
}
func requestSubscription(ctx context.Context, hc *http.Client, u, agent string) ([]Server, int, error) {
	client := *hc
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("redirect limit")
		}
		if _, e := subscriptionURL(req.URL.String()); e != nil {
			return e
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("https downgrade")
		}
		return nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, 0, errors.New("Не удалось создать запрос подписки")
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "text/plain, application/octet-stream;q=0.9, */*;q=0.5")
	resp, e := client.Do(req)
	if e != nil {
		return nil, 0, subscriptionNetworkFailure(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, &subscriptionHTTPError{resp.StatusCode}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, subscriptionLimit+1))
	if e != nil {
		return nil, 0, subscriptionNetworkFailure(e)
	}
	if len(b) > subscriptionLimit {
		return nil, 0, errors.New("Подписка больше 4 МБ; прежние серверы сохранены")
	}
	return parseSubscription(string(b))
}
func fetchAvailableSubscription(ctx context.Context, clients []*http.Client, raw string) ([]Server, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var last error
	for _, client := range clients {
		servers, skipped, e := fetchSubscription(ctx, client, raw)
		if e == nil {
			return servers, skipped, nil
		}
		last = e
		var network *subscriptionNetworkError
		var status *subscriptionHTTPError
		retry := errors.As(e, &network) || errors.Is(e, errSubscriptionFormat) || (errors.As(e, &status) && (status.status == 403 || status.status >= 500))
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return nil, 0, last
}
func (a *App) fetchUserSubscription(ctx context.Context, raw string) ([]Server, int, error) {
	primary := a.core.SystemProxyPort()
	clients := []*http.Client{geoClient(primary)}
	if primary > 0 {
		clients = append(clients, geoClient(0))
	} else if _, proxy := a.core.ProbePorts(); proxy > 0 {
		clients = append(clients, geoClient(proxy))
	}
	return fetchAvailableSubscription(ctx, clients, raw)
}

// Managed entries are authoritative. A stale settings form must not undo a
// background refresh, delete a subscription, or recreate removed credentials.
func mergeSubscriptionSettings(dst, current *Settings) {
	dst.Subscriptions = current.clone().Subscriptions
	managed := map[string]Server{}
	for _, sv := range current.Servers {
		if sv.SubscriptionID != "" {
			managed[sv.ID] = sv
		}
	}
	out := []Server{}
	seen := map[string]bool{}
	for _, sv := range dst.Servers {
		if latest, ok := managed[sv.ID]; ok {
			latest.Fav = sv.Fav
			out = append(out, latest)
			seen[sv.ID] = true
		} else if sv.SubscriptionID == "" {
			out = append(out, sv)
			seen[sv.ID] = true
		}
	}
	for _, sv := range current.Servers {
		if sv.SubscriptionID != "" && !seen[sv.ID] {
			out = append(out, sv)
			seen[sv.ID] = true
		}
	}
	dst.Servers = out
	if dst.Selected != "auto" && dst.Selected != "wl" && !strings.HasPrefix(dst.Selected, "w-") && !seen[dst.Selected] {
		dst.Selected = current.Selected
		if !seen[dst.Selected] {
			dst.Selected = "auto"
		}
	}
}
func replaceSubscription(s *Settings, id string, incoming []Server, connected bool) error {
	previous := map[string]Server{}
	endpoints := map[string][]Server{}
	for _, sv := range s.Servers {
		if sv.SubscriptionID == id {
			previous[subscriptionKey(sv)] = sv
			key := subscriptionEndpoint(sv)
			endpoints[key] = append(endpoints[key], sv)
		}
	}
	used := map[string]bool{}
	replacement := []Server{}
	for _, sv := range incoming {
		prior, ok := previous[subscriptionKey(sv)]
		if !ok {
			candidates := endpoints[subscriptionEndpoint(sv)]
			if len(candidates) == 1 && !used[candidates[0].ID] {
				prior = candidates[0]
				ok = true
			}
		}
		if ok && !used[prior.ID] {
			sv.ID = prior.ID
			sv.Fav = prior.Fav
		} else {
			sv.ID = "sub-" + id + "-" + subscriptionKey(sv)
		}
		sv.SubscriptionID = id
		used[sv.ID] = true
		replacement = append(replacement, sv)
	}
	out := []Server{}
	selectedRemoved := false
	for _, sv := range s.Servers {
		if sv.SubscriptionID == id {
			if sv.ID == s.Selected && !used[sv.ID] {
				selectedRemoved = true
			}
		} else {
			out = append(out, sv)
		}
	}
	if selectedRemoved && connected {
		return errors.New("Активный сервер исчез из подписки. Выберите другой сервер или отключитесь и повторите обновление; прежний список сохранён")
	}
	if selectedRemoved {
		s.Selected = "auto"
	}
	s.Servers = append(out, replacement...)
	return nil
}
func (a *App) commitSubscriptions(change func(*Settings) error) (*Settings, error) {
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	a.mu.Lock()
	next := a.settings.clone()
	if e := change(next); e != nil {
		a.mu.Unlock()
		return nil, e
	}
	next.normalize()
	if e := next.Validate(); e != nil {
		a.mu.Unlock()
		return nil, errors.New("Проверка серверов подписки не пройдена; прежний список сохранён")
	}
	if e := SaveSettings(a.dir, next); e != nil {
		a.mu.Unlock()
		return nil, errors.New("Не удалось сохранить подписку; прежний список сохранён")
	}
	changed := a.settings.fingerprint() != next.fingerprint()
	result := next.clone()
	a.settings = next
	a.mu.Unlock()
	if changed {
		a.selectionRev++
		a.scores.reset()
		_ = a.repair.stop("")
		a.scheduleAutoApply()
	}
	return result, nil
}
func (a *App) apiSubscriptionPreview(r *http.Request) (any, error) {
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
		Auto bool   `json:"auto"`
	}
	if e := decode(r, &req); e != nil {
		return nil, e
	}
	u, e := subscriptionURL(req.URL)
	if e != nil {
		return nil, e
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
	servers, skipped, e := a.fetchUserSubscription(r.Context(), u)
	if e != nil {
		return nil, e
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		p, _ := url.Parse(u)
		name = p.Hostname()
	}
	if len([]rune(name)) > 80 {
		return nil, errors.New("Название должно быть короче 80 символов")
	}
	now := time.Now()
	if a.subscriptions.previews == nil {
		a.subscriptions.previews = map[string]subscriptionPreview{}
	}
	for k, p := range a.subscriptions.previews {
		if now.After(p.expires) {
			delete(a.subscriptions.previews, k)
		}
	}
	if len(a.subscriptions.previews) >= 8 {
		a.subscriptions.previews = map[string]subscriptionPreview{}
	}
	token := randHex(24)
	a.subscriptions.previews[token] = subscriptionPreview{Subscription{ID: randHex(8), Name: name, URL: u, Auto: req.Auto}, servers, now.Add(10 * time.Minute)}
	names := []string{}
	for _, sv := range servers {
		names = append(names, sv.Name)
	}
	return map[string]any{"token": token, "names": names, "count": len(servers), "skipped": skipped}, nil
}
func subscriptionRequestID(r *http.Request) (string, error) {
	var req struct {
		ID string `json:"id"`
	}
	e := decode(r, &req)
	return req.ID, e
}
func (a *App) apiSubscriptionAdd(r *http.Request) (any, error) {
	var req struct {
		Token string `json:"token"`
	}
	if e := decode(r, &req); e != nil {
		return nil, e
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
	p, ok := a.subscriptions.previews[req.Token]
	if !ok || time.Now().After(p.expires) {
		return nil, errors.New("Предпросмотр устарел. Загрузите подписку заново")
	}
	result, e := a.commitSubscriptions(func(s *Settings) error {
		if len(s.Subscriptions) >= 20 {
			return errors.New("Допускается до 20 подписок")
		}
		for _, sub := range s.Subscriptions {
			if sub.URL == p.sub.URL {
				return errors.New("Эта подписка уже добавлена")
			}
		}
		if e := replaceSubscription(s, p.sub.ID, p.servers, false); e != nil {
			return e
		}
		p.sub.Updated = time.Now().Unix()
		p.sub.Attempt = p.sub.Updated
		s.Subscriptions = append(s.Subscriptions, p.sub)
		return nil
	})
	if e != nil {
		return nil, e
	}
	delete(a.subscriptions.previews, req.Token)
	return map[string]any{"settings": result}, nil
}
func (a *App) updateSubscription(ctx context.Context, id string) (*Settings, error) { // caller holds subscriptions.mu
	s := a.settingsCopy()
	var sub *Subscription
	for i := range s.Subscriptions {
		if s.Subscriptions[i].ID == id {
			sub = &s.Subscriptions[i]
			break
		}
	}
	if sub == nil {
		return nil, errors.New("Подписка не найдена")
	}
	servers, skipped, fetchErr := a.fetchUserSubscription(ctx, sub.URL)
	if fetchErr == nil && skipped > 0 {
		fetchErr = errors.New("Подписка содержит неподдерживаемые или повреждённые строки; прежний список сохранён")
	}
	connected := a.core.Running()
	result, e := a.commitSubscriptions(func(s *Settings) error {
		for i := range s.Subscriptions {
			if s.Subscriptions[i].ID == id {
				if fetchErr == nil {
					fetchErr = replaceSubscription(s, id, servers, connected)
				}
				s.Subscriptions[i].Attempt = time.Now().Unix()
				if fetchErr != nil {
					s.Subscriptions[i].Error = fetchErr.Error()
				} else {
					s.Subscriptions[i].Error = ""
					s.Subscriptions[i].Updated = time.Now().Unix()
				}
				return nil
			}
		}
		return errors.New("Подписка не найдена")
	})
	if e != nil {
		return nil, e
	}
	return result, fetchErr
}
func (a *App) apiSubscriptionUpdate(r *http.Request) (any, error) {
	id, e := subscriptionRequestID(r)
	if e != nil {
		return nil, e
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
	s, e := a.updateSubscription(r.Context(), id)
	if e != nil {
		return nil, e
	}
	return map[string]any{"settings": s}, nil
}
func (a *App) apiSubscriptionRemove(r *http.Request) (any, error) {
	id, e := subscriptionRequestID(r)
	if e != nil {
		return nil, e
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
	connected := a.core.Running()
	s, e := a.commitSubscriptions(func(s *Settings) error {
		found := false
		subs := []Subscription{}
		for _, sub := range s.Subscriptions {
			if sub.ID == id {
				found = true
			} else {
				subs = append(subs, sub)
			}
		}
		if !found {
			return errors.New("Подписка не найдена")
		}
		if e := replaceSubscription(s, id, nil, connected); e != nil {
			return e
		}
		s.Subscriptions = subs
		return nil
	})
	if e != nil {
		return nil, e
	}
	return map[string]any{"settings": s}, nil
}
func (a *App) apiSubscriptionAuto(r *http.Request) (any, error) {
	var req struct {
		ID   string `json:"id"`
		Auto bool   `json:"auto"`
	}
	if e := decode(r, &req); e != nil {
		return nil, e
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
	s, e := a.commitSubscriptions(func(s *Settings) error {
		for i := range s.Subscriptions {
			if s.Subscriptions[i].ID == req.ID {
				s.Subscriptions[i].Auto = req.Auto
				return nil
			}
		}
		return errors.New("Подписка не найдена")
	})
	if e != nil {
		return nil, e
	}
	return map[string]any{"settings": s}, nil
}
func (a *App) subscriptionLoop() {
	for {
		time.Sleep(time.Minute)
		s := a.settingsCopy()
		for _, sub := range s.Subscriptions {
			if sub.Auto && time.Since(time.Unix(sub.Attempt, 0)) >= subscriptionInterval {
				a.subscriptions.mu.Lock()
				latest := a.settingsCopy()
				for _, current := range latest.Subscriptions {
					if current.ID == sub.ID && current.Auto && time.Since(time.Unix(current.Attempt, 0)) >= subscriptionInterval {
						_, _ = a.updateSubscription(context.Background(), sub.ID)
						break
					}
				}
				a.subscriptions.mu.Unlock()
			}
		}
	}
}
