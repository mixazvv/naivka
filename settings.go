package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server — один сервер.
type Server struct {
	ID   string `json:"id"`
	Type string `json:"type"` // naive | vless | hysteria2 | tuic | trojan | shadowsocks | anytls
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
	// Fragment — маскировка начала соединения с сервером (TLS): "" — нет, record — разбить
	// приветствие на несколько записей TLS, full — ещё и на отдельные пакеты. Помогает, когда
	// провайдер режет соединение с сервером по имени сайта в первом пакете.
	Fragment    string `json:"fragment,omitempty"`
	Transport   string `json:"transport"`   // tcp | ws | grpc | httpupgrade
	Path        string `json:"path"`        // ws/httpupgrade
	HostHeader  string `json:"hostHeader"`  // ws/httpupgrade
	ServiceName string `json:"serviceName"` // grpc
	ALPN        string `json:"alpn"`        // через запятую
	Insecure    bool   `json:"insecure"`    // не проверять сертификат TLS
	XMode       string `json:"xmode"`       // xHTTP: auto | packet-up | stream-up | stream-one
	XExtra      string `json:"xextra"`      // xHTTP: дополнительные настройки (JSON)

	// Hysteria2
	Obfs         string `json:"obfs,omitempty"`         // salamander или пусто
	ObfsPassword string `json:"obfsPassword,omitempty"` // пароль obfs
	UpMbps       int    `json:"upMbps,omitempty"`       // 0 — пусть решает BBR
	DownMbps     int    `json:"downMbps,omitempty"`

	// TUIC
	Congestion string `json:"congestion,omitempty"` // bbr | cubic | new_reno
	UDPRelay   string `json:"udpRelay,omitempty"`   // native | quic

	// Shadowsocks
	Method string `json:"method,omitempty"`

	// Hysteria2, TUIC, Trojan, AnyTLS: свой сертификат сервера, проверяемый по SHA-256 открытого ключа
	// (base64) вместо удостоверяющего центра — так ставит серверы «Свой сервер» в Naivka.
	PinSHA256 string `json:"pinSHA256,omitempty"`

	Fav bool `json:"fav,omitempty"` // избранный: выше в списках и в мини-окне
}

// Panel — веб-панель на своём сервере (3X-UI или s-ui). Пароль панели шифруется DPAPI, как пароли серверов;
// пароль SSH не хранится: туннель к панели открывается с ним заново.
type Panel struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // 3x-ui | s-ui
	Version string `json:"version"`
	Host    string `json:"host"`    // сервер
	SSHPort int    `json:"sshPort"` // для туннеля
	SSHUser string `json:"sshUser"`
	HostKey string `json:"hostKey"` // отпечаток ключа SSH, подтверждённый при установке
	Access  string `json:"access"`  // tunnel | public
	Port    int    `json:"port"`    // порт панели на сервере
	Path    string `json:"path"`    // секретный путь панели
	User    string `json:"user"`
	Pass    string `json:"pass"`
	URL     string `json:"url"` // для public
	Added   int64  `json:"added"`
}

// serverTypes — протоколы, которые Naivka умеет. udp — может ли передавать UDP (голос, игры).
var serverTypes = map[string]struct{ udp bool }{
	"naive": {false}, "vless": {true}, "hysteria2": {true}, "tuic": {true},
	"trojan": {true}, "shadowsocks": {true}, "anytls": {false},
}

// ssMethods — шифры Shadowsocks: ключ (байт) для 2022, 0 — пароль любой длины.
var ssMethods = map[string]int{
	"2022-blake3-aes-128-gcm": 16, "2022-blake3-aes-256-gcm": 32, "2022-blake3-chacha20-poly1305": 32,
	"aes-128-gcm": 0, "aes-256-gcm": 0, "chacha20-ietf-poly1305": 0, "xchacha20-ietf-poly1305": 0,
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
	IPSet       string `json:"ipset"`       // none | loaded
	RouteDirect bool   `json:"routeDirect"` // сайты из списков zapret — напрямую, а не через сервер
	Fallback    bool   `json:"fallback"`    // zapret перестал справляться — пустить эти сайты через сервер
}

// TelegramSettings — tg-ws-proxy (Flowseal): Telegram Desktop без сервера.
type TelegramSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Secret  string `json:"secret"`
	// ViaServer — пока Naivka подключена, tg-ws-proxy ходит к Telegram через ваш сервер:
	// у многих провайдеров его прямые соединения с серверами Telegram режутся.
	ViaServer bool `json:"viaServer"`
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
	Routing string `json:"routing"` // ru-direct | global

	RuInsideDirect bool `json:"ruInsideDirect"` // сайты, работающие только из РФ, — напрямую
	BlockQUIC      bool `json:"blockQuic"`      // отклонять UDP/443
	UDPDirect      bool `json:"udpDirect"`      // остальной UDP — напрямую

	// SmartDirect — автоопределение (режим «Россия напрямую»): что открывается из России напрямую, идёт напрямую (smart.go).
	SmartDirect bool `json:"smartDirect"`
	// DownloadsDirect — загрузки Steam, Epic и обновлений Windows сразу напрямую (режим «Россия напрямую»).
	DownloadsDirect bool `json:"downloadsDirect"`

	MixedPort       int    `json:"mixedPort"`
	DNSRemote       string `json:"dnsRemote"` // DoH через сервер: IP, домен или https://host/path
	DNSDirect       string `json:"dnsDirect"` // DNS напрямую: https://… (DoH), tls://… (DoT), IP (обычный UDP) или "local"
	IPv4Only        bool   `json:"ipv4Only"`
	RuleSetViaProxy bool   `json:"ruleSetViaProxy"`

	TestURL      string `json:"testUrl"`
	TestInterval string `json:"testInterval"`

	Rules          []Rule     `json:"rules"`
	RuleLists      []RuleList `json:"ruleLists"`      // внешние списки по ссылке
	Panels         []Panel    `json:"panels"`         // веб-панели, поставленные через «Свой сервер»
	ExtraRules     string     `json:"extraRules"`     // JSON-массив правил route.rules
	ExtraOutbounds string     `json:"extraOutbounds"` // JSON-массив outbounds
	Override       string     `json:"override"`       // JSON-объект, накладывается на итоговый конфиг

	ShowIP          bool             `json:"showIp"` // определять IP и страну через внешние сервисы
	KillSwitch      bool             `json:"killSwitch"`
	Watchdog        bool             `json:"watchdog"`
	Failover        bool             `json:"failover"`       // выбранный сервер не отвечает — временно через другой
	SleepReconnect  bool             `json:"sleepReconnect"` // отключаться перед сном, подключаться после пробуждения
	Notify          bool             `json:"notify"`
	AutoStart       bool             `json:"autoStart"`
	Theme           string           `json:"theme"`       // dark | gold («Никогда не проси об этом») | blocks («Пиксели»)
	Accent          string           `json:"accent"`      // акцентный цвет, #RRGGBB
	Oled            bool             `json:"oled"`        // тёмная тема на чистом чёрном
	TrayMini        bool             `json:"trayMini"`    // щелчок по значку в трее открывает мини-окно
	NativeFrame     bool             `json:"nativeFrame"` // обычная рамка Windows вместо своей строки заголовка
	OwnServer       bool             `json:"ownServer"`   // показывать раздел «Свой сервер»
	Lang            string           `json:"lang"`        // язык интерфейса: ru | en
	Nav             string           `json:"nav"`         // боковое меню: list (список) | wheel (колесо с лепестками)
	Window          *WindowGeom      `json:"window,omitempty"`
	Zapret          ZapretSettings   `json:"zapret"`
	Telegram        TelegramSettings `json:"telegram"`
	UpdateCheck     bool             `json:"updateCheck"`
	UpdateChannel   string           `json:"updateChannel"` // stable | beta — получать ли бета-версии
	ZapretOnly      bool             `json:"zapretOnly"`    // режим «Только zapret»: кнопка включает zapret без сервера
	WL              WLSettings       `json:"wl"`
	Share           ShareSettings    `json:"share"`
	AdBlock         bool             `json:"adBlock"`
	TrafficLimitGB  int              `json:"trafficLimitGB"`  // предупреждать о трафике через сервер за месяц; 0 — нет
	TrafficResetDay int              `json:"trafficResetDay"` // с какого числа считается месяц (1–28)
	UDPProxy        bool             `json:"udpProxy"`        // UDP (голос Discord, игры) — через сервер VLESS
	LogLevel        string           `json:"logLevel"`
	AutoConnect     bool             `json:"autoConnect"`
	ExitWithWindow  bool             `json:"exitWithWindow"`
}

func DefaultSettings() *Settings {
	return &Settings{
		Servers:         []Server{},
		Selected:        "auto",
		Mode:            "proxy",
		Routing:         "ru-direct",
		RuInsideDirect:  true,
		SmartDirect:     true,
		DownloadsDirect: true,
		BlockQUIC:       true,
		UDPDirect:       true,
		MixedPort:       2080,
		DNSRemote:       "1.1.1.1",
		DNSDirect:       dnsYandexDoH,
		IPv4Only:        true,
		TestURL:         "https://www.gstatic.com/generate_204",
		TestInterval:    "3m",
		Rules:           []Rule{},
		LogLevel:        "warn",
		ShowIP:          true,
		KillSwitch:      true,
		Watchdog:        true,
		Failover:        true,
		SleepReconnect:  true,
		Notify:          true,
		Theme:           "dark",
		Accent:          accentDefault,
		TrayMini:        true,
		OwnServer:       true,
		Lang:            "ru",
		Nav:             "list",
		UpdateCheck:     true,
		Zapret:          ZapretSettings{Strategy: "general", GameFilter: "off", IPSet: "none", RouteDirect: true, Fallback: true},
		Telegram:        TelegramSettings{Port: 1443, ViaServer: true},
		WL:              WLSettings{Fallback: true, Max: 30},
		Share:           ShareSettings{Port: 2090, Auth: true, Username: "naivka"},
	}
}

// externalLinks — адреса, которые интерфейс может открыть в браузере (POST /api/open).
var externalLinks = map[string]string{
	"feedback": "https://t.me/mixazvv",
	"releases": "https://github.com/" + appRepo + "/releases/latest",
}

// accents — акцентные цвета интерфейса (сами цвета — в web/app.css).
// Акцент — любой цвет #RRGGBB (остальные цвета интерфейса выводятся из него в web/app.js).
// Названия готовых цветов из прошлых версий переводятся в HEX, неизвестные — в цвет по умолчанию.
const accentDefault = "#B6E35A"

var accentNames = map[string]string{"lime": "#B6E35A", "coral": "#FF9A6E", "rose": "#F58FC0"}

var accentHex = regexp.MustCompile(`^#[0-9A-F]{6}$`)

func normalizeAccent(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v != "" && v[0] != '#' {
		if hex, ok := accentNames[strings.ToLower(v)]; ok {
			return hex
		}
		v = "#" + v
	}
	if accentHex.MatchString(v) {
		return v
	}
	return accentDefault
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

// saveMu: сохранения идут из разных мест (окно, трей, подбор zapret) — по одному, иначе
// два одновременных могли бы испортить общий временный файл.
var saveMu sync.Mutex

func SaveSettings(dir string, s *Settings) error {
	saveMu.Lock()
	defer saveMu.Unlock()
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

// needsAdmin — нужны ли Naivka права администратора: режиму TUN и zapret (драйвер WinDivert,
// которого нет на Windows 7 и 8 — там zapret не запускается, и права ему не нужны).
func (s *Settings) needsAdmin() bool {
	return s.Mode == "tun" || (s.Zapret.Enabled && !legacyWindows())
}

// fingerprint — всё, что требует перезапуска ядра (выбор сервера применяется на лету).
func (s *Settings) fingerprint() string {
	c := s.clone()
	c.Selected = ""
	c.AutoConnect = false
	c.ExitWithWindow = false
	c.Window = nil
	c.Theme, c.Accent, c.Oled, c.TrayMini, c.NativeFrame, c.OwnServer, c.Lang, c.Nav = "", "", false, false, false, false, "", ""
	// Порядок и «избранное» на ядро не влияют: автовыбор всё равно берёт самый быстрый.
	for i := range c.Servers {
		c.Servers[i].Fav = false
	}
	sort.Slice(c.Servers, func(i, j int) bool { return c.Servers[i].ID < c.Servers[j].ID })
	c.Notify, c.ShowIP, c.Watchdog, c.KillSwitch, c.AutoStart, c.SleepReconnect = false, false, false, false, false, false
	c.Failover = false
	c.UpdateCheck = false
	c.TrafficLimitGB, c.TrafficResetDay = 0, 0
	c.Panels = nil
	// На ядро влияют только «включён» и «напрямую»; стратегия и фильтры — дело самого zapret.
	c.Zapret = ZapretSettings{Enabled: s.Zapret.Enabled, RouteDirect: s.Zapret.RouteDirect}
	c.Telegram = TelegramSettings{Enabled: s.Telegram.Enabled, ViaServer: s.Telegram.ViaServer}
	c.WL = WLSettings{Enabled: s.WL.Enabled, IncludeRU: s.WL.IncludeRU, Max: s.WL.Max}
	b, _ := json.Marshal(c)
	return string(b)
}

func (s *Settings) normalize() {
	if s.UpdateChannel != "beta" && s.UpdateChannel != "stable" {
		// Бету поставили вручную — по умолчанию и дальше получать бета-версии.
		s.UpdateChannel = "stable"
		if isPrerelease(appVersion) {
			s.UpdateChannel = "beta"
		}
	}
	d := DefaultSettings()
	if s.Mode != "proxy" && s.Mode != "tun" {
		s.Mode = d.Mode
	}
	switch s.Routing {
	case "ru-direct", "global":
	default:
		// Режима «Только заблокированное» больше нет: ближе всего к нему «Россия напрямую».
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
	if s.RuleLists == nil {
		s.RuleLists = []RuleList{}
	}
	if s.Panels == nil {
		s.Panels = []Panel{}
	}
	for i := range s.RuleLists {
		s.RuleLists[i].normalize()
	}
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	switch s.Zapret.GameFilter {
	case "off", "all", "tcp", "udp":
	default:
		s.Zapret.GameFilter = "off"
	}
	// «Любые IP» больше нет: тогда zapret трогал вообще все прямые соединения, а не только свои
	// сайты, и ломал то, что к нему не относится.
	switch s.Zapret.IPSet {
	case "none", "loaded":
	default:
		s.Zapret.IPSet = "none"
	}
	if s.Zapret.Strategy == "" {
		s.Zapret.Strategy = "general"
	}
	if s.WL.Max <= 0 || s.WL.Max > 100 {
		s.WL.Max = 30
	}
	if s.TrafficResetDay < 1 || s.TrafficResetDay > 28 {
		s.TrafficResetDay = 1
	}
	if s.TrafficLimitGB < 0 {
		s.TrafficLimitGB = 0
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
	// Светлой темы и темы «как в Windows» больше нет: остаётся тёмная.
	switch s.Theme {
	case "dark", "gold", "blocks":
	default:
		s.Theme = "dark"
	}
	s.Accent = normalizeAccent(s.Accent)
	if s.Lang != "en" {
		s.Lang = "ru"
	}
	if s.Nav != "wheel" {
		s.Nav = "list"
	}
	for i := range s.Servers {
		sv := &s.Servers[i]
		if sv.ID == "" {
			sv.ID = newID()
		}
		if sv.Fragment != "record" && sv.Fragment != "full" {
			sv.Fragment = ""
		}
		if _, ok := serverTypes[sv.Type]; !ok {
			sv.Type = "naive"
		}
		switch sv.Type {
		case "tuic":
			sv.UUID = strings.TrimSpace(sv.UUID)
			if sv.Congestion == "" {
				sv.Congestion = "bbr"
			}
			if sv.UDPRelay == "" {
				sv.UDPRelay = "native"
			}
		case "trojan":
			if sv.Transport == "" || sv.Transport == "raw" {
				sv.Transport = "tcp"
			}
			if sv.Security == "" {
				sv.Security = "tls"
			}
			if sv.Fingerprint == "" {
				sv.Fingerprint = "chrome"
			}
		case "anytls":
			if sv.Fingerprint == "" {
				sv.Fingerprint = "chrome"
			}
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
		if err := validateServerProto(sv); err != nil {
			return err
		}
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
	if _, err := parseDirectDNS(s.DNSDirect); err != nil {
		return err
	}
	if len(s.RuleLists) > maxRuleLists {
		return fmt.Errorf("Списков по ссылке больше %d — оставьте самые нужные", maxRuleLists)
	}
	for _, l := range s.RuleLists {
		if err := l.validate(); err != nil {
			return err
		}
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

// Зашифрованный DNS Яндекса: российские сайты получают «свои» адреса, как с обычным DNS провайдера,
// но провайдер не видит, какие сайты вы открываете.
const dnsYandexDoH = "https://common.dot.dns.yandex.net/dns-query"

// directDNS — как спрашивать DNS для сайтов, которые идут напрямую.
type directDNS struct {
	Kind string // local | udp | https | tls
	Host string // IP или домен
	Port int
	Path string // только https
}

func (d directDNS) Encrypted() bool { return d.Kind == "https" || d.Kind == "tls" }

// parseDirectDNS: «local», IP (обычный UDP), https://хост/путь (DoH) или tls://хост[:порт] (DoT).
func parseDirectDNS(v string) (directDNS, error) {
	v = strings.TrimSpace(v)
	bad := errors.New("«DNS напрямую»: укажите https://адрес/путь (DoH), tls://адрес (DoT), IP или local")
	switch {
	case v == "local":
		return directDNS{Kind: "local"}, nil
	case net.ParseIP(v) != nil:
		return directDNS{Kind: "udp", Host: v}, nil
	case strings.HasPrefix(v, "https://"), strings.HasPrefix(v, "tls://"):
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" {
			return directDNS{}, bad
		}
		d := directDNS{Kind: u.Scheme, Host: u.Hostname()}
		if p := u.Port(); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return directDNS{}, bad
			}
			d.Port = n
		}
		if d.Kind == "https" {
			d.Path = u.Path
		} else if u.Path != "" && u.Path != "/" {
			return directDNS{}, bad
		}
		return d, nil
	}
	return directDNS{}, bad
}

// validateServerProto — проверки новых протоколов (Naive и VLESS проверяются в Validate).
func validateServerProto(sv Server) error {
	bad := func(what string) error { return fmt.Errorf("У сервера «%s» %s", sv.Name, what) }
	if sv.PinSHA256 != "" {
		if b, err := base64.StdEncoding.DecodeString(sv.PinSHA256); err != nil || len(b) != 32 {
			return bad("неверный отпечаток ключа сертификата (нужен SHA-256 в base64)")
		}
	}
	switch sv.Type {
	case "hysteria2":
		if sv.Password == "" {
			return bad("не указан пароль")
		}
		if sv.Obfs != "" && sv.Obfs != "salamander" {
			return bad(fmt.Sprintf("неизвестная маскировка %q (поддерживается salamander)", sv.Obfs))
		}
		if sv.Obfs == "salamander" && sv.ObfsPassword == "" {
			return bad("не указан пароль маскировки salamander")
		}
		if sv.UpMbps < 0 || sv.DownMbps < 0 {
			return bad("скорость не может быть отрицательной")
		}
	case "tuic":
		if !uuidRe.MatchString(sv.UUID) {
			return bad("неверный UUID")
		}
		if sv.Password == "" {
			return bad("не указан пароль")
		}
		switch sv.Congestion {
		case "bbr", "cubic", "new_reno":
		default:
			return bad(fmt.Sprintf("неизвестный алгоритм перегрузки %q", sv.Congestion))
		}
		if sv.UDPRelay != "native" && sv.UDPRelay != "quic" {
			return bad(fmt.Sprintf("неизвестный режим UDP %q", sv.UDPRelay))
		}
	case "trojan":
		if sv.Password == "" {
			return bad("не указан пароль")
		}
		if sv.Security != "tls" {
			return bad("Trojan работает только с TLS")
		}
		switch sv.Transport {
		case "tcp", "ws", "grpc", "httpupgrade":
		default:
			return bad(fmt.Sprintf("транспорт %q не поддерживается", sv.Transport))
		}
	case "shadowsocks":
		keyLen, ok := ssMethods[sv.Method]
		if !ok {
			return bad(fmt.Sprintf("шифр %q не поддерживается — нужен один из AEAD или 2022", sv.Method))
		}
		if sv.Password == "" {
			return bad("не указан пароль")
		}
		if keyLen > 0 {
			// В 2022 «пароль» — ключ в base64; у сервера-ретранслятора их несколько через двоеточие.
			for _, k := range strings.Split(sv.Password, ":") {
				b, err := base64.StdEncoding.DecodeString(k)
				if err != nil || len(b) != keyLen {
					return bad(fmt.Sprintf("для %s пароль — ключ base64 длиной %d байт", sv.Method, keyLen))
				}
			}
		}
	case "anytls":
		if sv.Password == "" {
			return bad("не указан пароль")
		}
	}
	return nil
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
		if strings.HasPrefix(strings.ToLower(line), "ss://") {
			sv, err := parseSS(line)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", short(line), err))
			} else {
				out = append(out, sv)
			}
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", short(line), err))
			continue
		}
		var parse func(*url.URL) (Server, error)
		switch strings.ToLower(u.Scheme) {
		case "hysteria2", "hy2":
			parse = parseHysteria2
		case "tuic":
			parse = parseTUIC
		case "trojan":
			parse = parseTrojan
		case "anytls":
			parse = parseAnyTLS
		}
		if parse != nil {
			sv, err := parse(u)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", short(line), err))
			} else {
				out = append(out, sv)
			}
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
			errs = append(errs, fmt.Sprintf("%s: ожидается naive+https://, naive+quic://, vless://, hysteria2://, tuic://, trojan://, ss:// или anytls://", short(line)))
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
	if l := shareLinkProto(sv); l != "" {
		return l
	}
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
