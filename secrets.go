package main

import (
	"fmt"
	"strings"
)

const secretPrefix = "dpapi:"

// encryptSecrets включается в обычном режиме Windows. В портативном — нет:
// зашифрованные пароли нельзя было бы открыть на другом компьютере.
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
		if p := &c.Panels[i].Pass; *p != "" && !strings.HasPrefix(*p, secretPrefix) {
			if enc, err := protectSecret(*p); err == nil {
				*p = enc
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
		if p := &s.Panels[i].Pass; strings.HasPrefix(*p, secretPrefix) {
			if plain, err := unprotectSecret(*p); err == nil {
				*p = plain
			} else {
				*p = ""
				problems = append(problems, fmt.Sprintf("панель %s: пароль не расшифровался — сбросьте его на сервере", s.Panels[i].Host))
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
