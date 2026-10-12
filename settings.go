package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Server — один сервер NaiveProxy.
type Server struct {
	ID   string `json:"id"`
	Type string `json:"type"` // naive | vless
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	SNI  string `json:"sni"` // если пусто — используется Host

	// Naive
	Username string `json:"username"`
	Password string `json:"password"`
	QUIC     bool   `json:"quic"` // HTTP/3 вместо HTTP/2
	UoT      bool   `json:"uot"`  // UDP поверх TCP (только если сервер — sing-box)

	// VLESS
	UUID        string `json:"uuid"`
	Security    string `json:"security"`    // reality | tls | none
	PublicKey   string `json:"publicKey"`   // Reality
	ShortID     string `json:"shortId"`     // Reality
	Flow        string `json:"flow"`        // xtls-rprx-vision или пусто
	Fingerprint string `json:"fingerprint"` // chrome
	Transport   string `json:"transport"`   // tcp | ws | grpc | httpupgrade
	Path        string `json:"path"`        // ws/httpupgrade
	HostHeader  string `json:"hostHeader"`  // ws/httpupgrade
	ServiceName string `json:"serviceName"` // grpc
	ALPN        string `json:"alpn"`        // через запятую
	Insecure    bool   `json:"insecure"`    // не проверять сертификат TLS
	XMode       string `json:"xmode"`       // xHTTP: auto | packet-up | stream-up | stream-one
	XExtra      string `json:"xextra"`      // xHTTP: дополнительные настройки (JSON)
}

// Rule — пользовательское правило маршрутизации.
type Rule struct {
	Kind   string `json:"kind"`   // domain | ip | process
	Value  string `json:"value"`  // example.com | 1.2.3.0/24 | Telegram.exe
	Action string `json:"action"` // direct | proxy | block
}

// ZapretSettings — обход DPI без сервера (zapret от Flowseal).
type ZapretSettings struct {
	Enabled     bool   `json:"enabled"`
	Strategy    string `json:"strategy"`    // имя bat-файла без .bat, например "general (ALT)"
	GameFilter  string `json:"gameFilter"`  // off | all | tcp | udp
	IPSet       string `json:"ipset"`       // none | loaded | any
	RouteDirect bool   `json:"routeDirect"` // сайты из списков zapret — напрямую, а не через сервер
	Fallback    bool   `json:"fallback"`    // zapret перестал справляться — пустить эти сайты через сервер
}

// TelegramSettings — tg-ws-proxy (Flowseal): Telegram Desktop без сервера.
type TelegramSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Secret  string `json:"secret"`
}

// WLSettings — запасная подписка для белых списков (zieng2/wl).
type WLSettings struct {
	Enabled   bool `json:"enabled"`
	IncludeRU bool `json:"includeRu"` // российские серверы (через них заблокированное не открывается)
	Fallback  bool `json:"fallback"`  // переключаться на подписку, когда свои серверы не отвечают
	Max       int  `json:"max"`       // сколько серверов брать
}

// ShareSettings — раздача подключения другим устройствам в домашней сети.
type ShareSettings struct {
	Enabled  bool   `json:"enabled"`
	Port     int    `json:"port"`
	Auth     bool   `json:"auth"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type WindowGeom struct {
	X, Y, W, H int
	Max        bool
}

type Settings struct {
	Servers  []Server `json:"servers"`
	Selected string   `json:"selected"` // "auto" или ID сервера

	Mode    string `json:"mode"`    // proxy | tun
	Routing string `json:"routing"` // ru-direct | blocked-only | global

	RuInsideDirect bool `json:"ruInsideDirect"` // сайты, работающие только из РФ, — напрямую
	BlockQUIC      bool `json:"blockQuic"`      // отклонять UDP/443
	UDPDirect      bool `json:"udpDirect"`      // остальной UDP — напрямую

	MixedPort       int    `json:"mixedPort"`
	DNSRemote       string `json:"dnsRemote"` // DoH через сервер: IP, домен или https://host/path
	DNSDirect       string `json:"dnsDirect"` // UDP-DNS напрямую: IP или "local"
	IPv4Only        bool   `json:"ipv4Only"`
	RuleSetViaProxy bool   `json:"ruleSetViaProxy"`

	TestURL      string `json:"testUrl"`
	TestInterval string `json:"testInterval"`

	Rules          []Rule `json:"rules"`
	ExtraRules     string `json:"extraRules"`     // JSON-массив правил route.rules
	ExtraOutbounds string `json:"extraOutbounds"` // JSON-массив outbounds
	Override       string `json:"override"`       // JSON-объект, накладывается на итоговый конфиг

	ShowIP         bool             `json:"showIp"` // определять IP и страну через внешние сервисы
	KillSwitch     bool             `json:"killSwitch"`
	Watchdog       bool             `json:"watchdog"`
	Notify         bool             `json:"notify"`
	AutoStart      bool             `json:"autoStart"`
	Theme          string           `json:"theme"` // system | dark | light
	Window         *WindowGeom      `json:"window,omitempty"`
	Zapret         ZapretSettings   `json:"zapret"`
	Telegram       TelegramSettings `json:"telegram"`
	UpdateCheck    bool             `json:"updateCheck"`
	WL             WLSettings       `json:"wl"`
	Share          ShareSettings    `json:"share"`
	AdBlock        bool             `json:"adBlock"`
	TwitchAdFree   bool             `json:"twitchAdFree"` // плейлисты Twitch — через прокси без рекламы
	UDPProxy       bool             `json:"udpProxy"`     // UDP (голос Discord, игры) — через сервер VLESS
	LogLevel       string           `json:"logLevel"`
	AutoConnect    bool             `json:"autoConnect"`
	ExitWithWindow bool             `json:"exitWithWindow"`
}

func DefaultSettings() *Settings {
	return &Settings{
		Servers:        []Server{},
		Selected:       "auto",
		Mode:           "proxy",
		Routing:        "ru-direct",
		RuInsideDirect: true,
		BlockQUIC:      true,
		UDPDirect:      true,
		MixedPort:      2080,
		DNSRemote:      "1.1.1.1",
		DNSDirect:      "77.88.8.8",
		IPv4Only:       true,
		TestURL:        "https://www.gstatic.com/generate_204",
		TestInterval:   "3m",
		Rules:          []Rule{},
		LogLevel:       "warn",
		ShowIP:         true,
		KillSwitch:     true,
		Watchdog:       true,
		Notify:         true,
		Theme:          "dark",
		UpdateCheck:    true,
		Zapret:         ZapretSettings{Strategy: "general", GameFilter: "off", IPSet: "none", RouteDirect: true, Fallback: true},
		Telegram:       TelegramSettings{Port: 1443},
		WL:             WLSettings{Fallback: true, Max: 30},
		Share:          ShareSettings{Port: 2090, Auth: true, Username: "naivka"},
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func settingsPath(dir string) string { return filepath.Join(dir, "settings.json") }

func LoadSettings(dir string) (*Settings, error) {
	s := DefaultSettings()
	b, err := os.ReadFile(settingsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		// Не затираем испорченный файл молча — сохраняем копию.
		_ = os.WriteFile(settingsPath(dir)+".broken", b, 0o600)
		return DefaultSettings(), fmt.Errorf("settings.json повреждён, загружены настройки по умолчанию (копия сохранена в settings.json.broken): %w", err)
	}
	problems := openSettings(s)
	s.normalize()
	if len(problems) > 0 {
		return s, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return s, nil
}

func SaveSettings(dir string, s *Settings) error {
	b, err := json.MarshalIndent(sealSettings(s), "", "  ")
	if err != nil {
		return err
	}
	tmp := settingsPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath(dir))
}

func (s *Settings) clone() *Settings {
	b, _ := json.Marshal(s)
	c := &Settings{}
	_ = json.Unmarshal(b, c)
	return c
}

// fingerprint — всё, что требует перезапуска ядра (выбор сервера применяется на лету).
func (s *Settings) fingerprint() string {
	c := s.clone()
	c.Selected = ""
	c.AutoConnect = false
	c.ExitWithWindow = false
	c.Window = nil
	c.Theme = ""
	c.Notify, c.ShowIP, c.Watchdog, c.KillSwitch, c.AutoStart = false, false, false, false, false
	c.UpdateCheck = false
	// На ядро влияют только «включён» и «напрямую»; стратегия и фильтры — дело самого zapret.
	c.Zapret = ZapretSettings{Enabled: s.Zapret.Enabled, RouteDirect: s.Zapret.RouteDirect}
	c.Telegram = TelegramSettings{Enabled: s.Telegram.Enabled}
	c.WL = WLSettings{Enabled: s.WL.Enabled, IncludeRU: s.WL.IncludeRU, Max: s.WL.Max}
	b, _ := json.Marshal(c)
	return string(b)
}

func (s *Settings) normalize() {
	d := DefaultSettings()
	if s.Mode != "proxy" && s.Mode != "tun" {
		s.Mode = d.Mode
	}
	switch s.Routing {
	case "ru-direct", "blocked-only", "global":
	default:
		s.Routing = d.Routing
	}
	if s.MixedPort <= 0 || s.MixedPort > 65535 {
		s.MixedPort = d.MixedPort
	}
	s.DNSRemote = strings.TrimSpace(s.DNSRemote)
	if s.DNSRemote == "" {
		s.DNSRemote = d.DNSRemote
	}
	s.DNSDirect = strings.TrimSpace(s.DNSDirect)
	if s.DNSDirect == "" {
		s.DNSDirect = d.DNSDirect
	}
	if strings.TrimSpace(s.TestURL) == "" {
		s.TestURL = d.TestURL
	}
	if strings.TrimSpace(s.TestInterval) == "" {
		s.TestInterval = d.TestInterval
	}
	switch s.LogLevel {
	case "trace", "debug", "info", "warn", "error":
	default:
		s.LogLevel = d.LogLevel
	}
	if s.Servers == nil {
		s.Servers = []Server{}
	}
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	switch s.Zapret.GameFilter {
	case "off", "all", "tcp", "udp":
	default:
		s.Zapret.GameFilter = "off"
	}
	switch s.Zapret.IPSet {
	case "none", "loaded", "any":
	default:
		s.Zapret.IPSet = "none"
	}
	if s.Zapret.Strategy == "" {
		s.Zapret.Strategy = "general"
	}
	if s.WL.Max <= 0 || s.WL.Max > 100 {
		s.WL.Max = 30
	}
	if s.Share.Port <= 0 || s.Share.Port > 65535 {
		s.Share.Port = 2090
	}
	if s.Share.Username == "" {
		s.Share.Username = "naivka"
	}
	if s.Share.Password == "" {
		s.Share.Password = randHex(6)
	}
	if s.Telegram.Port <= 0 || s.Telegram.Port > 65535 {
		s.Telegram.Port = 1443
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(s.Telegram.Secret) {
		s.Telegram.Secret = randHex(16)
	}
	switch s.Theme {
	case "system", "dark", "light":
	default:
		s.Theme = "dark"
	}
	for i := range s.Servers {
		sv := &s.Servers[i]
		if sv.ID == "" {
			sv.ID = newID()
		}
		if sv.Type != "vless" {
			sv.Type = "naive"
		}
		if sv.Type == "vless" {
			sv.UUID = strings.TrimSpace(sv.UUID)
			sv.PublicKey = strings.TrimSpace(sv.PublicKey)
			sv.ShortID = strings.ToLower(strings.TrimSpace(sv.ShortID))
			if sv.Security == "" {
				sv.Security = "reality" // так сохранены серверы из прошлых версий
			}
			if sv.Transport == "" || sv.Transport == "raw" {
				sv.Transport = "tcp"
			}
			if sv.Fingerprint == "" && sv.Security != "none" {
				sv.Fingerprint = "chrome"
			}
		}
		sv.Host = strings.TrimSpace(sv.Host)
		sv.SNI = strings.TrimSpace(sv.SNI)
		sv.Name = strings.TrimSpace(sv.Name)
		if sv.Name == "" {
			sv.Name = sv.Host
		}
		if sv.Port == 0 {
			sv.Port = 443
		}
	}
	for i := range s.Rules {
		s.Rules[i].Value = strings.TrimSpace(s.Rules[i].Value)
	}
	if s.Selected == "" {
		s.Selected = "auto"
	}
	if s.Selected != "auto" && s.Selected != "wl" && !strings.HasPrefix(s.Selected, "w-") {
		found := false
		for _, sv := range s.Servers {
			if sv.ID == s.Selected {
				found = true
			}
		}
		if !found {
			s.Selected = "auto"
		}
	}
}

func (s *Settings) Validate() error {
	ids := map[string]bool{}
	for _, sv := range s.Servers {
		if sv.Host == "" {
			return fmt.Errorf("У сервера «%s» не указан адрес", sv.Name)
		}
		if sv.Port < 1 || sv.Port > 65535 {
			return fmt.Errorf("У сервера «%s» неверный порт %d", sv.Name, sv.Port)
		}
		if ids[sv.ID] {
			return fmt.Errorf("Повторяющийся ID сервера %s", sv.ID)
		}
		ids[sv.ID] = true
		if sv.Type == "vless" {
			if !uuidRe.MatchString(sv.UUID) {
				return fmt.Errorf("У сервера «%s» неверный UUID", sv.Name)
			}
			if sv.Security == "reality" && !keyRe.MatchString(sv.PublicKey) {
				return fmt.Errorf("У сервера «%s» неверный публичный ключ Reality", sv.Name)
			}
			if sv.Security == "reality" && !shortIDRe.MatchString(sv.ShortID) {
				return fmt.Errorf("У сервера «%s» short id должен быть из 0–16 шестнадцатеричных символов", sv.Name)
			}
		}
	}
	for _, r := range s.Rules {
		if r.Value == "" {
			return errors.New("Есть правило с пустым значением")
		}
		switch r.Kind {
		case "domain", "process":
		case "ip":
			v := r.Value
			if !strings.Contains(v, "/") {
				if ip := net.ParseIP(v); ip == nil {
					return fmt.Errorf("«%s» — не IP-адрес и не подсеть", v)
				}
			} else if _, _, err := net.ParseCIDR(v); err != nil {
				return fmt.Errorf("«%s» — неверная подсеть", v)
			}
		default:
			return fmt.Errorf("Неизвестный тип правила %q", r.Kind)
		}
		switch r.Action {
		case "direct", "proxy", "block":
		case "zapret":
			if r.Kind != "domain" {
				return fmt.Errorf("«Через zapret» работает только для доменов (%s)", r.Value)
			}
		default:
			return fmt.Errorf("Неизвестное действие правила %q", r.Action)
		}
	}
	if s.Share.Enabled && (s.Share.Port == s.MixedPort || (s.Telegram.Enabled && s.Share.Port == s.Telegram.Port)) {
		return fmt.Errorf("Порт раздачи (%d) совпадает с другим портом Naivka — выберите другой", s.Share.Port)
	}
	if s.Telegram.Enabled && s.Telegram.Port == s.MixedPort {
		return fmt.Errorf("Порт локального прокси и порт Telegram совпадают (%d) — сделайте их разными", s.MixedPort)
	}
	if _, err := time.ParseDuration(s.TestInterval); err != nil {
		return fmt.Errorf("Интервал проверки «%s» не похож на длительность (пример: 3m, 90s)", s.TestInterval)
	}
	if u, err := url.Parse(s.TestURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("Адрес для проверки серверов должен начинаться с http:// или https://")
	}
	if s.DNSDirect != "local" && net.ParseIP(s.DNSDirect) == nil {
		return errors.New("«DNS напрямую» должен быть IP-адресом или словом local")
	}
	if _, _, err := parseDoH(s.DNSRemote); err != nil {
		return err
	}
	if err := checkJSON(s.ExtraRules, '[', "Дополнительные правила"); err != nil {
		return err
	}
	if err := checkJSON(s.ExtraOutbounds, '[', "Дополнительные outbounds"); err != nil {
		return err
	}
	if err := checkJSON(s.Override, '{', "Ручные правки конфига"); err != nil {
		return err
	}
	return nil
}

var (
	uuidRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	keyRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{43}=?$`)
	shortIDRe = regexp.MustCompile(`^[0-9a-f]{0,16}$`)
)

func checkJSON(text string, want byte, what string) error {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if t[0] != want {
		if want == '[' {
			return fmt.Errorf("%s: нужен JSON-массив, начинающийся с [", what)
		}
		return fmt.Errorf("%s: нужен JSON-объект, начинающийся с {", what)
	}
	var v any
	if err := json.Unmarshal([]byte(t), &v); err != nil {
		return fmt.Errorf("%s: ошибка JSON: %v", what, err)
	}
	return nil
}

// parseDoH принимает "1.1.1.1", "dns.google" или "https://dns.google/dns-query".
func parseDoH(v string) (host, path string, err error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "https://") {
		u, e := url.Parse(v)
		if e != nil || u.Hostname() == "" {
			return "", "", errors.New("Неверный адрес DNS через сервер")
		}
		host = u.Hostname()
		path = u.Path
		if u.Port() != "" {
			host = net.JoinHostPort(host, u.Port())
		}
		return host, path, nil
	}
	if v == "" || strings.ContainsAny(v, "/ ") {
		return "", "", errors.New("DNS через сервер: укажите IP, домен или https://адрес/путь")
	}
	return v, "", nil
}

// ParseShareLinks разбирает ссылки вида naive+https://user:pass@host:443#Имя
// и naive+quic://user:pass@host:443#Имя (по одной в строке).
func ParseShareLinks(text string) ([]Server, []string) {
	var out []Server
	var errs []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", short(line), err))
			continue
		}
		var quic bool
		switch strings.ToLower(u.Scheme) {
		case "naive+https", "naive":
		case "naive+quic":
			quic = true
		case "vless":
			sv, err := parseVLESS(u)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", short(line), err))
			} else {
				out = append(out, sv)
			}
			continue
		default:
			errs = append(errs, fmt.Sprintf("%s: ожидается naive+https://, naive+quic:// или vless://", short(line)))
			continue
		}
		if u.Hostname() == "" {
			errs = append(errs, fmt.Sprintf("%s: нет адреса сервера", short(line)))
			continue
		}
		port := 443
		if p := u.Port(); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				errs = append(errs, fmt.Sprintf("%s: неверный порт", short(line)))
				continue
			}
			port = n
		}
		sv := Server{ID: newID(), Type: "naive", Host: u.Hostname(), Port: port, QUIC: quic}
		if u.User != nil {
			sv.Username = u.User.Username()
			sv.Password, _ = u.User.Password()
		}
		sv.Name = strings.TrimSpace(u.Fragment)
		if sv.Name == "" {
			sv.Name = sv.Host
		}
		out = append(out, sv)
	}
	return out, errs
}

func short(s string) string {
	if len([]rune(s)) > 40 {
		return string([]rune(s)[:40]) + "…"
	}
	return s
}

// parseVLESS: vless://UUID@host:port?security=reality|tls|none&type=tcp|ws|grpc|httpupgrade&…#Имя
func parseVLESS(u *url.URL) (Server, error) {
	q := u.Query()
	if u.User == nil || u.Hostname() == "" {
		return Server{}, errors.New("нет UUID или адреса сервера")
	}
	port := 443
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return Server{}, errors.New("неверный порт")
		}
		port = n
	}
	sec := q.Get("security")
	switch sec {
	case "", "none":
		sec = "none"
	case "reality", "tls":
	default:
		return Server{}, fmt.Errorf("шифрование %q не поддерживается", sec)
	}
	tr := q.Get("type")
	switch tr {
	case "", "tcp", "raw":
		tr = "tcp"
		if ht := q.Get("headerType"); ht != "" && ht != "none" {
			return Server{}, fmt.Errorf("TCP с маскировкой %q не поддерживается", ht)
		}
	case "ws", "grpc", "httpupgrade":
	case "xhttp", "splithttp":
		tr = "xhttp" // sing-box его не умеет — такие серверы работают через Xray
	default:
		return Server{}, fmt.Errorf("транспорт %q не поддерживается", tr)
	}
	sv := Server{
		ID: newID(), Type: "vless", Host: u.Hostname(), Port: port,
		UUID: u.User.Username(), Security: sec, SNI: q.Get("sni"),
		PublicKey: q.Get("pbk"), ShortID: strings.ToLower(q.Get("sid")),
		Flow: q.Get("flow"), Fingerprint: q.Get("fp"), Transport: tr,
		Path: q.Get("path"), HostHeader: q.Get("host"), ServiceName: q.Get("serviceName"), ALPN: q.Get("alpn"),
		Insecure: q.Get("allowInsecure") == "1" || q.Get("allowInsecure") == "true" || q.Get("insecure") == "1",
	}
	if tr == "xhttp" {
		sv.XMode = q.Get("mode")
		if ex := strings.TrimSpace(q.Get("extra")); ex != "" && ex != "null" && ex != "{}" {
			var probe map[string]any
			if json.Unmarshal([]byte(ex), &probe) != nil {
				return Server{}, errors.New("неверный параметр extra у xHTTP")
			}
			sv.XExtra = ex
		}
	}
	if sv.Flow != "" && sv.Flow != "xtls-rprx-vision" {
		return Server{}, fmt.Errorf("flow %q не поддерживается", sv.Flow)
	}
	if tr != "tcp" {
		sv.Flow = "" // Vision работает только поверх TCP
	}
	if sv.Fingerprint == "" && sec != "none" {
		sv.Fingerprint = "chrome"
	}
	sv.Name = strings.TrimSpace(u.Fragment)
	if sv.Name == "" {
		sv.Name = sv.Host
	}
	if !uuidRe.MatchString(sv.UUID) {
		return Server{}, errors.New("неверный UUID")
	}
	if sec == "reality" && (!keyRe.MatchString(sv.PublicKey) || !shortIDRe.MatchString(sv.ShortID)) {
		return Server{}, errors.New("неверный ключ (pbk) или short id (sid) Reality")
	}
	return sv, nil
}

// ShareLink — ссылка для переноса сервера в другой клиент (QR-код для телефона).
func ShareLink(sv Server) string {
	name := url.PathEscape(sv.Name)
	host := net.JoinHostPort(sv.Host, strconv.Itoa(sv.Port))
	if sv.Type == "vless" {
		q := url.Values{}
		q.Set("encryption", "none")
		set := func(k, v string) {
			if v != "" {
				q.Set(k, v)
			}
		}
		set("flow", sv.Flow)
		q.Set("security", sv.Security)
		sni := sv.SNI
		if sni == "" && sv.Security != "none" {
			sni = sv.Host
		}
		set("sni", sni)
		set("fp", sv.Fingerprint)
		set("pbk", sv.PublicKey)
		set("sid", sv.ShortID)
		set("alpn", sv.ALPN)
		q.Set("type", sv.Transport)
		set("path", sv.Path)
		set("host", sv.HostHeader)
		set("serviceName", sv.ServiceName)
		set("mode", sv.XMode)
		set("extra", sv.XExtra)
		if sv.Insecure {
			q.Set("allowInsecure", "1")
		}
		return "vless://" + url.PathEscape(sv.UUID) + "@" + host + "?" + q.Encode() + "#" + name
	}
	scheme := "naive+https"
	if sv.QUIC {
		scheme = "naive+quic"
	}
	return scheme + "://" + url.UserPassword(sv.Username, sv.Password).String() + "@" + host + "#" + name
}
