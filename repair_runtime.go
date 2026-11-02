package main

import (
	"context"
	"errors"
	"hash/fnv"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type repairCapture struct {
	choice                                        selectionSnapshot
	cl                                            *Clash
	selector, effective, from, to, doh            string
	mixedPort, directPort, proxyPort, primaryPort int
	ports                                         map[string]int
	network                                       uint64
	networkOK                                     bool
	proxyOwned                                    bool
	tunPresent                                    bool
	tunRoutes                                     string
	restoring                                     bool
	operationID                                   string
}

func repairNetwork() uint64 {
	interfaces, err := net.Interfaces()
	if err != nil {
		return 0
	}
	var entries []string
	for _, it := range interfaces {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		for _, addr := range addrs {
			entries = append(entries, it.Name+"|"+addr.String())
		}
	}
	if raw, e := readRepairRoutes(); e == nil {
		entries = append(entries, repairRouteSignature(raw)...)
	}
	sort.Strings(entries)
	h := fnv.New64a()
	for _, s := range entries {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return h.Sum64()
}
func repairCompatible(s *Settings, tag string) bool {
	for _, server := range s.Servers {
		if serverTag(server) == tag {
			return s.Mode != "tun" || server.Type != "naive" || server.UoT
		}
	}
	return false
}
func repairTUNPresent() bool {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, it := range interfaces {
		if strings.EqualFold(it.Name, "Naivka") && it.Flags&net.FlagUp != 0 {
			addresses, err := it.Addrs()
			return err == nil && len(addresses) > 0
		}
	}
	return false
}
func repairTUNRoutes() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "UNKNOWN"
	}
	for _, it := range interfaces {
		if strings.EqualFold(it.Name, "Naivka") {
			raw, err := readRepairRoutes()
			if err != nil {
				return "UNKNOWN"
			}
			v4, _, known := repairRouteCoverage(raw, uint32(it.Index))
			if !known {
				return "UNKNOWN"
			}
			if v4 {
				return "OK"
			}
			return "MISSING"
		}
	}
	return "UNKNOWN"
}
func repairDoH(s *Settings) string {
	host, path, err := parseDoH(s.DNSRemote)
	if err != nil {
		return ""
	}
	if path == "" {
		path = "/dns-query"
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: "https", Host: host, Path: path}).String()
}
func (a *App) captureRepair(ctx context.Context, cl *Clash) (*repairCapture, error) {
	choice := a.choiceSnapshot()
	s := choice.settings
	snap := &repairCapture{choice: choice, cl: cl, mixedPort: s.MixedPort, network: repairNetwork(), networkOK: networkUp(), doh: repairDoH(s)}
	snap.from, snap.to = a.fo.get()
	if s.Mode == "proxy" {
		snap.proxyOwned, _ = systemProxyOurs(s.MixedPort)
	}
	if s.Mode == "tun" {
		snap.tunPresent = repairTUNPresent()
		snap.tunRoutes = repairTUNRoutes()
	}
	if cl == nil || !a.core.Running() {
		return snap, errors.New("CORE_NOT_RUNNING")
	}
	snap.directPort, snap.proxyPort = a.core.ProbePorts()
	snap.ports = a.serverProbes()
	px, err := cl.proxiesContext(ctx)
	if err != nil {
		return snap, errors.New("CORE_API_UNAVAILABLE")
	}
	snap.selector = px.Selected
	snap.effective = px.Selected
	if px.Selected == tagAuto {
		snap.effective = px.AutoNow
	}
	allowed := false
	for _, tag := range serverScoreTags(s) {
		if tag == snap.effective {
			allowed = true
		}
	}
	if !allowed {
		return snap, errors.New("UNSUPPORTED_CURRENT_ROUTE")
	}
	snap.primaryPort = snap.ports[snap.effective]
	if snap.primaryPort == 0 {
		snap.primaryPort = snap.proxyPort
	}
	return snap, nil
}

type appRepairIO struct{ a *App }

func (d appRepairIO) owned(snap *repairCapture) bool {
	a := d.a
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	return a.repairOwnedLocked(snap)
}
func (a *App) repairOwnedLocked(snap *repairCapture) bool { return a.repairCurrentLocked(snap, true) }
func (a *App) repairCurrentLocked(snap *repairCapture, checkPolicy bool) bool {
	current := a.settingsCopy()
	cl := a.core.Clash()
	from, to := a.fo.get()
	a.mu.Lock()
	applied := a.applied
	a.mu.Unlock()
	if cl == nil || cl.secret != snap.cl.secret || a.life.gen.Load() != snap.choice.gen || !a.life.want.Load() || a.selectionRev != snap.choice.rev || current.Selected != snap.choice.settings.Selected || current.KillSwitch != snap.choice.settings.KillSwitch || current.fingerprint() != snap.choice.settings.fingerprint() || (applied != "" && applied != current.fingerprint()) || from != snap.from || to != snap.to || a.wl.Active() || repairNetwork() != snap.network {
		return false
	}
	if current.Mode == "proxy" {
		ours, _ := systemProxyOurs(current.MixedPort)
		if ours != snap.proxyOwned {
			return false
		}
	}
	if current.Mode == "tun" && (repairTUNPresent() != snap.tunPresent || repairTUNRoutes() != snap.tunRoutes) {
		return false
	}
	if checkPolicy && current.Mode == "tun" && (!repairTUNPresent() || repairTUNRoutes() == "MISSING") {
		return false
	}
	if checkPolicy && current.Mode == "proxy" {
		ours, _ := systemProxyOurs(current.MixedPort)
		if !ours {
			return false
		}
	}
	return true
}

// Diagnostic observations may themselves discover a broken proxy/TUN policy.
// Do not replace those diagnoses with STALE_STATE merely because policy is bad.
func (d appRepairIO) observed(ctx context.Context, snap *repairCapture) bool {
	d.a.selectionMu.Lock()
	current := d.a.repairCurrentLocked(snap, false)
	d.a.selectionMu.Unlock()
	if !current || ctx.Err() != nil {
		return false
	}
	px, err := snap.cl.proxiesContext(ctx)
	return err == nil && px.Selected == snap.selector && (snap.selector != tagAuto || px.AutoNow == snap.effective)
}
func (d appRepairIO) fresh(ctx context.Context, snap *repairCapture, expected string) bool {
	if ctx.Err() != nil || !d.owned(snap) {
		return false
	}
	px, err := snap.cl.proxiesContext(ctx)
	if err != nil || px.Selected != expected {
		return false
	}
	if expected == snap.selector && expected == tagAuto && px.AutoNow != snap.effective {
		return false
	}
	return true
}
func (d appRepairIO) probe(ctx context.Context, port int, doh string) repairPath {
	return probeRepairPath(ctx, port, doh)
}
func linkRepairContext(parent, operation context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(operation)
	stop := make(chan struct{})
	go func() {
		select {
		case <-parent.Done():
			cancel()
		case <-ctx.Done():
		case <-stop:
		}
	}()
	return ctx, func() { close(stop); cancel() }
}
func (d appRepairIO) selectRoute(operation context.Context, snap *repairCapture, expected, target string) (bool, error) {
	a := d.a
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	ctx, finish, err := a.life.operation(snap.choice.gen, true)
	if err != nil {
		return false, err
	}
	defer finish()
	ctx, stop := linkRepairContext(ctx, operation)
	defer stop()
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	if ctx.Err() != nil || !a.repairOwnedLocked(snap) {
		return false, errors.New("STALE_STATE")
	}
	px, err := snap.cl.proxiesContext(ctx)
	if err != nil || px.Selected != expected {
		return false, errors.New("ROUTE_CHANGED")
	}
	if expected == snap.selector && expected == tagAuto && px.AutoNow != snap.effective {
		return false, errors.New("ROUTE_CHANGED")
	}
	// Claim our attempt before the request: an error can be an ambiguous lost ACK.
	a.selectionRev++
	snap.choice.rev = a.selectionRev
	return true, snap.cl.SelectContext(ctx, target)
}
func (d appRepairIO) commit(ctx context.Context, snap *repairCapture, target string) bool {
	a := d.a
	a.life.mu.Lock()
	defer a.life.mu.Unlock()
	a.selectionMu.Lock()
	defer a.selectionMu.Unlock()
	if ctx.Err() != nil || !a.repairOwnedLocked(snap) {
		return false
	}
	px, err := snap.cl.proxiesContext(ctx)
	if err != nil || px.Selected != target || !a.repairOwnedLocked(snap) {
		return false
	}
	if snap.restoring {
		a.fo.clear()
		a.scores.reset()
		return true
	}
	from := selectedServerTag(snap.choice.settings)
	restore := from
	if from == "" {
		from = snap.effective
		restore = snap.selector
	}
	if snap.from != "" {
		from = snap.from
		restore = a.fo.restoreTag()
	}
	a.fo.setRepair(from, target, restore, snap.choice.settings.Selected == "auto")
	a.scores.reset()
	return true
}
func (a *App) diagnoseRepair(ctx context.Context, id string) {
	diagnosis := "UNKNOWN"
	var snap *repairCapture
	defer func() {
		stale := false
		if snap != nil && snap.cl != nil && snap.selector != "" {
			stale = !(appRepairIO{a}).observed(ctx, snap)
		}
		if stale && ctx.Err() == nil {
			diagnosis = "STALE_STATE"
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			diagnosis = "DIAGNOSTIC_TIMEOUT"
		}
		a.repair.finish(id, diagnosis, ctx.Err() != nil || stale)
	}()
	var err error
	snap, err = a.captureRepair(ctx, a.core.Clash())
	snap.operationID = id
	if !snap.networkOK {
		a.repair.check(id, "LOCAL_ADAPTER", "DOWN")
		diagnosis = "LOCAL_NETWORK_DOWN"
		return
	}
	a.repair.check(id, "LOCAL_ADAPTER", "OK")
	if snap.choice.settings.ZapretOnly {
		diagnosis = "VPN_NOT_APPLICABLE"
		return
	}
	if err != nil {
		diagnosis = err.Error()
		a.repair.check(id, "CORE", "UNKNOWN")
		return
	}
	a.repair.check(id, "CORE", "OK")
	if snap.choice.settings.Mode == "proxy" {
		ours, _ := systemProxyOurs(snap.mixedPort)
		if !ours {
			a.repair.check(id, "SYSTEM_PROXY", "CHANGED")
			diagnosis = "PROXY_CHANGED"
			return
		}
		a.repair.check(id, "SYSTEM_PROXY", "OK")
	} else {
		if !repairTUNPresent() {
			a.repair.check(id, "TUN_ADAPTER", "DOWN")
			diagnosis = "TUN_ADAPTER_UNAVAILABLE"
			return
		}
		a.repair.check(id, "TUN_ADAPTER", "OK")
		coverage := repairTUNRoutes()
		a.repair.check(id, "TUN_IPV4_ROUTES", coverage)
		if coverage == "MISSING" {
			diagnosis = "TUN_ROUTES_MISSING"
			return
		}
		for _, it := range mustRepairInterfaces() {
			if strings.EqualFold(it.Name, "Naivka") {
				if raw, e := readRepairRoutes(); e == nil {
					_, v6, bypass := repairRouteAudit(raw, uint32(it.Index))
					a.repair.check(id, "TUN_IPV6_ROUTES", v6)
					a.repair.check(id, "TUN_ROUTE_EXCLUSIONS", bypass)
				}
				break
			}
		}
		a.repair.check(id, "TUN_LEAK_POLICY", "UNKNOWN")
	}
	a.repair.check(id, "KILL_SWITCH", "UNCHANGED")
	// Direct diagnostics are explicitly forbidden while Kill Switch is enabled.
	direct := repairPath{DNS: "UNKNOWN"}
	if !snap.choice.settings.KillSwitch && snap.directPort > 0 {
		dns := ""
		if v, e := parseDirectDNS(snap.choice.settings.DNSDirect); e == nil && v.Kind == "https" {
			host := v.Host
			if v.Port > 0 {
				host = net.JoinHostPort(host, strconv.Itoa(v.Port))
			}
			path := v.Path
			if path == "" {
				path = "/dns-query"
			}
			dns = (&url.URL{Scheme: "https", Host: host, Path: path}).String()
		}
		direct = probeRepairPath(ctx, snap.directPort, dns)
		if v, e := parseDirectDNS(snap.choice.settings.DNSDirect); e == nil && v.Kind != "https" {
			direct.DNS = repairTransportDNS(ctx, snap.directPort, v)
		}
	}
	directStatus := repairPathStatus(direct)
	if direct.good() {
		directStatus = "OK"
	}
	a.repair.check(id, "DIRECT_INTERNET", directStatus)
	a.repair.check(id, "DNS_DIRECT", direct.DNS)
	var primary, actual repairPath
	var primaryDone = make(chan struct{})
	go func() { primary = probeRepairPath(ctx, snap.primaryPort, snap.doh); close(primaryDone) }()
	actual = probeRepairPath(ctx, snap.mixedPort, snap.doh)
	<-primaryDone
	a.repair.check(id, "PRIMARY_PATH", repairPathStatus(primary))
	a.repair.check(id, "DNS_PROXY", primary.DNS)
	a.repair.check(id, "HTTPS_A", actual.HTTP[0])
	a.repair.check(id, "HTTPS_B", actual.HTTP[1])
	a.repair.check(id, "USER_PATH", repairPathStatus(actual))
	if ctx.Err() != nil {
		return
	}
	if primary.good() && primary.DNS != "OK" {
		diagnosis = "DNS_FAILURE"
		return
	}
	if primary.verified() {
		if actual.verified() {
			diagnosis = "PATH_REACHABLE"
		} else if actual.partial() {
			diagnosis = "SITE_SPECIFIC_FAILURE"
		} else {
			diagnosis = "LOCAL_PROXY_PATH_FAILURE"
		}
		return
	}
	if primary.partial() || actual.partial() {
		diagnosis = "SITE_SPECIFIC_FAILURE"
		return
	}
	if direct.HTTP[0] == "REDIRECT" || direct.HTTP[1] == "REDIRECT" {
		diagnosis = "CAPTIVE_PORTAL_POSSIBLE"
		return
	}
	if !(appRepairIO{a}).fresh(ctx, snap, snap.selector) {
		diagnosis = "STALE_STATE"
		return
	}
	candidates := map[string]int{}
	type candidateResult struct {
		tag  string
		path repairPath
	}
	results := make(chan candidateResult, 3)
	tested := 0
	for _, sv := range snap.choice.settings.Servers {
		tag := serverTag(sv)
		port := snap.ports[tag]
		if tag == snap.effective || port == 0 || !repairCompatible(snap.choice.settings, tag) || a.scores.quarantined(tag, time.Now()) {
			continue
		}
		if tested == 3 {
			break
		}
		tested++
		go func(tag string, port int) { results <- candidateResult{tag, probeRepairPath(ctx, port, snap.doh)} }(tag, port)
	}
	for i := 0; i < tested; i++ {
		r := <-results
		a.repair.check(id, "BACKUP_"+strconv.Itoa(i+1), repairPathStatus(r.path))
		if r.path.verified() {
			candidates[r.tag] = snap.ports[r.tag]
		}
	}
	if len(candidates) == 0 {
		diagnosis = "NO_VERIFIED_BACKUP"
		return
	}
	diagnosis = "PRIMARY_UNREACHABLE"
	if !(appRepairIO{a}).fresh(ctx, snap, snap.selector) {
		diagnosis = "STALE_STATE"
		return
	}
	a.repair.offer(id, &repairPlan{snap: snap, candidates: candidates, expires: time.Now().Add(2 * time.Minute)})
}

func (a *App) startVerifiedFailback(cl *Clash, from, to string, choice selectionSnapshot) {
	if a.repair.busy() {
		return
	}
	ctx, id, err := a.repair.begin()
	if err != nil {
		return
	}
	snap, err := a.captureRepair(ctx, cl)
	if err != nil || snap.choice.rev != choice.rev || snap.from != from || snap.to != to || snap.ports[from] == 0 || !repairCompatible(snap.choice.settings, from) {
		a.repair.finish(id, "STALE_STATE", true)
		return
	}
	restore := a.fo.restoreTag()
	snap.restoring = true
	snap.operationID = id
	if restore == "" {
		restore = from
	}
	a.repair.mu.Lock()
	if a.repair.cancel == nil {
		a.repair.mu.Unlock()
		return
	}
	a.repair.switching = true
	a.repair.view.Phase = "RECHECKING"
	a.repair.mu.Unlock()
	go a.performRepair(ctx, id, &repairPlan{snap: snap, candidates: map[string]int{restore: snap.ports[from]}, restore: true}, restore, appRepairIO{a})
}

func mustRepairInterfaces() []net.Interface { v, _ := net.Interfaces(); return v }
