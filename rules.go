package main

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
)

// Формат экспорта правил (JSON).
type RulesFile struct {
	Format  string `json:"format"` // "naivka-rules"
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// Заголовки секций в текстовом списке: [Через сервер], [direct] и т. п.
var sectionAction = map[string]string{
	"через сервер": "proxy", "прокси": "proxy", "proxy": "proxy",
	"через zapret": "zapret", "zapret": "zapret",
	"напрямую": "direct", "direct": "direct",
	"блокировать": "block", "блок": "block", "block": "block", "reject": "block",
}

var actionTitle = map[string]string{"proxy": "Через сервер", "direct": "Напрямую", "block": "Блокировать", "zapret": "Через zapret"}

// RulesAsText — текстовый экспорт, который ParseRules читает обратно.
func RulesAsText(rules []Rule) string {
	var b strings.Builder
	for _, act := range []string{"proxy", "direct", "zapret", "block"} {
		first := true
		for _, r := range rules {
			if r.Action != act {
				continue
			}
			if first {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString("[" + actionTitle[act] + "]\n")
				first = false
			}
			b.WriteString(r.Value + "\n")
		}
	}
	return b.String()
}

type ParseResult struct {
	Rules    []Rule   `json:"rules"`
	Errors   []string `json:"errors"`
	FromJSON bool     `json:"fromJson"` // действия взяты из файла
}

// ParseRules разбирает либо JSON-экспорт Naivka, либо текст: по одному значению в строке
// (или через запятую/пробел). Тип определяется сам: IP/подсеть, программа (.exe), домен.
func ParseRules(text, action string) ParseResult {
	res := ParseResult{Rules: []Rule{}, Errors: []string{}}
	if _, ok := actionTitle[action]; !ok {
		action = "proxy"
	}
	t := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))

	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[{") {
		var rules []Rule
		var f RulesFile
		if err := json.Unmarshal([]byte(t), &f); err == nil && f.Rules != nil {
			rules = f.Rules
		} else if err := json.Unmarshal([]byte(t), &rules); err != nil {
			res.Errors = append(res.Errors, "Файл похож на JSON, но не читается: "+err.Error())
			return res
		}
		res.FromJSON = true
		seen := map[string]bool{}
		for _, r := range rules {
			act := r.Action
			if _, ok := actionTitle[act]; !ok {
				act = action
			}
			nr, err := normalizeValue(r.Value, r.Kind)
			if err != nil {
				res.Errors = append(res.Errors, err.Error())
				continue
			}
			nr.Action = act
			key := nr.Kind + "|" + nr.Value
			if !seen[key] {
				seen[key] = true
				res.Rules = append(res.Rules, nr)
			}
		}
		return res
	}

	seen := map[string]bool{}
	cur := action
	for _, raw := range strings.Split(t, "\n") {
		line := raw
		if i := strings.IndexAny(line, "#"); i >= 0 {
			line = line[:i]
		}
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line, "://") {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// [Через сервер] — смена действия для следующих строк
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if a, ok := sectionAction[strings.ToLower(strings.TrimSpace(line[1:len(line)-1]))]; ok {
				cur = a
				continue
			}
		}
		var tokens []string
		lower := strings.ToLower(line)
		switch {
		case strings.HasSuffix(lower, ".exe") && !strings.ContainsAny(line, ",;"):
			tokens = []string{line} // имя программы может содержать пробелы
		default:
			tokens = strings.FieldsFunc(line, func(r rune) bool {
				return r == ',' || r == ';' || r == ' ' || r == '\t'
			})
			// Строка hosts-файла: «0.0.0.0 ads.example.com»
			if len(tokens) > 1 && (tokens[0] == "0.0.0.0" || tokens[0] == "127.0.0.1" || tokens[0] == "::") {
				tokens = tokens[1:]
			}
		}
		for _, tok := range tokens {
			r, err := normalizeValue(tok, "")
			if err != nil {
				res.Errors = append(res.Errors, err.Error())
				continue
			}
			r.Action = cur
			if cur == "zapret" && r.Kind != "domain" {
				r.Action = "proxy"
			}
			key := r.Kind + "|" + r.Value
			if !seen[key] {
				seen[key] = true
				res.Rules = append(res.Rules, r)
			}
		}
	}
	return res
}

var ipLikeRe = regexp.MustCompile(`^[0-9./]+$`)

var labelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

// normalizeValue приводит значение к виду, который понимает sing-box.
// kind == "" — определить автоматически.
func normalizeValue(v, kind string) (Rule, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return Rule{}, fmt.Errorf("пустое значение")
	}
	if kind == "" {
		kind = detectKind(v)
	}
	switch kind {
	case "process":
		name := v
		if strings.ContainsAny(name, `\/`) {
			name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
		}
		return Rule{Kind: "process", Value: name}, nil
	case "ip":
		if strings.Contains(v, "/") {
			_, n, err := net.ParseCIDR(v)
			if err != nil {
				return Rule{}, fmt.Errorf("«%s» — неверная подсеть", short(v))
			}
			return Rule{Kind: "ip", Value: n.String()}, nil
		}
		ip := net.ParseIP(strings.Trim(v, "[]"))
		if ip == nil {
			return Rule{}, fmt.Errorf("«%s» — неверный IP-адрес", short(v))
		}
		return Rule{Kind: "ip", Value: ip.String()}, nil
	case "domain":
		d, err := normalizeDomain(v)
		if err != nil {
			return Rule{}, err
		}
		return Rule{Kind: "domain", Value: d}, nil
	}
	return Rule{}, fmt.Errorf("«%s»: неизвестный тип %q", short(v), kind)
}

func detectKind(v string) string {
	lv := strings.ToLower(v)
	if strings.HasSuffix(lv, ".exe") {
		return "process"
	}
	if strings.Contains(v, "/") && !strings.Contains(v, "://") {
		if _, _, err := net.ParseCIDR(v); err == nil {
			return "ip"
		}
	}
	if net.ParseIP(strings.Trim(v, "[]")) != nil {
		return "ip"
	}
	// похоже на IPv4, но с ошибкой — пусть ругается как IP, а не как домен
	if ipLikeRe.MatchString(v) {
		return "ip"
	}
	return "domain"
}

func normalizeDomain(v string) (string, error) {
	orig := v
	d := strings.ToLower(strings.TrimSpace(v))
	for _, p := range []string{"domain:", "full:", "domain_suffix:"} {
		d = strings.TrimPrefix(d, p)
	}
	if strings.HasPrefix(d, "keyword:") || strings.HasPrefix(d, "regexp:") {
		return "", fmt.Errorf("«%s»: ключевые слова и регулярки — через «Дополнительные правила sing-box»", short(orig))
	}
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndex(d, "@"); i >= 0 {
		d = d[i+1:]
	}
	if h, _, err := net.SplitHostPort(d); err == nil {
		d = h
	}
	d = strings.TrimPrefix(d, "*.")
	d = strings.Trim(d, ".")
	// www.youtube.com из ссылки — это сайт youtube.com целиком
	if strings.HasPrefix(d, "www.") && strings.Count(d, ".") >= 2 {
		d = d[4:]
	}
	if d == "" {
		return "", fmt.Errorf("«%s» — не домен", short(orig))
	}
	labels := strings.Split(d, ".")
	for i, l := range labels {
		if l == "" {
			return "", fmt.Errorf("«%s» — не домен", short(orig))
		}
		if hasNonASCII(l) {
			l = "xn--" + punyEncode(l)
			labels[i] = l
		}
		if len(l) > 63 || !labelRe.MatchString(l) {
			return "", fmt.Errorf("«%s» — не домен", short(orig))
		}
	}
	d = strings.Join(labels, ".")
	if len(d) > 253 {
		return "", fmt.Errorf("«%s» — слишком длинный домен", short(orig))
	}
	return d, nil
}

func hasNonASCII(s string) bool {
	for _, r := range s {
		if r >= 0x80 {
			return true
		}
	}
	return false
}

// punyEncode — Punycode (RFC 3492) для кириллических доменов: «рф» → «p1ai».
func punyEncode(s string) string {
	const (
		base, tmin, tmax = 36, 1, 26
		skew, damp       = 38, 700
		initialBias      = 72
		initialN         = 128
	)
	adapt := func(delta, numPoints int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / numPoints
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	runes := []rune(s)
	var out []byte
	for _, r := range runes {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	n, delta, bias := initialN, 0, initialBias
	for h < len(runes) {
		m := int(^uint(0) >> 1)
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tmin {
						t = tmin
					} else if t > tmax {
						t = tmax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out)
}
