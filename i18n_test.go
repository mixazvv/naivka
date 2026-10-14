package main

import "testing"

// Словарь общий со страницей: точные фразы, образцы с подстановками и склеенные строки.
func TestTranslator(t *testing.T) {
	tr := loadTranslator()
	if len(tr.exact) < 500 || len(tr.patterns) < 100 {
		t.Fatalf("dictionary not loaded: %d exact, %d patterns", len(tr.exact), len(tr.patterns))
	}
	cases := map[string]string{
		"Подключено":         "Connected",
		"Naivka: подключено": "Naivka: connected",
		"Порт локального прокси сменён на 2081":     "Local proxy port changed to 2081",
		"zapret включён: стратегия «general (ALT)»": "zapret on: strategy “general (ALT)”",
		"Ядро упало: exit status 1":                 "The core crashed: exit status 1",
		"no cyrillic here":                          "no cyrillic here",
	}
	for in, want := range cases {
		if got := tr.translate(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	// Слово внутри другого слова не заменяется: «сервер» в «серверов».
	if got := tr.translate("3 серверов"); got != "3 servers" {
		t.Errorf("plural → %q", got)
	}
}
