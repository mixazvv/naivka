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

func sealSettings(s *Settings) *Settings {
	if !encryptSecrets {
		return s
	}
	c := s.clone()
	if c.Share.Password != "" && !strings.HasPrefix(c.Share.Password, secretPrefix) {
		if enc, err := protectSecret(c.Share.Password); err == nil {
			c.Share.Password = enc
		}
	}
	for i := range c.Panels {
		// Секретный путь и логин панели — такая же тайна, как пароль: по ним панель находят и подбирают.
		for _, p := range []*string{&c.Panels[i].Pass, &c.Panels[i].Path, &c.Panels[i].User} {
			if *p != "" && !strings.HasPrefix(*p, secretPrefix) {
				if enc, err := protectSecret(*p); err == nil {
					*p = enc
				}
			}
		}
	}
	for i := range c.Servers {
		for _, f := range []*string{&c.Servers[i].Password, &c.Servers[i].UUID, &c.Servers[i].ObfsPassword} {
			if *f != "" && !strings.HasPrefix(*f, secretPrefix) {
				if enc, err := protectSecret(*f); err == nil {
					*f = enc
				}
			}
		}
	}
	return c
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
