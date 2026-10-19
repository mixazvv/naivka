package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const zapretRepo = "Flowseal/zapret-discord-youtube"

type SiteRes struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	OK   bool   `json:"ok"`
	Ms   int    `json:"ms"`
	Mine bool   `json:"mine"` // из правил «Через zapret»
}

type PickResult struct {
	Strategy string    `json:"strategy"`
	OK       int       `json:"ok"`
	Total    int       `json:"total"`
	Ms       int       `json:"ms"`
	Sites    []SiteRes `json:"sites"`
}

// TrialState — ручная проверка: стратегия запущена, пользователь сам смотрит, работает ли.
type TrialState struct {
	Active   bool      `json:"active"`
	Strategy string    `json:"strategy"`
	Probing  bool      `json:"probing"`
	Sites    []SiteRes `json:"sites"`
	Error    string    `json:"error"`
}

type PickState struct {
	Active  bool         `json:"active"`
	Current string       `json:"current"`
	Index   int          `json:"index"`
	Count   int          `json:"count"`
	Results []PickResult `json:"results"`
	Best    string       `json:"best"`
	Error   string       `json:"error"`
	Full    bool         `json:"full"`  // полный перебор, а не быстрый подбор
	Early   bool         `json:"early"` // быстрый подбор остановился на первой стратегии, открывшей всё
}

// Подбор: адрес, не ответивший за pickTimeout, считается не открывшимся; после запуска
// winws.exe ждём pickWarmup, пока перехват заработает.
const (
	pickTimeout = 5 * time.Second
	pickWarmup  = 500 * time.Millisecond
)

// pickOrder — порядок проверки: сначала выбранная сейчас стратегия, потом лучшие по прошлому
// подбору, потом остальные в обычном порядке (general, ALT, ALT2…). Так быстрый подбор
// обычно находит рабочую с первой-второй попытки.
func pickOrder(strats []string, current string, prev []PickResult) []string {
	have := map[string]bool{}
	for _, s := range strats {
		have[s] = true
	}
	out, seen := []string{}, map[string]bool{}
	add := func(s string) {
		if have[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(current)
	ranked := append([]PickResult(nil), prev...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].OK != ranked[j].OK {
			return ranked[i].OK > ranked[j].OK
		}
		return ranked[i].Ms < ranked[j].Ms
	})
	for _, r := range ranked {
		if r.OK > 0 {
			add(r.Strategy)
		}
	}
	for _, s := range strats {
		add(s)
	}
	return out
}

type Zapret struct {
	addonBase
	a *App

	pmu     sync.Mutex
	startMu sync.Mutex // запуск и остановка winws — строго по одному, иначе второй мог остаться работать

	domMu     sync.Mutex // кэш доменов из списков Flowseal
	domKey    string
	domList   []string
	cmd       *exec.Cmd
	done      chan struct{}
	running   bool
	stopping  bool
	strategy  string
	startedAt time.Time
	lastErr   string
	degraded  bool
	fails     int
	oks       int       // удачных проверок подряд, пока zapret считается не справляющимся
	holdUntil time.Time // до этого времени не возвращаемся на zapret (его сайты массово обрывались)
	pick      PickState
	cancel    bool

	trial            TrialState
	trialPrev        string
	trialPrevRunning bool

	revives crashBudget
}

func newZapret(a *App) *Zapret {
	z := &Zapret{a: a}
	z.state.Installed = readVersion(z.dir())
	return z
}

func (z *Zapret) dir() string { return z.a.addonDir("zapret") }

func (z *Zapret) root() string {
	v := readVersion(z.dir())
	if v == "" {
		return ""
	}
	return filepath.Join(z.dir(), v)
}

func (z *Zapret) setLatest(tag string) {
	z.mu.Lock()
	z.state.Latest = tag
	z.mu.Unlock()
}

// Install скачивает последний релиз Flowseal. Предыдущая версия остаётся рядом — на случай отката.
var errZapretLegacy = errors.New("zapret в Naivka пока работает только на Windows 10 и 11: на Windows 7 и 8 нужен другой драйвер WinDivert")

func (z *Zapret) Install() error {
	if legacyWindows() {
		return errZapretLegacy
	}
	if runtime.GOOS == "windows" && !isAdmin() {
		return errNeedAdmin
	}
	if !z.setBusy("download") {
		return nil
	}
	go func() {
		err := z.install()
		z.finish(err)
		if err != nil {
			z.a.mon.Event("error", "zapret: не удалось установить: "+err.Error(), false)
			return
		}
		z.a.mon.Event("ok", "zapret обновлён до "+readVersion(z.dir()), false)
	}()
	return nil
}

func (z *Zapret) install() error {
	tag, err := latestTag(zapretRepo)
	if err != nil {
		return fmt.Errorf("не удалось узнать последнюю версию: %v", err)
	}
	z.setLatest(tag)
	if err := os.MkdirAll(z.dir(), 0o755); err != nil {
		return err
	}
	zipPath := filepath.Join(z.dir(), tag+".zip")
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/zapret-discord-youtube-%s.zip", zapretRepo, tag, tag)
	if err := downloadTo(url, zipPath, z.progress); err != nil {
		return err
	}
	defer os.Remove(zipPath)
	if ok, err := verifyAsset(zapretRepo, tag, "zapret-discord-youtube-"+tag+".zip", zipPath); err != nil {
		return err
	} else if !ok {
		z.a.mon.Event("info", "zapret: GitHub не сообщил контрольную сумму, архив получен только по HTTPS", false)
	}
	tmp := filepath.Join(z.dir(), tag+".new")
	_ = os.RemoveAll(tmp)
	if err := extractZip(zipPath, tmp); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(tmp, "bin", "winws.exe")); err != nil {
		return errors.New("в архиве нет bin\\winws.exe — формат релиза изменился")
	}
	prev := readVersion(z.dir())
	wasRunning := z.Running()
	if wasRunning {
		z.Stop()
	}
	final := filepath.Join(z.dir(), tag)
	_ = os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(z.dir(), "version.txt"), []byte(tag), 0o644); err != nil {
		return err
	}
	z.mu.Lock()
	z.state.Installed = tag
	z.mu.Unlock()
	// Оставляем текущую и предыдущую версии.
	entries, _ := os.ReadDir(z.dir())
	for _, e := range entries {
		if e.IsDir() && e.Name() != tag && e.Name() != prev {
			_ = os.RemoveAll(filepath.Join(z.dir(), e.Name()))
		}
	}
	if wasRunning {
		err := z.Start(z.a.settingsCopy())
		go z.a.applyZapretRoute()
		return err
	}
	return nil
}

var stratNumRe = regexp.MustCompile(`^(.*?)(\d*)\)?$`)

// Strategies — имена bat-файлов general*.bat: «general», «general (ALT)», «general (ALT2)»…
func (z *Zapret) Strategies() []string {
	root := z.root()
	if root == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(root, "general*.bat"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".bat"))
	}
	key := func(s string) (string, int) {
		m := stratNumRe.FindStringSubmatch(s)
		n, _ := strconv.Atoi(m[2])
		return m[1], n
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == "general" || out[j] == "general" {
			return out[i] == "general"
		}
		a, an := key(out[i])
		b, bn := key(out[j])
		if a != b {
			return a < b
		}
		return an < bn
	})
	return out
}

// parseStrategy достаёт из bat-файла аргументы winws.exe и подставляет переменные.
func parseStrategy(bat, binDir, listsDir, gameTCP, gameUDP string) ([]string, error) {
	raw, err := os.ReadFile(bat)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r", ""), "\n")
	var cmd string
	for i := 0; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(strings.ToLower(l), "start ") || !strings.Contains(l, `winws.exe"`) {
			continue
		}
		for strings.HasSuffix(l, "^") && i+1 < len(lines) {
			i++
			l = strings.TrimSuffix(l, "^") + " " + strings.TrimSpace(lines[i])
		}
		cmd = l
		break
	}
	if cmd == "" {
		return nil, fmt.Errorf("в %s не найден запуск winws.exe", filepath.Base(bat))
	}
	args := cmd[strings.Index(cmd, `winws.exe"`)+len(`winws.exe"`):]
	sep := string(filepath.Separator)
	args = strings.NewReplacer(
		"%BIN%", binDir+sep, "%LISTS%", listsDir+sep,
		"%GameFilterTCP%", gameTCP, "%GameFilterUDP%", gameUDP, "%GameFilter%", gameTCP,
	).Replace(args)
	if m := regexp.MustCompile(`%[A-Za-z~0-9_]+%`).FindString(args); m != "" {
		return nil, fmt.Errorf("в стратегии неизвестная переменная %s", m)
	}
	// Разбор как в командной строке: пробелы делят аргументы, кавычки — нет.
	var out []string
	var b strings.Builder
	inQ, has, esc := false, false, false
	for _, r := range args {
		switch {
		case esc: // ^x в bat-файле означает просто x
			b.WriteRune(r)
			esc, has = false, true
		case r == '^' && !inQ:
			esc = true
		case r == '"':
			inQ = !inQ
			has = true
		case (r == ' ' || r == '\t') && !inQ:
			if has {
				out = append(out, b.String())
				b.Reset()
				has = false
			}
		default:
			b.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, b.String())
	}
	return out, nil
}

func gameRanges(mode string) (tcp, udp string) {
	const off, all = "12", "1024-65535" // «12» — заглушка Flowseal: порт, на котором ничего нет
	switch mode {
	case "all":
		return all, all
	case "tcp":
		return all, off
	case "udp":
		return off, all
	}
	return off, off
}

// writeLists: свои списки zapret формирует Naivka — из правил «Через zapret» и адресов серверов.
func (z *Zapret) writeLists(s *Settings) error {
	lists := filepath.Join(z.root(), "lists")
	hdr := "# Файл ведёт Naivka и перезаписывает при каждом запуске zapret.\n"
	var gen []string
	for _, r := range s.Rules {
		if r.Action == "zapret" && r.Kind == "domain" {
			gen = append(gen, r.Value)
		}
	}
	// Соединения с нашими серверами zapret трогать не должен.
	var exDom, exIP []string
	for _, sv := range zapretExcludedServers(s, z.a.wl.Servers(s)) {
		if ip := net.ParseIP(sv.Host); ip != nil {
			exIP = append(exIP, ip.String())
			continue
		}
		exDom = append(exDom, sv.Host)
		if ips, err := net.LookupIP(sv.Host); err == nil {
			for _, ip := range ips {
				exIP = append(exIP, ip.String())
			}
		}
	}
	// DNS-сервер для сайтов «напрямую» zapret тоже трогать не должен: иначе обрываются сами
	// запросы адресов (картинки ВКонтакте и прочее не грузятся с «unexpected EOF»).
	if d, err := parseDirectDNS(s.DNSDirect); err == nil && d.Host != "" {
		if ip := net.ParseIP(d.Host); ip != nil {
			exIP = append(exIP, ip.String())
		} else {
			exDom = append(exDom, d.Host)
			if ips, err := net.LookupIP(d.Host); err == nil {
				for _, ip := range ips {
					exIP = append(exIP, ip.String())
				}
			}
		}
	}
	exIP = append(exIP, googleDNSIP) // DNS Google для адресов YouTube — тоже мимо zapret
	files := map[string][]string{
		"list-general-user.txt":  append([]string{"domain.example.abc"}, gen...),
		"list-exclude-user.txt":  append([]string{"domain.example.abc"}, exDom...),
		"ipset-exclude-user.txt": append([]string{"203.0.113.113/32"}, exIP...),
	}
	for name, lines := range files {
		if err := os.WriteFile(filepath.Join(lists, name), []byte(hdr+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	ipset := filepath.Join(lists, "ipset-all.txt")
	if s.Zapret.IPSet == "loaded" {
		b, err := os.ReadFile(ipset + ".backup")
		if err != nil {
			return errors.New("в этой версии zapret нет полного списка IP (ipset-all.txt.backup)")
		}
		return os.WriteFile(ipset, b, 0o644)
	}
	// Без списка IP: один заведомо пустой адрес (пустой файл winws понял бы как «любые IP»).
	return os.WriteFile(ipset, []byte("203.0.113.113/32\n"), 0o644)
}

// zapretExcludedServers — серверы, соединения с которыми zapret трогать не должен:
// свои и из подписки «белые списки» (без повторов).
func zapretExcludedServers(s *Settings, wl []Server) []Server {
	seen := map[string]bool{}
	var out []Server
	for _, sv := range append(append([]Server(nil), s.Servers...), wl...) {
		h := strings.ToLower(strings.TrimSpace(sv.Host))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, sv)
	}
	return out
}

// zapretServersKey — по чему видно, что список исключений zapret устарел.
func zapretServersKey(s *Settings, wl []Server) string {
	var hs []string
	for _, sv := range zapretExcludedServers(s, wl) {
		hs = append(hs, strings.ToLower(sv.Host))
	}
	sort.Strings(hs)
	return strings.Join(hs, ",")
}

// Domains — домены из списков Flowseal и правил «Через zapret»: их Naivka пускает напрямую.
// Domains берёт списки из кэша: их спрашивают каждую секунду (окно, статистика), а меняются
// они только при обновлении zapret. Кэш сбрасывается, когда у файлов меняется время или размер.
func (z *Zapret) Domains(s *Settings) []string {
	root := z.root()
	if root == "" {
		return nil
	}
	key := root
	for _, name := range []string{"list-general.txt", "list-google.txt"} {
		if st, err := os.Stat(filepath.Join(root, "lists", name)); err == nil {
			key += fmt.Sprintf("|%d|%d", st.ModTime().UnixNano(), st.Size())
		}
	}
	z.domMu.Lock()
	defer z.domMu.Unlock()
	if z.domKey != key {
		z.domList, z.domKey = readZapretDomains(root), key
	}
	return z.domList
}

func readZapretDomains(root string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range []string{"list-general.txt", "list-google.txt"} {
		f, err := os.Open(filepath.Join(root, "lists", name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			d := strings.TrimSpace(sc.Text())
			if d == "" || strings.HasPrefix(d, "#") {
				continue
			}
			if nd, err := normalizeDomain(d); err == nil && !seen[nd] {
				seen[nd] = true
				out = append(out, nd)
			}
		}
		f.Close()
	}
	sort.Strings(out)
	return out
}

func (z *Zapret) Running() bool {
	z.pmu.Lock()
	defer z.pmu.Unlock()
	return z.running
}

// conflict: уже работает чужой zapret (служба Flowseal или запущенный bat) — два сразу не уживутся.
func zapretConflict() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	// Обе проверки ограничены по времени: подключение ждёт их под общей блокировкой.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sc := exec.CommandContext(ctx, "sc", "query", "zapret")
	hideWindow(sc) // консольная программа: без этого на долю секунды мелькает окно
	out, _ := sc.CombinedOutput()
	if strings.Contains(string(out), "RUNNING") {
		return "Уже работает служба zapret, установленная через service.bat. Удалите её: service.bat → Remove Services."
	}
	cmd := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq winws.exe", "/NH")
	hideWindow(cmd)
	out, _ = cmd.CombinedOutput()
	if strings.Contains(strings.ToLower(string(out)), "winws.exe") {
		return "Уже запущен другой winws.exe (bat-файл zapret). Закройте его — значок в трее или окно «zapret: …»."
	}
	return ""
}

func (z *Zapret) Start(s *Settings) error { return z.startStrategy(s, s.Zapret.Strategy) }

func (z *Zapret) startStrategy(s *Settings, strategy string) error {
	if runtime.GOOS != "windows" {
		return errors.New("zapret работает только в Windows")
	}
	if legacyWindows() {
		return errZapretLegacy
	}
	if !isAdmin() {
		return errNeedAdmin
	}
	root := z.root()
	if root == "" {
		return errors.New("zapret ещё не установлен")
	}
	// Стратегия — только одна из файлов самого zapret: имя из настроек (или из присланной копии)
	// не должно указывать на чужой .bat, который winws выполнил бы с правами администратора.
	if !knownStrategy(z.Strategies(), strategy) {
		return fmt.Errorf("неизвестная стратегия zapret %q — выберите из списка", strategy)
	}
	z.startMu.Lock()
	defer z.startMu.Unlock()
	z.stop()
	if c := zapretConflict(); c != "" {
		return errors.New(c)
	}
	if err := z.writeLists(s); err != nil {
		return err
	}
	bin := filepath.Join(root, "bin")
	tcp, udp := gameRanges(s.Zapret.GameFilter)
	args, err := parseStrategy(filepath.Join(root, strategy+".bat"), bin, filepath.Join(root, "lists"), tcp, udp)
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(bin, "winws.exe"), args...)
	cmd.Dir = bin
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("не удалось запустить winws.exe: %v", err)
	}
	attachToJob(cmd.Process)
	// В журнал — только сбои; полный вывод держим рядом, чтобы показать, если winws не запустился.
	out := &tailBuf{max: 40}
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			out.add(line)
			if !routineZapretLine(line) {
				z.a.core.logs.Add("[zapret] " + line)
			}
		}
	}()
	done := make(chan struct{})
	z.pmu.Lock()
	z.cmd, z.done, z.running, z.stopping, z.strategy, z.startedAt, z.lastErr = cmd, done, true, false, strategy, time.Now(), ""
	z.pmu.Unlock()
	go func() {
		werr := cmd.Wait()
		pw.Close()
		z.pmu.Lock()
		crashed := z.cmd == cmd && !z.stopping
		if z.cmd == cmd {
			z.running, z.cmd = false, nil
		}
		if crashed {
			z.lastErr = "winws.exe завершился"
			if werr != nil {
				z.lastErr += ": " + werr.Error()
			}
		}
		msg := z.lastErr
		ranFor := time.Since(z.startedAt)
		z.pmu.Unlock()
		close(done)
		// Упал при запуске — об этом скажет сам Start; упал на ходу — поднимаем снова.
		if crashed && ranFor > 3*time.Second {
			go z.revive(msg)
		}
	}()
	select {
	case <-done:
		time.Sleep(100 * time.Millisecond) // дочитать последние строки
		return fmt.Errorf("winws.exe сразу завершился («%s»):\n%s", strategy, out.String())
	case <-time.After(1500 * time.Millisecond):
	}
	z.pmu.Lock()
	picking := z.pick.Active
	z.pmu.Unlock()
	if !picking {
		z.a.core.logs.Add("[zapret] запущена стратегия «" + strategy + "»")
	}
	return nil
}

// revive: winws.exe закрылся на ходу. Пока его нет, сайты из списков zapret, которые идут
// напрямую, не открываются, — поэтому поднимаем его снова, а если не выходит, пускаем их через сервер.
func (z *Zapret) revive(msg string) {
	z.a.mon.Event("warn", "zapret: "+msg+" — запускаю снова", false)
	for _, d := range []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second} {
		time.Sleep(d)
		s := z.a.settingsCopy()
		z.pmu.Lock()
		busy := z.pick.Active || z.trial.Active
		z.pmu.Unlock()
		if !s.Zapret.Enabled || z.Running() || busy {
			return
		}
		if !z.revives.allow(5, 10*time.Minute) {
			break
		}
		if err := z.Start(s); err == nil {
			z.a.mon.Event("ok", "zapret снова работает", false)
			z.a.applyZapretRoute()
			return
		}
	}
	msg = "zapret не удаётся запустить снова. Подробности — в журнале"
	if z.a.core.Running() {
		msg += ". Пока его сайты идут через сервер"
		z.a.applyZapretRoute() // zapret не работает — его сайты через сервер, остальное не трогаем
	}
	z.a.mon.Event("error", msg, true)
}

func (z *Zapret) Stop() {
	z.startMu.Lock()
	defer z.startMu.Unlock()
	z.stop()
}

func (z *Zapret) stop() {
	z.pmu.Lock()
	cmd, done := z.cmd, z.done
	if cmd == nil {
		z.pmu.Unlock()
		return
	}
	z.stopping = true
	z.pmu.Unlock()
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// ---------- проверка доступности ----------

type target struct {
	name, url string
	mine      bool
}

var targetRe = regexp.MustCompile(`^\s*(\w+)\s*=\s*"(https?://[^"]+)"`)

func (z *Zapret) targets() []target {
	out := []target{}
	if root := z.root(); root != "" {
		if f, err := os.Open(filepath.Join(root, "utils", "targets.txt")); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if m := targetRe.FindStringSubmatch(sc.Text()); m != nil {
					out = append(out, target{m[1], m[2], false})
				}
			}
			f.Close()
		}
	}
	if len(out) == 0 {
		out = []target{{"YouTube", "https://www.youtube.com", false}, {"YouTubeImage", "https://i.ytimg.com", false},
			{"Discord", "https://discord.com", false}, {"DiscordCDN", "https://cdn.discordapp.com", false}}
	}
	// Свои сайты — правила «Через zapret» (не больше 10, чтобы подбор не затягивался).
	n := 0
	for _, r := range z.a.settingsCopy().Rules {
		if r.Action == "zapret" && r.Kind == "domain" && n < 10 {
			out = append(out, target{r.Value, "https://" + r.Value, true})
			n++
		}
	}
	return out
}

// probeTargets: какие адреса открываются напрямую (zapret обрабатывает именно прямые соединения).
func (z *Zapret) probeTargets(ts []target, timeout time.Duration) (ok, avgMs int, sites []SiteRes) {
	directPort, _ := z.a.core.ProbePorts()
	hc := geoClient(directPort)
	hc.Timeout = timeout
	sites = make([]SiteRes, len(ts))
	var wg sync.WaitGroup
	for i, t := range ts {
		sites[i] = SiteRes{Name: t.name, URL: t.url, Mine: t.mine}
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", t.url, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36")
			start := time.Now()
			resp, err := hc.Do(req)
			if err != nil {
				return
			}
			_, _ = io.CopyN(io.Discard, resp.Body, 16<<10)
			resp.Body.Close()
			sites[i].OK, sites[i].Ms = true, int(time.Since(start)/time.Millisecond)
		}(i, t)
	}
	wg.Wait()
	total := 0
	for _, r := range sites {
		if r.OK {
			ok++
			total += r.Ms
		}
	}
	if ok > 0 {
		avgMs = total / ok
	}
	return
}

// Pick перебирает стратегии и выбирает ту, с которой открывается больше всего адресов.
// Pick подбирает стратегию. Быстрый подбор (full = false) останавливается на первой стратегии,
// которая открыла все адреса; полный проверяет все и выбирает лучшую по числу открывшихся и скорости.
// Стратегии проверяются по одной: winws.exe перехватывает весь трафик, две сразу мешали бы друг другу.
func (z *Zapret) Pick(full bool) error {
	if runtime.GOOS != "windows" {
		return errors.New("zapret работает только в Windows")
	}
	if !isAdmin() {
		return errNeedAdmin
	}
	strats := z.Strategies()
	if len(strats) == 0 {
		return errors.New("zapret ещё не установлен")
	}
	z.pmu.Lock()
	if z.trial.Active {
		z.pmu.Unlock()
		return errors.New("Сначала закончите ручную проверку")
	}
	if z.pick.Active {
		z.pmu.Unlock()
		return nil
	}
	strats = pickOrder(strats, z.a.settingsCopy().Zapret.Strategy, z.pick.Results)
	z.pick = PickState{Active: true, Count: len(strats) + 1, Results: []PickResult{}, Full: full}
	z.cancel = false
	z.pmu.Unlock()

	go func() {
		s := z.a.settingsCopy()
		wasRunning := z.Running()
		ts := z.targets()
		set := func(f func(p *PickState)) { z.pmu.Lock(); f(&z.pick); z.pmu.Unlock() }
		z.Stop()
		set(func(p *PickState) { p.Current = "без обхода"; p.Index = 0 })
		ok, ms, sites := z.probeTargets(ts, pickTimeout)
		set(func(p *PickState) {
			p.Results = append(p.Results, PickResult{"без обхода", ok, len(ts), ms, sites})
		})
		best, bestOK, bestMs := "", -1, 0
		for i, st := range strats {
			z.pmu.Lock()
			cancelled := z.cancel
			z.pmu.Unlock()
			if cancelled {
				break
			}
			set(func(p *PickState) { p.Current = st; p.Index = i + 1 })
			r := PickResult{Strategy: st, Total: len(ts)}
			if err := z.startStrategy(s, st); err == nil {
				time.Sleep(pickWarmup)
				r.OK, r.Ms, r.Sites = z.probeTargets(ts, pickTimeout)
				z.a.core.logs.Add(fmt.Sprintf("[zapret] подбор: «%s» — открылось %d из %d", st, r.OK, r.Total))
			} else {
				z.a.core.logs.Add(fmt.Sprintf("[zapret] подбор: «%s» не запустилась: %s", st, firstLine(err.Error())))
			}
			z.Stop()
			set(func(p *PickState) { p.Results = append(p.Results, r) })
			if r.OK > bestOK || (r.OK == bestOK && r.Ms < bestMs) {
				best, bestOK, bestMs = st, r.OK, r.Ms
			}
			if !full && r.OK == len(ts) && len(ts) > 0 {
				set(func(p *PickState) { p.Early = true })
				break
			}
		}
		set(func(p *PickState) { p.Active = false; p.Current = ""; p.Best = best })
		if best != "" && bestOK > 0 {
			z.a.mu.Lock()
			z.a.settings.Zapret.Strategy = best
			_ = SaveSettings(z.a.dir, z.a.settings)
			z.a.mu.Unlock()
			z.a.mon.Event("ok", fmt.Sprintf("zapret: подобрана стратегия «%s» — открывается %d из %d адресов", best, bestOK, len(ts)), true)
		} else {
			set(func(p *PickState) {
				p.Error = "Ни одна стратегия не помогла. Проверьте Secure DNS и попробуйте позже."
			})
			z.a.mon.Event("warn", "zapret: ни одна стратегия не помогла", true)
		}
		s = z.a.settingsCopy()
		if wasRunning || s.Zapret.Enabled {
			if err := z.Start(s); err != nil {
				z.a.mon.Event("error", "zapret: "+err.Error(), false)
			}
		}
		z.a.applyZapretRoute() // заработал — его сайты снова напрямую, не заработал — через сервер
	}()
	return nil
}

func (z *Zapret) CancelPick() {
	z.pmu.Lock()
	z.cancel = true
	z.pmu.Unlock()
}

// healthLoop: раз в 3 минуты проверяем, справляется ли zapret. Если нет — сайты из его
// списков временно идут через сервер (если включено), а когда zapret снова справится — обратно.
// controlTargets — российские сайты, которые открываются напрямую без всякого обхода.
// Если не открываются и они — это не zapret, а нет связи вообще.
var controlTargets = []target{{"Yandex", "https://ya.ru", false}, {"VK", "https://vk.com", false}}

func (z *Zapret) healthLoop() {
	last := time.Now()
	for {
		time.Sleep(3 * time.Minute)
		// Компьютер спал (пауза сильно больше 3 минут) или только что проснулся — начинаем счёт заново.
		if time.Since(last) > 6*time.Minute || z.a.mon.SinceWake() < wakeGrace {
			last = time.Now()
			z.pmu.Lock()
			z.fails = 0
			z.pmu.Unlock()
			continue
		}
		last = time.Now()
		s := z.a.settingsCopy()
		z.pmu.Lock()
		busy := z.pick.Active || z.trial.Active
		z.pmu.Unlock()
		if !s.Zapret.Enabled || !z.Running() || busy {
			continue
		}
		ts := z.targets()
		if len(ts) > 4 {
			ts = ts[:4]
		}
		ok, _, _ := z.probeTargets(ts, 8*time.Second)
		if ok == 0 {
			if c, _, _ := z.probeTargets(controlTargets, 8*time.Second); c == 0 {
				continue // интернета нет совсем — zapret тут ни при чём
			}
		}
		z.pmu.Lock()
		if ok == 0 {
			z.fails, z.oks = z.fails+1, 0
		} else {
			z.fails, z.oks = 0, z.oks+1
		}
		fails, oks, degraded := z.fails, z.oks, z.degraded
		z.pmu.Unlock()
		switch {
		case fails == 2 && !degraded:
			z.setDegraded(true)
			msg := "zapret перестал справляться: YouTube и Discord не открываются напрямую. Попробуйте подобрать стратегию заново"
			if s.Zapret.Fallback {
				msg += ". Пока они идут через сервер"
			}
			z.a.mon.Event("warn", msg, true)
		case oks >= 2 && degraded && time.Now().After(z.holdTill()):
			// Возвращаемся только после двух удачных проверок подряд (6 минут), чтобы сайты
			// не перескакивали между zapret и сервером при каждой случайной удаче или сбое.
			z.setDegraded(false)
			z.a.mon.Event("ok", "zapret снова справляется — сайты из его списков идут напрямую", true)
		}
	}
}

func (z *Zapret) holdTill() time.Time {
	z.pmu.Lock()
	defer z.pmu.Unlock()
	return z.holdUntil
}

// zapretFailLine — строка журнала ядра о том, что соединение с сайтом zapret напрямую не удалось.
func zapretFailLine(l string) bool {
	if !strings.Contains(l, "outbound/selector["+tagZapretOut+"]") {
		return false
	}
	for _, e := range []string{"i/o timeout", "forcibly closed", "connection reset", "connection refused"} {
		if strings.Contains(l, e) {
			return true
		}
	}
	return false
}

// failWatch: проверка zapret раз в 3 минуты смотрит только YouTube и Discord, а остальные сайты
// из его списков могут не открываться. Поэтому смотрим и в журнал ядра: если за 3 минуты
// соединения с сайтами zapret напрямую оборвались много раз, пускаем их через сервер и не
// возвращаемся как минимум полчаса.
func (z *Zapret) failWatch() {
	const window, limit = 3 * time.Minute, 8
	var seq int64
	var fails []time.Time
	for {
		time.Sleep(20 * time.Second)
		lines := z.a.core.logs.After(seq)
		if len(lines) > 0 {
			seq = lines[len(lines)-1].Seq
		}
		s := z.a.settingsCopy()
		_, applied := z.a.zr.get()
		z.pmu.Lock()
		busy, degraded := z.pick.Active || z.trial.Active, z.degraded
		z.pmu.Unlock()
		// Считаем только обрывы, пока сайты zapret правда идут напрямую через него: обрывы времён,
		// когда они шли через сервер или шёл подбор стратегии, к нему не относятся.
		if busy || degraded || !s.Zapret.Fallback || applied != tagDirect || !z.Running() || z.a.mon.SinceWake() < wakeGrace {
			fails = nil
			continue
		}
		now := time.Now()
		for _, l := range lines {
			if zapretFailLine(l.Text) {
				fails = append(fails, now)
			}
		}
		keep := fails[:0]
		for _, t := range fails {
			if now.Sub(t) < window {
				keep = append(keep, t)
			}
		}
		fails = keep
		if len(fails) < limit {
			continue
		}
		n := len(fails)
		fails = nil
		if !z.a.internetUp() {
			continue // пропал сам интернет — zapret тут ни при чём
		}
		z.pmu.Lock()
		z.holdUntil = now.Add(30 * time.Minute)
		z.pmu.Unlock()
		z.setDegraded(true)
		z.a.mon.Event("warn", fmt.Sprintf("zapret не справляется с частью своих сайтов (%d обрывов за 3 минуты) — пока они идут через сервер. Попробуйте подобрать стратегию заново", n), true)
	}
}

func (z *Zapret) setDegraded(v bool) {
	z.pmu.Lock()
	z.degraded, z.oks = v, 0
	z.pmu.Unlock()
	if z.a.settingsCopy().Zapret.Fallback {
		z.a.applyZapretRoute()
	}
}

// routed — в конфиге ядра есть переключатель zapret-out: zapret включён (или идёт ручная проверка)
// и установлен. Тогда сайты zapret переводятся между ним и сервером без перезапуска ядра.
func (z *Zapret) routed(s *Settings) bool {
	return (s.Zapret.Enabled || z.TrialActive()) && z.root() != ""
}

type ZapretView struct {
	AddonState
	Running    bool       `json:"running"`
	Strategy   string     `json:"strategy"`
	Strategies []string   `json:"strategies"`
	Since      int64      `json:"since"`
	Error      string     `json:"lastError"`
	Degraded   bool       `json:"degraded"`
	Domains    int        `json:"domains"`
	Pick       PickState  `json:"pick"`
	Trial      TrialState `json:"trial"`
	Path       string     `json:"path"`
	Targets    []string   `json:"targets"`
}

func (z *Zapret) View() ZapretView {
	v := ZapretView{AddonState: z.snapshot(), Strategies: z.Strategies(), Path: z.dir()}
	if v.Strategies == nil {
		v.Strategies = []string{}
	}
	v.Domains = len(z.Domains(z.a.settingsCopy()))
	for _, t := range z.targets() {
		v.Targets = append(v.Targets, t.name)
	}
	z.pmu.Lock()
	defer z.pmu.Unlock()
	v.Running, v.Strategy, v.Error, v.Degraded, v.Pick, v.Trial = z.running, z.strategy, z.lastErr, z.degraded, z.pick, z.trial
	if z.running {
		v.Since = z.startedAt.Unix()
	}
	return v
}

// active — для генерации конфига: можно ли пускать сайты zapret напрямую.
// Во время ручной проверки — да: иначе браузер открыл бы их через сервер и проверка не имела бы смысла.
func (z *Zapret) active(s *Settings) bool {
	z.pmu.Lock()
	trial := z.trial.Active
	z.pmu.Unlock()
	if !z.Running() || (!s.Zapret.Enabled && !trial) {
		return false
	}
	z.pmu.Lock()
	defer z.pmu.Unlock()
	return trial || !(z.degraded && s.Zapret.Fallback)
}

// busy — идёт автоподбор или ручная проверка: zapret сейчас запускают они.
func (z *Zapret) busy() bool {
	z.pmu.Lock()
	defer z.pmu.Unlock()
	return z.pick.Active || z.trial.Active
}

func (z *Zapret) TrialActive() bool {
	z.pmu.Lock()
	defer z.pmu.Unlock()
	return z.trial.Active
}

// TrialStart запускает стратегию для ручной проверки. Выбор сохраняется только по TrialAccept.
func (z *Zapret) TrialStart(strategy string) error {
	if runtime.GOOS != "windows" {
		return errors.New("zapret работает только в Windows")
	}
	if legacyWindows() {
		return errZapretLegacy
	}
	if !isAdmin() {
		return errNeedAdmin
	}
	found := false
	for _, st := range z.Strategies() {
		if st == strategy {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("нет стратегии «%s»", strategy)
	}
	s := z.a.settingsCopy()
	z.pmu.Lock()
	if z.pick.Active {
		z.pmu.Unlock()
		return errors.New("Дождитесь окончания автоподбора")
	}
	first := !z.trial.Active
	if first {
		z.trialPrev, z.trialPrevRunning = s.Zapret.Strategy, z.running
	}
	z.trial = TrialState{Active: true, Strategy: strategy, Probing: true, Sites: []SiteRes{}}
	z.pmu.Unlock()

	if err := z.startStrategy(s, strategy); err != nil {
		z.pmu.Lock()
		z.trial.Probing, z.trial.Error = false, firstLine(err.Error())
		z.pmu.Unlock()
		return nil // ошибка видна в окне проверки, можно перейти к следующей
	}
	// В фоне: если для этого нужен перезапуск ядра, окно не должно его ждать.
	go func() {
		if first {
			z.a.applyZapretRoute() // сайты zapret на время проверки — напрямую
		}
		z.TrialProbe()
	}()
	return nil
}

func (z *Zapret) TrialProbe() {
	z.pmu.Lock()
	if !z.trial.Active {
		z.pmu.Unlock()
		return
	}
	z.trial.Probing = true
	strategy := z.trial.Strategy
	z.pmu.Unlock()
	time.Sleep(time.Second)
	_, _, sites := z.probeTargets(z.targets(), 6*time.Second)
	z.pmu.Lock()
	if z.trial.Active && z.trial.Strategy == strategy {
		z.trial.Probing, z.trial.Sites = false, sites
	}
	z.pmu.Unlock()
}

// TrialAccept — «Работает»: стратегия запоминается, zapret включается.
func (z *Zapret) TrialAccept() error {
	z.pmu.Lock()
	if !z.trial.Active || z.trial.Strategy == "" {
		z.pmu.Unlock()
		return errors.New("Проверка не идёт")
	}
	strategy := z.trial.Strategy
	z.trial = TrialState{}
	z.pmu.Unlock()
	z.a.mu.Lock()
	z.a.settings.Zapret.Strategy = strategy
	z.a.settings.Zapret.Enabled = true
	_ = SaveSettings(z.a.dir, z.a.settings)
	z.a.mu.Unlock()
	z.a.mon.Event("ok", "zapret: выбрана стратегия «"+strategy+"»", false)
	if !z.Running() {
		if err := z.Start(z.a.settingsCopy()); err != nil {
			return err
		}
	}
	go z.a.applyZapretRoute()
	return nil
}

// TrialCancel — закончить без выбора: возвращаем всё как было.
func (z *Zapret) TrialCancel() {
	z.pmu.Lock()
	if !z.trial.Active {
		z.pmu.Unlock()
		return
	}
	prev, prevRunning := z.trialPrev, z.trialPrevRunning
	z.trial = TrialState{}
	z.pmu.Unlock()
	if prevRunning {
		if err := z.startStrategy(z.a.settingsCopy(), prev); err != nil {
			z.a.mon.Event("error", "zapret: "+firstLine(err.Error()), false)
		}
	} else {
		z.Stop()
	}
	go z.a.applyZapretRoute()
}

// routineZapretLine — обычные строки запуска winws, которые не несут информации:
// версия, число профилей, «загружен такой-то список», «перехват запущен».
func routineZapretLine(line string) bool {
	l := strings.TrimSpace(line)
	if l == "" {
		return true
	}
	for _, p := range []string{"github version", "we have ", "Loading hostlist", "Loading ipset", "loading plain text list",
		"Loaded ", "windivert initialized", "capture is started"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// tailBuf — последние строки вывода программы.
type tailBuf struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func (t *tailBuf) add(s string) {
	t.mu.Lock()
	t.lines = append(t.lines, s)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
	t.mu.Unlock()
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var keep []string
	for _, l := range t.lines {
		if !routineZapretLine(l) {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 { // ничего необычного — покажем хотя бы конец
		keep = t.lines
		if len(keep) > 6 {
			keep = keep[len(keep)-6:]
		}
	}
	return strings.Join(keep, "\n")
}

func knownStrategy(list []string, name string) bool {
	if name == "" || strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") {
		return false
	}
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}
