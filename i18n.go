package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Английский для того, что рисует сама Naivka: меню и подсказка в трее, уведомления Windows.
// Словарь общий со страницей (web/i18n/en.js), порядок перевода тот же, что в web/i18n.js:
// точная фраза → образец с подстановками → замена известных кусков целыми словами.

type translator struct {
	exact    map[string]string
	patterns []trPattern
	frags    [][2]string
	mu       sync.Mutex
	memo     map[string]string
}

type trPattern struct {
	re *regexp.Regexp
	en string
}

var (
	trOnce sync.Once
	trEN   *translator
	trArg  = regexp.MustCompile(`\{(\d+)\}`)
)

func loadTranslator() *translator {
	t := &translator{exact: map[string]string{}, memo: map[string]string{}}
	b, err := webFS.ReadFile("web/i18n/en.js")
	if err != nil {
		return t
	}
	s := string(b)
	i := strings.Index(s, "window.I18N_EN=")
	if i < 0 {
		return t
	}
	s = strings.TrimRight(strings.TrimSpace(s[i+len("window.I18N_EN="):]), ";")
	var raw struct {
		Exact    map[string]string `json:"exact"`
		Patterns [][2]string       `json:"patterns"`
	}
	if json.Unmarshal([]byte(s), &raw) != nil {
		return t
	}
	t.exact = raw.Exact
	for _, p := range raw.Patterns {
		if re, err := regexp.Compile("(?s)" + p[0]); err == nil {
			t.patterns = append(t.patterns, trPattern{re, p[1]})
		}
	}
	for ru, en := range raw.Exact {
		if len([]rune(strings.TrimSpace(ru))) >= 3 && hasCyrillic(ru) {
			t.frags = append(t.frags, [2]string{ru, en})
		}
	}
	sort.Slice(t.frags, func(i, j int) bool { return len(t.frags[i][0]) > len(t.frags[j][0]) })
	return t
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// translate переводит строку на английский; без кириллицы возвращает как есть.
func (t *translator) translate(s string) string {
	if s == "" || !hasCyrillic(s) {
		return s
	}
	t.mu.Lock()
	r, ok := t.memo[s]
	t.mu.Unlock()
	if ok {
		return r
	}
	r = strings.NewReplacer("«", "“", "»", "”").Replace(t.core(s))
	t.mu.Lock()
	if len(t.memo) > 5000 {
		t.memo = map[string]string{}
	}
	t.memo[s] = r
	t.mu.Unlock()
	return r
}

func (t *translator) core(s string) string {
	trimmed := strings.TrimSpace(s)
	lead := s[:strings.Index(s, trimmed)]
	trail := s[len(lead)+len(trimmed):]
	if en, ok := t.exact[trimmed]; ok {
		return lead + en + trail
	}
	for _, p := range t.patterns {
		if m := p.re.FindStringSubmatch(trimmed); m != nil {
			out := trArg.ReplaceAllStringFunc(p.en, func(x string) string {
				n, _ := strconv.Atoi(x[1 : len(x)-1])
				if n < len(m) {
					return t.translate(m[n])
				}
				return ""
			})
			return lead + out + trail
		}
	}
	out := s
	for _, f := range t.frags {
		if !hasCyrillic(out) {
			break
		}
		ru, en := f[0], f[1]
		from := 0
		for {
			i := strings.Index(out[from:], ru)
			if i < 0 {
				break
			}
			i += from
			before, after := lastRune(out[:i]), firstRune(out[i+len(ru):])
			first, last := firstRune(ru), lastRune(ru)
			ok := (!unicode.Is(unicode.Cyrillic, first) || !isWordRune(before)) && (!unicode.Is(unicode.Cyrillic, last) || !isWordRune(after))
			if ok {
				out = out[:i] + en + out[i+len(ru):]
				from = i + len(en)
			} else {
				from = i + len(ru)
			}
		}
	}
	return out
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	if s == "" {
		return 0
	}
	r := []rune(s)
	return r[len(r)-1]
}

// tr — текст на языке интерфейса, выбранном в настройках.
func (a *App) tr(s string) string {
	if a.settingsCopy().Lang != "en" {
		return s
	}
	trOnce.Do(func() { trEN = loadTranslator() })
	return trEN.translate(s)
}
