package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Client is opt-in. A provider-scoped installation ID avoids exposing hardware
// serial numbers or sharing one identifier with unrelated subscription services.
func validSubscriptionClient(client string) bool {
	return client == "" || client == "device" || client == "happ"
}

func subscriptionDeviceID(dir, rawURL string) (string, error) {
	path := filepath.Join(dir, "subscription-device-id")
	seed, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		seed = make([]byte, 32)
		if _, err = rand.Read(seed); err != nil {
			return "", errors.New("Не удалось создать идентификатор установки")
		}
		// Callers hold subscriptions.mu. Persist before the first request, even
		// when preview later fails, so retries do not occupy new device slots.
		if err = os.WriteFile(path+".tmp", seed, 0600); err == nil {
			err = os.Rename(path+".tmp", path)
		}
	}
	if err != nil || len(seed) != 32 {
		return "", errors.New("Не удалось прочитать или сохранить идентификатор установки")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("Некорректная ссылка подписки")
	}
	sum := sha256.Sum256(append(seed, []byte("\x00"+strings.ToLower(u.Hostname()))...))
	return hex.EncodeToString(sum[:]), nil
}

type subscriptionFetchOptions struct {
	client   string
	deviceID string
}

func subscriptionProviderError(h http.Header) error {
	flag := func(name string) bool {
		v := strings.TrimSpace(h.Get(name))
		return v == "1" || strings.EqualFold(v, "true")
	}
	switch {
	case flag("x-hwid-max-devices-reached") || flag("x-hwid-limit"):
		return errors.New("Достигнут лимит устройств подписки. Освободите устройство у провайдера; прежние серверы сохранены")
	case flag("x-hwid-not-supported"):
		return errors.New("Сервис требует идентификатор устройства. Выберите режим «С идентификатором устройства» или «Совместимость с Happ»; прежние серверы сохранены")
	}
	return nil
}

func (a *App) apiSubscriptionClient(r *http.Request) (any, error) {
	var req struct {
		ID     string `json:"id"`
		Client string `json:"client"`
	}
	if e := decode(r, &req); e != nil {
		return nil, e
	}
	if !validSubscriptionClient(req.Client) {
		return nil, errors.New("Неизвестный режим подписки")
	}
	a.subscriptions.mu.Lock()
	defer a.subscriptions.mu.Unlock()
	s, e := a.commitSubscriptions(func(s *Settings) error {
		for i := range s.Subscriptions {
			if s.Subscriptions[i].ID == req.ID {
				s.Subscriptions[i].Client = req.Client
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
