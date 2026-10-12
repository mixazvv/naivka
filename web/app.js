'use strict';

// ---------- доступ ----------
const params = new URLSearchParams(location.search);
const TOKEN = params.get('k') || sessionStorage.getItem('naivka-k') || '';
if (params.get('k')) {
  sessionStorage.setItem('naivka-k', TOKEN);
  history.replaceState(null, '', '/');
}

const $ = (sel, root = document) => root.querySelector(sel);

// replaceChildren превращает null в текст «null» — пропускаем пустые значения везде.
const nativeReplace = Element.prototype.replaceChildren;
Element.prototype.replaceChildren = function (...kids) {
  return nativeReplace.apply(this, kids.flat(Infinity).filter(k => k !== null && k !== undefined && k !== false));
};
const main = $('#main');
const drawerBody = $('#drawerBody');

let S = null;   // настройки
let ST = null;  // состояние ядра
let PX = null;  // серверы глазами ядра
let PXAt = 0;
let STATS = null;
let STATSAt = 0;
let view = sessionStorage.getItem('naivka-view') || 'overview';
let diagTab = sessionStorage.getItem('naivka-diag') || 'now';
let busy = false;
let elevating = false;
let updating = false;
let failures = 0;
const delays = {};
let connList = { list: [], count: 0 };
let logBuf = [];
let logSeq = 0;
const sigs = {};
const ip = { loading: false, data: null, key: '', error: '' };
let srvGeo = {};
let srvGeoKey = '';
let ruleFilter = '';

// ---------- утилиты ----------
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  setAttrs(el, attrs);
  append(el, kids);
  return el;
}
const SVGNS = 'http://www.w3.org/2000/svg';
function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  setAttrs(el, attrs);
  append(el, kids);
  return el;
}
const BOOL_ATTRS = new Set(['disabled', 'checked', 'selected', 'hidden', 'readonly', 'autofocus', 'open', 'required']);
function setAttrs(el, attrs) {
  if (!attrs) return;
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    // Логические атрибуты: пустая строка или 0 значат «нет», а не «есть».
    if (BOOL_ATTRS.has(k) && !v) continue;
    if (k === 'class') el.setAttribute('class', v);
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else if (k === 'checked') el.checked = true;
    else if (k === 'style') el.style.cssText = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
}
function append(el, kids) {
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : String(kid));
  }
}

function rid() {
  const a = new Uint8Array(4);
  crypto.getRandomValues(a);
  return Array.from(a, b => b.toString(16).padStart(2, '0')).join('');
}

function fmtBytes(n) {
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  n = n || 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toLocaleString('ru-RU', { maximumFractionDigits: i > 0 && n < 10 ? 1 : 0 }) + ' ' + u[i];
}
const fmtSpeed = n => fmtBytes(n) + '/с';

function fmtDur(sec) {
  sec = Math.max(0, Math.floor(sec));
  const hh = Math.floor(sec / 3600), mm = Math.floor(sec % 3600 / 60);
  if (hh) return `${hh} ч ${mm} мин`;
  if (mm) return `${mm} мин`;
  return `${sec} с`;
}
const fmtTime = t => new Date(t * 1000).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });

function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}
const count = (n, one, few, many) => `${n} ${plural(n, one, few, many)}`;

let toastTimer;
function toast(msg, bad) {
  const t = $('#toast');
  t.textContent = msg;
  t.className = 'toast show' + (bad ? ' bad' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.className = 'toast'; }, bad ? 5000 : 2200);
}

const serverById = id => S.servers.find(x => x.id === id);
let WL = null; // состояние подписки «белые списки»
let SELF = null; // обновление самой MZ
async function refreshSelf() { try { SELF = await api('/api/self'); } catch (_) { /* не важно */ } }
const wlName = tag => WL && WL.names && WL.names[tag];
const serverByTag = tag => {
  if (tag && tag.startsWith('s-')) return serverById(tag.slice(2));
  if (tag && tag.startsWith('w-')) return { id: tag, name: wlName(tag) || 'сервер подписки', host: 'белые списки', wl: true };
  return null;
};
async function refreshWL() { try { WL = await api('/api/wl'); } catch (_) { /* не важно */ } }
const TR_NAME = { ws: 'WS', grpc: 'gRPC', httpupgrade: 'HTTPUpgrade', xhttp: 'xHTTP' };
const PROTO_TITLE = { hysteria2: 'Hysteria2', tuic: 'TUIC', trojan: 'Trojan', shadowsocks: 'Shadowsocks', anytls: 'AnyTLS' };
const protoName = sv => sv.type === 'vless'
  ? [sv.security === 'tls' ? 'TLS' : sv.security === 'none' ? 'VLESS' : 'Reality', TR_NAME[sv.transport]].filter(Boolean).join(' ')
  : PROTO_TITLE[sv.type] ? [PROTO_TITLE[sv.type], sv.type === 'trojan' ? TR_NAME[sv.transport] : ''].filter(Boolean).join(' ')
  : (sv.quic ? 'Naive H3' : 'Naive');
const xrayReady = () => !!(AD && AD.updates && AD.updates.xray && AD.updates.xray.installed);

function patch(id, sig, build) {
  const el = document.getElementById(id);
  if (!el) return;
  const key = id + ':' + view;
  if (sigs[key] === sig && el.dataset.built) return;
  sigs[key] = sig;
  el.dataset.built = '1';
  el.replaceChildren(...[build()].flat().filter(Boolean));
}

const cc = code => flag(code); // флаг страны или значок с буквами

function download(name, text, type) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = h('a', { href: url, download: name });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 2000);
}

function qrSvg(text) {
  const qr = qrcode(0, 'M');
  qr.addData(text);
  qr.make();
  const n = qr.getModuleCount();
  let d = '';
  for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`;
  return s('svg', { viewBox: `-2 -2 ${n + 4} ${n + 4}`, 'shape-rendering': 'crispEdges' }, s('path', { d, fill: '#000' }));
}

// Окно — приложение, а не браузер: никаких перезагрузок, поиска и масштаба.
document.addEventListener('keydown', e => {
  const k = e.key.toLowerCase();
  if (e.key === 'F5' || e.key === 'F3' || e.key === 'F7' ||
      (e.ctrlKey && ['r', 'f', 'p', 'g', 'u', 's', 'j', '+', '-', '=', '0'].includes(k))) e.preventDefault();
});
document.addEventListener('wheel', e => { if (e.ctrlKey) e.preventDefault(); }, { passive: false });
document.addEventListener('contextmenu', e => {
  if (!e.target.closest('input, textarea')) e.preventDefault();
});

// ---------- тема ----------
const darkQuery = matchMedia('(prefers-color-scheme: dark)');
let themeSent = '';
function applyTheme() {
  // «Чёрное золото» — тёмная тема со своей палитрой и геометрией (срезанные углы, шестиугольники).
  const gold = !!S && S.theme === 'gold';
  const blocks = !!S && S.theme === 'blocks';
  const t = gold || blocks ? 'dark' : S && S.theme === 'system' ? (darkQuery.matches ? 'dark' : 'light') : (S ? S.theme : 'dark');
  const root = document.documentElement;
  root.dataset.theme = t;
  if (gold || blocks) root.dataset.skin = gold ? 'gold' : 'blocks'; else delete root.dataset.skin;
  root.dataset.accent = gold || blocks ? root.dataset.skin : (S && S.accent) || 'lime';
  if (S && S.oled && t === 'dark' && !root.dataset.skin) root.dataset.oled = ''; else delete root.dataset.oled;
  // Сплошной фон: в OLED и тематических стилях полупрозрачный Mica не нужен.
  if (root.dataset.skin || root.dataset.oled !== undefined) root.dataset.solid = ''; else delete root.dataset.solid;
  if (root.dataset.skin === 'blocks' && root.dataset.prevSkin !== 'blocks' && $('#main') && $('#main').firstChild) setTimeout(() => chunkReveal($('#main'), 'var(--fog)'), 30);
  root.dataset.prevSkin = root.dataset.skin || '';
  if (themeSent !== t) { themeSent = t; api('/api/window/theme', { dark: t === 'dark' }).catch(() => { themeSent = ''; }); }
}
darkQuery.addEventListener('change', () => { if (S && S.theme === 'system') applyTheme(); });

// ---------- API ----------
async function api(path, body) {
  const opt = { method: body === undefined ? 'GET' : 'POST', headers: { 'X-Naivka-Token': TOKEN } };
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, opt);
  } catch (e) {
    setOffline(true);
    throw new Error('MZ не отвечает');
  }
  setOffline(false);
  let data = {};
  try { data = await res.json(); } catch (_) { /* пустой ответ */ }
  if (!res.ok) throw new Error(data.error || `Ошибка ${res.status}`);
  return data;
}

function setOffline(on) {
  const box = $('#offline');
  if (!on) { failures = 0; box.hidden = true; return; }
  failures++;
  if (elevating) {
    $('#offlineText').textContent = updating ? 'MZ обновляется и сейчас откроет новое окно. Это окно можно закрыть.'
      : 'MZ перезапускается с правами администратора и откроет новое окно. Это окно можно закрыть.';
    box.hidden = false;
    setTimeout(() => window.close(), 1500);
  } else if (failures >= 3) {
    box.hidden = false;
  }
}

// ---------- сохранение настроек ----------
let saveTimer = null;
let savePending = false;
function save(delay = 350) {
  savePending = true;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveNow, delay);
}
async function saveNow() {
  clearTimeout(saveTimer);
  if (!savePending) return true;
  savePending = false;
  try {
    await api('/api/settings', S);
    refreshState();
    return true;
  } catch (e) {
    toast(e.message, true);
    return false;
  }
}

// ---------- опрос ----------
async function refreshState() {
  try { ST = await api('/api/state'); } catch (_) { return; }
  updateStatus();
}

async function refreshProxies() {
  try { PX = await api('/api/proxies'); PXAt = Date.now(); } catch (_) { return; }
  if (PX && PX.running === false) PX = null;
  updateStatus();
  if (view === 'servers') renderServers();
}

async function refreshStats() {
  try { STATS = await api('/api/stats?n=120'); STATSAt = Date.now(); } catch (_) { return; }
  renderOverviewLive();
  if (view === 'servers') renderServers();
  if (view === 'rules') renderRules();
}

async function refreshConns() {
  try { connList = await api('/api/conns'); } catch (_) { return; }
  updateConnTable();
}

async function refreshLogs() {
  let lines;
  try { lines = await api('/api/logs?after=' + logSeq); } catch (_) { return; }
  if (!lines.length) return;
  logSeq = lines[lines.length - 1].seq;
  logBuf.push(...lines.map(l => l.text));
  if (logBuf.length > 3000) logBuf = logBuf.slice(-3000);
  const box = $('#logBox');
  if (box) {
    box.append(...lines.map(l => logLine(l.text)));
    if ($('#logFollow')?.checked) box.scrollTop = box.scrollHeight;
  }
}

function ipKey() {
  if (!ST) return '';
  if (!ST.running) return 'off';
  if (!PX) return 'on';
  return 'on|' + (PX.selected === 'auto' ? PX.autoNow : PX.selected);
}

async function refreshIP(force) {
  if (!S || !S.showIp || !ST || ip.loading || ST.starting || busy) return;
  const key = ipKey();
  if (!force && key === ip.key) return;
  if (ST.running && !PX) return;
  ip.loading = true;
  ip.key = key;
  renderIP();
  try {
    ip.data = await api('/api/ip');
    ip.error = '';
  } catch (e) {
    ip.error = e.message;
  } finally {
    ip.loading = false;
    renderIP();
  }
}

async function refreshServerGeo(force) {
  if (!S || !S.showIp || !S.servers.length) return;
  const key = S.servers.map(x => x.host).join(',');
  if (!force && key === srvGeoKey) return;
  srvGeoKey = key;
  try { srvGeo = await api('/api/geo/servers'); } catch (_) { srvGeoKey = ''; return; }
  if (view === 'servers') renderServers();
  updateStatus();
}

// skeletonCol — серая «заготовка» колонки, пока данные грузятся.
function skeletonCol() {
  return h('div', { class: 'ipcol' }, h('div', { class: 'skel', style: 'width:38%;height:12px' }),
    h('div', { class: 'skel', style: 'width:64%;height:22px;margin-top:8px' }),
    h('div', { class: 'skel', style: 'width:52%;height:12px;margin-top:10px' }),
    h('div', { class: 'skel', style: 'width:70%;height:12px;margin-top:8px' }));
}

let tickN = 0;
async function tick() {
  tickN++;
  try {
    await refreshState();
    const running = ST && ST.running;
    if (running && Date.now() - PXAt > 3000) await refreshProxies();
    if (!running && PX) { PX = null; updateStatus(); }
    if (!document.hidden || tickN % 5 === 0) await refreshStats();
    refreshIP(false);
    if (view === 'diag' && diagTab === 'conns' && running) await refreshConns();
    if (view === 'diag' && diagTab === 'logs') await refreshLogs();
    if (view === 'bypass' || tickN % 4 === 0) await refreshAddons();
    if (S && S.wl && S.wl.enabled && (view === 'servers' ? tickN % 3 === 0 : tickN % 20 === 2)) { await refreshWL(); if (view === 'servers') renderServers(); }
    if (tickN % 30 === 3 || (view === 'settings' && SELF && SELF.busy)) { await refreshSelf(); if (view === 'settings') patch('selfBox', JSON.stringify(SELF), selfBox); }
    if (view === 'diag' && diagTab === 'speed') await refreshSpeed();
  } finally {
    setTimeout(tick, 1000);
  }
}

// ---------- состояние ----------
function phase() {
  if (!ST) return 'off';
  if (busy || ST.starting) return 'wait';
  if (ST.blocked) return 'blocked';
  if (ST.running) return 'on';
  if (ST.error) return 'err';
  return 'off';
}

const TITLES = { on: 'Подключено', off: 'Отключено', wait: 'Подключение…', err: 'Не удалось подключиться', blocked: 'Трафик заблокирован' };
const ROUTING = { 'ru-direct': 'Россия напрямую', 'blocked-only': 'Только заблокированное', 'global': 'Всё через сервер' };

function currentServer() {
  if (PX && ST && ST.running && (PX.selected === 'wl' || (PX.selected || '').startsWith('w-'))) {
    const tag = PX.selected === 'wl' ? PX.wlNow : PX.selected;
    const f = splitFlag(wlName(tag) || '');
    const ms = PX.delays && PX.delays[tag];
    return { name: PX.selected === 'wl' ? 'Белые списки' : 'Белые списки (закреплён)', host: f.name || 'проверяю серверы',
      cc: f.cc, wlTag: tag, wlMs: ms, wlGroup: true, auto: false };
  }
  if (!S.servers.length) return { name: 'Нет серверов', host: 'добавьте сервер' };
  if (PX && ST && ST.running) {
    if (PX.selected === 'auto') {
      const sv = serverByTag(PX.autoNow);
      return sv ? { ...sv, auto: true } : { name: 'Автовыбор', host: 'проверяю серверы' };
    }
    const sv = serverByTag(PX.selected);
    if (sv) return sv;
  }
  if (S.selected === 'auto') return { name: 'Автовыбор', host: count(S.servers.length, 'сервер', 'сервера', 'серверов') };
  return serverById(S.selected) || { name: 'Автовыбор', host: '' };
}

function updateStatus() {
  if (!ST || !S) return;
  const p = phase();
  document.body.dataset.state = p;
  $('#navTitle').textContent = TITLES[p];
  const cs = currentServer();
  $('#navSub').textContent = p === 'on' ? (cs.auto ? `${cs.name}, авто` : cs.name) : (ST.mode === 'tun' || S.mode === 'tun' ? 'режим TUN' : '');
  navCounts();
  const hbt = $('#hbToggle');
  if (hbt) hbt.textContent = p === 'on' || p === 'blocked' ? 'Отключить' : 'Подключить';
  const hbs = $('#hbSession');
  if (hbs) hbs.textContent = p === 'on' ? `канал открыт ${fmtDur(Date.now() / 1000 - ST.startedAt)} · ${cs.name}` : TITLES[p].toLowerCase();
  const sp0 = lastSpeeds();
  const kb = (p === 'on' ? sp0.pd + sp0.pu : 0) / 1024;
  document.body.style.setProperty('--energy', Math.min(1, Math.log10(kb + 1) / 3.5).toFixed(2));
  const xp = $('#hbXp');
  if (xp) xp.style.width = (Math.min(1, Math.log10(kb + 1) / 4) * 100).toFixed(1) + '%';
  const tbs = $('#tbStatus');
  if (tbs) tbs.textContent = TITLES[p] + (p === 'on' ? ' · ' + cs.name : '');

  if ($('#statusTitle')) {
    $('#statusTitle').textContent = TITLES[p];
    let sub = 'Трафик идёт как обычно, без сервера.';
    if (p === 'on') sub = (ST.mode === 'tun' ? 'Весь трафик компьютера' : `Системный прокси 127.0.0.1:${ST.mixedPort}`) + `, ${fmtDur(Date.now() / 1000 - ST.startedAt)}`;
    else if (p === 'wait') sub = 'Запускаю sing-box и проверяю конфиг.';
    else if (p === 'err') sub = 'Подробности ниже и в журнале.';
    else if (p === 'blocked') sub = 'Ядро упало, защита от утечек не выпускает трафик напрямую. MZ пробует восстановить связь.';
    $('#statusSub').textContent = sub;
    const btn = $('#connectBtn');
    btn.disabled = p === 'wait';
    btn.setAttribute('aria-label', powerLabel(p));
    btn.title = powerLabel(p);
    renderTiles();
    const n = [SELF && SELF.available, ST.error, ST.needsRestart, S.mode, ST.admin, ST.core.present, ST.core.naive, S.servers.length, ST.running, ST.blocked, JSON.stringify(ST.portIssue || null), S.dnsDirect, S.routing, sessionStorage.getItem('naivka-dns-ok')].join('|');
    patch('notices', n, buildNotices);
    renderLanes();
  }
  if (view === 'settings' && $('#coreBox')) {
    patch('coreBox', JSON.stringify([ST.core, ST.download, ST.running, ST.dataDir, ST.protected]), buildCoreBox);
    patch('adminBox', [S.mode, ST.admin, ST.os].join('|'), buildAdminBox);
  }
}

// Числа на вкладках (видны в теме «Чёрное золото»): серверы, правила, соединения.
function navCounts() {
  const counts = { servers: S.servers.length, rules: S.rules.length + (S.ruleLists || []).length, diag: ST && ST.running ? connList.count || 0 : 0 };
  document.querySelectorAll('.nav-item').forEach(b => {
    const n = counts[b.dataset.view];
    let el = b.querySelector('.nav-count');
    if (!n) { if (el) el.remove(); return; }
    if (!el) { el = h('span', { class: 'nav-count', 'aria-hidden': 'true' }); b.querySelector('.nav-label').append(el); }
    el.textContent = n > 99 ? '99+' : n;
  });
}

// Заголовок раздела «проступает» из случайных символов (только в «Чёрном золоте»).
function decodeTitle(el) {
  if (!el || REDUCE.matches || document.documentElement.dataset.skin !== 'gold') return;
  const text = el.textContent;
  const glyphs = 'АБВГДЕЖЗИКЛМНОПРСТУФХЦЧШЭЮЯ0123456789#/<>_';
  const t0 = performance.now(), dur = 520;
  const step = now => {
    const k = Math.min(1, (now - t0) / dur);
    const shown = Math.floor(text.length * k);
    el.textContent = text.slice(0, shown) + Array.from(text.slice(shown), ch => ch === ' ' ? ' ' : glyphs[Math.random() * glyphs.length | 0]).join('');
    if (k < 1) requestAnimationFrame(step); else el.textContent = text;
  };
  requestAnimationFrame(step);
}

function notice(kind, text, action) {
  return h('div', { class: 'notice ' + kind }, h('div', { class: 'grow' }, text), action);
}

function buildNotices() {
  const out = [];
  if (!S.servers.length) out.push(notice('info', 'Сначала добавьте сервер.', h('button', { class: 'btn small', onclick: () => go('servers') }, 'Добавить сервер')));
  if (!ST.core.present) out.push(notice('warn', 'Ядро sing-box ещё не скачано.', h('button', { class: 'btn small', onclick: () => go('settings') }, 'Перейти к загрузке')));
  if (ST.core.present && !ST.core.naive && S.servers.some(x => x.type === 'naive')) {
    out.push(notice('warn', 'Эта сборка ядра не поддерживает Naive (так бывает на Windows 7) — будут работать серверы остальных протоколов (Reality, Hysteria2 и другие).'));
  }
  if (S.mode === 'tun' && !ST.admin && ST.os === 'windows') out.push(notice('warn', 'Для режима TUN нужны права администратора.', h('button', { class: 'btn small', onclick: elevate }, 'Перезапустить с правами')));
  if (SELF && SELF.available) out.push(notice('info', `Вышла новая версия MZ: ${SELF.latest} (у вас ${SELF.version}).`, h('button', { class: 'btn small', onclick: () => go('settings') }, 'Подробнее')));
  if (S.servers.length && S.routing !== 'global' && !dnsEncrypted(S.dnsDirect) && !sessionStorage.getItem('naivka-dns-ok')) {
    out.push(notice('info', 'DNS для сайтов «напрямую» не зашифрован: провайдер видит, какие сайты вы открываете.',
      h('div', { class: 'row', style: 'flex-wrap:nowrap' },
        h('button', { class: 'btn small', onclick: () => {
          S.dnsDirect = DNS_DIRECT[0][0]; save(0);
          toast('Включён зашифрованный DNS Яндекса — переподключитесь, чтобы применить');
        } }, 'Зашифровать'),
        h('button', { class: 'btn small', onclick: () => { sessionStorage.setItem('naivka-dns-ok', '1'); updateStatus(); } }, 'Не сейчас'))));
  }
  if (ST.needsRestart) out.push(notice('info', 'Настройки изменены. Они применятся после переподключения.', h('button', { class: 'btn small', onclick: reconnect }, 'Переподключить')));
  const pi = !ST.running && ST.portIssue;
  if (pi) {
    const who = pi.kind === 'naivka' ? 'другая копия MZ' : pi.name ? `программа ${pi.name}` : 'другая программа';
    out.push(h('div', { class: 'notice error' }, h('div', { class: 'grow' },
      h('b', null, `Порт ${pi.port} занят`),
      h('p', { style: 'margin:4px 0 0' }, `Его держит ${who}` + (pi.pid ? ` (PID ${pi.pid})` : '') + '. ' + (pi.kind === 'naivka'
        ? 'Закройте её через значок в трее (он может прятаться под стрелкой у часов) или возьмите свободный порт.'
        : 'Закройте эту программу или пусть MZ возьмёт свободный порт — браузеры и программы, которые используют системный прокси, подхватят его сами.')),
      pi.path ? h('div', { class: 'cell-sub', style: 'margin-top:4px;word-break:break-all' }, pi.path) : null),
      h('div', { class: 'row', style: 'flex-wrap:nowrap' },
        h('button', { class: 'btn small primary', onclick: async () => {
          busy = true; updateStatus();
          try { const r = await api('/api/port/auto', {}); S.mixedPort = r.port; toast(`Порт сменён на ${r.port}, подключено`); }
          catch (e) { toast(e.message.split('\n')[0], true); }
          finally { busy = false; await refreshState(); }
        } }, 'Взять свободный порт'),
        h('button', { class: 'btn small', onclick: toggleConnect }, 'Повторить'))));
  }
  if (ST.blocked) out.push(notice('error', 'Защита от утечек держит трафик, пока ядро не поднимется. Чтобы вернуть обычный интернет без сервера, отключитесь.', h('button', { class: 'btn small', onclick: toggleConnect }, 'Отключить')));
  else if (ST.error && !ST.running && !pi) out.push(h('div', { class: 'notice error' }, h('div', { class: 'grow' }, h('b', null, 'Ошибка'), h('pre', null, ST.error))));
  return out;
}

// ---------- действия ----------
async function toggleConnect() {
  if (busy) return;
  busy = true;
  updateStatus();
  try {
    if (ST && (ST.running || ST.blocked)) await api('/api/disconnect', {});
    else { await saveNow(); await api('/api/connect', {}); PXAt = 0; }
  } catch (e) {
    toast(e.message.split('\n')[0], true);
  } finally {
    busy = false;
    await refreshState();
    if (ST && ST.running) await refreshProxies();
  }
}

async function reconnect() {
  busy = true;
  updateStatus();
  try { await saveNow(); await api('/api/reconnect', {}); toast('Переподключено'); }
  catch (e) { toast(e.message.split('\n')[0], true); }
  finally { busy = false; await refreshState(); }
}

async function elevate() {
  elevating = true;
  try { await saveNow(); await api('/api/elevate', {}); }
  catch (e) { elevating = false; toast(e.message, true); }
}

// ---------- навигация ----------
function go(v) {
  goNow(VIEWS[v] && sectionOn(v) ? v : 'overview');
}

// «Свой сервер» можно убрать из меню в настройках.
function sectionOn(v) {
  return v !== 'own' || !S || S.ownServer !== false;
}

// Разделы в меню слева: какие видны, их номера для клавиш 1–7 и «пройденный» отрезок линии до текущего.
function navOrder() {
  return [...document.querySelectorAll('#rail .nav-item')].filter(b => !b.hidden).map(b => b.dataset.view);
}

function applySections() {
  const own = $('#navOwn');
  if (own) own.hidden = !sectionOn('own');
  const order = navOrder();
  document.querySelectorAll('#rail .nav-item').forEach(b => {
    const i = order.indexOf(b.dataset.view);
    b.querySelector('.nav-key').textContent = i >= 0 ? i + 1 : '';
    b.title = b.querySelector('.nav-label').firstChild.textContent + (i >= 0 ? ` (${i + 1})` : '');
  });
  const last = $('#hbLast');
  if (last) last.textContent = order.length;
  railMark();
  if (!sectionOn(view)) go('overview');
}

function railMark() {
  const rail = $('#rail');
  if (!rail) return;
  const order = navOrder(), at = Math.max(0, order.indexOf(view));
  rail.style.setProperty('--nav-i', at);
  rail.querySelectorAll('.nav-item').forEach(b => b.classList.toggle('passed', order.indexOf(b.dataset.view) < at && !b.hidden));
}

// Главный экран («сцена» с ветвями) всегда на месте; разделы открываются панелью справа поверх него.
function goNow(v) {
  const was = view;
  view = v;
  sessionStorage.setItem('naivka-view', v);
  document.querySelectorAll('.nav-item, .brand').forEach(b => {
    if (b.dataset.view === v) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
  });
  railMark();
  Object.keys(sigs).forEach(k => delete sigs[k]);
  const drawer = $('#drawer');
  if (v === 'overview') {
    drawer.classList.remove('open');
    $('#scrim').hidden = true;
    setTimeout(() => { if (view === 'overview') { drawer.hidden = true; drawerBody.replaceChildren(); } }, 260);
    main.removeAttribute('inert');
  } else {
    drawerBody.replaceChildren(VIEWS[v]());
    drawerBody.scrollTop = 0;
    drawer.hidden = false;
    $('#scrim').hidden = false;
    requestAnimationFrame(() => { drawer.classList.add('open'); setTimeout(() => chunkReveal(drawer, 'var(--bg)'), 30); });
    main.setAttribute('inert', '');
    decodeTitle(drawerBody.querySelector('h1'));
    if (was === 'overview') setTimeout(() => $('#drawerClose').focus({ preventScroll: true }), 50);
  }
  updateStatus();
  renderOverviewLive(); renderIP(); refreshIP(false); refreshServerGeo(false); refreshStats();
  if (v === 'servers') { renderServers(); refreshProxies(); }
  if (v === 'own') renderOwn();
  if (v === 'rules') { renderRules(); renderLists(); }
  if (v === 'bypass') refreshAddons();
  if (v === 'settings') { shareBox(); refreshSelf().then(() => patch('selfBox', JSON.stringify(SELF), selfBox)); }
  if (v === 'servers') Promise.all([refreshAddons(), S.wl.enabled ? refreshWL() : null]).then(renderServers);
}

function openDiag(tab) {
  diagTab = tab;
  sessionStorage.setItem('naivka-diag', tab);
  go('diag');
}

// ---------- элементы форм ----------
function segmented(options, get, set, label) {
  const wrap = h('div', { class: 'seg', role: 'group', 'aria-label': label });
  const mark = () => wrap.querySelectorAll('button').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.v === get())));
  for (const [v, text] of options) wrap.append(h('button', { 'data-v': v, onclick: () => { set(v); mark(); } }, text));
  mark();
  return wrap;
}

function check(key, title, desc, after) {
  return h('label', { class: 'check' },
    h('input', { type: 'checkbox', checked: S[key], onchange: e => { S[key] = e.target.checked; save(); if (after) after(); } }),
    h('span', null, h('b', null, title), desc && h('small', null, desc)));
}

function field(key, label, opts = {}) {
  const input = h('input', {
    type: opts.type || 'text', value: S[key], placeholder: opts.placeholder, min: opts.min, max: opts.max, spellcheck: 'false',
    onchange: e => { S[key] = opts.type === 'number' ? parseInt(e.target.value, 10) || 0 : e.target.value.trim(); save(); },
  });
  return h('label', { class: 'field' }, h('span', null, label), input, opts.hint && h('small', { class: 'hint' }, opts.hint));
}

const DNS_DIRECT = [
  ['https://common.dot.dns.yandex.net/dns-query', 'Яндекс, зашифрованный (DoH)'],
  ['tls://common.dot.dns.yandex.net', 'Яндекс, зашифрованный (DoT)'],
  ['https://dns.adguard-dns.com/dns-query', 'AdGuard, зашифрованный, без рекламы (DoH)'],
  ['https://1.1.1.1/dns-query', 'Cloudflare, зашифрованный (DoH)'],
  ['77.88.8.8', 'Яндекс, обычный — без шифрования'],
  ['local', 'Системный — DNS провайдера, без шифрования'],
];
const dnsEncrypted = v => /^(https|tls):\/\//.test(v || '');

function dnsDirectField() {
  const known = DNS_DIRECT.some(([v]) => v === S.dnsDirect);
  const custom = h('input', { type: 'text', value: known ? '' : S.dnsDirect, placeholder: 'https://адрес/dns-query или tls://адрес', spellcheck: 'false', hidden: known,
    onchange: e => { S.dnsDirect = e.target.value.trim(); save(); upd(); } });
  const note = h('small', { class: 'hint' });
  const upd = () => {
    note.className = dnsEncrypted(S.dnsDirect) ? 'hint' : 'hint err-text';
    note.textContent = dnsEncrypted(S.dnsDirect)
      ? 'Для российских сайтов. Запросы зашифрованы: провайдер не видит, какие сайты вы открываете.'
      : 'Без шифрования провайдер видит имена всех сайтов, которые идут напрямую. Лучше выбрать зашифрованный вариант.';
  };
  const sel = h('select', { onchange: e => {
    const v = e.target.value;
    custom.hidden = v !== 'custom';
    if (v === 'custom') { custom.focus(); return; }
    S.dnsDirect = v; save(); upd();
  } },
    DNS_DIRECT.map(([v, t]) => h('option', { value: v, selected: v === S.dnsDirect }, t)),
    h('option', { value: 'custom', selected: !known }, 'Свой адрес…'));
  upd();
  return h('label', { class: 'field' }, h('span', null, 'DNS напрямую'), sel, custom, note);
}

function choices(name, options, get, set) {
  return h('div', { class: 'choices', role: 'radiogroup' }, options.map(([v, title, desc]) =>
    h('label', { class: 'choice' },
      h('input', { type: 'radio', name, value: v, checked: get() === v, onchange: () => set(v) }),
      h('span', null, h('b', null, title), h('small', null, desc)))));
}

function jsonArea(key, placeholder, wantArray) {
  const err = h('div', { class: 'err-text' });
  const ta = h('textarea', {
    spellcheck: 'false', placeholder, value: S[key],
    oninput: e => {
      const t = e.target.value;
      S[key] = t;
      if (!t.trim()) { err.textContent = ''; save(800); return; }
      try {
        const v = JSON.parse(t);
        if (wantArray && !Array.isArray(v)) throw new Error('нужен массив [ … ]');
        if (!wantArray && (Array.isArray(v) || typeof v !== 'object' || v === null)) throw new Error('нужен объект { … }');
        err.textContent = '';
        save(800);
      } catch (ex) { err.textContent = 'Ошибка JSON: ' + ex.message; }
    },
  });
  return h('div', null, ta, err);
}

function more(title, note, ...content) {
  return h('details', { class: 'more' },
    h('summary', null, h('span', null, title), note && h('small', null, note)),
    h('div', { class: 'more-body' }, ...content));
}

function openDialog(...content) {
  const dlg = h('dialog', null, ...content);
  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  return dlg;
}

// ---------- обзор ----------
function viewOverview() {
  return h('div', { class: 'stage-inner' },
    h('div', { class: 'stage-head' },
      h('div', { class: 'stage-status' },
        h('div', { class: 'status-title', id: 'statusTitle' }, '…'),
        h('div', { class: 'sub', id: 'statusSub' })),
      h('div', { class: 'sysline', id: 'sysLine' })),
    h('div', { id: 'notices', class: 'stage-notices' }),
    h('div', { class: 'lanes-stage', id: 'lanesCard' },
      h('div', { class: 'lanes-box', id: 'lanesBox' }, h('div', { id: 'lanesSvg' }), powerButton('connectBtn')),
      h('div', { class: 'lanes-legend', id: 'lanesLegend' })),
    h('div', { class: 'stage-controls' },
      h('label', null, 'Перехват', segmented([['proxy', 'Системный прокси'], ['tun', 'Весь трафик (TUN)']], () => S.mode, v => { S.mode = v; save(); updateStatus(); }, 'Перехват')),
      h('label', null, 'Маршрут', segmented(Object.entries(ROUTING), () => S.routing, v => { S.routing = v; save(); updateStatus(); }, 'Маршрут'))),
    h('div', { class: 'stage-tiles' },
      h('button', { class: 'tile', type: 'button', id: 'tileSpeed', onclick: () => openDiag('now'), title: 'График скорости и активные сайты' }),
      h('button', { class: 'tile', type: 'button', id: 'tileIp', onclick: () => openDiag('now'), title: 'Как вас видят сайты и карта' }),
      h('button', { class: 'tile', type: 'button', id: 'tileSession', onclick: () => openDiag('traffic'), title: 'История трафика' })));
}

// Плитки под схемой: скорость, как вас видят сайты, сеанс. Щелчок открывает подробности.
function renderTiles() {
  const t1 = $('#tileSpeed');
  if (!t1 || !S || !ST) return;
  const p = phase();
  const on = p === 'on';
  const sp = lastSpeeds();
  const cs = currentServer();
  const d = cs.id ? delayFor(cs.id) : null;
  t1.replaceChildren(h('span', { class: 'tile-k' }, 'Скорость'),
    h('span', { class: 'tile-v' }, h('span', { class: 'hint' }, '↓ '), tnum('t-down', on ? sp.pd + sp.dd + sp.zd + sp.td : 0, fmtSpeed),
      h('span', { class: 'hint' }, '   ↑ '), tnum('t-up', on ? sp.pu + sp.du + sp.zu + sp.tu : 0, fmtSpeed)),
    h('span', { class: 'tile-spark' }, speedSpark()));
  const t2 = $('#tileIp');
  const dd = ip.data;
  const g = dd && ((dd.proxy && dd.proxy.info) || (dd.direct && dd.direct.info));
  t2.replaceChildren(h('span', { class: 'tile-k' }, 'Сайты видят'),
    !S.showIp ? h('span', { class: 'tile-v muted' }, 'проверка IP выключена')
      : !g ? (ip.loading ? h('span', { class: 'skel', style: 'width:70%;height:18px' }) : h('span', { class: 'tile-v muted' }, 'не определено'))
      : h('span', { class: 'tile-v' }, flag(g.countryCode), g.ip),
    g ? h('span', { class: 'tile-sub' }, [g.city || g.country, g.isp].filter(Boolean).join(', ')) : null);
  const t = (STATS && STATS.totals) || [0, 0];
  const t3 = $('#tileSession');
  t3.replaceChildren(h('span', { class: 'tile-k' }, 'Сеанс'),
    h('span', { class: 'tile-v' }, on ? fmtDur(Date.now() / 1000 - ST.startedAt) : 'не подключено'),
    h('span', { class: 'tile-sub' }, on ? `через сервер ${fmtBytes(t[0] + t[1])}` : 'щёлкните, чтобы открыть историю трафика'));
  const sl = $('#sysLine');
  if (sl) {
    const word = { on: 'защищён', off: 'открыт', wait: 'синхронизация…', err: 'сбой', blocked: 'заблокирован' }[p];
    sl.replaceChildren(h('span', { class: 'sl-key' }, 'Канал /'), h('b', { class: 'sl-val ' + p }, word),
      on && d && d.ms ? h('span', { class: 'sl-ms' }, `· ${cs.name} · ${d.ms} мс`) : on ? h('span', { class: 'sl-ms' }, `· ${cs.name}`) : null);
  }
}

const ZERO = { pd: 0, pu: 0, dd: 0, du: 0, zd: 0, zu: 0, td: 0, tu: 0 };
function lastSpeeds() {
  const smp = STATS && STATS.samples || [];
  const l = smp[smp.length - 1];
  if (!l || Date.now() / 1000 - l.t > 4) return ZERO;
  return { ...ZERO, ...l };
}

const zapretOn = () => S.zapret && S.zapret.enabled && AD && AD.zapret && AD.zapret.running;
const tgOn = () => S.telegram && S.telegram.enabled && AD && AD.telegram && AD.telegram.running;

// Иконки 24×24, рисуются линией.
const ICONS = {
  pc: 'M4 5h16a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM9 20h6M12 16v4',
  globe: 'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18',
  server: 'M5 4h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM5 14h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1zM8 7h.01M8 17h.01',
  shield: 'M12 3l7 3v5c0 4.5-3 8.2-7 10-4-1.8-7-5.5-7-10V6l7-3zM9 12l2 2 4-4',
  tg: 'M21 4L3 11.5l6.5 2.2M21 4l-3 16-8.5-6.3M21 4L9.5 13.7v5.8l3.2-3.7',
};
function icon(name, cx, cy, size, color) {
  const sc = size / 24;
  return s('g', { transform: `translate(${cx - 12 * sc} ${cy - 12 * sc}) scale(${sc})`, class: 'lane-icon', style: `stroke:${color}` },
    s('path', { d: ICONS[name] }));
}


function lanePath(x0, y0, nx, ny, x1, y1) {
  const a = x0 + (nx - x0) * 0.5, b = nx + (x1 - nx) * 0.5;
  return `M${x0} ${y0} C ${a} ${y0} ${a} ${ny} ${nx} ${ny} C ${b} ${ny} ${b} ${y1} ${x1} ${y1}`;
}

// Список полос: что сейчас реально работает и сколько через него идёт.
function laneList() {
  const p = phase();
  const on = p === 'on';
  const sp = lastSpeeds();
  const cs = currentServer();
  const g = cs.id && srvGeo[cs.id] && srvGeo[cs.id].info;
  const d = cs.id ? delayFor(cs.id) : null;
  const ms = d && !d.error && d.ms ? `${d.ms} мс` : '';
  const lanes = [{
    key: 'proxy', name: 'Через сервер', color: 'var(--tunnel)', speed: on ? sp.pd + sp.pu : 0, active: on,
    node: cs.wlGroup
      ? { icon: 'server', title: cs.name, cc: cs.cc, sub: [cs.host, cs.wlMs ? `${cs.wlMs} мс` : ''].filter(Boolean).join(', ') }
      : { icon: 'server', title: cs.auto ? cs.name + ' (авто)' : cs.name, cc: g && g.countryCode, sub: [g && g.city, ms].filter(Boolean).join(', ') || cs.host || '' },
  }];
  if (tgOn()) lanes.push({
    key: 'tg', name: 'Telegram', color: 'var(--tg)', speed: sp.td + sp.tu, active: true,
    node: { icon: 'tg', title: 'Telegram', sub: AD.telegram.conns ? count(AD.telegram.conns, 'соединение', 'соединения', 'соединений') : 'ждёт Telegram' },
  });
  if (zapretOn()) lanes.push({
    key: 'zapret', name: 'zapret', color: 'var(--warn)', speed: on ? sp.zd + sp.zu : 0, active: true, unmeasured: !on,
    node: { icon: 'shield', title: 'zapret', sub: AD.zapret.degraded ? 'не справляется' : AD.zapret.strategy },
  });
  lanes.push({ key: 'direct', name: 'Напрямую', color: 'var(--direct)', speed: on ? sp.dd + sp.du : 0, active: true, unmeasured: !on, node: null });
  return lanes;
}

function renderLanes() {
  const box = $('#lanesCard');
  if (!box || !ST || !S) return;
  const lanes = laneList();
  const total = lanes.reduce((a, l) => a + l.speed, 0);
  const n = lanes.length;
  const W = 1000, top = 58, bottom = n > 2 ? 70 + 82 * (n - 1) : 236, H = bottom + 92;
  const Y0 = (top + bottom) / 2;
  const nodeX = { proxy: 500, tg: 405, zapret: 595, direct: 500 };
  const mc = document.documentElement.dataset.skin === 'blocks';
  const geo = lanes.map((l, i) => ({ ...l, ny: n === 1 ? Y0 : top + i * (bottom - top) / (n - 1), nx: nodeX[l.key] }));
  geo.forEach(l => {
    const share = total > 0 ? l.speed / total : 0;
    l.w = !l.active ? 2.5 : total > 0 ? Math.round(3 + 9 * share) : 3.5;
    l.flow = l.active && l.speed >= 50;
  });
  const sig = JSON.stringify([mc, phase(), S.mode, S.mixedPort, S.routing, geo.map(l => [l.key, l.w, l.flow, l.active, l.node && [l.node.title, l.node.sub, l.node.cc]])]);
  let binds = null;
  patch('lanesSvg', sig, () => {
    binds = [];
    const defs = s('defs', null, s('filter', { id: 'laneGlow', x: '-20%', y: '-50%', width: '140%', height: '200%' },
      s('feGaussianBlur', { stdDeviation: 5 })));
    const paths = [], nodes = [];
    const dots = s('g', { class: 'lane-pts' });
    geo.forEach(l => {
      const d = lanePath(95, Y0, l.nx, l.ny, 905, Y0);
      let line;
      if (mc) {
        // «Пиксели»: та же кривая, растрированная по клеткам 6×6; частицы бегут по невидимой исходной кривой.
        paths.push(s('path', { d: pxLaneCells(95, Y0, l.nx, l.ny, 905, Y0, 6, l.w > 7 ? 2 : 1), class: 'px-lane' + (l.active ? '' : ' idle'), style: `fill:${l.color}` }));
        line = s('path', { d, class: 'lane-motion' });
        paths.push(line);
      } else {
        paths.push(s('path', { d, class: 'lane-track', 'stroke-width': 14 }));
        if (l.flow) paths.push(s('path', { d, class: 'lane-glow', style: `stroke:${l.color}`, 'stroke-width': l.w + 6, filter: 'url(#laneGlow)' }));
        line = s('path', { d, class: 'lane-line' + (l.active ? '' : ' idle'), style: `stroke:${l.color}`, 'stroke-width': l.w });
        paths.push(line);
      }
      if (l.active) binds.push([l.key, line, mc ? 3 : Math.min(4.5, 2.2 + l.w / 4), l.color]);
      if (l.node) {
        const x = l.nx, y = l.ny;
        const open = () => go(l.key === 'proxy' ? 'servers' : 'bypass');
        const tip = s('title', null, l.key === 'proxy' ? 'Выбрать сервер' : 'Открыть «Обход»');
        // «Пиксели»: тот же круг радиусом 19, растрированный.
        if (mc) nodes.push(s('g', { class: 'clickable' + (l.active ? ' on' : ''), onclick: open }, tip, pxNode(x, y, 19, l.node.icon, l.active ? l.color : 'var(--faint)', l.active ? 'on' : '')));
        else {
          nodes.push(s('circle', { cx: x, cy: y, r: 19, class: 'lane-node clickable' + (l.active ? ' on' : ''), style: `stroke:${l.color}`, onclick: open }, tip));
          nodes.push(icon(l.node.icon, x, y, 20, l.active ? l.color : 'var(--faint)'));
        }
        nodes.push(s('text', { x, y: y - 40, 'text-anchor': 'middle', class: 'lbl halo' }, l.node.title));
        nodes.push(s('text', { x, y: y - 25, 'text-anchor': 'middle', class: 'sub-lbl halo' },
          !flagShapes((l.node.cc || '').toUpperCase()) && l.node.cc ? s('tspan', { class: 'cc-text' }, l.node.cc + '  ') : null, l.node.sub));
        if (l.node.cc) nodes.push(flagInSvg(l.node.cc, x - 19 - 32, y - 8, 24));
      } else {
        nodes.push(s('text', { x: l.nx, y: l.ny + 26, 'text-anchor': 'middle', class: 'sub-lbl halo', style: `fill:${l.color}` }, 'напрямую'));
      }
    });
    // Узел «Компьютер» — сама кнопка подключения (HTML поверх схемы), здесь только подписи.
    const end = (x, ic, title, sub) => ic === 'pc' ? [
      s('text', { x, y: Y0 + 74, 'text-anchor': 'middle', class: 'lbl halo' }, title),
      s('text', { x, y: Y0 + 90, 'text-anchor': 'middle', class: 'sub-lbl halo' }, sub)] : [
      ...(mc ? [pxNode(x, Y0, 25, ic, 'var(--ink)', 'end')] : [s('circle', { cx: x, cy: Y0, r: 25, class: 'lane-end' }), icon(ic, x, Y0, 24, 'var(--ink)')]),
      s('text', { x, y: Y0 + 46, 'text-anchor': 'middle', class: 'lbl halo' }, title),
      s('text', { x, y: Y0 + 62, 'text-anchor': 'middle', class: 'sub-lbl halo' }, sub)];
    return s('svg', { class: 'lanes', viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Схема маршрутов трафика' },
      defs, ...paths, dots, ...nodes,
      ...end(95, 'pc', 'Компьютер', phase() === 'on' ? (S.mode === 'tun' ? 'весь трафик' : `прокси :${S.mixedPort}`) : 'нажмите, чтобы подключить'),
      ...end(905, 'globe', 'Интернет', ROUTING[S.routing]));
  });
  // Кнопка подключения стоит точно на месте узла «Компьютер».
  const lb = $('#lanesBox');
  if (lb) { lb.style.setProperty('--pcx', (95 / W * 100) + '%'); lb.style.setProperty('--pcy', (Y0 / H * 100) + '%'); }
  // Частицы продолжают бег по новой схеме с того же места.
  if (binds) {
    const layer = $('#lanesSvg .lane-pts');
    flowsBegin('lane:');
    for (const [key, line, size, color] of binds) flowBind('lane:' + key, line, layer, color, size);
    flowsEnd('lane:');
  }
  for (const l of lanes) flowSpeed('lane:' + l.key, l.active ? l.speed : 0);
  // Легенда обновляется каждую секунду отдельно — анимацию схемы это не трогает.
  const leg = $('#lanesLegend');
  if (leg) leg.replaceChildren(...lanes.map(l => {
    const share = total > 0 && l.speed > 0 ? Math.round(l.speed / total * 100) : 0;
    return h('div', { class: 'leg-chip' + (l.active ? '' : ' off') },
      h('span', { class: 'leg-dot', style: `background:${l.color};color:${l.color}` }),
      h('span', { class: 'leg-name' }, l.name),
      h('b', null, !l.active ? 'выключено' : l.unmeasured ? (l.key === 'zapret' ? 'работает' : 'всё остальное') : tnum('lane-' + l.key, l.speed, fmtSpeed)),
      share ? h('span', { class: 'leg-share num' }, share + '%') : null,
      signalBar(l.active && !l.unmeasured ? l.speed : 0));
  }));
}

// Сила сигнала 0–15, как у проводов в кубических играх: растёт со скоростью (логарифмически).
function signalBar(bps) {
  const lvl = bps < 50 ? 0 : Math.max(1, Math.round(Math.min(1, Math.log10(bps / 1024 + 1) / 3.5) * 15));
  return h('span', { class: 'sig', title: `сила сигнала ${lvl} из 15`, 'aria-hidden': 'true' }, ...Array.from({ length: 15 }, (_, i) => h('i', { class: i < lvl ? 'on' : '' })));
}

function renderChart() {
  const box = $('#chartBox');
  if (!box) return;
  const smp = (STATS && STATS.samples) || [];
  const N = 120, W = 600, H = 170, top = 14, bottom = 152;
  const pts = smp.slice(-N).map(p => ({ ...ZERO, ...p }));
  const pad = N - pts.length;
  // снизу вверх: напрямую, zapret, Telegram, через сервер
  const layers = [['dd', 'var(--chart-direct)'], ['zd', 'var(--warn)'], ['td', 'var(--tg)'], ['pd', 'var(--chart-tunnel)']];
  let max = 64 * 1024;
  for (const p of pts) max = Math.max(max, p.dd + p.zd + p.td + p.pd, p.du + p.zu + p.tu + p.pu);
  max *= 1.15;
  const x = i => (i + pad) / (N - 1) * W;
  const y = v => bottom - v / max * (bottom - top);
  const areas = [];
  const base = pts.map(() => 0);
  for (const [k, color] of layers) {
    if (!pts.some(p => p[k] > 0)) continue;
    const hi = pts.map((p, i) => base[i] + p[k]);
    let d = `M${x(0)} ${y(hi[0])}`;
    hi.forEach((v, i) => { d += ` L${x(i)} ${y(v)}`; });
    for (let i = pts.length - 1; i >= 0; i--) d += ` L${x(i)} ${y(base[i])}`;
    areas.push(s('path', { d: d + 'Z', class: 'area', style: `fill:${color}` }));
    hi.forEach((v, i) => { base[i] = v; });
  }
  let up = '';
  pts.forEach((p, i) => { up += `${i ? 'L' : 'M'}${x(i)} ${y(p.pu + p.du + p.zu + p.tu)} `; });
  box.replaceChildren(s('svg', { class: 'chart', viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: 'none' },
    s('line', { x1: 0, x2: W, y1: top, y2: top, class: 'grid' }),
    s('line', { x1: 0, x2: W, y1: (top + bottom) / 2, y2: (top + bottom) / 2, class: 'grid', opacity: .5 }),
    s('line', { x1: 0, x2: W, y1: bottom, y2: bottom, class: 'grid' }),
    ...areas,
    up ? s('path', { d: up, class: 'line-up', 'vector-effect': 'non-scaling-stroke' }) : null,
    s('text', { x: 4, y: top - 3 }, fmtSpeed(max))));
  const l = lastSpeeds();
  const leg = [['Через сервер', 'var(--chart-tunnel)', l.pd], ['Напрямую', 'var(--chart-direct)', l.dd]];
  if (zapretOn()) leg.push(['zapret', 'var(--warn)', l.zd]);
  if (tgOn()) leg.push(['Telegram', 'var(--tg)', l.td]);
  $('#chartLegend').replaceChildren(
    ...leg.map(([n, c, v]) => h('span', null, h('i', { style: 'background:' + c }), n, h('b', null, tnum('c-' + n, v, fmtSpeed)))),
    h('span', null, h('i', { style: 'background:none;border-top:1.5px dashed var(--muted);border-radius:0;height:0;vertical-align:3px' }), 'Отдача', h('b', null, fmtSpeed(l.pu + l.du + l.zu + l.tu))));
}

const ROUTE_STYLE = { direct: ['direct', 'напрямую'], zapret: ['zapret', 'через zapret'], telegram: ['tg', 'tg-ws-proxy'], 'twitch-adfree': ['proxy', 'Twitch без рекламы'] };

function renderTop() {
  const list = $('#topList');
  if (!list) return;
  const top = (STATS && STATS.top) || [];
  if ((!ST || !ST.running) && !top.length) {
    list.replaceChildren(h('div', { class: 'empty' }, 'Подключитесь, чтобы видеть, куда идёт трафик.'));
    $('#topNote').textContent = '';
    return;
  }
  $('#topNote').textContent = 'по скорости';
  if (!top.length) { list.replaceChildren(h('div', { class: 'empty' }, 'Пока тихо.')); return; }
  list.replaceChildren(...top.slice(0, 7).map(t => {
    const st = ROUTE_STYLE[t.route];
    const cls = st ? st[0] : 'proxy';
    const name = st ? st[1] : (serverByTag(t.route) || {}).name || t.route;
    const proc = t.route === 'telegram' ? '' : t.proc;
    return h('div', { class: 'top-item' },
      h('div', { class: 'host', title: t.host }, displayDomain(t.host || '?')),
      h('div', { class: 'meta' }, h('span', { class: 'dot ' + cls }), [name, proc, t.conns > 1 ? `${t.conns} соед.` : ''].filter(Boolean).join(', ')),
      h('div', { class: 'speed num' }, t.speed > 0 ? fmtSpeed(t.speed) : h('span', { class: 'hint' }, fmtBytes(t.total))));
  }));
}

function spark(points, w = 200, hgt = 34) {
  if (!points || points.length < 2) return null;
  const vals = points.map(p => p.ms);
  const max = Math.max(...vals, 100) * 1.1;
  const x = i => i / (points.length - 1) * w;
  const y = v => hgt - 3 - v / max * (hgt - 6);
  const ok = points.map((p, i) => p.ms ? `${x(i)},${y(p.ms)}` : null).filter(Boolean).join(' ');
  return s('svg', { class: 'spark', viewBox: `0 0 ${w} ${hgt}`, preserveAspectRatio: 'none' },
    s('polyline', { points: ok, 'vector-effect': 'non-scaling-stroke' }),
    ...points.map((p, i) => p.ms ? null : s('line', { x1: x(i), x2: x(i), y1: 4, y2: hgt - 4, class: 'bad' })));
}

function renderFacts() {
  const box = $('#facts');
  if (!box) return;
  const t = (STATS && STATS.totals) || [0, 0, 0, 0, 0, 0, 0, 0];
  const running = ST && ST.running;
  const cs = currentServer();
  const d = cs.id ? delayFor(cs.id) : null;
  const hist = cs.id && STATS && STATS.delays ? STATS.delays[cs.id] : null;
  $('#sessionNote').textContent = running && STATS && STATS.session ? 'с ' + fmtTime(STATS.session) : '';
  const health = STATS && STATS.health;
  box.replaceChildren(
    h('div', { class: 'fact' }, h('b', null, running ? fmtDur(Date.now() / 1000 - ST.startedAt) : '—'), h('span', null, 'на связи')),
    h('div', { class: 'fact' }, h('b', { class: health === 'fail' ? 'delay bad' : '' }, !running ? '—' : d && d.ms ? `${d.ms} мс` : health === 'fail' ? 'нет связи' : '…'),
      h('span', null, 'задержка до сервера')),
    h('div', { class: 'fact' }, h('b', null, tnum('f-proxy', t[0] + t[1], fmtBytes)), h('span', null, 'через сервер')),
    h('div', { class: 'fact' }, h('b', null, tnum('f-direct', t[2] + t[3], fmtBytes)), h('span', null, 'напрямую')),
    zapretOn() && running ? h('div', { class: 'fact' }, h('b', null, fmtBytes((t[4] || 0) + (t[5] || 0))), h('span', null, 'через zapret')) : null,
    tgOn() ? h('div', { class: 'fact' }, h('b', null, fmtBytes((t[6] || 0) + (t[7] || 0))), h('span', null, 'Telegram')) : null,
    hist && hist.length > 1 ? h('div', { class: 'fact wide' }, h('span', null, 'Задержка за последний час'), spark(hist, 400, 34)) : null);
}

function renderEvents() {
  const box = $('#events');
  if (!box) return;
  const ev = ((STATS && STATS.events) || []).slice().reverse();
  if (!ev.length) { box.replaceChildren(h('div', { class: 'empty' }, 'Здесь появятся подключения, переключения серверов и сбои.')); return; }
  box.replaceChildren(...ev.slice(0, 30).map(e => h('div', { class: 'event' },
    h('time', null, fmtTime(e.t)), h('span', { class: 'k ' + e.kind }), h('span', null, e.text))));
}

function renderOverviewLive() {
  renderTiles();
  renderMap();
  renderLanes();
  renderChart();
  renderTop();
  renderFacts();
  renderEvents();
}

function ipColumn(title, res, note) {
  if (!res) return null;
  if (res.error || !res.info) {
    return h('div', { class: 'ipcol' }, h('div', { class: 'ipcol-title' }, title),
      h('div', { class: 'ip muted' }, 'не определён'),
      h('div', { class: 'cell-sub', title: res.error || '' }, title === 'Через сервер'
        ? 'Сервисы проверки не ответили через сервер.' : 'Сервисы проверки не ответили.'));
  }
  const g = res.info;
  const place = [g.country || null, g.city || null].filter(Boolean).join(', ') || 'страна неизвестна';
  let kind = null;
  if (g.hosting === true) kind = h('span', { class: 'iptag' }, 'адрес дата-центра');
  if (g.hosting === false) kind = h('span', { class: 'iptag home' }, 'домашний или мобильный адрес');
  return h('div', { class: 'ipcol' },
    h('div', { class: 'ipcol-title' }, title),
    h('div', { class: 'ip num' }, g.ip),
    h('div', { class: 'ipplace' }, cc(g.countryCode), place),
    h('div', { class: 'cell-sub' }, [g.isp, g.asn].filter(Boolean).join(', ')),
    kind,
    note && h('div', { class: 'cell-sub', style: 'margin-top:6px' }, note));
}

function renderIP() {
  const card = $('#ipCard');
  if (!card || !S) return;
  card.hidden = !S.showIp;
  if (!S.showIp) return;
  const d = ip.data;
  const head = h('div', { class: 'card-head' },
    h('h3', null, 'Как вас видят сайты'),
    h('button', { class: 'btn small', disabled: ip.loading, onclick: () => refreshIP(true) }, ip.loading ? 'Определяю…' : 'Обновить'));
  let cols;
  if (!d && ip.loading) cols = h('div', { class: 'ipcols', 'aria-label': 'Определяю адрес…' }, skeletonCol(), skeletonCol());
  else if (!d) cols = h('div', { class: 'cell-sub' }, ip.error || '');
  else if (d.running && d.proxy) {
    cols = h('div', { class: 'ipcols' },
      ipColumn('Через сервер', d.proxy, 'Его видят сайты, которые идут через сервер.'),
      ipColumn('Без сервера', d.direct, 'Ваш настоящий адрес. Его видят сайты, которые идут напрямую.'));
  } else cols = h('div', { class: 'ipcols' }, ipColumn('Сейчас', d.direct, 'Подключитесь, чтобы сайты видели адрес сервера.'));
  card.classList.toggle('loading', ip.loading);
  card.replaceChildren(head, cols);
  renderMap();
  renderTiles();
}
// ---------- диагностика ----------
function routeOf(c) {
  const chains = c.chains || [];
  const srv = chains.find(t => t.startsWith('s-') || t.startsWith('w-'));
  if (srv) { const sv = serverByTag(srv); return { cls: 'proxy', text: sv ? sv.name : srv }; }
  if (chains.includes('direct')) return { cls: 'direct', text: 'Напрямую' };
  if (chains.includes('twitch-adfree')) return { cls: 'proxy', text: 'Twitch без рекламы' };
  if (chains.length) return { cls: 'proxy', text: chains[0] };
  return { cls: 'block', text: '—' };
}
function connHost(c) {
  const m = c.metadata || {};
  const host = m.host || m.destinationIP || '?';
  return m.destinationPort ? `${displayDomain(host)}:${m.destinationPort}` : displayDomain(host);
}
const connProc = c => ((c.metadata && c.metadata.processPath) || '').split(/[\\/]/).pop();
const connText = c => [connHost(c), connProc(c), routeOf(c).text, c.rule].join(' ');

function updateConnTable() {
  const body = $('#connBody');
  if (!body) return;
  const running = ST && ST.running;
  const q = ($('#connFilter')?.value || '').trim().toLowerCase();
  const all = connList.connections || [];
  const rows = (q ? all.filter(c => connText(c).toLowerCase().includes(q)) : all).slice(0, 300);
  const cnt = $('#connCount');
  if (cnt) cnt.textContent = running ? count(connList.count || 0, 'соединение', 'соединения', 'соединений') : '';
  if (!running) { body.replaceChildren(h('tr', null, h('td', { class: 'empty', colspan: 5 }, 'Подключитесь, чтобы видеть, куда идёт трафик.'))); return; }
  if (!rows.length) { body.replaceChildren(h('tr', null, h('td', { class: 'empty', colspan: 5 }, 'Пока нет соединений.'))); return; }
  body.replaceChildren(...rows.map(c => {
    const r = routeOf(c);
    const net = c.metadata && c.metadata.network === 'udp' ? ' UDP' : '';
    return h('tr', null,
      h('td', null, h('div', { class: 'cell-main trunc', title: connHost(c) }, connHost(c) + net),
        c.rule && h('div', { class: 'cell-sub trunc', title: c.rule }, c.rule)),
      h('td', { class: 'cell-sub trunc' }, connProc(c)),
      h('td', null, h('span', { class: 'route-tag ' + r.cls }, r.text)),
      h('td', { class: 'r num cell-sub' }, fmtBytes(c.download)),
      h('td', { class: 'r num cell-sub' }, fmtBytes(c.upload)));
  }));
}

function logLine(text) {
  let cls = '';
  if (text.startsWith('[naivka]')) cls = 'l-app';
  else if (/\b(ERROR|FATAL|PANIC)\b/.test(text)) cls = 'l-err';
  else if (/\bWARN\b/.test(text)) cls = 'l-warn';
  return h('div', { class: cls }, text);
}

let lastCheck = null;
function viewDiag() {
  const body = h('div', { id: 'diagBody' });
  const render = () => {
    if (diagTab === 'now') {
      body.replaceChildren(
        h('div', { class: 'grid-main' },
          h('section', { class: 'card' },
            h('div', { class: 'card-head' }, h('h3', null, 'Скорость'), h('small', null, 'последние 2 минуты')),
            h('div', { id: 'chartBox' }), h('div', { class: 'legend', id: 'chartLegend' })),
          h('section', { class: 'card' },
            h('div', { class: 'card-head' }, h('h3', null, 'Сейчас активны'), h('small', { id: 'topNote' })),
            h('div', { class: 'top-list', id: 'topList' }))),
        h('div', { class: 'grid-main' },
          h('section', { class: 'card' },
            h('div', { class: 'card-head' }, h('h3', null, 'Сеанс'), h('small', { id: 'sessionNote' })),
            h('div', { class: 'facts', id: 'facts' })),
          h('section', { class: 'card' }, h('div', { class: 'card-head' }, h('h3', null, 'События')), h('div', { class: 'events', id: 'events' }))),
        h('section', { class: 'card ipcard mt', id: 'ipCard' }),
        h('div', { class: 'card mapcard mt' }, h('div', { class: 'map-box', id: 'heroMap' })));
      MAP.key = '';
      renderOverviewLive();
      renderIP();
    } else if (diagTab === 'conns') {
      body.replaceChildren(
        h('p', { class: 'sub' }, 'Что сейчас открыто и каким маршрутом идёт. Под адресом — правило, которое сработало.'),
        h('div', { class: 'row' },
          h('input', { type: 'text', id: 'connFilter', placeholder: 'Фильтр: домен, программа, маршрут', class: 'grow-input', oninput: updateConnTable }),
          h('span', { class: 'hint', id: 'connCount' }),
          h('button', { class: 'btn', onclick: async () => { try { await api('/api/conns/close', {}); toast('Соединения закрыты'); } catch (e) { toast(e.message, true); } } }, 'Закрыть все')),
        h('div', { class: 'panel mt' }, h('table', { class: 'table' },
          h('thead', null, h('tr', null, h('th', null, 'Адрес'), h('th', null, 'Программа'), h('th', null, 'Маршрут'), h('th', { class: 'r' }, 'Получено'), h('th', { class: 'r' }, 'Отправлено'))),
          h('tbody', { id: 'connBody' }))));
      updateConnTable();
      if (ST && ST.running) refreshConns();
    } else if (diagTab === 'check') {
      body.replaceChildren(viewCheck());
    } else if (diagTab === 'speed') {
      body.replaceChildren(viewSpeed());
      refreshSpeed();
    } else if (diagTab === 'traffic') {
      body.replaceChildren(viewTraffic());
      refreshTraffic();
    } else {
      const box = h('pre', { class: 'log', id: 'logBox' }, logBuf.map(logLine));
      body.replaceChildren(
        h('div', { class: 'row', style: 'margin:0 0 12px' },
          h('label', { class: 'check', style: 'padding:0' }, h('input', { type: 'checkbox', id: 'logFollow', checked: true }), h('span', null, 'Прокручивать к новым')),
          h('span', { class: 'spacer' }),
          h('button', { class: 'btn small', onclick: async () => { try { await navigator.clipboard.writeText(logBuf.join('\n')); toast('Журнал скопирован'); } catch (e) { toast('Не удалось скопировать', true); } } }, 'Скопировать'),
          h('button', { class: 'btn small', onclick: () => { logBuf = []; box.replaceChildren(); } }, 'Очистить окно')),
        box);
      setTimeout(() => { box.scrollTop = box.scrollHeight; });
      refreshLogs();
    }
  };
  const tabs = segmented([['now', 'Сейчас'], ['conns', 'Соединения'], ['traffic', 'Трафик'], ['check', 'Проверить сайт'], ['speed', 'Скорость'], ['logs', 'Журнал']], () => diagTab, v => {
    diagTab = v; sessionStorage.setItem('naivka-diag', v); render();
  }, 'Раздел диагностики');
  render();
  return h('div', { class: 'view', style: 'max-width:none' }, h('div', { class: 'title-row' }, h('h1', null, 'Сводка'), tabs), body);
}

function viewCheck() {
  const input = h('input', { type: 'text', class: 'grow-input', placeholder: 'например, youtube.com или ссылка на страницу', spellcheck: 'false', value: lastCheck ? lastCheck.host : '' });
  const out = h('div', { id: 'checkOut' });
  const run = async () => {
    const target = input.value.trim();
    if (!target) { input.focus(); return; }
    btn.disabled = true; btn.textContent = 'Проверяю…';
    out.replaceChildren(h('p', { class: 'hint mt' }, 'Пробую открыть сайт напрямую и через сервер, смотрю, какое правило сработает. Это займёт до 15 секунд.'));
    try { lastCheck = await api('/api/check', { target }); renderCheck(out, lastCheck); }
    catch (e) { out.replaceChildren(h('p', { class: 'err-text mt' }, e.message)); }
    finally { btn.disabled = false; btn.textContent = 'Проверить'; }
  };
  input.addEventListener('keydown', e => { if (e.key === 'Enter') run(); });
  const btn = h('button', { class: 'btn primary', onclick: run }, 'Проверить');
  if (lastCheck) renderCheck(out, lastCheck);
  return h('div', null,
    h('p', { class: 'sub' }, 'Не открывается сайт? MZ проверит, заблокирован ли он у провайдера, открывается ли через сервер и по какому правилу сейчас идёт.'),
    h('div', { class: 'row' }, input, btn), out);
}

function probeCard(title, p) {
  let main, note;
  if (p.skipped) { main = h('b', { class: 'hint' }, 'не проверялось'); note = 'Нужно подключение.'; }
  else if (p.ok) { main = h('b', { class: 'ok' }, `открывается, ${p.ms} мс`); note = `Ответ ${p.code}`; }
  else { main = h('b', { class: 'bad' }, p.stub ? 'заглушка провайдера' : 'не открывается'); note = p.error || (p.code ? `Ответ ${p.code}` : ''); }
  return h('div', { class: 'card probe' }, h('div', { class: 'hint' }, title), main, h('div', { class: 'cell-sub' }, note));
}

function renderCheck(out, r) {
  const routeTag = r.routeKind ? h('span', { class: 'route-tag ' + r.routeKind }, r.routeName) : h('span', { class: 'hint' }, 'подключитесь, чтобы узнать');
  const actions = [];
  if (r.suggest) {
    const act = r.suggest;
    actions.push(h('button', { class: 'btn primary', onclick: async () => {
      const res = mergeRules(S.rules, [{ kind: 'domain', value: r.host, action: act }]);
      save(0);
      if (await saveNow()) toast(res.added ? 'Правило добавлено. Переподключитесь, чтобы оно заработало.' : 'Действие правила изменено');
    } }, act === 'proxy' ? `Добавить правило: ${displayDomain(r.host)} через сервер` : `Добавить правило: ${displayDomain(r.host)} напрямую`));
  }
  out.replaceChildren(
    h('div', { class: 'verdict' }, r.verdict),
    h('div', { class: 'check-grid' },
      probeCard('Напрямую', r.direct),
      probeCard('Через сервер', r.proxy),
      h('div', { class: 'card probe' }, h('div', { class: 'hint' }, 'Сейчас идёт'), h('div', { style: 'margin:4px 0' }, routeTag),
        h('div', { class: 'cell-sub trunc', title: r.rule || '' }, r.rule ? 'правило: ' + r.rule : (r.routeKind ? 'без особого правила, по режиму' : '')))),
    actions.length ? h('div', { class: 'row mt' }, actions) : null);
}

// ---------- серверы ----------
function delayFor(id) {
  if (delays[id]) return delays[id];
  if (PX && PX.delays && PX.delays['s-' + id] !== undefined) {
    const ms = PX.delays['s-' + id];
    return { ms, kind: 'proxy', error: ms ? '' : 'нет ответа' };
  }
  return null;
}

function delayEl(id) {
  const d = delayFor(id);
  if (!d) return h('span', { class: 'delay cell-sub' }, '');
  if (d.pending) return h('span', { class: 'delay cell-sub' }, 'проверяю…');
  if (d.error) return h('span', { class: 'delay bad' }, d.error);
  const cls = d.ms < 300 ? 'good' : d.ms < 800 ? 'mid' : 'bad';
  return h('span', { class: 'delay ' + cls, title: d.kind === 'tls' ? 'TLS-рукопожатие без подключения' : 'Реальный запрос через сервер' }, `${d.ms} мс` + (d.kind === 'tls' ? ' TLS' : ''));
}

function viewServers() {
  return h('div', { class: 'view' },
    h('div', { class: 'title-row' }, h('h1', null, 'Серверы'),
      h('div', { class: 'row' },
        h('button', { class: 'btn', onclick: importLinksDialog }, 'Вставить ссылки'),
        h('button', { class: 'btn primary', onclick: () => editServer(null) }, 'Добавить сервер'))),
    h('div', { class: 'scards', id: 'srvList', role: 'radiogroup', 'aria-label': 'Сервер' }),
    h('div', { class: 'row mt' },
      h('button', { class: 'btn small', id: 'testAll', onclick: () => testServers('') }, 'Проверить все'),
      h('span', { class: 'hint' }, 'Без подключения проверяется TLS-рукопожатие, после — настоящий запрос через сервер. Линия — задержка за последний час. Карточки можно перетаскивать, звёздочка поднимает сервер наверх.')),
    h('div', { id: 'wlBox' }),
    h('h2', null, 'Телефон'),
    h('div', { class: 'card' },
      h('p', null, 'Те же серверы, режим маршрутов и ваши правила — на iPhone или Android. Профиль открывается в приложении sing-box, его понимают и другие клиенты на sing-box.'),
      h('button', { class: 'btn primary', onclick: phoneDialog }, 'Профиль для телефона')));
}

// Серверы — карточками: избранные первыми, порядок меняется перетаскиванием.
let dragId = null;

function serverOrder() {
  return [...S.servers.filter(x => x.fav), ...S.servers.filter(x => !x.fav)];
}

async function selectServer(id) {
  S.selected = id;
  renderServers();
  try { await api('/api/select', { id }); PXAt = 0; updateStatus(); } catch (e) { toast(e.message, true); }
}

// moveServer ставит сервер from перед to или после него (в том порядке, который видно на экране).
function moveServer(from, to, after) {
  const order = serverOrder();
  const item = order.find(x => x.id === from);
  const target = order.find(x => x.id === to);
  if (!item || !target || item === target) return;
  if (item.fav !== target.fav) item.fav = target.fav; // перетащили в другую группу
  const rest = order.filter(x => x !== item);
  rest.splice(rest.indexOf(target) + (after ? 1 : 0), 0, item);
  S.servers = rest;
  save();
  renderServers(true);
}

function srvCardBase(id, cls, kids, opts = {}) {
  const sel = S.selected === id || (id === 'wl' && S.selected.startsWith('w-'));
  const card = h('div', {
    class: 'scard ' + cls + (sel ? ' sel' : '') + (opts.live ? ' live' : ''), role: 'radio', tabindex: 0, 'aria-checked': sel ? 'true' : 'false',
    'data-id': id, style: opts.off ? 'opacity:.55' : '',
    onclick: e => { if (!e.target.closest('button, a, input')) selectServer(id); },
    onkeydown: e => { if ((e.key === 'Enter' || e.key === ' ') && e.target === card) { e.preventDefault(); selectServer(id); } },
  }, ...kids);
  if (opts.drag) {
    card.draggable = true;
    card.addEventListener('dragstart', e => { dragId = id; e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', id); card.classList.add('dragging'); });
    card.addEventListener('dragend', () => { dragId = null; document.querySelectorAll('.scard').forEach(c => c.classList.remove('dragging', 'drop-before', 'drop-after')); });
    card.addEventListener('dragover', e => {
      if (!dragId || dragId === id) return;
      e.preventDefault();
      const r = card.getBoundingClientRect();
      const after = e.clientX > r.left + r.width / 2;
      card.classList.toggle('drop-after', after);
      card.classList.toggle('drop-before', !after);
    });
    card.addEventListener('dragleave', () => card.classList.remove('drop-before', 'drop-after'));
    card.addEventListener('drop', e => {
      e.preventDefault();
      const after = card.classList.contains('drop-after');
      card.classList.remove('drop-before', 'drop-after');
      if (dragId) moveServer(dragId, id, after);
    });
  }
  return card;
}

function renderServers(force) {
  const list = $('#srvList');
  if (!list || (dragId && !force)) return;
  renderOwn();
  const running = ST && ST.running;
  const nowTag = PX && running ? (PX.selected === 'auto' ? PX.autoNow : PX.selected) : '';
  const noNaive = ST && ST.core && ST.core.present && !ST.core.naive;
  const xr = xrayReady();
  // Перерисовываем, только если что-то видимое изменилось: иначе сбивались бы наведение и фокус.
  const sig = JSON.stringify([S.servers, S.selected, S.wl.enabled, nowTag, delays, PX && PX.delays, PX && PX.wlNow, srvGeo,
    STATS && STATS.delays, noNaive, xr, WL && [WL.used, WL.active]]);
  patch('srvList', sig, () => {
    const cards = [];
    if (S.servers.length > 1 || S.selected === 'auto') {
      const now = PX && PX.autoNow ? serverByTag(PX.autoNow) : null;
      cards.push(srvCardBase('auto', 'wide', [
        h('div', { class: 'scard-top' },
          h('span', { class: 'scard-ico' }, s('svg', { viewBox: '0 0 24 24' }, s('path', { d: 'M4 12a8 8 0 0 1 14-5.3M20 12a8 8 0 0 1-14 5.3M18 3v4h-4M6 21v-4h4' }))),
          h('div', { class: 'scard-title' }, h('b', null, 'Автовыбор'),
            h('small', null, now ? `Сейчас: ${now.name}` : 'Самый быстрый рабочий сервер'))),
        h('div', { class: 'scard-sub' }, 'Берёт самый быстрый сервер, в том числе другого протокола, и переключается сам, если текущий перестал отвечать.')],
      { live: running && PX && PX.selected === 'auto' }));
    }
    for (const sv of serverOrder()) {
      const geo = srvGeo[sv.id] && srvGeo[sv.id].info;
      const where = geo ? [geo.city || geo.country, geo.isp].filter(Boolean).join(', ') : '';
      const hist = STATS && STATS.delays && STATS.delays[sv.id];
      const off = noNaive && sv.type === 'naive';
      const d = delayFor(sv.id);
      const note = off ? 'Naive не поддерживается этим ядром'
        : sv.transport === 'xhttp' && !xr ? 'нужен Xray — установите его в «Обходе» → «Версии»' : where;
      const star = h('button', {
        class: 'star' + (sv.fav ? ' on' : ''), type: 'button', title: sv.fav ? 'Убрать из избранного' : 'В избранное',
        'aria-label': sv.fav ? 'Убрать из избранного' : 'В избранное', 'aria-pressed': sv.fav ? 'true' : 'false',
        onclick: () => { sv.fav = !sv.fav; save(); renderServers(true); },
      }, s('svg', { viewBox: '0 0 24 24' }, s('path', { d: 'M12 3.5l2.6 5.3 5.9.9-4.2 4.1 1 5.8-5.3-2.8-5.3 2.8 1-5.8-4.2-4.1 5.9-.9z' })));
      cards.push(srvCardBase(sv.id, '', [
        h('span', { class: 'scard-grip', title: 'Перетащите, чтобы поменять порядок', 'aria-hidden': 'true' }, '⋮⋮'),
        h('div', { class: 'scard-top' },
          h('span', { class: 'scard-flag' }, geo && geo.countryCode ? flag(geo.countryCode, true) : h('span', { class: 'scard-ico' }, s('svg', { viewBox: '0 0 24 24' }, s('path', { d: ICONS.globe })))),
          h('div', { class: 'scard-title' }, h('b', { title: sv.name }, sv.name),
            h('small', null, h('span', { class: 'proto' + (sv.type !== 'naive' ? ' vless' : '') }, protoName(sv)), ` ${sv.host}:${sv.port}`)),
          star),
        h('div', { class: 'scard-mid' },
          h('div', { class: 'scard-delay' }, pingBars(d), delayEl(sv.id)),
          nowTag === 's-' + sv.id ? h('span', { class: 'live-tag' }, 'сейчас') : null,
          h('div', { class: 'scard-spark' }, spark(hist, 130, 26))),
        note ? h('div', { class: 'scard-sub trunc', title: note }, note) : null,
        h('div', { class: 'scard-actions' },
          h('button', { class: 'btn small ghost', type: 'button', onclick: () => testServers(sv.id) }, btnIcon('test'), 'Проверить'),
          h('button', { class: 'btn small ghost', type: 'button', onclick: () => qrDialog(sv) }, btnIcon('qr'), 'QR'),
          h('button', { class: 'btn small ghost', type: 'button', onclick: () => editServer(sv) }, btnIcon('edit'), 'Изменить'))],
      { drag: true, off, live: nowTag === 's-' + sv.id }));
    }
    if (S.wl.enabled) {
      cards.push(srvCardBase('wl', 'wide', [
        h('div', { class: 'scard-top' },
          h('span', { class: 'cc', style: 'background:var(--warn-soft);color:var(--warn)' }, 'WL'),
          h('div', { class: 'scard-title' }, h('b', null, 'Белые списки', h('span', { class: 'proto' }, 'подписка')), wlNowLine()))],
      { live: running && (PX && (PX.selected === 'wl' || (PX.selected || '').startsWith('w-'))) }));
    }
    if (!S.servers.length && !S.wl.enabled) cards.push(h('div', { class: 'empty wide' }, 'Серверов пока нет. Добавьте вручную или вставьте ссылки naive+https://… или vless://…'));
    return cards;
  });
  patch('wlBox', JSON.stringify([WL, S.wl, S.selected, PX && PX.delays, PX && PX.wlNow, AD && AD.updates && AD.updates.xray]), wlCard);
}

async function testServers(id) {
  const ids = id ? [id] : S.servers.map(x => x.id);
  if (!ids.length) return;
  ids.forEach(i => { delays[i] = { pending: true }; });
  renderServers();
  const btn = $('#testAll');
  if (btn) btn.disabled = true;
  try {
    const res = await api('/api/test', { id });
    for (const r of res) delays[r.id] = { ms: r.ms, error: r.error, kind: r.kind };
  } catch (e) { ids.forEach(i => { delete delays[i]; }); toast(e.message, true); }
  finally { if (btn) btn.disabled = false; renderServers(); updateStatus(); }
}

async function qrDialog(sv) {
  await saveNow();
  let link;
  try { link = (await api('/api/link?id=' + encodeURIComponent(sv.id))).link; } catch (e) { toast(e.message, true); return; }
  const dlg = openDialog(
    h('h2', null, `${sv.name}: QR-код`),
    h('p', { class: 'hint' }, 'Отсканируйте в клиенте на телефоне (sing-box, Karing, Hiddify и другие). В коде пароль сервера — не показывайте его посторонним.'),
    h('div', { class: 'qr-box' }, qrSvg(link)),
    h('div', { class: 'link-box' }, link),
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: async () => { try { await navigator.clipboard.writeText(link); toast('Ссылка скопирована'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Скопировать ссылку'),
      h('button', { class: 'btn primary', autofocus: true, onclick: () => dlg.close() }, 'Готово')));
}

async function phoneDialog() {
  await saveNow();
  const body = h('div', null, h('p', { class: 'hint' }, 'Готовлю профиль…'));
  const dlg = openDialog(h('h2', null, 'Профиль для телефона'), body);
  dlg.classList.add('wide');
  dlg.addEventListener('close', () => api('/api/mobile/stop', {}).catch(() => {}));
  const fileBtn = h('button', { class: 'btn', onclick: async () => {
    try { const r = await api('/api/mobile/config'); download('naivka-phone.json', r.config, 'application/json'); toast('Файл сохранён в «Загрузки»'); } catch (e) { toast(e.message, true); }
  } }, 'Скачать файлом');
  let res;
  try { res = await api('/api/mobile/share', {}); }
  catch (e) {
    body.replaceChildren(h('p', { class: 'err-text' }, e.message), h('div', { class: 'row end mt' }, fileBtn, h('button', { class: 'btn primary', onclick: () => dlg.close() }, 'Закрыть')));
    return;
  }
  let idx = 0;
  const show = () => {
    const l = res.links[idx];
    const left = Math.max(0, Math.round((res.expires - Date.now() / 1000) / 60));
    body.replaceChildren(
      h('p', null, 'Телефон должен быть в той же Wi-Fi-сети, что и компьютер. В приложении sing-box наведите камеру на код — профиль добавится сам. В других клиентах вставьте ссылку как подписку или «профиль по ссылке».'),
      res.links.length > 1 ? h('div', { class: 'row' }, h('span', { class: 'hint' }, 'Сеть:'),
        segmented(res.links.map((x, i) => [String(i), x.ip]), () => String(idx), v => { idx = +v; show(); }, 'Сеть')) : null,
      h('div', { class: 'qr-box' }, qrSvg(l.singbox)),
      h('div', { class: 'link-box' }, l.url),
      h('p', { class: 'hint mt' }, `Ссылка работает ${left} мин и не больше трёх скачиваний, потом закроется сама. В профиле пароли серверов — открывайте его только в своей сети. Windows может спросить разрешение для MZ в брандмауэре: разрешите для частных сетей.`),
      h('div', { class: 'row end mt' },
        fileBtn,
        h('button', { class: 'btn', onclick: async () => { try { await navigator.clipboard.writeText(l.url); toast('Ссылка скопирована'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Скопировать ссылку'),
        h('button', { class: 'btn primary', onclick: () => dlg.close() }, 'Готово')));
  };
  show();
}

function editServer(srv) {
  const isNew = !srv;
  const d = srv ? { ...srv } : { id: rid(), type: 'naive', name: '', host: '', port: 443, username: '', password: '', sni: '', quic: false, uot: false, uuid: '', publicKey: '', shortId: '', flow: 'xtls-rprx-vision', fingerprint: 'chrome',
    obfs: '', obfsPassword: '', upMbps: 0, downMbps: 0, congestion: 'bbr', udpRelay: 'native', method: '2022-blake3-aes-128-gcm', transport: 'tcp', security: '', insecure: false };
  const inp = (key, label, extra = {}) => h('label', { class: 'field' }, h('span', null, label),
    h('input', { type: extra.type || 'text', value: d[key], placeholder: extra.placeholder, spellcheck: 'false', autocomplete: 'off', oninput: e => { d[key] = e.target.value; } }));
  const box = (key, title, desc) => h('label', { class: 'check' },
    h('input', { type: 'checkbox', checked: d[key], onchange: e => { d[key] = e.target.checked; } }),
    h('span', null, h('b', null, title), h('small', null, desc)));
  const err = h('div', { class: 'err-text' });
  const fields = h('div');
  const sel = (key, label, options) => h('label', { class: 'field' }, h('span', null, label),
    h('select', { onchange: e => { d[key] = e.target.value; } }, options.map(([v, t]) => h('option', { value: v, selected: d[key] === v }, t))));
  const num = (key, label, hint) => h('label', { class: 'field' }, h('span', null, label),
    h('input', { type: 'number', min: 0, value: d[key] || '', placeholder: hint, oninput: e => { d[key] = parseInt(e.target.value, 10) || 0; } }));
  const insecureBox = () => box('insecure', 'Не проверять сертификат', 'Только для своего сервера с самоподписанным сертификатом: иначе соединение можно перехватить.');
  const renderFields = () => {
    if (d.type === 'trojan' && d.transport && d.transport !== 'tcp') {
      fields.replaceChildren(
        h('div', { class: 'grid2' }, inp('name', 'Название')),
        h('p', { class: 'hint mt' }, `${protoName(d)}, ${d.host}:${d.port}. Такие серверы добавляются ссылкой trojan://. Чтобы поменять транспорт, удалите сервер и вставьте новую ссылку.`));
      return;
    }
    if (d.type === 'hysteria2') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды Hysteria2' }),
          inp('host', 'Домен или IP сервера', { placeholder: 'hy.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'SNI, маскировка, скорость',
          h('div', { class: 'grid2' },
            inp('sni', 'SNI', { placeholder: 'как домен' }),
            sel('obfs', 'Маскировка', [['', 'нет'], ['salamander', 'salamander']]),
            inp('obfsPassword', 'Пароль маскировки'),
            h('span'),
            num('upMbps', 'Отдача, Мбит/с', 'авто (BBR)'),
            num('downMbps', 'Загрузка, Мбит/с', 'авто (BBR)')),
          h('p', { class: 'hint' }, 'Скорость указывайте, только если знаете свой канал: завышенная хуже, чем автоматическая.'),
          insecureBox()),
        h('p', { class: 'hint mt' }, 'Hysteria2 работает поверх UDP (QUIC). Хорош на плохом мобильном интернете, но у части провайдеров UDP за границу режут — держите рядом сервер Reality.'));
    } else if (d.type === 'tuic') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды TUIC' }),
          inp('host', 'Домен или IP сервера', { placeholder: 'tuic.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('uuid', 'UUID'),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'SNI, перегрузка, UDP',
          h('div', { class: 'grid2' },
            inp('sni', 'SNI', { placeholder: 'как домен' }),
            inp('alpn', 'ALPN', { placeholder: 'h3' }),
            sel('congestion', 'Алгоритм перегрузки', [['bbr', 'BBR'], ['cubic', 'CUBIC'], ['new_reno', 'NewReno']]),
            sel('udpRelay', 'Передача UDP', [['native', 'native'], ['quic', 'quic']])),
          insecureBox()),
        h('p', { class: 'hint mt' }, 'TUIC, как и Hysteria2, работает поверх UDP (QUIC).'));
    } else if (d.type === 'trojan') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды Trojan' }),
          inp('host', 'Домен сервера', { placeholder: 'tr.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'SNI, отпечаток',
          h('div', { class: 'grid2' }, inp('sni', 'SNI', { placeholder: 'как домен' }), inp('fingerprint', 'Отпечаток браузера', { placeholder: 'chrome' }), inp('alpn', 'ALPN', { placeholder: 'h2,http/1.1' })),
          insecureBox()),
        h('p', { class: 'hint mt' }, 'Trojan через WebSocket или gRPC добавляйте ссылкой trojan://.'));
    } else if (d.type === 'shadowsocks') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды SS' }),
          inp('host', 'Домен или IP сервера', { placeholder: 'ss.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          sel('method', 'Шифр', [['2022-blake3-aes-128-gcm', '2022-blake3-aes-128-gcm'], ['2022-blake3-aes-256-gcm', '2022-blake3-aes-256-gcm'], ['2022-blake3-chacha20-poly1305', '2022-blake3-chacha20-poly1305'],
            ['aes-128-gcm', 'aes-128-gcm'], ['aes-256-gcm', 'aes-256-gcm'], ['chacha20-ietf-poly1305', 'chacha20-ietf-poly1305'], ['xchacha20-ietf-poly1305', 'xchacha20-ietf-poly1305']]),
          inp('password', 'Пароль или ключ', { type: 'password' })),
        h('p', { class: 'hint mt' }, 'Обычный Shadowsocks в России распознают и блокируют чаще других. Если выбираете его — только шифры 2022 (у них пароль — ключ base64 от сервера), и держите рядом сервер Reality.'));
    } else if (d.type === 'anytls') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды AnyTLS' }),
          inp('host', 'Домен сервера', { placeholder: 'any.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'SNI, отпечаток',
          h('div', { class: 'grid2' }, inp('sni', 'SNI', { placeholder: 'как домен' }), inp('fingerprint', 'Отпечаток браузера', { placeholder: 'chrome' })),
          insecureBox()),
        h('p', { class: 'hint mt' }, 'AnyTLS — TLS с перемешиванием размеров пакетов: против распознавания «TLS внутри TLS». Нужен сервер на sing-box 1.12 или новее. UDP не передаёт.'));
    } else if (d.type === 'vless' && ((d.security && d.security !== 'reality') || (d.transport && d.transport !== 'tcp'))) {
      fields.replaceChildren(
        h('div', { class: 'grid2' }, inp('name', 'Название')),
        h('p', { class: 'hint mt' }, `${protoName(d)}, ${d.host}:${d.port}. Такие серверы добавляются ссылкой vless://. Чтобы поменять параметры, удалите сервер и вставьте новую ссылку.`));
      return;
    }
    if (d.type === 'vless') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды Reality' }),
          inp('host', 'Домен или IP сервера', { placeholder: 'proxy.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('uuid', 'UUID'),
          inp('publicKey', 'Публичный ключ (pbk)'),
          inp('shortId', 'Short ID (sid)')),
        more('Дополнительно', 'SNI, отпечаток',
          h('div', { class: 'grid2' }, inp('sni', 'SNI', { placeholder: 'как домен' }), inp('fingerprint', 'Отпечаток браузера', { placeholder: 'chrome' }))));
    } else if (d.type === 'naive') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды' }),
          inp('host', 'Домен сервера', { placeholder: 'proxy.example.com' }),
          inp('username', 'Логин'),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'порт, SNI, HTTP/3',
          h('div', { class: 'grid2' }, inp('port', 'Порт', { type: 'number' }), inp('sni', 'SNI', { placeholder: 'как домен' })),
          box('quic', 'HTTP/3 (QUIC)', 'Только если сервер слушает QUIC. В России UDP за границу часто режут.'),
          box('uot', 'UDP поверх TCP', 'Только для сервера на sing-box. Caddy с forwardproxy это не умеет.')));
    }
  };
  renderFields();
  const dlg = openDialog(
    h('h2', null, isNew ? 'Новый сервер' : d.name),
    isNew ? h('div', { class: 'row', style: 'margin-bottom:14px' }, h('span', { class: 'hint' }, 'Протокол'),
      h('select', { onchange: e => { d.type = e.target.value; d.security = d.type === 'vless' ? 'reality' : d.type === 'trojan' ? 'tls' : ''; renderFields(); } },
        [['naive', 'Naive'], ['vless', 'VLESS + Reality'], ['hysteria2', 'Hysteria2'], ['tuic', 'TUIC'], ['trojan', 'Trojan'], ['anytls', 'AnyTLS'], ['shadowsocks', 'Shadowsocks']]
          .map(([v, t]) => h('option', { value: v, selected: d.type === v }, t)))) : null,
    fields, err,
    h('div', { class: 'row mt' },
      !isNew && h('button', { class: 'btn danger', onclick: async () => {
        if (!confirm(`Удалить сервер «${d.name}»?`)) return;
        S.servers = S.servers.filter(x => x.id !== d.id);
        if (S.selected === d.id) S.selected = 'auto';
        save(0); await saveNow(); dlg.close(); renderServers();
      } }, 'Удалить'),
      h('span', { class: 'spacer' }),
      h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'),
      h('button', { class: 'btn primary', onclick: async () => {
        d.host = d.host.trim().replace(/^[a-z+]+:\/\//i, '').replace(/[/?#].*$/, '');
        d.port = parseInt(d.port, 10) || 443;
        if (!d.host) { err.textContent = 'Укажите адрес сервера.'; return; }
        if (!d.name.trim()) d.name = d.host;
        if (isNew) S.servers.push(d); else S.servers[S.servers.findIndex(x => x.id === d.id)] = d;
        save(0);
        if (await saveNow()) { dlg.close(); renderServers(); refreshServerGeo(false); toast(isNew ? 'Сервер добавлен' : 'Сервер сохранён'); }
        else if (isNew) S.servers.pop();
      } }, isNew ? 'Добавить' : 'Сохранить')));
  const first = dlg.querySelector('dialog .grid2 input');
  if (first) first.focus();
}

// ---------- установка своего сервера по SSH ----------
const REALITY_SNI = ['www.microsoft.com', 'dl.google.com', 'www.samsung.com', 'addons.mozilla.org'];
const PANEL_TITLE = { '3x-ui': '3X-UI', 's-ui': 's-ui' };

function installServerDialog(prev) {
  const body = h('div');
  const dlg = openDialog(h('h2', null, 'Свой сервер'), body);
  dlg.classList.add('wide');

  // Шаг 1: предупреждение. Без галочки дальше не пустит.
  const warnStep = () => {
    const ok = h('input', { type: 'checkbox' });
    const next = h('button', { class: 'btn primary', disabled: true, onclick: () => formStep(prev || {}) }, 'Дальше');
    ok.addEventListener('change', () => { next.disabled = !ok.checked; });
    body.replaceChildren(
      h('div', { class: 'notice warn' }, h('div', { class: 'grow' },
        h('b', null, 'Делайте это, только если понимаете, что происходит, и это ваш сервер.'),
        h('p', { style: 'margin:6px 0 0' }, 'MZ подключится к серверу по SSH с правами root и изменит его настройки. На сервере, где уже работает что-то важное (сайт, другой VPN, панель управления), установка может ему помешать.'))),
      h('h3', { class: 'mt' }, 'Что может быть сделано на сервере (вы выберете на следующем шаге)'),
      h('ul', { class: 'steps' },
        h('li', null, 'sing-box из официального репозитория SagerNet (apt): VLESS + Reality на TCP 443 или 8443 и, по желанию, Hysteria2 на UDP. Нужен Debian 11+ или Ubuntu 20.04+.'),
        h('li', null, 'NaiveProxy: Caddy с модулем forwardproxy — готовая сборка автора NaiveProxy (проверяется по контрольной сумме) или сборка из исходников прямо на сервере. Нужен свой домен; Caddy займёт порты 80 и 443 и получит сертификат Let\'s Encrypt.'),
        h('li', null, 'Веб-панель 3X-UI или s-ui — их официальными установщиками с GitHub.'),
        h('li', null, 'Файл /etc/sing-box/config.json будет перезаписан — прежний сохранится рядом с пометкой .bak. Службы включатся в автозапуск; если работает ufw, в нём откроются нужные порты; по желанию — BBR.')),
      h('h3', null, 'Что останется как было'),
      h('ul', { class: 'steps' },
        h('li', null, 'SSH, пароли, пользователи, другие программы и их порты.'),
        h('li', null, 'Пароль и ключ SSH MZ не сохраняет — они нужны только на время установки (и потом — чтобы открыть панель).')),
      h('p', { class: 'hint' }, 'Перед вводом пароля MZ покажет отпечаток ключа сервера — сверьте его с панелью хостинга или консолью. Скрипты, которые выполнятся на сервере, можно прочитать заранее. Откатить протоколы: на сервере bash install-server.sh --remove; панели удаляются их же меню (x-ui uninstall, s-ui uninstall).'),
      h('label', { class: 'check mt' }, ok, h('span', null, h('b', null, 'Понимаю, что делаю, и это мой сервер'))),
      h('div', { class: 'row mt' },
        h('button', { class: 'btn', onclick: () => showScript(false) }, 'Скрипт протоколов'),
        h('button', { class: 'btn', onclick: () => showScript(true) }, 'Скрипт панели'),
        h('span', { class: 'spacer' }),
        h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'), next));
  };

  const showScript = async panel => {
    try {
      const r = await api('/api/server/script' + (panel ? '?panel=1' : ''));
      const ta = h('textarea', { readonly: true, style: 'min-height:420px' });
      ta.value = r.script;
      const d2 = openDialog(h('h2', null, panel ? 'install-panel.sh' : 'install-server.sh'), ta, h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d2.close() }, 'Закрыть')));
      d2.classList.add('wide');
    } catch (e) { toast(e.message, true); }
  };

  // Шаг 2: адрес, вход, что ставить, проверка ключа сервера.
  const formStep = d => {
    const f = { host: '', port: 22, user: 'root', password: '', key: '', passphrase: '', sni: REALITY_SNI[0], hy2: true, bbr: true, name: '',
      protocols: true, naive: false, domain: '', panel: '', panelAccess: 'tunnel', ...d };
    let fp = null;
    const err = h('div', { class: 'err-text' });
    const fpBox = h('div');
    const installBtn = h('button', { class: 'btn primary', disabled: true, onclick: () => runStep(f, fp) }, 'Установить');
    const resetFp = () => { fp = null; fpBox.replaceChildren(); installBtn.disabled = true; };
    const inp = (key, label, extra = {}) => h('label', { class: 'field' }, h('span', null, label),
      h(extra.area ? 'textarea' : 'input', { type: extra.type || 'text', value: f[key], placeholder: extra.placeholder, spellcheck: 'false', autocomplete: 'off',
        style: extra.area ? 'min-height:90px' : null, oninput: e => { f[key] = e.target.value; if (extra.resets !== false) resetFp(); } }),
      extra.hint ? h('small', { class: 'hint' }, extra.hint) : null);
    const box = (key, title, desc, after) => h('label', { class: 'check' },
      h('input', { type: 'checkbox', checked: f[key], onchange: e => { f[key] = e.target.checked; if (after) after(); } }),
      h('span', null, h('b', null, title), desc ? h('small', null, desc) : null));

    const useKey = h('input', { type: 'checkbox', checked: !!f.key });
    const keyBox = h('div', { hidden: !f.key }, h('div', { class: 'grid2' }, inp('key', 'Закрытый ключ SSH', { area: true, placeholder: '-----BEGIN OPENSSH PRIVATE KEY-----', resets: false }), inp('passphrase', 'Пароль ключа (если есть)', { type: 'password', resets: false })));
    useKey.addEventListener('change', () => { keyBox.hidden = !useKey.checked; if (!useKey.checked) f.key = ''; });

    // протоколы
    const sniCustom = h('input', { type: 'text', value: REALITY_SNI.includes(f.sni) ? '' : f.sni, placeholder: 'www.example.com', hidden: REALITY_SNI.includes(f.sni), oninput: e => { f.sni = e.target.value.trim(); } });
    const sniSel = h('select', { onchange: e => { if (e.target.value !== 'custom') { f.sni = e.target.value; sniCustom.hidden = true; } else { sniCustom.hidden = false; sniCustom.focus(); } } },
      REALITY_SNI.map(v => h('option', { value: v, selected: f.sni === v }, v)), h('option', { value: 'custom', selected: !REALITY_SNI.includes(f.sni) }, 'Свой сайт…'));
    const sniField = h('label', { class: 'field' }, h('span', null, 'Сайт для маскировки Reality'), sniSel, sniCustom,
      h('small', { class: 'hint' }, 'Посторонний, заглянувший на ваш сервер, увидит этот сайт. Нужен TLS 1.3 и чтобы он не был заблокирован в России. Лучше всего — сайт у того же хостера, что и сервер (их ищет программа RealiTLScanner).'));
    const domainField = h('label', { class: 'field' }, h('span', null, 'Домен для NaiveProxy'),
      h('input', { type: 'text', value: f.domain, placeholder: 'proxy.example.com', spellcheck: 'false', oninput: e => { f.domain = e.target.value.trim(); } }),
      h('small', { class: 'hint' }, 'Заранее создайте у регистратора A-запись этого домена на IP сервера (без прокси Cloudflare) и подождите 5–15 минут. Reality тогда будет маскироваться под ваш же сайт.'));
    const protoBody = h('div');
    const renderProto = () => {
      sniField.hidden = f.naive;
      domainField.hidden = !f.naive;
      protoBody.hidden = !f.protocols;
    };
    protoBody.append(
      h('p', { class: 'hint' }, 'VLESS + Reality ставится всегда — это основной протокол.'),
      h('div', { class: 'grid2' }, sniField, domainField, inp('name', 'Название', { placeholder: 'как адрес', resets: false })),
      box('naive', 'Ещё и NaiveProxy (нужен домен)', 'Настоящий Chromium внутри: трафик неотличим от браузера. Caddy займёт порты 80 и 443, Reality переедет на 8443.', renderProto),
      box('hy2', 'Ещё и Hysteria2 (UDP)', 'Запасной протокол: быстрый на плохом мобильном интернете, но где-то UDP за границу режут. Свой сертификат, MZ проверяет его по отпечатку.'),
      box('bbr', 'Включить BBR', 'Обычно ускоряет загрузку через далёкий сервер. Меняет настройку ядра сервера.'));

    // панель
    const panelBody = h('div');
    const renderPanel = () => {
      if (!f.panel) { panelBody.replaceChildren(); return; }
      const agree = h('input', { type: 'checkbox', checked: !!f.panelConfirm, onchange: e => { f.panelConfirm = e.target.checked; } });
      panelBody.replaceChildren(
        f.panel === '3x-ui'
          ? choices('panelAccess', [
            ['tunnel', 'Только через MZ (рекомендуется)', 'Панель слушает только сам сервер — из интернета её не найти и не взломать. MZ открывает её через SSH-туннель (спросит пароль SSH).'],
            ['public', 'Из интернета по HTTPS', 'Откроется по адресу https://IP:порт/секретный-путь с сертификатом Let\'s Encrypt на IP (нужен свободный порт 80). Удобно с телефона, но панель видна всему интернету — её будут пробовать подобрать.'],
          ], () => f.panelAccess, v => { f.panelAccess = v; })
          : h('p', { class: 'hint' }, 's-ui работает без HTTPS, поэтому открывается только через MZ по SSH-туннелю — из интернета её не видно.'),
        h('div', { class: 'notice warn mt' }, h('div', { class: 'grow' },
          h('b', null, 'Панель — сторонняя программа с правами root.'),
          h('ul', { class: 'steps', style: 'margin-bottom:0' },
            h('li', null, `Ставится официальный установщик ${PANEL_TITLE[f.panel]} с GitHub (последний релиз): его код выполняется на сервере от root. MZ не может за него ручаться.`),
            f.panel === '3x-ui' ? h('li', null, 'Установщик 3X-UI заодно ставит fail2ban и cron для обновления сертификатов.') : null,
            h('li', null, 'Логин и длинный пароль панели MZ придумает сам и сохранит у вас в зашифрованном виде.'),
            h('li', null, 'Протоколы, которые вы создадите в панели, не должны занимать уже занятые порты (Reality, Hysteria2, NaiveProxy).')))),
        h('label', { class: 'check' }, agree, h('span', null, h('b', null, 'Понимаю риски и всё равно ставлю панель'))));
    };
    const panelSel = h('select', { onchange: e => { f.panel = e.target.value; f.panelConfirm = false; renderPanel(); } },
      [['', 'Без панели'], ['3x-ui', '3X-UI — на Xray: Reality, xHTTP, Trojan, пользователи, лимиты'], ['s-ui', 's-ui — на sing-box: Hysteria2, TUIC, AnyTLS, Naive']]
        .map(([v, t]) => h('option', { value: v, selected: f.panel === v }, t)));

    const check = async () => {
      err.textContent = ''; fpBox.replaceChildren(h('p', { class: 'hint' }, 'Получаю ключ сервера…'));
      try {
        const r = await api('/api/server/hostkey', { host: f.host, port: parseInt(f.port, 10) || 22 });
        fp = r.fingerprint;
        const agree = h('input', { type: 'checkbox' });
        agree.addEventListener('change', () => { installBtn.disabled = !agree.checked; });
        fpBox.replaceChildren(h('div', { class: 'card mt' },
          h('p', null, 'Отпечаток ключа сервера (', r.type, '):'),
          h('code', { class: 'fp' }, fp),
          h('p', { class: 'hint' }, 'Сверьте его с тем, что показывает хостинг, или с выводом команды на сервере (через консоль в панели хостинга): ', h('code', null, `ssh-keygen -lf /etc/ssh/ssh_host_${r.type === 'ssh-ed25519' ? 'ed25519' : r.type.startsWith('ecdsa') ? 'ecdsa' : 'rsa'}_key.pub`), '. Не совпадает — не продолжайте: возможно, вы подключаетесь не к своему серверу.'),
          h('label', { class: 'check' }, agree, h('span', null, h('b', null, 'Отпечаток совпадает — это мой сервер')))));
      } catch (e) { fpBox.replaceChildren(); err.textContent = e.message; }
    };
    body.replaceChildren(
      h('div', { class: 'grid2' },
        inp('host', 'IP или домен сервера', { placeholder: '203.0.113.10' }),
        inp('port', 'Порт SSH', { type: 'number' }),
        inp('user', 'Пользователь', { hint: 'root или пользователь с sudo без пароля' }),
        inp('password', 'Пароль SSH', { type: 'password', hint: 'Не сохраняется.', resets: false })),
      h('label', { class: 'check' }, useKey, h('span', null, h('b', null, 'Вход по ключу SSH'))),
      keyBox,
      h('h3', { class: 'mt' }, 'Протоколы'),
      box('protocols', 'Поставить протоколы MZ', 'Снимите, если нужна только панель.', renderProto),
      protoBody,
      h('h3', { class: 'mt' }, 'Веб-панель (необязательно)'),
      h('p', { class: 'hint' }, 'Панель нужна, если хотите сами управлять протоколами и пользователями в браузере: раздавать доступ друзьям, ставить лимиты трафика. Для себя одного хватит протоколов выше.'),
      h('label', { class: 'field' }, panelSel),
      panelBody,
      err, fpBox,
      h('div', { class: 'row mt' },
        h('button', { class: 'btn', onclick: warnStep }, 'Назад'),
        h('span', { class: 'spacer' }),
        h('button', { class: 'btn', onclick: check }, 'Проверить ключ сервера'),
        installBtn));
    renderProto();
    renderPanel();
  };

  // Шаг 3: установка, журнал с сервера.
  const runStep = async (f, fp) => {
    const log = h('pre', { class: 'log', style: 'max-height:360px;min-height:200px' });
    const status = h('p', { class: 'hint' }, 'Устанавливаю… Обычно 1–5 минут, со сборкой Caddy для NaiveProxy — до 15. Окно можно не закрывать.');
    const close = h('button', { class: 'btn', disabled: true, onclick: () => dlg.close() }, 'Закрыть');
    const actions = h('div', { class: 'row end mt' }, close);
    body.replaceChildren(status, log, actions);
    try {
      await api('/api/server/install', { host: f.host, port: parseInt(f.port, 10) || 22, user: f.user, password: f.password, key: f.key || '', passphrase: f.passphrase,
        fingerprint: fp, confirm: true, protocols: f.protocols, sni: f.sni, hy2: f.hy2, naive: f.naive, domain: f.domain, bbr: f.bbr, name: f.name,
        panel: f.panel, panelAccess: f.panelAccess, panelConfirm: !!f.panelConfirm });
    } catch (e) {
      status.className = 'err-text'; status.textContent = e.message; close.disabled = false;
      actions.prepend(h('button', { class: 'btn', onclick: () => formStep(f) }, 'Изменить параметры'));
      return;
    }
    f.password = ''; f.passphrase = ''; f.key = ''; // больше не нужны
    const poll = async () => {
      let v;
      try { v = await api('/api/server/install'); } catch (_) { setTimeout(poll, 1500); return; }
      log.textContent = v.log.join('\n');
      log.scrollTop = log.scrollHeight;
      if (v.active) { setTimeout(poll, 1000); return; }
      close.disabled = false;
      S = await api('/api/settings');
      renderServers(); refreshServerGeo(true);
      if (v.error) { status.className = 'err-text'; status.textContent = v.error; return; }
      status.className = '';
      const parts = [];
      if (v.added && v.added.length) parts.push(`добавлены серверы: ${v.added.join(', ')}`);
      if (v.panel) parts.push(`панель ${PANEL_TITLE[f.panel]} установлена — логин и пароль в разделе «Панели»`);
      status.replaceChildren(h('b', null, 'Готово: '), parts.join('; ') + '.');
      const p = v.panel && (S.panels || []).find(x => x.id === v.panel);
      if (p) actions.prepend(h('button', { class: 'btn primary', onclick: () => openPanel(p) }, 'Открыть панель'));
    };
    poll();
  };

  warnStep();
}

// ---------- панели ----------
// ---------- свой сервер ----------
function viewOwn() {
  const can = ST && ST.install;
  const proto = (name, what) => h('div', { class: 'own-proto' }, h('b', null, name), h('span', null, what));
  return h('div', { class: 'view' },
    h('h1', null, 'Свой сервер'),
    h('p', { class: 'sub' }, 'MZ сам поставит протоколы и панель управления на ваш VPS по SSH (Debian или Ubuntu) и добавит готовые серверы в список.'),
    h('div', { class: 'card own-hero' },
      h('div', { class: 'own-protos' },
        proto('VLESS Reality', 'основной: маскируется под чужой сайт, ставится всегда'),
        proto('Hysteria2', 'быстрый запасной по UDP'),
        proto('NaiveProxy', 'HTTPS как у браузера, нужен свой домен'),
        proto('Панель 3X-UI или s-ui', 'пользователи, лимиты, подписки — по желанию')),
      h('div', { class: 'row mt' },
        h('button', { class: 'btn primary', disabled: !can, onclick: () => installServerDialog() }, 'Начать установку'),
        h('span', { class: 'hint' }, can ? 'Перед началом MZ покажет, что именно сделает на сервере, и попросит подтвердить.' : 'Недоступно в этой сборке (Windows 7).'))),
    h('h2', null, 'Панели'),
    h('div', { id: 'panelsBox' }),
    h('div', { class: 'row mt' },
      h('button', { class: 'btn small', onclick: () => {
        S.ownServer = false; save(); applySections(); go('overview');
        toast('Раздел скрыт. Вернуть его можно в «Настройках» → «Разделы».');
      } }, 'Убрать раздел из меню'),
      h('span', { class: 'hint' }, 'Серверы и панели, которые уже поставлены, останутся.')));
}

function renderOwn() {
  patch('panelsBox', JSON.stringify(S.panels || []), () => (S.panels || []).length ? panelsBox()
    : h('p', { class: 'hint' }, 'Панелей пока нет. Её можно поставить вместе с протоколами — отметьте панель в окне установки.'));
}

function panelsBox() {
  const list = S.panels || [];
  if (!list.length) return null;
  return h('div', null,
    h('h2', null, 'Панели'),
    h('div', { class: 'panel' }, h('table', { class: 'table' }, h('tbody', null, list.map(p => h('tr', null,
      h('td', null, h('div', { class: 'cell-main' }, PANEL_TITLE[p.type] || p.type, p.version ? h('span', { class: 'ver' }, ' ', p.version) : null),
        h('div', { class: 'cell-sub' }, p.host + ' · ' + (p.access === 'public' ? 'из интернета по HTTPS' : 'через SSH-туннель'))),
      h('td', { class: 'r' }, h('div', { class: 'row end', style: 'flex-wrap:nowrap' },
        h('button', { class: 'btn small primary', onclick: () => openPanel(p) }, 'Открыть'),
        h('button', { class: 'btn small', onclick: () => panelCreds(p) }, 'Логин и пароль'),
        h('button', { class: 'btn small', onclick: async () => {
          if (!confirm(`Убрать панель ${PANEL_TITLE[p.type]} на ${p.host} из списка? На сервере она останется.`)) return;
          S.panels = S.panels.filter(x => x.id !== p.id); save(0); await saveNow(); renderServers();
        } }, 'Убрать')))))))),
    h('p', { class: 'hint' }, 'Через туннель панель открывается в браузере по адресу 127.0.0.1 — соединение до сервера зашифровано SSH. Туннель живёт 3 часа или до выхода из MZ.'));
}

function panelCreds(p) {
  const row = (label, value) => h('div', { class: 'row', style: 'flex-wrap:nowrap' }, h('span', { class: 'hint', style: 'min-width:80px' }, label), h('code', { class: 'fp grow' }, value),
    h('button', { class: 'btn small', onclick: async () => { try { await navigator.clipboard.writeText(value); toast('Скопировано'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Копировать'));
  const d = openDialog(h('h2', null, `${PANEL_TITLE[p.type]} на ${p.host}`),
    row('Логин', p.user), row('Пароль', p.pass),
    p.access === 'public' ? row('Адрес', p.url) : h('p', { class: 'hint' }, `Порт панели на сервере ${p.port}, путь ${p.path} — открывается только через туннель MZ.`),
    h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Закрыть')));
}

function openPanel(p) {
  if (p.access === 'public' && p.url) {
    api('/api/panel/open', { id: p.id }).catch(e => toast(e.message, true));
    return;
  }
  const pass = h('input', { type: 'password', placeholder: 'Пароль SSH', autocomplete: 'current-password' });
  const key = h('textarea', { placeholder: 'или закрытый ключ SSH', spellcheck: 'false', style: 'min-height:70px' });
  const phrase = h('input', { type: 'password', placeholder: 'пароль ключа (если есть)' });
  const err = h('div', { class: 'err-text' });
  const go = h('button', { class: 'btn primary' }, 'Открыть');
  const d = openDialog(h('h2', null, `Открыть ${PANEL_TITLE[p.type]}`),
    h('p', { class: 'hint' }, `MZ подключится к ${p.host} по SSH (ключ сервера проверяется по отпечатку, подтверждённому при установке) и откроет панель в браузере. Пароль SSH не сохраняется.`),
    pass, h('details', { class: 'more' }, h('summary', null, h('span', null, 'Вход по ключу')), h('div', { class: 'more-body' }, key, phrase)), err,
    h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Отмена'), go));
  const submit = async () => {
    err.textContent = ''; go.disabled = true; go.textContent = 'Подключаюсь…';
    try {
      const r = await api('/api/panel/open', { id: p.id, password: pass.value, key: key.value, passphrase: phrase.value });
      d.close();
      toast('Панель открывается в браузере');
      if (ST && ST.os !== 'windows') openDialog(h('p', null, 'Откройте в браузере: ', h('code', null, r.url)));
    } catch (e) { err.textContent = e.message; go.disabled = false; go.textContent = 'Открыть'; }
  };
  go.addEventListener('click', submit);
  pass.addEventListener('keydown', e => { if (e.key === 'Enter') submit(); });
  pass.focus();
}

function importLinksDialog() {
  const ta = h('textarea', { placeholder: 'naive+https://user:pass@nl.example.com:443#Нидерланды\nvless://…@nl.example.com:8443?security=reality&…#Reality', spellcheck: 'false' });
  const err = h('div', { class: 'err-text' });
  const dlg = openDialog(
    h('h2', null, 'Вставить ссылки'),
    h('p', { class: 'hint' }, 'По одной в строке: naive+https://…, naive+quic://…, vless://…, hysteria2://…, tuic://…, trojan://…, anytls://… или ss://…. Ссылку для Reality выдаёт скрипт сервера add-reality.sh или установка сервера из MZ.'),
    ta, err,
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'),
      h('button', { class: 'btn primary', onclick: async () => {
        await saveNow();
        try {
          const res = await api('/api/import', { text: ta.value });
          S = res.settings;
          renderServers(); refreshServerGeo(false);
          if (res.errors.length) err.textContent = `Добавлено: ${res.added}. Не разобраны:\n` + res.errors.join('\n');
          else { dlg.close(); toast(`Добавлено серверов: ${res.added}`); }
        } catch (e) { err.textContent = e.message; }
      } }, 'Добавить')));
  ta.focus();
}
// ---------- правила ----------
const KIND = { domain: 'домен', ip: 'IP', process: 'программа' };
const ACTION = { proxy: 'Через сервер', direct: 'Напрямую', zapret: 'Через zapret', block: 'Блокировать' };
const ruleKey = r => r.kind + '|' + r.value.toLowerCase();
const shownValue = r => r.kind === 'domain' ? displayDomain(r.value) : r.value;

// Punycode → кириллица для показа: xn--p1ai → рф (RFC 3492).
function punyDecode(input) {
  const base = 36, tmin = 1, tmax = 26, skew = 38, damp = 700;
  let n = 128, i = 0, bias = 72;
  const out = [];
  const b = input.lastIndexOf('-');
  for (let j = 0; j < Math.max(b, 0); j++) out.push(input.charCodeAt(j));
  for (let idx = b > 0 ? b + 1 : 0; idx < input.length;) {
    const oldi = i;
    let w = 1;
    for (let k = base; ; k += base) {
      if (idx >= input.length) throw new Error('bad');
      const c = input.charCodeAt(idx++);
      let d;
      if (c >= 48 && c <= 57) d = c - 22;
      else if (c >= 65 && c <= 90) d = c - 65;
      else if (c >= 97 && c <= 122) d = c - 97;
      else throw new Error('bad');
      i += d * w;
      const t = k <= bias ? tmin : k >= bias + tmax ? tmax : k - bias;
      if (d < t) break;
      w *= base - t;
    }
    const len = out.length + 1;
    let delta = oldi === 0 ? Math.floor((i - oldi) / damp) : Math.floor((i - oldi) / 2);
    delta += Math.floor(delta / len);
    let k = 0;
    while (delta > ((base - tmin) * tmax) >> 1) { delta = Math.floor(delta / (base - tmin)); k += base; }
    bias = k + Math.floor((base - tmin + 1) * delta / (delta + skew));
    n += Math.floor(i / len);
    i %= len;
    out.splice(i, 0, n);
    i++;
  }
  return String.fromCodePoint(...out);
}

function displayDomain(v) {
  if (!v.includes('xn--')) return v;
  return v.split('.').map(l => {
    if (!l.startsWith('xn--')) return l;
    try { return punyDecode(l.slice(4)); } catch (_) { return l; }
  }).join('.');
}

function viewRules() {
  return h('div', { class: 'view' },
    h('h1', null, 'Правила'),
    h('p', { class: 'sub' }, 'Что идёт через сервер, а что напрямую. Изменения применяются после переподключения.'),
    h('h2', null, 'Режим'),
    segmented(Object.entries(ROUTING), () => S.routing, v => { S.routing = v; save(); $('#modeDesc').textContent = MODE_DESC[v]; }, 'Режим'),
    h('p', { class: 'hint mode-desc', id: 'modeDesc' }, MODE_DESC[S.routing]),
    h('div', { id: 'catBox' }),

    h('div', { class: 'title-row mt-lg' },
      h('h2', { class: 'flush' }, 'Свои правила'),
      h('div', { class: 'row' },
        h('button', { class: 'btn', onclick: importRulesFile }, 'Импорт'),
        h('button', { class: 'btn', onclick: exportRulesDialog }, 'Экспорт'),
        h('button', { class: 'btn', onclick: pickProcesses }, 'Выбрать программы'),
        h('button', { class: 'btn primary', onclick: () => addRulesDialog('') }, 'Добавить'))),
    h('p', { class: 'sub', id: 'ruleSummary' }),
    h('div', { id: 'ruleSearch' }),
    h('div', { class: 'panel' }, h('table', { class: 'table rules' }, h('tbody', { id: 'ruleBody' }))),
    h('div', { id: 'ruleFoot' }),

    h('div', { class: 'title-row mt-lg' },
      h('h2', { class: 'flush' }, 'Списки по ссылке'),
      h('div', { class: 'row' },
        h('button', { class: 'btn', id: 'listRefreshBtn', onclick: refreshListsNow }, 'Обновить'),
        h('button', { class: 'btn primary', onclick: addListDialog }, 'Добавить список'))),
    h('p', { class: 'sub' }, 'Готовые списки сайтов из интернета: весь список идёт одним действием и обновляется сам раз в сутки. Ваши правила выше важнее списков.'),
    h('div', { id: 'listBox' }),

    h('div', { class: 'mt-lg' }),
    more('Особые случаи', specialNote(),
      check('ruInsideDirect', 'Сайты, которые работают только из России, всегда напрямую', 'Госуслуги, банки и другие сервисы, которые не пускают иностранные IP.', refreshSpecialNote),
      check('blockQuic', 'Блокировать QUIC', 'Naive передаёт только TCP. Без QUIC браузеры сами перейдут на обычный HTTPS, и он пойдёт через сервер.', refreshSpecialNote),
      check('udpDirect', 'Остальной UDP напрямую', 'Игры и звонки пойдут мимо сервера: через Naive UDP всё равно не проходит.', refreshSpecialNote),
      check('udpProxy', 'UDP через сервер — голос Discord, игры', 'Naive и AnyTLS передают только TCP, а VLESS, Hysteria2, TUIC, Trojan и Shadowsocks — и UDP. Тогда UDP, который по правилам должен идти через сервер, пойдёт через них (или через подписку). Работает в режиме TUN: в режиме прокси программы отправляют UDP мимо MZ.', refreshSpecialNote),
      check('adBlock', 'Блокировать рекламу и трекеры', 'По готовому списку для sing-box (geosite category-ads-all, обновляется раз в сутки): баннеры, счётчики, рекламные сети. Видеорекламу YouTube так не убрать — она идёт с тех же серверов, что и само видео; для неё нужен uBlock Origin в браузере. Если какой-то сайт сломается — добавьте его правилом «Напрямую» или «Через сервер».', refreshSpecialNote),
      check('twitchAdFree', 'Twitch без рекламы', 'Twitch вставляет рекламу в трансляцию в зависимости от страны, откуда запрошен плейлист. MZ запрашивает плейлисты через прокси проекта TTV LOL PRO в стране без рекламы, а само видео идёт как обычно. Прокси видит только то, что вы смотрите Twitch, но не какой канал. Работает для трансляций, не для записей; баннеры на сайте остаются. Если трансляции перестали открываться — выключите.', refreshSpecialNote)),
    more('Правила sing-box вручную', S.extraRules.trim() ? 'есть свои правила' : 'для опытных',
      h('p', { class: 'hint' }, 'JSON-массив для route.rules, вставляется сразу после ваших правил. Можно ссылаться на outbound proxy, direct, auto и на свои outbounds из настроек.'),
      jsonArea('extraRules', '[\n  { "domain_keyword": ["openai"], "outbound": "proxy" }\n]', true)));
}

// ---------- списки по ссылке ----------
const LIST_PRESETS = [
  ['Заблокированное в России (itdoginfo)', 'https://github.com/itdoginfo/allow-domains/releases/latest/download/russia_inside.srs', 'proxy',
    'Сайты, недоступные из России. Хорош в режиме «Только заблокированное».'],
  ['Не пускают пользователей из России (itdoginfo)', 'https://github.com/itdoginfo/allow-domains/releases/latest/download/geoblock.srs', 'proxy',
    'Сервисы, которые сами закрылись для российских IP: ИИ-сервисы, магазины, облака.'],
  ['Работает только из России (itdoginfo)', 'https://github.com/itdoginfo/allow-domains/releases/latest/download/russia_outside.srs', 'direct',
    'Российские сайты, которые не открываются из-за рубежа: их — напрямую.'],
];
const LIST_ACTION = { proxy: 'Через сервер', direct: 'Напрямую', block: 'Блокировать' };
const LIST_FORMAT = { srs: 'sing-box .srs', json: 'sing-box .json', text: 'текстовый список' };
let LISTS = [];

async function renderLists() {
  const box = $('#listBox');
  if (!box) return;
  try { LISTS = await api('/api/lists'); } catch (_) { /* покажем без счётчиков */ }
  const info = id => LISTS.find(x => x.id === id) || {};
  if (!S.ruleLists.length) {
    box.replaceChildren(h('p', { class: 'hint' }, 'Списков нет. Добавьте готовый или свой по ссылке.'));
    return;
  }
  box.replaceChildren(h('div', { class: 'panel' }, h('table', { class: 'table rules' }, h('tbody', null, S.ruleLists.map(l => {
    const st = info(l.id);
    const sub = l.format === 'text'
      ? (st.error ? h('span', { class: 'err-text' }, st.error) : st.updated ? `${count(st.count || 0, 'запись', 'записи', 'записей')}, обновлён ${fmtTime(st.updated)}` : 'скачается при подключении')
      : 'скачивает ядро при подключении';
    return h('tr', null,
      h('td', null, sw(l.enabled, false, v => { l.enabled = v; save(0); }, '')),
      h('td', null, h('div', { class: 'cell-main trunc', title: l.url }, l.name), h('div', { class: 'cell-sub' }, LIST_FORMAT[l.format] + ' · ', sub)),
      h('td', null, h('select', { onchange: e => { l.action = e.target.value; save(0); } },
        Object.entries(LIST_ACTION).map(([v, t]) => h('option', { value: v, selected: l.action === v }, t)))),
      h('td', { class: 'r' }, h('button', { class: 'btn small', onclick: () => {
        if (!confirm(`Удалить список «${l.name}»?`)) return;
        S.ruleLists = S.ruleLists.filter(x => x !== l); save(0); renderLists();
      } }, 'Удалить')));
  })))));
}

async function refreshListsNow() {
  const btn = $('#listRefreshBtn');
  if (btn) { btn.disabled = true; btn.textContent = 'Обновляю…'; }
  await saveNow();
  try { await api('/api/lists/refresh', {}); toast(ST && ST.running ? 'Списки обновлены, переподключаюсь' : 'Списки обновлены'); }
  catch (e) { toast(e.message.split('\n')[0], true); }
  if (btn) { btn.disabled = false; btn.textContent = 'Обновить'; }
  renderLists();
}

function addListDialog() {
  const name = h('input', { type: 'text', placeholder: 'Название' });
  const url = h('input', { type: 'text', placeholder: 'https://…/list.srs, .json или .txt/.lst', spellcheck: 'false' });
  let action = 'proxy';
  const err = h('div', { class: 'err-text' });
  const have = new Set(S.ruleLists.map(l => l.url));
  const add = (n, u, a) => {
    u = u.trim();
    if (!/^https:\/\//.test(u)) { err.textContent = 'Нужна ссылка, начинающаяся с https://'; return false; }
    if (have.has(u)) { err.textContent = 'Такой список уже есть'; return false; }
    S.ruleLists.push({ id: rid(), name: n.trim(), url: u, format: '', action: a, enabled: true });
    return true;
  };
  const done = async () => {
    if (!(await saveNow())) { S.ruleLists = S.ruleLists.filter(l => have.has(l.url)); return; }
    S = await api('/api/settings');
    dlg.close(); renderLists();
    toast(ST && ST.running ? 'Список добавлен — переподключитесь, чтобы он заработал' : 'Список добавлен');
  };
  const dlg = openDialog(
    h('h2', null, 'Добавить список'),
    h('h3', null, 'Готовые'),
    h('div', { class: 'presets' }, LIST_PRESETS.map(([n, u, a, d]) => h('div', { class: 'preset' },
      h('span', { class: 'grow' }, h('b', null, n), h('small', null, `${d} Действие: ${LIST_ACTION[a].toLowerCase()}.`)),
      have.has(u) ? h('span', { class: 'hint' }, 'уже есть')
        : h('button', { class: 'btn small', onclick: () => { if (add(n, u, a)) { save(0); done(); } } }, 'Добавить')))),
    h('h3', { class: 'mt' }, 'Свой'),
    h('p', { class: 'hint' }, 'Rule-set sing-box (.srs или .json) или обычный текст: домены и IP по одному в строке, строки hosts тоже подходят. Добавляйте списки только из источников, которым доверяете: список решает, куда пойдёт ваш трафик.'),
    h('div', { class: 'grid2' }, name, url),
    h('div', { class: 'row mt' }, h('span', { class: 'hint' }, 'Действие'), segmented(Object.entries(LIST_ACTION), () => action, v => { action = v; }, 'Действие')),
    err,
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'),
      h('button', { class: 'btn primary', onclick: () => { err.textContent = ''; if (add(name.value, url.value, action)) { save(0); done(); } } }, 'Добавить')));
  dlg.classList.add('wide');
  url.focus();
}

const MODE_DESC = {
  'ru-direct': 'Российские сайты напрямую, остальное через сервер. Заблокированное в России всегда идёт через сервер, даже если это сайт в зоне .ru.',
  'blocked-only': 'Через сервер только сайты из списка блокировок, остальное напрямую. Быстрее, но свежие блокировки попадают в список с задержкой.',
  'global': 'Весь трафик через сервер, кроме локальной сети и ваших правил «Напрямую».',
};

function specialNote() {
  const on = [S.adBlock && 'реклама блокируется', S.twitchAdFree && 'Twitch без рекламы', S.ruInsideDirect && 'российские сервисы напрямую', S.blockQuic && 'QUIC блокируется', S.udpProxy ? 'UDP через VLESS' : S.udpDirect && 'UDP напрямую'].filter(Boolean);
  return on.length ? on.join(', ') : 'всё выключено';
}
function refreshSpecialNote() {
  const s = document.querySelector('details.more summary small');
  if (s) s.textContent = specialNote();
}

const CATS = [
  ['mine', 'Ваши правила', 'var(--warn)'],
  ['blocked', 'Списки блокировок — через сервер', 'var(--tunnel)'],
  ['ru', 'Российские сайты — напрямую', 'var(--direct)'],
  ['inside', 'Только из России — напрямую', 'color-mix(in srgb, var(--direct) 60%, var(--ink))'],
  ['final', 'Остальное — по режиму', 'var(--faint)'],
];

function ruleCategory(rule) {
  if (rule === 'final') return 'final';
  if (/inbound=\[?probe/.test(rule) || rule.includes('twitch-adfree') || rule.includes('ttvnw') || rule.includes('ip_is_private') || rule.includes('network=udp') || rule.includes('protocol=dns')) return null;
  if (rule.includes('ru-blocked')) return 'blocked';
  if (rule.includes('ru-inside')) return 'inside';
  if (rule.includes('category-ru') || rule.includes('geoip-ru') || /domain_suffix=\[?ru\b/.test(rule)) return 'ru';
  return 'mine';
}

function renderCats() {
  const box = $('#catBox');
  if (!box) return;
  const hits = (STATS && STATS.ruleHits) || {};
  const sum = {};
  let total = 0;
  for (const [rule, n] of Object.entries(hits)) {
    const c = ruleCategory(rule);
    if (!c) continue;
    sum[c] = (sum[c] || 0) + n;
    total += n;
  }
  if (!total) { box.replaceChildren(h('p', { class: 'hint mt' }, 'После подключения здесь будет видно, сколько соединений пошло по каждому правилу.')); return; }
  box.replaceChildren(
    h('div', { class: 'cat-bar' }, CATS.filter(([k]) => sum[k]).map(([k, , col]) => h('span', { style: `width:${sum[k] / total * 100}%;background:${col}`, title: `${sum[k]}` }))),
    h('div', { class: 'legend' }, CATS.filter(([k]) => sum[k]).map(([k, label, col]) =>
      h('span', null, h('i', { style: 'background:' + col }), label, h('b', null, String(sum[k]))))),
    h('p', { class: 'hint', style: 'margin-top:6px' }, `${count(total, 'соединение', 'соединения', 'соединений')} за сеанс.`));
}

function renderRules() {
  const body = $('#ruleBody');
  if (!body) return;
  renderCats();
  // Не перестраиваем таблицу, пока пользователь выбирает действие в списке.
  if (document.activeElement && document.activeElement.tagName === 'SELECT' && body.contains(document.activeElement)) return;
  const userHits = (STATS && STATS.userHits) || {};
  const hitsOf = r => userHits[r.kind + '|' + r.value.toLowerCase()] || 0;
  const n = { proxy: 0, direct: 0, block: 0, zapret: 0 };
  S.rules.forEach(r => { n[r.action]++; });
  const parts = [n.proxy && `${n.proxy} через сервер`, n.direct && `${n.direct} напрямую`,
    n.zapret && `${n.zapret} через zapret`,
    n.block && `${n.block} ${plural(n.block, 'блокируется', 'блокируются', 'блокируются')}`].filter(Boolean);
  $('#ruleSummary').textContent = S.rules.length
    ? `${count(S.rules.length, 'правило', 'правила', 'правил')}: ${parts.join(', ')}. Срабатывают раньше режима.`
    : 'Срабатывают раньше режима. Можно вставить сразу целый список доменов, IP-адресов и программ.';

  patch('ruleSearch', String(S.rules.length > 8), () => S.rules.length > 8
    ? h('input', { type: 'text', class: 'grow-input search', placeholder: 'Найти правило', value: ruleFilter, oninput: e => { ruleFilter = e.target.value; renderRules(); } })
    : null);

  const q = ruleFilter.trim().toLowerCase();
  const visible = S.rules.map((r, i) => [r, i]).filter(([r]) => !q || r.value.toLowerCase().includes(q) || shownValue(r).toLowerCase().includes(q));
  const LIMIT = 300;
  if (!S.rules.length) {
    body.replaceChildren(h('tr', null, h('td', { class: 'empty', colspan: 5 }, 'Своих правил пока нет.')));
  } else if (!visible.length) {
    body.replaceChildren(h('tr', null, h('td', { class: 'empty', colspan: 5 }, 'Ничего не найдено.')));
  } else {
    body.replaceChildren(...visible.slice(0, LIMIT).map(([r, i]) => h('tr', null,
      h('td', { class: 'cell-main trunc', title: r.value }, shownValue(r)),
      h('td', { class: 'cell-sub' }, KIND[r.kind]),
      h('td', { class: 'r hits' + (hitsOf(r) ? ' hot' : ''), title: 'Соединений по этому правилу за сеанс' }, hitsOf(r) ? `${hitsOf(r)}×` : '—'),
      h('td', null, h('select', {
        class: 'tag-select ' + r.action, 'aria-label': 'Действие',
        onchange: e => { r.action = e.target.value; save(); renderRules(); },
      }, Object.entries(ACTION).filter(([v]) => v !== 'zapret' || r.kind === 'domain').map(([v, t]) => h('option', { value: v, selected: r.action === v }, t)))),
      h('td', { class: 'r' }, h('button', {
        class: 'icon-btn', title: 'Удалить', 'aria-label': `Удалить ${r.value}`,
        onclick: () => { S.rules.splice(i, 1); save(); renderRules(); },
      }, '×')))));
  }
  const foot = $('#ruleFoot');
  foot.replaceChildren(...[
    visible.length > LIMIT ? h('p', { class: 'hint mt' }, `Показаны первые ${LIMIT} из ${visible.length}. Воспользуйтесь поиском.`) : null,
    S.rules.length ? h('div', { class: 'row end mt' }, h('button', {
      class: 'btn link danger', onclick: () => {
        if (!confirm(`Удалить все ${count(S.rules.length, 'правило', 'правила', 'правил')}? Перед этим можно сделать экспорт.`)) return;
        S.rules = [];
        save(0);
        renderRules();
      },
    }, 'Удалить все правила')) : null].filter(Boolean));
}

function mergeRules(target, incoming) {
  const idx = new Map(target.map((r, i) => [ruleKey(r), i]));
  const res = { added: 0, updated: 0, same: 0 };
  for (const r of incoming) {
    const k = ruleKey(r);
    if (idx.has(k)) {
      const ex = target[idx.get(k)];
      if (ex.action !== r.action) { ex.action = r.action; res.updated++; } else res.same++;
    } else {
      target.push({ ...r });
      idx.set(k, target.length - 1);
      res.added++;
    }
  }
  return res;
}

function addRulesDialog(text, fileName) {
  let action = 'proxy';
  let parsed = null;
  let timer;
  const ta = h('textarea', {
    class: 'rules-input', spellcheck: 'false', value: text,
    placeholder: 'youtube.com\ndiscord.com, discord.gg\n203.0.113.0/24\nTelegram.exe\n\n[Напрямую]\nsberbank.ru',
    oninput: () => { clearTimeout(timer); timer = setTimeout(preview, 250); },
  });
  const actionWrap = h('div', { class: 'row' }, h('span', { class: 'hint' }, 'Действие для списка'),
    segmented(Object.entries(ACTION), () => action, v => { action = v; preview(); }, 'Действие'));
  const info = h('div', { class: 'parse-info' });
  const addBtn = h('button', { class: 'btn primary', disabled: true }, 'Добавить');

  async function preview() {
    const t = ta.value;
    if (!t.trim()) { parsed = null; info.replaceChildren(); addBtn.disabled = true; addBtn.textContent = 'Добавить'; return; }
    try { parsed = await api('/api/rules/parse', { text: t, action }); } catch (e) { info.textContent = e.message; return; }
    actionWrap.hidden = parsed.fromJson;
    const dry = mergeRules(S.rules.map(r => ({ ...r })), parsed.rules);
    const kinds = { domain: 0, ip: 0, process: 0 };
    parsed.rules.forEach(r => { kinds[r.kind]++; });
    const found = [kinds.domain && count(kinds.domain, 'домен', 'домена', 'доменов'),
      kinds.ip && count(kinds.ip, 'IP-адрес', 'IP-адреса', 'IP-адресов'),
      kinds.process && count(kinds.process, 'программа', 'программы', 'программ')].filter(Boolean).join(', ');
    const lines = [];
    if (found) lines.push(h('p', null, 'Распознано: ' + found + '.'));
    const tail = [dry.updated && `у ${dry.updated} изменится действие`, dry.same && `${dry.same} уже есть`].filter(Boolean);
    if (tail.length) lines.push(h('p', { class: 'hint' }, 'Из них ' + tail.join(', ') + '.'));
    if (parsed.fromJson) lines.push(h('p', { class: 'hint' }, 'Действия взяты из файла.'));
    if (parsed.errors.length) {
      lines.push(h('p', { class: 'err-text' }, `Не распознано: ${parsed.errors.length}`),
        h('ul', { class: 'err-list' }, parsed.errors.slice(0, 6).map(e => h('li', null, e)),
          parsed.errors.length > 6 ? h('li', null, `и ещё ${parsed.errors.length - 6}`) : null));
    }
    info.replaceChildren(...lines);
    const n = dry.added + dry.updated;
    addBtn.disabled = n === 0;
    addBtn.textContent = n ? `Добавить ${count(n, 'правило', 'правила', 'правил')}` : 'Нечего добавлять';
  }

  addBtn.addEventListener('click', async () => {
    if (!parsed) return;
    const backup = S.rules.map(r => ({ ...r }));
    const res = mergeRules(S.rules, parsed.rules);
    save(0);
    if (await saveNow()) {
      dlg.close();
      renderRules();
      toast([res.added && `Добавлено: ${res.added}`, res.updated && `обновлено: ${res.updated}`].filter(Boolean).join(', ') || 'Без изменений');
    } else {
      S.rules = backup;
    }
  });

  const dlg = openDialog(
    h('h2', null, fileName ? 'Импорт правил' : 'Добавить правила'),
    h('p', { class: 'hint' }, fileName
      ? `Из файла ${fileName}. Проверьте список и выберите действие.`
      : 'По одному в строке или через запятую: домены, ссылки, IP-адреса и подсети, программы (.exe). Тип определяется сам. Строки [Напрямую], [Через сервер], [Через zapret], [Блокировать] меняют действие для следующих строк. «Через zapret» — только для доменов: сайт идёт напрямую, а zapret обходит блокировку.'),
    ta, actionWrap, info,
    h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'), addBtn));
  dlg.classList.add('wide');
  ta.focus();
  if (text) preview();
}

function importRulesFile() {
  const input = $('#fileInput');
  input.value = '';
  input.onchange = async () => {
    const f = input.files && input.files[0];
    if (!f) return;
    if (f.size > 5 * 1024 * 1024) { toast('Файл больше 5 МБ', true); return; }
    addRulesDialog(await f.text(), f.name);
  };
  input.click();
}

async function exportRulesDialog() {
  await saveNow();
  let data;
  try { data = await api('/api/rules/export'); } catch (e) { toast(e.message, true); return; }
  const stamp = new Date().toISOString().slice(0, 10);
  const humanText = data.text.split('\n').map(l => (l.startsWith('[') || /\.exe$/i.test(l)) ? l : displayDomain(l)).join('\n');
  const dlg = openDialog(
    h('h2', null, 'Экспорт правил'),
    h('p', { class: 'hint' }, data.json.rules.length
      ? `${count(data.json.rules.length, 'правило', 'правила', 'правил')}. Файл JSON можно импортировать обратно в MZ. Текст удобно читать и править: строки в квадратных скобках задают действие.`
      : 'Правил пока нет.'),
    h('textarea', { readonly: true, class: 'rules-input', value: humanText }),
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: () => dlg.close() }, 'Закрыть'),
      h('button', {
        class: 'btn', disabled: !data.json.rules.length, onclick: async () => {
          try { await navigator.clipboard.writeText(humanText); toast('Список скопирован'); } catch (_) { toast('Не удалось скопировать', true); }
        },
      }, 'Скопировать текстом'),
      h('button', {
        class: 'btn primary', disabled: !data.json.rules.length, autofocus: true,
        onclick: () => { download(`naivka-rules-${stamp}.json`, JSON.stringify(data.json, null, 2), 'application/json'); toast('Файл сохранён в «Загрузки»'); },
      }, 'Скачать файл')));
  dlg.classList.add('wide');
}

// ---------- настройки ----------
function buildAdminBox() {
  if (S.mode === 'tun' && !ST.admin && ST.os === 'windows') {
    return notice('warn', 'Для TUN нужны права администратора. Включите автозапуск — тогда MZ будет стартовать с правами без запроса.',
      h('button', { class: 'btn small', onclick: elevate }, 'Перезапустить с правами'));
  }
  return null;
}

function buildCoreBox() {
  const c = ST.core, d = ST.download;
  const out = [];
  out.push(h('p', null, c.present ? `Установлен sing-box ${c.version || '(версия неизвестна)'}` + (c.naive ? '.' : ' — сборка без Naive, работают серверы остальных протоколов.') : 'Ядро ещё не скачано.'));
  if (c.present && c.naive && !c.cronet) out.push(h('p', { class: 'err-text' }, 'Рядом с sing-box нет библиотеки libcronet — без неё Naive не заработает.'));
  if (d.active) out.push(h('p', { class: 'num' }, `Скачиваю… ${fmtBytes(d.done)}` + (d.total > 0 ? ` из ${fmtBytes(d.total)}` : '')));
  if (d.error) out.push(h('p', { class: 'err-text' }, 'Не удалось скачать: ' + d.error));
  const btn = h('button', {
    class: 'btn' + (c.present ? '' : ' primary'), disabled: d.active || ST.running || ST.starting,
    onclick: async () => { try { await api('/api/core/download', {}); refreshState(); } catch (e) { toast(e.message, true); } },
  }, c.present ? 'Обновить до последней версии' : 'Скачать ядро');
  out.push(h('div', { class: 'row' }, btn, ST.running && h('span', { class: 'hint' }, 'Отключитесь, чтобы обновить ядро.')));
  if (ST.protected) out.push(h('p', { class: 'hint mt' }, 'Режим администратора: ядро хранится в Program Files\\Naivka, где его не может подменить обычная программа.'));
  out.push(h('p', { class: 'hint mt' }, (ST.portable ? 'Портативный режим. Данные: ' : 'Данные: ') + ST.dataDir));
  return out;
}

function viewSettings() {
  const cfgOut = h('textarea', { readonly: true, style: 'min-height:320px', hidden: true });
  return h('div', { class: 'view', style: 'max-width:860px' },
    h('h1', null, 'Настройки'),

    h('h2', null, 'О программе'),
    h('div', { id: 'selfBox' }),
    h('div', { class: 'row mt' }, h('button', { class: 'btn', onclick: openFeedback }, 'Написать автору в Telegram'),
      h('span', { class: 'hint' }, 'Вопросы, ошибки и идеи — @mixazvv.')),

    h('h2', null, 'Внешний вид'),
    segmented([['dark', 'Тёмная'], ['light', 'Светлая'], ['system', 'Как в Windows'], ['gold', 'Никогда не проси об этом'], ['blocks', 'Пиксели']], () => S.theme, v => { S.theme = v; save(); applyTheme(); }, 'Тема'),
    h('p', { class: 'hint gold-only' }, 'Чёрное с золотом, тонкие заглавные, огни на фоне разгораются от трафика, заголовки «расшифровываются». Акцентный цвет и OLED в этой теме свои.'),
    h('p', { class: 'hint blocks-only' }, 'Тёмный интерфейс с пиксельными акцентами: рамки «ступеньками», крупные пиксельные цифры, кольцо из делений, экраны проступают мозаикой. Акцентный цвет и OLED в этой теме свои.'),
    h('div', { class: 'accent-row' }, h('span', { class: 'hint' }, 'Акцент'),
      h('div', { class: 'swatches', role: 'radiogroup', 'aria-label': 'Акцентный цвет' },
        ...ACCENTS.map(([id, name]) => h('button', {
          class: 'swatch', type: 'button', role: 'radio', 'data-accent': id, title: name, 'aria-label': name,
          'aria-checked': S.accent === id ? 'true' : 'false',
          onclick: e => {
            S.accent = id; save(); applyTheme();
            e.currentTarget.parentNode.querySelectorAll('.swatch').forEach(b => b.setAttribute('aria-checked', b.dataset.accent === id ? 'true' : 'false'));
          },
        }))),
      h('span', { class: 'hint' }, 'Им же на схемах и графиках показан трафик через сервер.')),
    h('div', { class: 'oled-check' }, check('oled', 'Чистый чёрный (OLED)', 'Тёмная тема на чёрном фоне: контрастнее и бережёт заряд на OLED-экранах.', applyTheme)),
    h('h2', null, 'Разделы'),
    check('ownServer', 'Свой сервер', 'Установка протоколов и панели управления на ваш VPS по SSH. Если пользуетесь готовыми серверами, выключите — раздел пропадёт из меню, а уже поставленные серверы останутся в списке.', applySections),

    window.NAIVKA_HOST ? h('h2', null, 'Окно') : null,
    window.NAIVKA_HOST ? check('trayMini', 'Мини-окно по щелчку в трее', 'Маленькое окно у часов: подключение, сервер, скорость. Если выключить, щелчок сразу открывает главное окно.') : null,
    window.NAIVKA_HOST ? check('nativeFrame', 'Классическое окно Windows', 'Обычная строка заголовка и фон без Mica (полупрозрачный фон есть в Windows 11). Применится после перезапуска MZ.') : null,

    h('h2', null, 'Перехват трафика'),
    choices('mode', [
      ['proxy', 'Системный прокси', 'Без прав администратора. Браузеры и большинство программ используют его сами.'],
      ['tun', 'Весь трафик (TUN)', 'Ловит трафик всех программ, включая те, что игнорируют прокси. Нужны права администратора.'],
    ], () => S.mode, v => { S.mode = v; save(); updateStatus(); }),
    h('div', { id: 'adminBox' }),

    h('h2', null, 'Надёжность'),
    check('killSwitch', 'Защита от утечек', 'Если ядро упадёт, трафик не пойдёт напрямую мимо сервера, пока связь не восстановится. Работает в режиме системного прокси; в TUN ядро просто быстро перезапускается.'),
    check('watchdog', 'Следить за соединением', 'Каждые 20 секунд проверять связь через сервер. После сна, смены сети или обрыва — переподключать соединения, а если не помогло — перезапускать ядро.'),
    check('sleepReconnect', 'Отключаться перед сном', 'Перед сном компьютера MZ отключается, а после пробуждения ждёт сеть и подключается снова — без зависших соединений и прокси, который указывает в никуда.'),
    check('notify', 'Уведомления Windows', 'О сбоях, восстановлении связи и смене сервера автовыбором.'),

    h('h2', null, 'Запуск'),
    check('autoStart', 'Запускать вместе с Windows', 'MZ стартует в трее без окна. В режиме TUN — с правами администратора и без запроса UAC (один раз Windows спросит разрешение при включении).'),
    check('autoConnect', 'Подключаться при запуске MZ'),
    check('exitWithWindow', 'Закрывать MZ вместе с окном', 'Иначе при закрытии окна MZ уходит в трей и соединение остаётся.'),
    check('showIp', 'Показывать мой IP и страну', 'MZ спрашивает ipwho.is и ipinfo.io по HTTPS (последний запасной — ip-api.com), как вас видят сайты.', () => { ip.key = ''; srvGeoKey = ''; }),

    h('h2', null, 'Раздача на другие устройства'),
    h('p', { class: 'hint' }, 'Телевизор, приставка или второй компьютер в той же сети смогут пользоваться подключением MZ как прокси.'),
    h('div', { id: 'shareBox' }),
    h('h2', null, 'Ядро sing-box'),
    h('div', { id: 'coreBox' }),
    check('updateCheck', 'Проверять обновления', 'Раз в 12 часов MZ смотрит новые версии ядра, zapret и tg-ws-proxy на GitHub и сообщает о них. Устанавливаются они только по вашей кнопке.'),

    h('h2', null, 'Резервная копия'),
    h('p', { class: 'hint' }, (ST && ST.secrets
      ? 'Пароли серверов в настройках зашифрованы средствами Windows и откроются только у вас на этом компьютере. '
      : '') + 'Копию можно зашифровать паролем — тогда без него из файла ничего не достать. Пароль не восстановить: забудете — копия бесполезна.'),
    h('div', { class: 'row' },
      h('button', { class: 'btn', onclick: backupDialog }, 'Сохранить копию'),
      h('button', { class: 'btn', onclick: restoreBackup }, 'Восстановить из копии')),

    h('div', { class: 'mt-lg' }),
    more('Сеть и DNS', `порт ${S.mixedPort}, DNS ${S.dnsRemote} и ${S.dnsDirect}`,
      h('div', { class: 'grid2' },
        field('mixedPort', 'Порт локального прокси', { type: 'number', min: 1, max: 65535, hint: 'HTTP и SOCKS5 на 127.0.0.1.' }),
        h('span'),
        field('dnsRemote', 'DNS через сервер (DoH)', { placeholder: '1.1.1.1', hint: 'Для зарубежных и заблокированных сайтов.' }),
        dnsDirectField()),
      check('ipv4Only', 'Только IPv4', 'Меньше проблем у провайдеров с кривым IPv6.'),
      check('ruleSetViaProxy', 'Скачивать списки блокировок через сервер', 'Если GitHub у провайдера открывается плохо.')),
    more('Проверка серверов', `каждые ${S.testInterval}`,
      h('div', { class: 'grid2' },
        field('testUrl', 'Адрес для проверки', { hint: 'Должен быстро отвечать и не быть заблокирован на сервере.' }),
        field('testInterval', 'Как часто проверять', { placeholder: '3m', hint: 'Например 90s, 3m, 10m.' }))),
    more('Для опытных', 'журнал, свои outbounds, правка конфига',
      h('div', { class: 'grid2' },
        h('label', { class: 'field' }, h('span', null, 'Подробность журнала'),
          h('select', { onchange: e => { S.logLevel = e.target.value; save(); } },
            ['error', 'warn', 'info', 'debug', 'trace'].map(l => h('option', { value: l, selected: S.logLevel === l }, l))))),
      h('h3', { class: 'mt' }, 'Дополнительные outbounds'),
      h('p', { class: 'hint' }, 'JSON-массив, добавляется в конец outbounds.'),
      jsonArea('extraOutbounds', '[\n  { "type": "socks", "tag": "work", "server": "10.0.0.5", "server_port": 1080 }\n]', true),
      h('h3', { class: 'mt' }, 'Ручные правки конфига'),
      h('p', { class: 'hint' }, 'JSON-объект накладывается на итоговый конфиг последним: объекты сливаются, массивы и значения заменяются, null удаляет ключ. В режиме администратора пути к файлам из правок убираются.'),
      jsonArea('override', '{\n  "route": { "final": "direct" }\n}', false),
      h('div', { class: 'row mt' },
        h('button', { class: 'btn', onclick: async () => {
          await saveNow();
          try { cfgOut.value = (await api('/api/config')).config; cfgOut.hidden = false; } catch (e) { toast(e.message, true); }
        } }, 'Показать итоговый конфиг'),
        h('button', { class: 'btn', onclick: async () => {
          if (cfgOut.hidden) return;
          try { await navigator.clipboard.writeText(cfgOut.value); toast('Конфиг скопирован'); } catch (_) { toast('Не удалось скопировать', true); }
        } }, 'Скопировать')),
      cfgOut),

    h('div', { class: 'exit-row' },
      h('span', { class: 'hint' }, 'Отключит соединение, вернёт настройки прокси Windows и закроет приложение.'),
      h('button', { class: 'btn danger', onclick: async () => {
        try { await saveNow(); await api('/api/quit', {}); } catch (_) { /* уже закрывается */ }
        setTimeout(() => window.close(), 300);
      } }, 'Выйти из MZ')));
}

function backupDialog() {
  const p1 = h('input', { type: 'password', placeholder: 'Пароль, от 8 символов', autocomplete: 'new-password' });
  const p2 = h('input', { type: 'password', placeholder: 'Ещё раз', autocomplete: 'new-password' });
  const err = h('div', { class: 'err-text' });
  const plain = h('input', { type: 'checkbox' });
  const pass = h('div', { class: 'grid2' }, p1, p2);
  plain.addEventListener('change', () => { pass.hidden = plain.checked; err.textContent = ''; });
  const dlg = openDialog(
    h('h2', null, 'Резервная копия'),
    h('p', { class: 'hint' }, 'В копии все серверы с паролями, правила и настройки. С паролем файл зашифрован (AES-256): открыть его можно только в MZ и только зная пароль.'),
    pass,
    h('label', { class: 'check mt' }, plain, h('span', null, h('b', null, 'Без пароля'), h('small', null, 'Пароли серверов будут в файле открытым текстом — любой, кто получит файл, сможет пользоваться вашими серверами.'))),
    err,
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'),
      h('button', { class: 'btn primary', onclick: async () => {
        err.textContent = '';
        if (!plain.checked && p1.value !== p2.value) { err.textContent = 'Пароли не совпадают'; return; }
        if (!plain.checked && p1.value.length < 8) { err.textContent = 'Пароль должен быть не короче 8 символов'; return; }
        await saveNow();
        try {
          const b = await api('/api/backup', { password: plain.checked ? '' : p1.value });
          const day = new Date().toISOString().slice(0, 10);
          download(`naivka-backup-${day}${plain.checked ? '' : '-encrypted'}.json`, JSON.stringify(b, null, 2), 'application/json');
          dlg.close();
          toast('Копия сохранена в «Загрузки»');
        } catch (e) { err.textContent = e.message; }
      } }, 'Сохранить')));
  p1.focus();
}

function askBackupPassword(wrong) {
  return new Promise(resolve => {
    const inp = h('input', { type: 'password', placeholder: 'Пароль копии', autocomplete: 'current-password' });
    let done = false;
    const finish = v => { if (!done) { done = true; resolve(v); dlg.close(); } };
    const dlg = openDialog(
      h('h2', null, 'Копия зашифрована'),
      wrong ? h('p', { class: 'err-text' }, 'Пароль не подошёл.') : h('p', { class: 'hint' }, 'Введите пароль, который задали при сохранении копии.'),
      inp,
      h('div', { class: 'row end mt' },
        h('button', { class: 'btn', onclick: () => finish(null) }, 'Отмена'),
        h('button', { class: 'btn primary', onclick: () => finish(inp.value) }, 'Открыть')));
    dlg.addEventListener('close', () => finish(null));
    inp.addEventListener('keydown', e => { if (e.key === 'Enter') finish(inp.value); });
    inp.focus();
  });
}

function restoreBackup() {
  const input = $('#fileInput');
  input.value = '';
  input.onchange = async () => {
    const f = input.files && input.files[0];
    if (!f) return;
    const text = await f.text();
    if (!confirm('Заменить текущие серверы, правила и настройки содержимым копии?')) return;
    let password = '';
    for (let attempt = 0; ; attempt++) {
      try {
        S = await api('/api/restore', { text, password });
        applyTheme();
        toast('Настройки восстановлены');
        go('settings');
        return;
      } catch (e) {
        if (e.message !== 'needPassword') { toast(e.message, true); return; }
        password = await askBackupPassword(attempt > 0);
        if (password === null) return;
      }
    }
  };
  input.click();
}

const ACCENTS = [['lime', 'Лайм'], ['coral', 'Коралл'], ['rose', 'Роза']];
// Обратная связь — в Telegram автора. Ссылку открывает Naivka в браузере (в своём окне её открывать нельзя).
async function openFeedback() {
  try { await api('/api/open', { link: 'feedback' }); toast('Открываю Telegram @mixazvv'); }
  catch (_) { window.open('https://t.me/mixazvv', '_blank', 'noopener'); }
}

const VIEWS = { overview: () => h('div'), own: viewOwn, servers: viewServers, rules: viewRules, bypass: viewBypass, diag: viewDiag, settings: viewSettings };

// ---------- старт ----------
async function boot() {
  setupChrome();
  if (MINI) { miniBoot(); return; }
  document.querySelectorAll('.nav-item, .brand').forEach(b => b.addEventListener('click', () => go(b.dataset.view === view ? 'overview' : b.dataset.view)));
  $('#drawerClose').addEventListener('click', () => go('overview'));
  $('#scrim').addEventListener('click', () => go('overview'));
  $('#feedbackBtn').addEventListener('click', openFeedback);
  try { S = await api('/api/settings'); }
  catch (e) { setOffline(true); setOffline(true); setOffline(true); return; }
  applyTheme();
  applySections();
  // Сервер могли сменить в мини-окне или меню трея — подтягиваем настройки, когда окно снова на экране.
  const resync = async () => {
    if (savePending || document.hidden) return;
    try { const fresh = await api('/api/settings'); if (!savePending) { S = fresh; applyTheme(); applySections(); updateStatus(); if (view === 'servers') renderServers(); } } catch (_) { /* не важно */ }
  };
  window.addEventListener('focus', resync);
  // Клавиши: 1–7 — разделы по порядку в меню, Ctrl+Enter — подключить или отключить.
  document.addEventListener('keydown', e => {
    if (e.defaultPrevented || document.querySelector('dialog[open]')) return;
    if (e.key === 'Escape' && view !== 'overview') { e.preventDefault(); go('overview'); return; }
    const t = e.target;
    const typing = t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName));
    if (e.ctrlKey && e.key === 'Enter' && !typing) { e.preventDefault(); toggleConnect(); return; }
    if (typing || e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
    const i = /^Digit[1-7]$/.test(e.code) ? +e.code.slice(5) - 1 : -1;
    const order = navOrder();
    if (i >= 0 && i < order.length) { e.preventDefault(); go(order[i]); }
  });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) { resync(); refreshState(); } });
  await refreshState();
  if (view === 'conns' || view === 'logs') { diagTab = view; view = 'diag'; }
  if (!VIEWS[view]) view = 'overview';
  main.replaceChildren(viewOverview());
  setTimeout(() => chunkReveal(main, 'var(--fog)'), 60);
  const first = view;
  view = 'overview';
  go(first);
  tick();
}


// ---------- обход без сервера ----------
let AD = null;

async function refreshAddons() {
  try { AD = await api('/api/addons' + (view === 'bypass' ? '?full=1' : '')); } catch (_) { return; }
  const u = AD.updates;
  const newer = x => x.installed && x.latest && x.installed !== x.latest;
  const badge = $('#navBadge');
  if (badge) badge.hidden = !(newer(u.zapret) || newer(u.telegram) || newer(u.core) || (AD.zapret.degraded));
  if (view === 'bypass') renderBypass();
}

function verLabel(st) {
  if (!st.installed) return h('span', { class: 'ver' }, 'не установлен');
  if (st.latest && st.latest !== st.installed) return h('span', { class: 'ver new' }, `${st.installed}, есть ${st.latest}`);
  return h('span', { class: 'ver' }, st.installed);
}

function busyLine(st) {
  if (st.busy !== 'download') return null;
  return h('p', { class: 'hint num' }, `Скачиваю… ${fmtBytes(st.done)}` + (st.total > 0 ? ` из ${fmtBytes(st.total)}` : ''));
}

function sw(checked, disabled, onchange, label) {
  return h('label', { class: 'switch' }, h('input', { type: 'checkbox', checked, disabled, onchange: e => onchange(e.target.checked) }), h('i'), label);
}

async function act(path, okText) {
  try { await api(path, {}); if (okText) toast(okText); } catch (e) { toast(e.message.split('\n')[0], true); }
  refreshAddons();
}

function viewBypass() {
  return h('div', { class: 'view', style: 'max-width:960px' },
    h('h1', null, 'Обход без сервера'),
    h('p', { class: 'sub' }, 'Программы Flowseal, которые обходят блокировки без вашего сервера и не тратят его трафик. MZ скачивает их из официальных релизов на GitHub, обновляет и запускает вместе с собой.'),
    h('div', { id: 'bypassBox' }, h('p', { class: 'hint' }, 'Загружаю…')));
}

function renderBypass() {
  if (!AD) return;
  const z = AD.zapret, t = AD.telegram;
  const sig = JSON.stringify([z, t, AD.updates, AD.conflict, ST && ST.admin, S.zapret, S.telegram]);
  patch('bypassBox', sig, () => [
    ST && ST.os === 'windows' && !ST.admin ? notice('warn', 'zapret работает через драйвер WinDivert и требует прав администратора. Чтобы MZ стартовала с ними без запроса, включите автозапуск в настройках.',
      h('button', { class: 'btn small', onclick: elevate }, 'Перезапустить с правами')) : null,
    AD.conflict ? notice('error', AD.conflict) : null,
    zapretCard(z),
    telegramCard(t),
    versionsCard(AD.updates),
  ]);
}

function zapretCard(z) {
  const zs = S.zapret;
  if (ST && ST.legacy) {
    return h('section', { class: 'card addon' }, h('div', { class: 'addon-head' }, h('div', null, h('h3', null, 'zapret'),
      h('p', null, 'В MZ zapret пока работает только на Windows 10 и 11: на Windows 7 ему нужен другой драйвер WinDivert. Заблокированные сайты на этом компьютере идут через ваш сервер Reality.'))));
  }
  const installed = !!z.installed;
  const status = !installed ? null
    : z.pick.active ? h('div', { class: 'addon-status' }, h('span', { class: 'k warn' }), 'Подбираю стратегию…')
    : z.trial.active ? h('div', { class: 'addon-status' }, h('span', { class: 'k warn' }), 'Идёт ручная проверка — выбранная стратегия не меняется, пока вы не нажмёте «Работает».')
    : z.running && z.degraded ? h('div', { class: 'addon-status' }, h('span', { class: 'k warn' }), 'Работает, но YouTube и Discord не открываются — подберите стратегию заново.' + (zs.fallback ? ' Пока они идут через сервер.' : ''))
    : z.running ? h('div', { class: 'addon-status' }, h('span', { class: 'k on' }), `Работает: «${z.strategy}», ${fmtDur(Date.now() / 1000 - z.since)}. В списках ${count(z.domains, 'домен', 'домена', 'доменов')}.`)
    : z.lastError ? h('div', { class: 'addon-status' }, h('span', { class: 'k err' }), z.lastError)
    : h('div', { class: 'addon-status' }, h('span', { class: 'k' }), 'Выключен.');
  const pick = z.pick;
  const trial = z.trial;
  const pickBox = pick.active ? h('div', { class: 'mt' },
      h('div', { class: 'row' }, h('span', null, `Проверяю «${pick.current}» — ${pick.index} из ${pick.count - 1}`), h('span', { class: 'spacer' }),
        h('button', { class: 'btn small', onclick: () => act('/api/zapret/pick/cancel') }, 'Остановить')),
      h('div', { class: 'pick-bar' }, h('span', { style: `width:${pick.index / Math.max(1, pick.count - 1) * 100}%` })),
      h('p', { class: 'hint' }, 'MZ запускает стратегии по очереди и пробует открыть YouTube, Discord, Google и Cloudflare напрямую. Пока идёт подбор, эти сайты могут подтормаживать.'))
    : null;
  const results = !pick.active && !trial.active && pick.results && pick.results.length ? pickTable(pick, zs) : null;
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' },
      h('div', null, h('h3', null, 'zapret', verLabel(z)),
        h('p', null, 'Обход DPI для YouTube, Discord и сайтов из ваших правил «Через zapret». Эти сайты идут напрямую — быстрее и мимо сервера.')),
      installed ? sw(zs.enabled, z.pick.active || (ST && ST.os === 'windows' && !ST.admin), v => { S.zapret.enabled = v; save(0); }, zs.enabled ? 'Включён' : 'Выключен') : null),
    status,
    !installed ? h('div', { class: 'row mt' },
      h('button', { class: 'btn primary', disabled: !!z.busy, onclick: () => act('/api/zapret/install') }, 'Установить zapret'),
      h('span', { class: 'hint' }, 'Около 3 МБ с github.com/Flowseal/zapret-discord-youtube. Антивирус может ругаться на WinDivert — это драйвер перехвата пакетов, он не вирус.')) : null,
    busyLine(z),
    z.error ? h('p', { class: 'err-text' }, z.error) : null,
    installed && trial.active ? trialPanel(z) : null,
    installed && !trial.active ? h('div', { class: 'strat-row' },
      h('span', { class: 'hint' }, 'Стратегия'),
      h('select', { disabled: pick.active, onchange: e => { S.zapret.strategy = e.target.value; save(0); } },
        z.strategies.map(n => h('option', { value: n, selected: n === zs.strategy }, n))),
      h('button', { class: 'btn', disabled: pick.active || (ST && ST.os === 'windows' && !ST.admin), onclick: () => act('/api/zapret/pick') }, 'Подобрать автоматически'),
      h('button', { class: 'btn', disabled: pick.active || (ST && ST.os === 'windows' && !ST.admin), onclick: () => startTrial(z) }, 'Проверить вручную'),
      z.latest && z.installed !== z.latest ? h('button', { class: 'btn primary', disabled: !!z.busy, onclick: () => act('/api/zapret/install') }, `Обновить до ${z.latest}`) : null) : null,
    pickBox,
    installed && !pick.active && !trial.active ? sitesBox(z) : null,
    results,
    installed ? more('Настройки zapret', [zs.gameFilter !== 'off' && 'режим игр', zs.ipset !== 'none' && 'ipset ' + zs.ipset, zs.routeDirect ? 'сайты напрямую' : 'сайты через сервер'].filter(Boolean).join(', '),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: zs.routeDirect, onchange: e => { S.zapret.routeDirect = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Сайты из списков zapret — напрямую'), h('small', null, 'YouTube, Discord и другие из списков Flowseal идут мимо сервера. Выключите, если хотите, чтобы zapret только помогал, а трафик шёл как раньше.'))),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: zs.fallback, onchange: e => { S.zapret.fallback = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Подстраховка сервером'), h('small', null, 'Если zapret перестал справляться, MZ временно пустит эти сайты через сервер и вернёт обратно, когда обход снова заработает.'))),
      h('div', { class: 'row mt' }, h('span', { class: 'hint', style: 'min-width:120px' }, 'Режим для игр'),
        segmented([['off', 'Выключен'], ['all', 'TCP и UDP'], ['tcp', 'TCP'], ['udp', 'UDP']], () => S.zapret.gameFilter, v => { S.zapret.gameFilter = v; save(0); }, 'Режим для игр')),
      h('p', { class: 'hint' }, 'Обход для игр и программ на портах выше 1023. Античиты иногда ругаются на WinDivert — включайте, только если нужно.'),
      h('div', { class: 'row mt' }, h('span', { class: 'hint', style: 'min-width:120px' }, 'Список IP'),
        segmented([['none', 'Не использовать'], ['loaded', 'Список Flowseal'], ['any', 'Любые IP']], () => S.zapret.ipset, v => { S.zapret.ipset = v; save(0); }, 'Список IP')),
      h('p', { class: 'hint' }, '«Любые IP» обрабатывает весь трафик и ломает многие сайты — только для проверки. Ваши серверы MZ всегда исключает из обработки.'),
      h('p', { class: 'hint mt' }, 'Папка: ' + z.path)) : null);
}

function telegramCard(t) {
  const ts = S.telegram;
  const installed = !!t.installed;
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' },
      h('div', null, h('h3', null, 'Telegram без сервера', verLabel(t)),
        h('p', null, 'tg-ws-proxy — локальный прокси для Telegram Desktop. Заворачивает трафик Telegram в WebSocket к его же серверам: работает даже без вашего сервера.')),
      installed ? sw(ts.enabled, false, v => { S.telegram.enabled = v; save(0); }, ts.enabled ? 'Включён' : 'Выключен') : null),
    installed ? h('div', { class: 'addon-status' }, h('span', { class: 'k ' + (t.running ? 'on' : t.lastError ? 'err' : '') }),
      t.running ? `Работает на 127.0.0.1:${t.port}` : t.lastError || 'Выключен.') : null,
    !installed ? h('div', { class: 'row mt' },
      h('button', { class: 'btn primary', disabled: !!t.busy, onclick: () => act('/api/tg/install') }, 'Установить tg-ws-proxy'),
      h('span', { class: 'hint' }, 'С github.com/Flowseal/tg-ws-proxy. Антивирусы иногда ошибочно ругаются на её упаковщик.')) : null,
    busyLine(t),
    t.error ? h('p', { class: 'err-text' }, t.error) : null,
    installed ? h('div', { class: 'row mt' },
      h('button', { class: 'btn primary', disabled: !t.running, onclick: () => act('/api/tg/open', 'Telegram должен предложить подключить прокси') }, 'Подключить Telegram'),
      h('button', { class: 'btn', onclick: async () => { try { await navigator.clipboard.writeText(t.link); toast('Ссылка скопирована'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Скопировать ссылку'),
      t.latest && t.installed !== t.latest ? h('button', { class: 'btn primary', disabled: !!t.busy, onclick: () => act('/api/tg/install') }, `Обновить до ${t.latest}`) : null) : null,
    installed ? h('p', { class: 'hint mt' }, 'Кнопка открывает Telegram Desktop с предложением добавить прокси — нажмите «Подключить». Если не сработало, отправьте ссылку себе в «Избранное» и нажмите на неё. У tg-ws-proxy свой значок в трее с окном настроек; закрывать его не нужно, MZ управляет им сам.') : null,
    installed ? more('Порт', String(ts.port),
      h('div', { class: 'grid2' }, h('label', { class: 'field' }, h('span', null, 'Порт прокси'),
        h('input', { type: 'number', value: ts.port, min: 1, max: 65535, onchange: e => { S.telegram.port = parseInt(e.target.value, 10) || 1443; save(0); } }))),
      h('p', { class: 'hint' }, 'После смены порта заново нажмите «Подключить Telegram».')) : null);
}

function versionsCard(u) {
  const row = (name, st) => h('tr', null, h('td', null, name), h('td', { class: 'num' }, st.installed || '—'),
    h('td', { class: 'num ' + (st.latest && st.installed && st.latest !== st.installed ? 'delay mid' : 'cell-sub') }, st.latest || '—'));
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' }, h('div', null, h('h3', null, 'Версии'),
      h('p', null, u.checked ? 'Проверено в ' + fmtTime(u.checked) + '. Ядро обновляется в «Настройках».' : 'Ещё не проверялось.')),
      h('button', { class: 'btn', onclick: async () => { toast('Проверяю…'); try { await api('/api/addons/check', {}); } catch (e) { toast(e.message, true); } refreshAddons(); } }, 'Проверить сейчас')),
    h('table', { class: 'table versions mt' },
      h('thead', null, h('tr', null, h('th', null, 'Компонент'), h('th', null, 'Установлено'), h('th', null, 'Последняя'))),
      h('tbody', null, row('Ядро sing-box', u.core), row('zapret', u.zapret), row('tg-ws-proxy', u.telegram), row('Xray (для xHTTP)', u.xray || {}))),
    u.xray && !u.xray.installed ? h('div', { class: 'row mt' }, h('button', { class: 'btn small', onclick: () => act('/api/xray/install') }, 'Установить Xray'),
      h('span', { class: 'hint' }, 'Нужен только для серверов на транспорте xHTTP.')) : null,
    u.xray && u.xray.installed && u.xray.latest && u.xray.latest !== u.xray.installed ? h('div', { class: 'row mt' }, h('button', { class: 'btn small', onclick: () => act('/api/xray/install') }, `Обновить Xray до ${u.xray.latest}`)) : null);
}


// ---------- подбор: результаты по сайтам ----------
const siteLabel = r => r.mine ? displayDomain(r.name)
  : r.name.replace(/YouTube/g, '\u0001').replace(/([a-z])([A-Z])/g, '$1 $2').replace(/\u0001/g, 'YouTube ').replace(/\s+/g, ' ').trim();

function pickTable(pick, zs) {
  const rows = pick.results.slice().sort((a, b) => b.ok - a.ok || a.ms - b.ms);
  const mineTotal = (rows[0] && rows[0].sites || []).filter(x => x.mine).length;
  return more('Результаты подбора', pick.best ? `лучшая — «${pick.best}»` : (pick.error || ''),
    h('p', { class: 'hint' }, 'По каждой стратегии видно, какие адреса не открылись. Выбирайте по тому, что важно вам, а не только по числу открывшихся.'),
    h('div', { style: 'overflow-x:auto' }, h('table', { class: 'table pick-table' },
      h('thead', null, h('tr', null, h('th', null, 'Стратегия'), h('th', null, 'Открылось'),
        mineTotal ? h('th', null, 'Ваши сайты') : null, h('th', null, 'Не открылись'), h('th', { class: 'r' }, 'Время'), h('th'))),
      h('tbody', null, rows.map(r => {
        const sites = r.sites || [];
        const mine = sites.filter(x => x.mine), mineOk = mine.filter(x => x.ok).length;
        const failed = sites.filter(x => !x.ok).map(siteLabel);
        return h('tr', { class: r.strategy === pick.best ? 'best' : '' },
          h('td', { class: 'cell-main' }, r.strategy),
          h('td', { class: 'score' }, h('div', { class: 'row', style: 'flex-wrap:nowrap' },
            h('div', { class: 'score-bar', style: 'flex:1' }, h('span', { style: `width:${r.total ? r.ok / r.total * 100 : 0}%` })),
            h('span', { class: 'num cell-sub' }, `${r.ok}/${r.total}`))),
          mineTotal ? h('td', { class: 'num ' + (mineOk === mine.length ? 'delay good' : mineOk ? 'delay mid' : 'delay bad') }, `${mineOk} из ${mine.length}`) : null,
          h('td', { class: 'cell-sub trunc', style: 'max-width:280px', title: failed.join(', ') }, failed.length ? failed.join(', ') : 'всё открылось'),
          h('td', { class: 'num cell-sub r' }, r.ms ? `${r.ms} мс` : '—'),
          h('td', { class: 'r' }, r.strategy !== 'без обхода' && r.strategy !== zs.strategy && r.ok
            ? h('button', { class: 'btn small', onclick: () => { S.zapret.strategy = r.strategy; save(0); } }, 'Выбрать') : null));
      })))));
}

// ---------- что проверять: стандартные адреса Flowseal + ваши сайты «Через zapret» ----------
function sitesBox(z) {
  const mine = S.rules.filter(r => r.action === 'zapret' && r.kind === 'domain');
  const input = h('input', { type: 'text', placeholder: 'например, rutracker.org', spellcheck: 'false', style: 'min-width:220px' });
  const add = async () => {
    const v = input.value.trim();
    if (!v) { input.focus(); return; }
    let parsed;
    try { parsed = await api('/api/rules/parse', { text: '[Через zapret]\n' + v, action: 'zapret' }); } catch (e) { toast(e.message, true); return; }
    const doms = parsed.rules.filter(r => r.kind === 'domain');
    if (!doms.length) { toast(parsed.errors[0] || 'Это не домен', true); return; }
    mergeRules(S.rules, doms.map(r => ({ ...r, action: 'zapret' })));
    save(0);
    if (await saveNow()) { toast('Сайт добавлен в правила «Через zapret»'); refreshAddons(); }
  };
  input.addEventListener('keydown', e => { if (e.key === 'Enter') add(); });
  const std = z.targets.length - mine.length;
  return more('Что проверять', `${std} стандартных адресов` + (mine.length ? ` и ${count(mine.length, 'ваш сайт', 'ваших сайта', 'ваших сайтов')}` : ''),
    h('p', { class: 'hint' }, 'Стандартные адреса — YouTube, Discord, Google и Cloudflare из списка Flowseal. Свои сайты добавляются правилом «Через zapret»: zapret обходит блокировку только для доменов из своих списков, поэтому проверять имеет смысл именно их.'),
    mine.length ? h('div', { class: 'row', style: 'margin:8px 0' }, mine.map(r => h('span', { class: 'route-tag zapret' }, displayDomain(r.value)))) : null,
    h('div', { class: 'row' }, input, h('button', { class: 'btn', onclick: add }, 'Добавить сайт')),
    h('p', { class: 'hint mt' }, 'Убрать сайт можно на странице «Правила».'));
}

// ---------- ручная проверка ----------
let trialOrder = [];
const trialMarks = {};

function trialSequence(z) {
  const res = (z.pick.results || []).filter(r => r.strategy !== 'без обхода');
  if (res.length) {
    const rank = Object.fromEntries(res.map(r => [r.strategy, r.ok * 100000 - (r.ms || 99999)]));
    return z.strategies.slice().sort((a, b) => (rank[b] ?? -1e9) - (rank[a] ?? -1e9));
  }
  return z.strategies.slice();
}

async function trialGo(name) {
  try { await api('/api/zapret/trial/start', { strategy: name }); } catch (e) { toast(e.message.split('\n')[0], true); }
  refreshAddons();
}

function startTrial(z) {
  trialOrder = trialSequence(z);
  Object.keys(trialMarks).forEach(k => delete trialMarks[k]);
  trialGo(trialOrder[0]);
}

function trialPanel(z) {
  const t = z.trial;
  if (!trialOrder.length) trialOrder = trialSequence(z);
  const i = Math.max(0, trialOrder.indexOf(t.strategy));
  const next = () => { trialMarks[t.strategy] = 'bad'; if (i + 1 < trialOrder.length) trialGo(trialOrder[i + 1]); else toast('Это была последняя стратегия'); };
  const prev = () => { if (i > 0) trialGo(trialOrder[i - 1]); };
  const fromPick = (z.pick.results || []).length > 1;
  const marks = Object.entries(trialMarks).filter(([k]) => k !== t.strategy);
  return h('div', { class: 'trial' },
    h('div', { class: 'row' },
      h('b', null, 'Ручная проверка'),
      h('span', { class: 'hint' }, `${i + 1} из ${trialOrder.length}` + (fromPick ? ', сначала лучшие по автоподбору' : '')),
      h('span', { class: 'spacer' }),
      h('select', { onchange: e => trialGo(e.target.value), 'aria-label': 'Перейти к стратегии' },
        trialOrder.map(n => h('option', { value: n, selected: n === t.strategy }, (trialMarks[n] === 'bad' ? '✗ ' : '') + n)))),
    h('div', { class: 'trial-name' }, t.strategy),
    t.error ? h('p', { class: 'err-text' }, 'Не запустилась: ' + t.error)
      : h('p', null, 'Стратегия уже работает. Откройте в браузере YouTube и включите видео, зайдите в Discord, откройте свои сайты — и решите. Пока идёт проверка, эти сайты идут напрямую, а не через сервер.'),
    t.probing ? h('p', { class: 'hint' }, 'Проверяю адреса…')
      : h('div', { class: 'chips' }, (t.sites || []).map(x => h('span', { class: 'chip ' + (x.ok ? 'ok' : 'bad') + (x.mine ? ' mine' : ''), title: x.url + (x.ok ? `, ${x.ms} мс` : ', не открылся') },
        (x.ok ? '✓ ' : '✗ ') + siteLabel(x)))),
    h('div', { class: 'row mt' },
      h('button', { class: 'btn primary', disabled: !!t.error, onclick: async () => {
        try { await api('/api/zapret/trial/accept', {}); S.zapret.strategy = t.strategy; S.zapret.enabled = true; toast(`Выбрана «${t.strategy}», zapret включён`); }
        catch (e) { toast(e.message, true); }
        trialOrder = []; refreshAddons();
      } }, 'Работает — оставить эту'),
      h('button', { class: 'btn', onclick: next, disabled: i + 1 >= trialOrder.length }, 'Не работает — следующая'),
      h('button', { class: 'btn', onclick: prev, disabled: i === 0 }, 'Назад'),
      h('span', { class: 'spacer' }),
      h('button', { class: 'btn small', disabled: t.probing || !!t.error, onclick: () => act('/api/zapret/trial/probe') }, 'Проверить адреса ещё раз'),
      h('button', { class: 'btn small', onclick: async () => { await act('/api/zapret/trial/cancel'); trialOrder = []; toast('Проверка закончена, всё как было'); } }, 'Закончить без выбора')),
    marks.length ? h('p', { class: 'hint mt' }, 'Не подошли: ' + marks.map(([k]) => k).join(', ')) : null);
}

// ---------- запасная подписка для белых списков ----------
function wlCard() {
  const w = WL || {};
  const on = S.wl.enabled;
  const upd = w.fetched ? 'обновлена ' + new Date(w.fetched * 1000).toLocaleString('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : 'ещё не загружена';
  const src = w.source ? w.source.replace(/^https:\/\/([^/]+).*$/, '$1') : '';
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' },
      h('div', null, h('h3', null, 'Запасная подписка для белых списков', h('span', { class: 'ver' }, 'zieng2/wl')),
        h('p', null, 'Общедоступные серверы для работы при белых списках на мобильном интернете. Обновляется каждый час. MZ переключается на неё сам, когда ваши серверы перестают отвечать, и возвращается обратно.')),
      sw(on, false, v => { S.wl.enabled = v; save(0); if (v) setTimeout(() => act('/api/wl/refresh'), 600); }, on ? 'Включена' : 'Выключена')),
    on ? h('div', { class: 'addon-status' }, h('span', { class: 'k ' + (w.error ? 'err' : w.active ? 'warn' : w.used ? 'on' : '') }),
      w.busy ? 'Загружаю подписку…' : w.error ? w.error :
        `${count(w.used || 0, 'сервер', 'сервера', 'серверов')} в работе из ${w.total || 0}, ${upd}` + (src ? ` (${src})` : '') + '.') : null,
    on && w.active ? notice('warn', 'Сейчас трафик идёт через подписку: ваши серверы не отвечали. MZ проверяет их каждые 2 минуты.') : null,
    on && w.ru && !S.wl.includeRu ? h('p', { class: 'hint mt' }, `Российские серверы (${w.ru}) не используются — через них заблокированное не открывается.`) : null,
    on ? h('div', { class: 'row mt' },
      h('button', { class: 'btn', disabled: !!w.busy, onclick: () => act('/api/wl/refresh', 'Подписка обновлена').then(refreshWL) }, 'Обновить сейчас'),
      h('button', { class: 'btn', disabled: S.selected === 'wl', onclick: async () => { S.selected = 'wl'; try { await api('/api/select', { id: 'wl' }); toast('Трафик через сервер теперь идёт через подписку'); } catch (e) { toast(e.message, true); } renderServers(); } }, 'Использовать сейчас')) : null,
    on ? xrayBlock(w) : null,
    on && w.list && w.list.length ? wlList(w) : null,
    on ? more('Настройки подписки', `${S.wl.max} серверов` + (S.wl.fallback ? ', запасной вариант' : '') + (S.wl.includeRu ? ', с российскими' : ''),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: S.wl.fallback, onchange: e => { S.wl.fallback = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Включать, когда свои серверы не отвечают'), h('small', null, 'Сторож соединения дважды подряд не достучался через ваш сервер — MZ переходит на подписку и возвращается, как только ваши серверы оживут.'))),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: S.wl.includeRu, onchange: e => { S.wl.includeRu = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Использовать и российские серверы'), h('small', null, 'Через них работает российский сегмент при белых списках, но заблокированное (Telegram, YouTube) не откроется. Автовыбор их любит из-за малой задержки.'))),
      h('div', { class: 'grid2' }, h('label', { class: 'field' }, h('span', null, 'Сколько серверов брать'),
        h('input', { type: 'number', min: 5, max: 100, value: S.wl.max, onchange: e => { S.wl.max = Math.max(5, Math.min(100, parseInt(e.target.value, 10) || 30)); save(0); } })))) : null,
    h('p', { class: 'hint mt' }, 'Это чужие серверы неизвестных владельцев. Содержимое HTTPS-сайтов они не видят, но видят, куда вы ходите. Поэтому подписка — запасной вариант, а не основной.'));
}

// ---------- история трафика ----------
let TRAFFIC = null;
const tsum = v => v ? v[0] + v[1] : 0;
const aroundOf = k => tsum(k.direct) + tsum(k.zapret) + tsum(k.telegram);
const fmtDay = d => { const [y, m, dd] = d.split('-'); return `${dd}.${m}`; };
const fmtDayLong = d => new Date(d + 'T12:00:00').toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' });

async function refreshTraffic() {
  try { TRAFFIC = await api('/api/traffic'); } catch (_) { return; }
  renderTraffic();
}

function viewTraffic() {
  return h('div', null,
    h('p', { class: 'sub' }, 'Сколько прошло через сервер и мимо него — по дням. Это счётчики MZ: хостинг и оператор насчитают немного больше (шифрование, служебные данные).'),
    h('div', { id: 'trafficBox', 'aria-label': 'Загружаю…' },
      h('div', { class: 'skel', style: 'width:180px;height:12px' }),
      h('div', { class: 'skel', style: 'width:240px;height:30px;margin-top:8px' }),
      h('div', { class: 'skel', style: 'width:100%;height:180px;margin-top:18px;border-radius:12px' })),
    h('h2', { class: 'mt-lg' }, 'Лимит'),
    h('p', { class: 'hint' }, 'Если у сервера или у мобильного тарифа есть лимит — MZ предупредит на 80% и 100% (уведомлением Windows). Считается только трафик через сервер.'),
    h('div', { class: 'grid2' },
      h('label', { class: 'field' }, h('span', null, 'Лимит в месяц, ГБ'),
        h('input', { type: 'number', min: 0, value: S.trafficLimitGB || '', placeholder: 'без лимита', onchange: e => { S.trafficLimitGB = Math.max(0, parseInt(e.target.value, 10) || 0); save(0); setTimeout(refreshTraffic, 500); } })),
      h('label', { class: 'field' }, h('span', null, 'Месяц начинается'),
        h('select', { onchange: e => { S.trafficResetDay = parseInt(e.target.value, 10); save(0); setTimeout(refreshTraffic, 500); } },
          Array.from({ length: 28 }, (_, i) => h('option', { value: i + 1, selected: (S.trafficResetDay || 1) === i + 1 }, `${i + 1}-го числа`))),
        h('small', { class: 'hint' }, 'Как у тарифа: например, в день оплаты сервера.'))));
}

function renderTraffic() {
  const box = $('#trafficBox');
  if (!box || !TRAFFIC) return;
  const t = TRAFFIC;
  const used = t.periodDown + t.periodUp;
  const limit = t.limitGB * 1024 ** 3;
  const pct = limit ? used / limit * 100 : 0;
  const head = h('div', { class: 'traffic-head' },
    h('div', null, h('div', { class: 'hint' }, `Через сервер с ${fmtDayLong(t.period)}`),
      h('div', { class: 'hero-num' }, tnum('tr-used', used, fmtBytes), h('small', null, `↓ ${fmtBytes(t.periodDown)} · ↑ ${fmtBytes(t.periodUp)}`))),
    limit ? h('div', { class: 'limit-meter' + (pct >= 100 ? ' over' : pct >= 80 ? ' warn' : '') },
      h('div', { class: 'hint' }, pct >= 100 ? 'Лимит превышен' : pct >= 80 ? 'Лимит почти исчерпан' : 'Лимит'),
      h('div', { class: 'score-bar', role: 'meter', 'aria-valuenow': Math.round(pct), 'aria-valuemin': 0, 'aria-valuemax': 100 },
        h('span', { style: `width:${Math.min(100, pct)}%` })),
      h('div', { class: 'num' }, `${fmtBytes(used)} из ${t.limitGB} ГБ — ${Math.round(pct)}%`)) : null);

  // столбики по дням: через сервер у основания, мимо сервера сверху
  const days = t.days;
  const W = 620, H = 180, top = 16, bottom = 156, slot = W / days.length, bw = Math.max(4, Math.min(14, slot - 6)), gap = 2;
  const totals = days.map(d => [tsum(d.keys.proxy), aroundOf(d.keys)]);
  let max = Math.max(1, ...totals.map(([a, b]) => a + b));
  const y = v => (bottom - top) * v / max;
  const tip = h('div', { class: 'chart-tip', hidden: true });
  const cols = [], hits = [];
  const topRound = (x, yTop, w, hgt) => {
    const r = Math.min(4, hgt, w / 2);
    return `M${x} ${yTop + hgt}V${yTop + r}Q${x} ${yTop} ${x + r} ${yTop}H${x + w - r}Q${x + w} ${yTop} ${x + w} ${yTop + r}V${yTop + hgt}Z`;
  };
  days.forEach((d, i) => {
    const [p, a] = totals[i];
    const x = i * slot + (slot - bw) / 2;
    const hp = y(p), ha = y(a);
    const g = s('g', { class: 'col' });
    // скруглён только верх всего столбика
    if (p > 0) g.append(s('path', { d: a > 0 ? `M${x} ${bottom - hp}h${bw}v${hp}h${-bw}Z` : topRound(x, bottom - hp, bw, hp), fill: 'var(--chart-tunnel)' }));
    if (a > 0) {
      const base = bottom - hp - (p > 0 ? gap : 0);
      g.append(s('path', { d: topRound(x, base - ha, bw, ha), fill: 'var(--chart-direct)' }));
    }
    cols.push(g);
    const hit = s('rect', { class: 'hit', x: i * slot, y: top, width: slot, height: bottom - top });
    hit.addEventListener('mouseenter', () => {
      cols.forEach((c, j) => c.classList.toggle('dim', j !== i));
      tip.replaceChildren(h('b', null, fmtDayLong(d.date)),
        h('div', null, h('i', { style: 'background:var(--chart-tunnel)' }), `Через сервер: ${fmtBytes(p)}`),
        h('div', null, h('i', { style: 'background:var(--chart-direct)' }), `Мимо сервера: ${fmtBytes(a)}`));
      const r = hit.getBoundingClientRect(), wr = box.querySelector('.bars-wrap').getBoundingClientRect();
      tip.style.left = `${r.left - wr.left + r.width / 2}px`;
      tip.style.top = `${Math.max(0, r.top - wr.top + (bottom - y(p + a) - top) * r.height / (bottom - top) - 6)}px`;
      tip.hidden = false;
    });
    hit.addEventListener('mouseleave', () => { cols.forEach(c => c.classList.remove('dim')); tip.hidden = true; });
    hits.push(hit);
  });
  const xLabels = days.map((d, i) => (i % 7 === days.length % 7 || i === days.length - 1) && i > 1
    ? s('text', { x: i === days.length - 1 ? W - 2 : i * slot + slot / 2, y: H - 6, 'text-anchor': i === days.length - 1 ? 'end' : 'middle' }, fmtDay(d.date)) : null);
  const chart = h('div', { class: 'bars-wrap' },
    s('svg', { class: 'bars', viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Трафик по дням за последний месяц' },
      s('line', { x1: 0, x2: W, y1: top, y2: top, class: 'grid' }),
      s('line', { x1: 0, x2: W, y1: bottom, y2: bottom, class: 'grid' }),
      s('text', { x: 4, y: top - 4 }, fmtBytes(max)),
      ...cols, ...hits, ...xLabels),
    tip);
  const k = t.periodKeys || {};
  const legend = h('div', { class: 'legend' },
    h('span', null, h('i', { style: 'background:var(--chart-tunnel)' }), 'Через сервер', h('b', null, fmtBytes(tsum(k.proxy)))),
    h('span', null, h('i', { style: 'background:var(--chart-direct)' }), 'Мимо сервера', h('b', null, fmtBytes(aroundOf(k)))),
    h('span', null, 'за месяц'));

  // таблица: за расчётный месяц по маршрутам и серверам
  const rows = [['Через сервер — всего', k.proxy]];
  for (const [key, v] of Object.entries(k).sort((a, b) => tsum(b[1]) - tsum(a[1]))) {
    let name = null;
    if (key.startsWith('s-')) { const sv = serverById(key.slice(2)); name = sv ? sv.name : 'удалённый сервер'; }
    else if (key === 'wl') name = 'подписка «белые списки»';
    else if (key === 'twitch-adfree') name = 'Twitch без рекламы';
    if (name) rows.push(['  ' + name, v]);
  }
  rows.push(['Напрямую', k.direct], ['Через zapret', k.zapret], ['Telegram (tg-ws-proxy)', k.telegram]);
  const table = h('div', { class: 'panel mt' }, h('table', { class: 'table' },
    h('thead', null, h('tr', null, h('th', null, 'За месяц'), h('th', { class: 'r' }, 'Загрузка'), h('th', { class: 'r' }, 'Отдача'), h('th', { class: 'r' }, 'Всего'))),
    h('tbody', null, rows.filter(([, v]) => v && tsum(v) > 0).map(([n, v]) => h('tr', null,
      h('td', { class: n.startsWith('  ') ? 'cell-sub' : '', style: n.startsWith('  ') ? 'padding-left:24px' : '' }, n.trim()),
      h('td', { class: 'r num' }, fmtBytes(v[0])), h('td', { class: 'r num' }, fmtBytes(v[1])), h('td', { class: 'r num' }, fmtBytes(tsum(v))))))));
  box.replaceChildren(head, h('div', { class: 'panel mt', style: 'padding:14px 14px 8px' }, chart, legend), table);
}

// ---------- тест скорости ----------
let SPEED = null;
async function refreshSpeed() {
  try { SPEED = await api('/api/speedtest'); } catch (_) { return; }
  renderSpeed();
}

function viewSpeed() {
  return h('div', null,
    h('p', { class: 'sub' }, 'Задержка и скорость загрузки напрямую и через каждый ваш сервер по очереди. Замер идёт отдельными служебными входами и не мешает текущему трафику. Цель — серверы Cloudflare (запасная — Hetzner).'),
    h('div', { class: 'row' },
      h('button', { class: 'btn primary', id: 'speedBtn', onclick: async () => {
        try { await api('/api/speedtest', {}); } catch (e) { toast(e.message, true); }
        refreshSpeed();
      } }, 'Запустить тест'),
      h('span', { class: 'hint', id: 'speedNote' }, ST && !ST.running ? 'Сейчас MZ отключена — будет замер только напрямую.' : '')),
    h('div', { class: 'panel mt' }, h('table', { class: 'table speed' },
      h('thead', null, h('tr', null, h('th', null, 'Маршрут'), h('th', { class: 'r' }, 'Задержка'), h('th', null, 'Скорость загрузки'), h('th', { class: 'r' }, 'К прямому'))),
      h('tbody', { id: 'speedBody' }))));
}

function renderSpeed() {
  const body = $('#speedBody');
  if (!body) return;
  const btn = $('#speedBtn');
  if (btn) { btn.disabled = !!(SPEED && SPEED.active); btn.textContent = SPEED && SPEED.active ? 'Идёт замер…' : 'Запустить тест'; }
  const rows = (SPEED && SPEED.rows) || [];
  if (!rows.length) { body.replaceChildren(h('tr', null, h('td', { class: 'empty', colspan: 4 }, 'Ещё не запускали.'))); return; }
  const max = Math.max(1, ...rows.map(r => r.mbps || 0));
  const direct = rows.find(r => r.kind === 'direct');
  body.replaceChildren(...rows.map(r => {
    const vs = r.kind !== 'direct' && direct && direct.mbps > 0 && r.mbps > 0 ? Math.round((r.mbps / direct.mbps - 1) * 100) : null;
    return h('tr', null,
      h('td', null, h('div', { class: 'cell-main' }, r.name), h('div', { class: 'cell-sub' }, r.kind === 'direct' ? 'без сервера' : r.kind === 'wl' ? 'подписка' : 'ваш сервер')),
      h('td', { class: 'r num' }, r.state === 'done' ? `${r.ping} мс` : ''),
      h('td', null, r.state === 'wait' ? h('span', { class: 'hint' }, 'в очереди')
        : r.state === 'run' ? h('span', { class: 'hint' }, 'меряю…')
        : r.state === 'error' ? h('span', { class: 'err-text' }, r.error)
        : h('div', { class: 'row', style: 'flex-wrap:nowrap' },
          h('div', { class: 'score-bar', style: 'flex:1;height:8px' }, h('span', { style: `width:${r.mbps / max * 100}%;background:${r.kind === 'direct' ? 'var(--direct)' : 'var(--tunnel)'}` })),
          h('b', { class: 'num', style: 'min-width:92px;text-align:right' }, `${r.mbps.toLocaleString('ru-RU', { maximumFractionDigits: 1 })} Мбит/с`))),
      h('td', { class: 'r num ' + (vs === null ? 'cell-sub' : vs >= -15 ? 'delay good' : vs >= -50 ? 'delay mid' : 'delay bad') },
        vs === null ? '—' : (vs > 0 ? '+' : '') + vs + '%'));
  }));
}

// ---------- выбор программ ----------
async function pickProcesses() {
  let list = [];
  const chosen = new Set();
  let action = 'proxy';
  const search = h('input', { type: 'text', class: 'grow-input', placeholder: 'Найти программу', spellcheck: 'false' });
  const box = h('div', { class: 'proc-list' }, h('p', { class: 'hint' }, 'Загружаю список…'));
  const addBtn = h('button', { class: 'btn primary', disabled: true }, 'Добавить');
  const existing = new Set(S.rules.filter(r => r.kind === 'process').map(r => r.value.toLowerCase()));
  const render = () => {
    const q = search.value.trim().toLowerCase();
    const items = list.filter(p => !q || p.name.toLowerCase().includes(q) || p.path.toLowerCase().includes(q));
    box.replaceChildren(...(items.length ? items.map(p => {
      const has = existing.has(p.name.toLowerCase());
      return h('label', { class: 'proc' + (has ? ' has' : '') },
        h('input', { type: 'checkbox', checked: chosen.has(p.name), disabled: has, onchange: e => { e.target.checked ? chosen.add(p.name) : chosen.delete(p.name); upd(); } }),
        h('span', { class: 'proc-main' }, h('b', null, p.name), h('small', null, p.path || '')),
        has ? h('span', { class: 'hint' }, 'уже в правилах') : p.net ? h('span', { class: 'iptag' }, 'в сети') : null);
    }) : [h('p', { class: 'hint' }, 'Ничего не найдено.')]));
  };
  const upd = () => { addBtn.disabled = !chosen.size; addBtn.textContent = chosen.size ? `Добавить ${count(chosen.size, 'программу', 'программы', 'программ')}` : 'Добавить'; };
  search.addEventListener('input', render);
  addBtn.addEventListener('click', async () => {
    const backup = S.rules.map(r => ({ ...r }));
    const res = mergeRules(S.rules, [...chosen].map(n => ({ kind: 'process', value: n, action })));
    save(0);
    if (await saveNow()) { dlg.close(); renderRules(); toast(`Добавлено: ${res.added}`); } else S.rules = backup;
  });
  const dlg = openDialog(
    h('h2', null, 'Выбрать программы'),
    h('p', { class: 'hint' }, 'Сверху — программы, которые выходили в сеть, пока работает MZ; ниже — остальные запущенные. Правило по программе работает в режиме TUN, а в режиме прокси — для программ, которые им пользуются.'),
    h('div', { class: 'row' }, search),
    box,
    h('div', { class: 'row mt' }, h('span', { class: 'hint' }, 'Действие'),
      segmented(Object.entries(ACTION).filter(([v]) => v !== 'zapret'), () => action, v => { action = v; }, 'Действие'),
      h('span', { class: 'spacer' }), h('button', { class: 'btn', onclick: () => dlg.close() }, 'Отмена'), addBtn));
  dlg.classList.add('wide');
  search.focus();
  try { list = await api('/api/processes'); } catch (e) { box.replaceChildren(h('p', { class: 'err-text' }, e.message)); return; }
  render();
}

// ---------- раздача на другие устройства ----------
async function shareBox() {
  const box = $('#shareBox');
  if (!box) return;
  let info;
  try { info = await api('/api/share'); } catch (_) { return; }
  const sh = S.share;
  box.replaceChildren(
    sw(sh.enabled, false, v => { S.share.enabled = v; save(0); setTimeout(shareBox, 500); }, sh.enabled ? 'Раздача включена' : 'Раздача выключена'),
    sh.enabled ? h('div', { class: 'card mt' },
      h('p', null, 'На телевизоре, приставке или другом компьютере в той же сети укажите прокси (HTTP или SOCKS5):'),
      info.ips.length ? h('div', { class: 'share-addr' }, info.ips.map(ip => h('code', null, `${ip}:${info.port}`)))
        : h('p', { class: 'err-text' }, 'Компьютер не подключён к домашней сети.'),
      sh.auth ? h('p', { class: 'mt' }, 'Логин ', h('code', null, info.username), ' пароль ', h('code', null, info.password)) : h('p', { class: 'hint mt' }, 'Без пароля — подключиться может любое устройство в этой сети.'),
      h('p', { class: 'hint' }, ST && ST.running ? 'Устройства получат то же, что этот компьютер: те же серверы и правила.' : 'Раздача работает, пока MZ подключена.'),
      h('div', { class: 'row mt' },
        h('button', { class: 'btn', onclick: () => act('/api/share/firewall', 'Брандмауэр Windows разрешил подключения из домашней сети') }, 'Разрешить в брандмауэре'),
        h('span', { class: 'hint' }, 'Один раз: иначе Windows не пустит другие устройства. Попросит права администратора.')),
      more('Настройки раздачи', `порт ${sh.port}` + (sh.auth ? ', с паролем' : ', без пароля'),
        h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: sh.auth, onchange: e => { S.share.auth = e.target.checked; save(0); setTimeout(shareBox, 500); } }),
          h('span', null, h('b', null, 'Требовать логин и пароль'), h('small', null, 'Многие телевизоры не умеют прокси с паролем — тогда выключите. Подключения не из домашней сети MZ отклоняет в любом случае.'))),
        h('div', { class: 'grid2' },
          h('label', { class: 'field' }, h('span', null, 'Порт'), h('input', { type: 'number', value: sh.port, onchange: e => { S.share.port = parseInt(e.target.value, 10) || 2090; save(0); setTimeout(shareBox, 500); } })),
          h('label', { class: 'field' }, h('span', null, 'Пароль'), h('input', { type: 'text', value: sh.password, spellcheck: 'false', onchange: e => { S.share.password = e.target.value.trim(); save(0); setTimeout(shareBox, 500); } }))))) : null);
}

function xrayBlock(w) {
  const x = (AD && AD.updates && AD.updates.xray) || w.xray || {};
  if (!w.xhttp) return null;
  if (x.installed) {
    return h('p', { class: 'hint mt' }, `Серверы xHTTP (${w.xhttp}) работают через Xray ${x.installed}` + (w.xray && w.xray.running ? `, сейчас через него ${count(w.xray.servers, 'сервер', 'сервера', 'серверов')}.` : '.'));
  }
  return h('div', { class: 'notice info' }, h('div', { class: 'grow' },
      h('b', null, `${w.xhttp} серверов подписки — на транспорте xHTTP`),
      h('p', { style: 'margin:4px 0 0' }, 'sing-box его не умеет. MZ может поставить рядом ядро Xray (около 10 МБ, официальный релиз XTLS) — только для этих серверов; правила, статистика и автовыбор остаются прежними.'),
      x.busy ? h('p', { class: 'hint num' }, `Скачиваю… ${fmtBytes(x.done)}` + (x.total > 0 ? ` из ${fmtBytes(x.total)}` : '')) : null,
      x.error ? h('p', { class: 'err-text' }, x.error) : null),
    h('button', { class: 'btn small primary', disabled: !!x.busy, onclick: async () => { await act('/api/xray/install'); toast('Xray ставится — после установки переподключитесь'); } }, 'Установить Xray'));
}

function wlList(w) {
  const delays = (PX && PX.delays) || {};
  const now = PX && PX.selected === 'wl' ? PX.wlNow : PX && PX.selected && PX.selected.startsWith('w-') ? PX.selected : '';
  const items = w.list.slice().sort((a, b) => (b.used - a.used) || ((delays[a.tag] || 1e9) - (delays[b.tag] || 1e9)));
  return more('Серверы подписки', `${w.used} в работе из ${w.total}` + (now && wlName(now) ? `, сейчас: ${splitFlag(wlName(now)).name}` : ''),
    h('div', { class: 'row', style: 'margin-bottom:6px' },
      h('button', { class: 'btn small', id: 'wlTestBtn', disabled: !(ST && ST.running), onclick: testWL }, 'Проверить задержки'),
      h('span', { class: 'hint' }, ST && ST.running ? 'Все серверы подписки проверяются разом, за несколько секунд. Сами по себе задержки обновляются раз в 5 минут.' : 'Подключитесь, чтобы проверить серверы.')),
    h('p', { class: 'hint' }, 'Сервер можно закрепить — тогда автовыбор подписки не будет его менять.'),
    h('div', { class: 'wl-list' }, h('table', { class: 'table' }, h('tbody', null, items.map(it => {
      const d = delays[it.tag];
      const cur = it.tag === now;
      return h('tr', { class: cur ? 'best' : '' },
        h('td', { class: 'cell-main trunc', style: 'max-width:300px', title: it.name }, (f => [flag(f.cc), f.name])(splitFlag(it.name))),
        h('td', null, h('span', { class: 'proto' + (it.transport === 'xhttp' ? '' : ' vless') }, protoName({ type: 'vless', security: it.security, transport: it.transport }))),
        h('td', { class: 'r num' }, !it.used ? h('span', { class: 'hint' }, it.why) : d === undefined ? h('span', { class: 'hint' }, '—')
          : d === 0 ? h('span', { class: 'delay bad' }, 'нет ответа') : h('span', { class: 'delay ' + (d < 300 ? 'good' : d < 800 ? 'mid' : 'bad') }, `${d} мс`)),
        h('td', { class: 'r' }, it.used ? (S.selected === it.tag
          ? h('button', { class: 'btn small', onclick: () => pickServer('wl') }, 'Открепить')
          : h('button', { class: 'btn small', onclick: () => pickServer(it.tag) }, 'Закрепить')) : null));
    })))));
}

async function pickServer(id) {
  S.selected = id;
  try { await api('/api/select', { id }); PXAt = 0; await refreshProxies(); toast(id === 'wl' ? 'Подписка снова выбирает сервер сама' : 'Сервер подписки закреплён'); }
  catch (e) { toast(e.message, true); }
  renderServers();
}

// ---------- обновление самой Naivka ----------
function selfBox() {
  const u = SELF;
  if (!u) return h('p', { class: 'hint' }, 'Загружаю…');
  if (!u.enabled) return h('p', { class: 'hint' }, `MZ ${u.version}. Это сборка для разработки: автообновление есть только в релизных сборках.`);
  const out = [h('p', null, `MZ ${u.version}. Обновления приходят из github.com/${u.repo}.`)];
  if (u.available) {
    out.push(h('div', { class: 'card' },
      h('b', null, `Доступна версия ${u.latest}`),
      u.notes ? h('pre', { class: 'changes' }, u.notes) : null,
      u.error ? h('p', { class: 'err-text' }, u.error) : null,
      h('div', { class: 'row mt' },
        h('button', { class: 'btn primary', disabled: !!u.busy, onclick: async () => {
          SELF.busy = 'download'; patch('selfBox', JSON.stringify(SELF), selfBox);
          try { await api('/api/self/apply', {}); elevating = true; updating = true; toast('Обновляю и перезапускаю…'); }
          catch (e) { toast(e.message, true); await refreshSelf(); patch('selfBox', JSON.stringify(SELF), selfBox); }
        } }, u.busy === 'download' ? 'Скачиваю…' : 'Обновить и перезапустить'),
        h('span', { class: 'hint' }, 'Настройки и серверы сохранятся. Подпись релиза проверяется перед установкой.'))));
  } else {
    out.push(h('div', { class: 'row' },
      h('button', { class: 'btn', disabled: !!u.busy, onclick: async () => {
        try { SELF = await api('/api/self/check', {}); toast(SELF.available ? `Вышла версия ${SELF.latest}` : 'У вас последняя версия'); }
        catch (e) { toast(e.message, true); }
        patch('selfBox', JSON.stringify(SELF), selfBox);
      } }, u.busy === 'check' ? 'Проверяю…' : 'Проверить обновления'),
      h('span', { class: 'hint' }, u.checked ? 'Проверено в ' + fmtTime(u.checked) : 'MZ проверяет сам раз в 12 часов.')));
    if (u.error) out.push(h('p', { class: 'err-text' }, u.error));
  }
  return out;
}

// ---------- флаги стран ----------
// На Windows эмодзи-флаги не рисуются (видны буквы «CH»), поэтому флаги рисуем сами, 30×20.
const FLAGS = {
  // горизонтальные полосы
  DE: ['h', '#000', '#DD0000', '#FFCE00'], NL: ['h', '#AE1C28', '#FFF', '#21468B'], RU: ['h', '#FFF', '#0039A6', '#D52B1E'],
  BG: ['h', '#FFF', '#00966E', '#D62612'], EE: ['h', '#0072CE', '#000', '#FFF'], LT: ['h', '#FDB913', '#006A44', '#C1272D'],
  AT: ['h', '#ED2939', '#FFF', '#ED2939'], HU: ['h', '#CE2939', '#FFF', '#477050'], LU: ['h', '#ED2939', '#FFF', '#00A1DE'],
  AM: ['h', '#D90012', '#0033A0', '#F2A800'], UA: ['h', '#0057B7', '#FFD700'], PL: ['h', '#FFF', '#DC143C'],
  SK: ['h', '#FFF', '#0B4EA2', '#EE1C25'], SI: ['h', '#FFF', '#005DA4', '#ED1C24'], RS: ['h', '#C6363C', '#0C4076', '#FFF'],
  HR: ['h', '#FF0000', '#FFF', '#171796'], ID: ['h', '#CE1126', '#FFF'], MC: ['h', '#CE1126', '#FFF'], YE: ['h', '#CE1126', '#FFF', '#000'],
  AZ: ['h', '#0092BC', '#E4002B', '#00AF66'], AR: ['h', '#74ACDF', '#FFF', '#74ACDF'], UZ: ['h', '#0099B5', '#FFF', '#1EB53A'],
  GA: ['h', '#009E60', '#FCD116', '#3A75C4'], SL: ['h', '#1EB53A', '#FFF', '#0072C6'], GH: ['h', '#CE1126', '#FCD116', '#006B3F'], CO: ['h', '#FCD116', '#FCD116', '#003893', '#CE1126'], TH: ['h', '#A51931', '#F4F5F8', '#2D2A4A', '#2D2A4A', '#F4F5F8', '#A51931'],
  // вертикальные полосы
  FR: ['v', '#002395', '#FFF', '#ED2939'], IT: ['v', '#009246', '#FFF', '#CE2B37'], BE: ['v', '#000', '#FDDA24', '#EF3340'],
  IE: ['v', '#169B62', '#FFF', '#FF883E'], RO: ['v', '#002B7F', '#FCD116', '#CE1126'], MD: ['v', '#0046AE', '#FFD200', '#CC092F'],
  NG: ['v', '#008751', '#FFF', '#008751'], MT: ['v', '#FFF', '#CF142B'], ML: ['v', '#14B53A', '#FCD116', '#CE1126'],
  GN: ['v', '#CE1126', '#FCD116', '#009460'], TD: ['v', '#002664', '#FECB00', '#C60C30'], CI: ['v', '#F77F00', '#FFF', '#009E60'], PE: ['v', '#D91023', '#FFF', '#D91023'], MX: ['v', '#006847', '#FFF', '#CE1126'],
  // скандинавский крест: фон, крест, (внутренний крест)
  SE: ['n', '#006AA7', '#FECC00'], FI: ['n', '#FFF', '#002F6C'], DK: ['n', '#C8102E', '#FFF'],
  NO: ['n', '#BA0C2F', '#FFF', '#00205B'], IS: ['n', '#02529C', '#FFF', '#DC1E35'],
};

function flagShapes(code) {
  const r = (x, y, w, h, f) => s('rect', { x, y, width: w, height: h, fill: f });
  const spec = FLAGS[code];
  if (spec) {
    const [kind, ...c] = spec;
    if (kind === 'h') return c.map((f, i) => r(0, i * 20 / c.length, 30, 20 / c.length + 0.1, f));
    if (kind === 'v') return c.map((f, i) => r(i * 30 / c.length, 0, 30 / c.length + 0.1, 20, f));
    if (kind === 'n') return [r(0, 0, 30, 20, c[0]), r(9, 0, 5, 20, c[1]), r(0, 7.5, 30, 5, c[1]),
      ...(c[2] ? [r(10.25, 0, 2.5, 20, c[2]), r(0, 8.75, 30, 2.5, c[2])] : [])];
  }
  switch (code) {
    case 'AL': return [r(0, 0, 30, 20, '#E41E20'), s('path', { d: 'M15 4 L19 10 L15 16 L11 10 Z', fill: '#000' }), r(9, 8.5, 12, 1.6, '#000')];
    case 'KG': return [r(0, 0, 30, 20, '#E8112D'), s('circle', { cx: 15, cy: 10, r: 4.5, fill: '#FFEF00' }), s('circle', { cx: 15, cy: 10, r: 2.4, fill: '#E8112D' })];
    case 'MK': return [r(0, 0, 30, 20, '#D20000'), s('path', { d: 'M0 0 L30 20 M30 0 L0 20 M15 0 L15 20 M0 10 L30 10', stroke: '#FFE600', 'stroke-width': 2 }), s('circle', { cx: 15, cy: 10, r: 3.5, fill: '#FFE600' })];
    case 'KR': return [r(0, 0, 30, 20, '#FFF'), s('path', { d: 'M10 10 A5 5 0 0 1 20 10 Z', fill: '#C60C30' }), s('path', { d: 'M10 10 A5 5 0 0 0 20 10 Z', fill: '#003478' })];
    case 'BR': return [r(0, 0, 30, 20, '#009C3B'), s('path', { d: 'M15 2.5 L27 10 L15 17.5 L3 10 Z', fill: '#FFDF00' }), s('circle', { cx: 15, cy: 10, r: 4, fill: '#002776' })];
    case 'CY': return [r(0, 0, 30, 20, '#FFF'), s('ellipse', { cx: 15, cy: 9, rx: 6, ry: 2.6, fill: '#D57800' })];
    case 'BA': return [r(0, 0, 30, 20, '#002395'), s('path', { d: 'M9 0 L21 0 L21 20 Z', fill: '#FECB00' })];
    case 'ZA': return [r(0, 0, 30, 10, '#E03C31'), r(0, 10, 30, 10, '#001489'), s('path', { d: 'M0 0 L14 10 L0 20 Z', fill: '#007749' }), r(14, 8, 16, 4, '#007749')];
    case 'CH': return [r(0, 0, 30, 20, '#DA291C'), r(13, 4, 4, 12, '#FFF'), r(9, 8, 12, 4, '#FFF')];
    case 'GE': return [r(0, 0, 30, 20, '#FFF'), r(13, 0, 4, 20, '#E8112D'), r(0, 8, 30, 4, '#E8112D')];
    case 'JP': return [r(0, 0, 30, 20, '#FFF'), s('circle', { cx: 15, cy: 10, r: 6, fill: '#BC002D' })];
    case 'HK': return [r(0, 0, 30, 20, '#DE2910'), s('circle', { cx: 15, cy: 10, r: 4.5, fill: '#FFF' })];
    case 'CN': return [r(0, 0, 30, 20, '#DE2910'), s('circle', { cx: 7, cy: 6, r: 3, fill: '#FFDE00' })];
    case 'VN': return [r(0, 0, 30, 20, '#DA251D'), s('circle', { cx: 15, cy: 10, r: 4.5, fill: '#FFFF00' })];
    case 'TR': return [r(0, 0, 30, 20, '#E30A17'), s('circle', { cx: 12, cy: 10, r: 5, fill: '#FFF' }), s('circle', { cx: 13.4, cy: 10, r: 4, fill: '#E30A17' }), s('circle', { cx: 18.5, cy: 10, r: 1.6, fill: '#FFF' })];
    case 'KZ': return [r(0, 0, 30, 20, '#00AFCA'), s('circle', { cx: 15, cy: 9, r: 4, fill: '#FEC50C' })];
    case 'BY': return [r(0, 0, 30, 13.3, '#C8313E'), r(0, 13.3, 30, 6.7, '#4AA657'), r(0, 0, 4, 20, '#FFF')];
    case 'LV': return [r(0, 0, 30, 20, '#9E3039'), r(0, 8, 30, 4, '#FFF')];
    case 'ES': return [r(0, 0, 30, 20, '#AA151B'), r(0, 5, 30, 10, '#F1BF00')];
    case 'PT': return [r(0, 0, 12, 20, '#006600'), r(12, 0, 18, 20, '#FF0000'), s('circle', { cx: 12, cy: 10, r: 3.5, fill: '#FFCC00' })];
    case 'CA': return [r(0, 0, 30, 20, '#FFF'), r(0, 0, 7.5, 20, '#D80621'), r(22.5, 0, 7.5, 20, '#D80621'), r(13, 6, 4, 8, '#D80621'), r(11, 8, 8, 3, '#D80621')];
    case 'IL': return [r(0, 0, 30, 20, '#FFF'), r(0, 2, 30, 3, '#0038B8'), r(0, 15, 30, 3, '#0038B8'), s('circle', { cx: 15, cy: 10, r: 3.2, fill: 'none', stroke: '#0038B8', 'stroke-width': 1.2 })];
    case 'SG': return [r(0, 0, 30, 10, '#EF3340'), r(0, 10, 30, 10, '#FFF'), s('circle', { cx: 7, cy: 5, r: 3, fill: '#FFF' }), s('circle', { cx: 8.3, cy: 5, r: 2.6, fill: '#EF3340' })];
    case 'AE': return [r(0, 0, 30, 6.7, '#00732F'), r(0, 6.7, 30, 6.7, '#FFF'), r(0, 13.3, 30, 6.7, '#000'), r(0, 0, 8, 20, '#FF0000')];
    case 'IN': return [r(0, 0, 30, 6.7, '#FF9933'), r(0, 6.7, 30, 6.7, '#FFF'), r(0, 13.3, 30, 6.7, '#138808'), s('circle', { cx: 15, cy: 10, r: 2.4, fill: 'none', stroke: '#000080', 'stroke-width': 0.9 })];
    case 'CZ': return [r(0, 0, 30, 10, '#FFF'), r(0, 10, 30, 10, '#D7141A'), s('path', { d: 'M0 0 L15 10 L0 20 Z', fill: '#11457E' })];
    case 'GR': return [...Array.from({ length: 9 }, (_, i) => r(0, i * 20 / 9, 30, 20 / 9 + 0.1, i % 2 ? '#FFF' : '#0D5EAF')),
      r(0, 0, 11.1, 11.1, '#0D5EAF'), r(4.45, 0, 2.2, 11.1, '#FFF'), r(0, 4.45, 11.1, 2.2, '#FFF')];
    case 'US': return [...Array.from({ length: 13 }, (_, i) => r(0, i * 20 / 13, 30, 20 / 13 + 0.1, i % 2 ? '#FFF' : '#B22234')),
      r(0, 0, 12, 10.8, '#3C3B6E')];
    case 'GB': case 'UK': return [r(0, 0, 30, 20, '#012169'),
      s('path', { d: 'M0 0 L30 20 M30 0 L0 20', stroke: '#FFF', 'stroke-width': 4 }),
      s('path', { d: 'M0 0 L30 20 M30 0 L0 20', stroke: '#C8102E', 'stroke-width': 1.4 }),
      r(12, 0, 6, 20, '#FFF'), r(0, 7, 30, 6, '#FFF'), r(13.3, 0, 3.4, 20, '#C8102E'), r(0, 8.3, 30, 3.4, '#C8102E')];
    case 'AU': case 'NZ': return [r(0, 0, 30, 20, '#012169'), r(0, 0, 15, 10, '#012169'),
      s('path', { d: 'M0 0 L15 10 M15 0 L0 10', stroke: '#FFF', 'stroke-width': 2 }), r(6, 0, 3, 10, '#FFF'), r(0, 3.5, 15, 3, '#FFF'),
      r(6.75, 0, 1.5, 10, '#C8102E'), r(0, 4.25, 15, 1.5, '#C8102E'), s('circle', { cx: 22, cy: 13, r: 1.5, fill: '#FFF' })];
  }
  return null;
}

// flag — флаг страны (HTML) или значок с буквами, если флаг не знаем.
function flag(code, big) {
  code = (code || '').toUpperCase();
  const shapes = code && flagShapes(code);
  if (!shapes) return code ? h('span', { class: 'cc', title: code }, code) : null;
  const el = s('svg', { viewBox: '0 0 30 20', class: 'flag' + (big ? ' big' : ''), role: 'img', 'aria-label': code }, ...shapes);
  const t = s('title', null, code);
  el.prepend(t);
  return el;
}

// splitFlag: «🇨🇭 Switzerland — #46» → { cc: 'CH', name: 'Switzerland — #46' }.
function splitFlag(name) {
  const cps = Array.from(name || '');
  const ri = c => { const p = c && c.codePointAt(0); return p >= 0x1F1E6 && p <= 0x1F1FF ? String.fromCharCode(65 + p - 0x1F1E6) : null; };
  const a = ri(cps[0]), b = ri(cps[1]);
  if (a && b) return { cc: a + b, name: cps.slice(2).join('').trim() };
  return { cc: '', name: name || '' };
}

// Флаг внутри схемы маршрутов (SVG в SVG).
function flagInSvg(code, x, y, w) {
  const shapes = code && flagShapes(code.toUpperCase());
  if (!shapes) return null;
  return s('svg', { x, y, width: w, height: w * 2 / 3, viewBox: '0 0 30 20', class: 'lane-flag' },
    ...shapes, s('rect', { x: 0.25, y: 0.25, width: 29.5, height: 19.5, fill: 'none', stroke: 'rgba(0,0,0,.35)', 'stroke-width': 0.5 }));
}

async function testWL() {
  const btn = $('#wlTestBtn');
  if (btn) { btn.disabled = true; btn.textContent = 'Проверяю…'; }
  try {
    const r = await api('/api/wl/test', {});
    PXAt = 0;
    await refreshProxies();
    toast(r.ok ? `Ответили ${count(r.ok, 'сервер', 'сервера', 'серверов')} подписки` : 'Ни один сервер подписки не ответил', !r.ok);
  } catch (e) { toast(e.message, true); }
  if (view === 'servers') renderServers();
}

// wlNowLine — строка под «Белыми списками»: какой сервер выбран сейчас, его флаг и задержка.
function wlNowLine() {
  const pinned = S.selected.startsWith('w-');
  const tag = pinned ? S.selected : PX && PX.wlNow;
  const parts = [WL ? `${WL.used} серверов` : 'загружаю…'];
  const out = h('div', { class: 'cell-sub trunc wl-now' });
  if (tag && wlName(tag)) {
    const f = splitFlag(wlName(tag));
    const ms = PX && PX.delays && PX.delays[tag];
    out.append(parts[0] + (pinned ? ', закреплён: ' : ', сейчас: '));
    const fl = flag(f.cc);
    if (fl) out.append(fl);
    out.append(f.name);
    if (ms) out.append(h('span', { class: 'delay ' + (ms < 300 ? 'good' : ms < 800 ? 'mid' : 'bad'), style: 'margin-left:8px' }, `${ms} мс`));
    else if (ms === 0) out.append(h('span', { class: 'delay bad', style: 'margin-left:8px' }, 'нет ответа'));
  } else out.append(parts[0]);
  if (WL && WL.active) out.append(' — включена вместо своих');
  return out;
}

boot();
