//go:build !legacy

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type addressRequest struct {
	Host     string   `json:"host"`
	Address  string   `json:"address"`
	Revision string   `json:"revision"`
	Apply    bool     `json:"apply"`
	Confirm  bool     `json:"confirm"`
	Servers  []string `json:"servers"`
}
type addressItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type addressPlan struct {
	Revision    string        `json:"revision"`
	Old         string        `json:"old"`
	Address     string        `json:"address"`
	Fingerprint string        `json:"fingerprint"`
	Servers     []addressItem `json:"servers"`
	Panels      []addressItem `json:"panels"`
	Backup      string        `json:"backup,omitempty"`
}

func addressRevision(s *Settings) string {
	b, _ := json.Marshal([]any{s.OwnHosts, s.Servers, s.Panels})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func sameAddress(a, b string) bool {
	x, y := net.ParseIP(a), net.ParseIP(b)
	if x != nil && y != nil {
		return x.Equal(y)
	}
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}
func planAddress(s *Settings, r addressRequest) (OwnHost, addressPlan, error) {
	p := addressPlan{Revision: addressRevision(s), Servers: []addressItem{}, Panels: []addressItem{}}
	var host OwnHost
	for _, h := range s.OwnHosts {
		if h.ID == r.Host {
			host = h
			break
		}
	}
	if host.ID == "" {
		return host, p, errors.New("Свой сервер не найден")
	}
	ip := net.ParseIP(strings.TrimSpace(r.Address))
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return host, p, errors.New("Укажите новый IP-адрес VPS, без порта и маски")
	}
	p.Old, p.Address, p.Fingerprint = host.Host, ip.String(), host.HostKey
	if sameAddress(host.Host, p.Address) {
		return host, p, errors.New("Этот адрес уже используется")
	}
	if !strings.HasPrefix(host.HostKey, "SHA256:") {
		return host, p, errors.New("У сервера нет подтверждённого ключа SSH. Сначала добавьте его заново с проверкой ключа")
	}
	for _, h := range s.OwnHosts {
		if h.ID != host.ID && sameAddress(h.Host, p.Address) && h.SSHPort == host.SSHPort {
			return host, p, errors.New("На новом адресе уже есть запись своего сервера")
		}
	}
	// Domain names and subscription-managed records must retain their source.
	for _, sv := range s.Servers {
		if net.ParseIP(sv.Host) != nil && sameAddress(sv.Host, host.Host) && sv.SubscriptionID == "" {
			p.Servers = append(p.Servers, addressItem{sv.ID, sv.Name})
		}
	}
	for _, panel := range s.Panels {
		if sameAddress(panel.Host, host.Host) && panel.SSHPort == host.SSHPort && panel.SSHUser == host.SSHUser && panel.HostKey == host.HostKey {
			p.Panels = append(p.Panels, addressItem{panel.ID, panel.Type})
		}
	}
	return host, p, nil
}

func rewriteAddress(s *Settings, r addressRequest, p addressPlan) (*Settings, error) {
	if r.Revision != p.Revision {
		return nil, errors.New("Настройки изменились. Проверьте новый адрес ещё раз")
	}
	if !r.Confirm {
		return nil, errors.New("Подтвердите обновление адресов")
	}
	allowed, chosen := map[string]bool{}, map[string]bool{}
	for _, item := range p.Servers {
		allowed[item.ID] = true
	}
	for _, id := range r.Servers {
		if !allowed[id] || chosen[id] {
			return nil, errors.New("Список подключений изменился. Повторите проверку")
		}
		chosen[id] = true
	}
	ns := s.clone()
	for i := range ns.OwnHosts {
		if ns.OwnHosts[i].ID == r.Host {
			ns.OwnHosts[i].Host = p.Address
		}
	}
	for i := range ns.Servers {
		if chosen[ns.Servers[i].ID] {
			ns.Servers[i].Host = p.Address
		}
	}
	panels := map[string]bool{}
	for _, it := range p.Panels {
		panels[it.ID] = true
	}
	for i := range ns.Panels {
		x := &ns.Panels[i]
		if !panels[x.ID] {
			continue
		}
		x.Host = p.Address
		if u, err := url.Parse(x.URL); err == nil && net.ParseIP(u.Hostname()) != nil && sameAddress(u.Hostname(), p.Old) {
			h := p.Address
			if u.Port() != "" {
				h = net.JoinHostPort(h, u.Port())
			} else if strings.Contains(h, ":") {
				h = "[" + h + "]"
			}
			u.Host = h
			x.URL = u.String()
		}
	}
	return ns, ns.Validate()
}

func (a *App) changeVPSAddress(r addressRequest, probe func(string, int) (string, string, error)) (addressPlan, error) {
	s := a.settingsCopy()
	host, p, err := planAddress(s, r)
	if err != nil {
		return p, err
	}
	if r.Apply {
		if _, err = rewriteAddress(s, r, p); err != nil {
			return p, err
		}
	}
	// Public host-key handshake only: no password, remote command or network
	// reconfiguration. A restored server must prove the saved SSH identity.
	key, _, err := probe(p.Address, host.SSHPort)
	if err != nil {
		return p, errors.New("SSH по новому адресу недоступен. Восстановите сеть через консоль Fornex и проверьте порт SSH; настройки не изменены")
	}
	if key != host.HostKey {
		return p, errors.New("Ключ SSH по новому адресу отличается. Сверьте его в консоли хостинга; автоматическая замена отменена")
	}
	if !r.Apply {
		return p, nil
	}
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	if a.life.want.Load() || (a.core != nil && (a.core.Status().Running || a.core.Status().Starting)) {
		return p, errors.New("Сначала отключите VPN, затем сохраните новый адрес")
	}
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	a.friends.ops.Lock()
	defer a.friends.ops.Unlock()
	if a.installer != nil {
		a.installer.mu.Lock()
		defer a.installer.mu.Unlock()
		if a.installer.state.Active {
			return p, errors.New("Дождитесь завершения установки на VPS")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, fresh, err := planAddress(a.settings, r)
	if err != nil {
		return p, err
	}
	ns, err := rewriteAddress(a.settings, r, fresh)
	if err != nil {
		return p, err
	}
	backup, err := os.MkdirTemp(a.dir, "address-backup-")
	if err != nil {
		return p, err
	}
	if err = SaveSettings(backup, a.settings); err != nil {
		_ = os.RemoveAll(backup)
		return p, err
	}
	if err = SaveSettings(a.dir, ns); err != nil {
		return p, fmt.Errorf("Не удалось сохранить адрес; прежние настройки сохранены: %w", err)
	}
	a.settings = ns
	a.selectionRev++
	a.scores.reset()
	a.fo.clear()
	a.friends.drop(host.ID, nil)
	if a.installer != nil {
		a.installer.tunMu.Lock()
		for _, panel := range p.Panels {
			if t := a.installer.tunnels[panel.ID]; t != nil {
				t.close()
				delete(a.installer.tunnels, panel.ID)
			}
		}
		a.installer.tunMu.Unlock()
	}
	p.Backup = filepath.Base(backup)
	return p, nil
}

func (a *App) addressRoutes(api func(string, string, apiFunc)) {
	api("POST", "/api/ownhost/address", func(r *http.Request) (any, error) {
		var req addressRequest
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		plan, err := a.changeVPSAddress(req, hostKey)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"plan": plan}
		if req.Apply {
			result["settings"] = a.settingsCopy()
		}
		return result, nil
	})
}
