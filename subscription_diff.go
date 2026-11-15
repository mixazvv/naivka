package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type subscriptionChanges struct {
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Changed   []string `json:"changed"`
	Unchanged int      `json:"unchanged"`
}

func subscriptionServers(s *Settings, id string) []Server {
	out := []Server{}
	for _, sv := range s.Servers {
		if sv.SubscriptionID == id {
			out = append(out, sv)
		}
	}
	return out
}
func subscriptionRevision(sub Subscription, servers []Server) string {
	b, _ := json.Marshal(struct {
		Sub     Subscription
		Servers []Server
	}{sub, servers})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func diffSubscription(old, next []Server) subscriptionChanges {
	d := subscriptionChanges{Added: []string{}, Removed: []string{}, Changed: []string{}}
	used := make([]bool, len(old))
	matched := make([]bool, len(next))
	keys, ends := make([]string, len(old)), make([]string, len(old))
	for i, o := range old {
		keys[i] = subscriptionKey(o)
		ends[i] = subscriptionEndpoint(o)
	}
	// Match every unchanged entry first, including several profiles on one host.
	for j, n := range next {
		key := subscriptionKey(n)
		for i, o := range old {
			if !used[i] && keys[i] == key && o.Name == n.Name {
				used[i] = true
				matched[j] = true
				d.Unchanged++
				break
			}
		}
	}
	for j, n := range next {
		if matched[j] {
			continue
		}
		match := -1
		endpoint := subscriptionEndpoint(n)
		for i := range old {
			if !used[i] && ends[i] == endpoint {
				match = i
				break
			}
		}
		if match >= 0 {
			used[match] = true
			d.Changed = append(d.Changed, n.Name)
		} else {
			d.Added = append(d.Added, n.Name)
		}
	}
	for i, o := range old {
		if !used[i] {
			d.Removed = append(d.Removed, o.Name)
		}
	}
	return d
}
func (a *App) apiSubscriptionPreviewUpdate(r *http.Request) (any, error) {
	id, err := subscriptionRequestID(r)
	if err != nil {
		return nil, err
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
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
	servers, skipped, err := a.fetchUserSubscription(r.Context(), sub.URL, sub.Client)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		return nil, errors.New("Есть неподдерживаемые строки; прежний список сохранён")
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
		oldest := ""
		var expiry time.Time
		for key, p := range a.subscriptions.previews {
			if oldest == "" || p.expires.Before(expiry) {
				oldest = key
				expiry = p.expires
			}
		}
		delete(a.subscriptions.previews, oldest)
	}
	old := subscriptionServers(s, id)
	token := randHex(24)
	a.subscriptions.previews[token] = subscriptionPreview{sub: *sub, servers: servers, expires: now.Add(10 * time.Minute), base: subscriptionRevision(*sub, old)}
	return map[string]any{"token": token, "changes": diffSubscription(old, servers), "count": len(servers)}, nil
}
func (a *App) commitSubscriptionPreview(id, token string) (any, error) {
	p, ok := a.subscriptions.previews[token]
	if !ok || p.base == "" || p.sub.ID != id || time.Now().After(p.expires) {
		return nil, errors.New("Предпросмотр устарел. Проверьте изменения заново")
	}
	connected := a.core.Running()
	result, err := a.commitSubscriptions(func(s *Settings) error {
		for i := range s.Subscriptions {
			sub := &s.Subscriptions[i]
			if sub.ID != id {
				continue
			}
			if subscriptionRevision(*sub, subscriptionServers(s, id)) != p.base {
				return errors.New("Подписка изменилась после предпросмотра. Проверьте изменения заново")
			}
			if err := replaceSubscription(s, id, p.servers, connected); err != nil {
				return err
			}
			sub.Updated = time.Now().Unix()
			sub.Attempt = sub.Updated
			sub.Error = ""
			return nil
		}
		return errors.New("Подписка удалена после предпросмотра")
	})
	if err != nil {
		return nil, err
	}
	delete(a.subscriptions.previews, token)
	return map[string]any{"settings": result}, nil
}
