package main

import (
	"fmt"
	"strings"
)

const secretPrefix = "dpapi:"

// encryptSecrets включается в Windows, в том числе в портативном режиме. Ключ привязан
// к учётной записи Windows: на другом компьютере пароли серверов придётся ввести заново,
// а переносить настройки целиком — резервной копией (она расшифровывает пароли и защищает своим).
var encryptSecrets bool

func sealSettings(s *Settings) (*Settings, error) {
	if !encryptSecrets {
		return s, nil
	}
	c := s.clone()
	// Не удалось зашифровать хоть одно поле — не сохраняем вовсе: открытым текстом секрет на диск не попадёт,
	// на диске останутся прежние (зашифрованные) настройки.
	var failed error
	seal := func(f *string) {
		if failed != nil || *f == "" || strings.HasPrefix(*f, secretPrefix) {
			return
		}
		enc, err := protectSecret(*f)
		if err != nil {
			failed = fmt.Errorf("не удалось зашифровать секреты настроек средствами Windows, настройки не сохранены: %v", err)
			return
		}
		*f = enc
	}
	seal(&c.Share.Password)
	seal(&c.Telegram.Secret)
	for i := range c.Panels {
		// Секретный путь и логин панели — такая же тайна, как пароль: по ним панель находят и подбирают.
		for _, p := range []*string{&c.Panels[i].Pass, &c.Panels[i].Path, &c.Panels[i].User} {
			seal(p)
		}
	}
	for i := range c.Servers {
		for _, f := range []*string{&c.Servers[i].Password, &c.Servers[i].UUID, &c.Servers[i].ObfsPassword} {
			seal(f)
		}
	}
	if failed != nil {
		return nil, failed
	}
	return c, nil
}

func openSettings(s *Settings) []string {
	var problems []string
	if strings.HasPrefix(s.Share.Password, secretPrefix) {
		if plain, err := unprotectSecret(s.Share.Password); err == nil {
			s.Share.Password = plain
		} else {
			s.Share.Password = "" // сгенерируется новый
		}
	}
	if strings.HasPrefix(s.Telegram.Secret, secretPrefix) {
		if plain, err := unprotectSecret(s.Telegram.Secret); err == nil {
			s.Telegram.Secret = plain
		} else {
			s.Telegram.Secret = "" // сгенерируется новый, ссылку tg:// придётся открыть заново
		}
	}
	for i := range s.Panels {
		for _, p := range []*string{&s.Panels[i].Pass, &s.Panels[i].Path, &s.Panels[i].User} {
			if strings.HasPrefix(*p, secretPrefix) {
				if plain, err := unprotectSecret(*p); err == nil {
					*p = plain
				} else {
					*p = ""
					problems = append(problems, fmt.Sprintf("панель %s: данные входа не расшифровались — сбросьте их на сервере", s.Panels[i].Host))
				}
			}
		}
	}
	for i := range s.Servers {
		sv := &s.Servers[i]
		for _, f := range []*string{&sv.Password, &sv.UUID, &sv.ObfsPassword} {
			if strings.HasPrefix(*f, secretPrefix) {
				plain, err := unprotectSecret(*f)
				if err != nil {
					problems = append(problems, fmt.Sprintf("сервер «%s»: %v — введите пароль заново", sv.Name, err))
					plain = ""
				}
				*f = plain
			}
		}
	}
	return problems
}
