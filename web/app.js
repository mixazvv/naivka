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
  const u = LANG === 'en' ? ['B', 'KB', 'MB', 'GB', 'TB'] : ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  n = n || 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toLocaleString(LOCALE, { maximumFractionDigits: i > 0 && n < 10 ? 1 : 0 }) + ' ' + u[i];
}
const fmtSpeed = n => fmtBytes(n) + (LANG === 'en' ? '/s' : '/с');

function fmtDur(sec) {
  sec = Math.max(0, Math.floor(sec));
  const hh = Math.floor(sec / 3600), mm = Math.floor(sec % 3600 / 60);
  if (LANG === 'en') return hh ? `${hh} h ${mm} min` : mm ? `${mm} min` : `${sec} s`;
  if (hh) return `${hh} ч ${mm} мин`;
  if (mm) return `${mm} мин`;
  return `${sec} с`;
}
const fmtTime = t => new Date(t * 1000).toLocaleTimeString(LOCALE, { hour: '2-digit', minute: '2-digit' });

// Форма слова для числа. В английском две формы: для 1 и для остальных (слово переведёт i18n.js).
function plural(n, one, few, many) {
  if (LANG === 'en') return n === 1 ? one : many;
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
  if (bad && S && S.mascot === true) Pet.startle();
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.className = 'toast'; }, bad ? 5000 : 2200);
}

const serverById = id => S.servers.find(x => x.id === id);
let WL = null; // состояние подписки «белые списки»
let SELF = null; // номер версии Naivka
async function refreshSelf() { if (SELF) return; try { SELF = await api('/api/self'); } catch (_) { /* не важно */ } }
let APPUP = null; // обновление самой Naivka: {installed, latest, newer, busy, done, total, error}
async function refreshAppUpdate() {
  try { APPUP = await api('/api/app/update'); } catch (_) { return; }
  updateStatus();
  if (view === 'settings') patch('selfBox', JSON.stringify([SELF, APPUP]), selfBox);
}
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
  // Человек печатает в поле или выбирает в списке внутри блока — не выдёргиваем их из-под рук,
  // пересоберём на следующем обновлении.
  const f = document.activeElement;
  if (el.dataset.built && f && el.contains(f) && /^(INPUT|SELECT|TEXTAREA)$/.test(f.tagName) && f.type !== 'checkbox' && f.type !== 'radio') return;
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
let themeSent = '';
function applyTheme() {
  // Тема одна — тёмная; «Чёрное с золотом» и «Пиксели» — стили поверх неё со своей палитрой.
  const gold = !!S && S.theme === 'gold';
  const blocks = !!S && S.theme === 'blocks';
  const root = document.documentElement;
  root.dataset.theme = 'dark';
  root.dataset.nav = S && S.nav === 'wheel' ? 'wheel' : 'list';
  if (gold || blocks) root.dataset.skin = gold ? 'gold' : 'blocks'; else delete root.dataset.skin;
  applyAccent(S && S.accent);
  const custom = !!S && S.theme === 'custom';
  applyCustom(custom ? S.custom : null);
  if (S && S.oled && !root.dataset.skin && !custom) root.dataset.oled = ''; else delete root.dataset.oled;
  // Сплошной фон: в OLED, тематических стилях и своей теме полупрозрачный Mica не нужен.
  if (root.dataset.skin || root.dataset.oled !== undefined || custom) root.dataset.solid = ''; else delete root.dataset.solid;
  if (root.dataset.skin === 'blocks' && root.dataset.prevSkin !== 'blocks' && $('#main') && $('#main').firstChild) setTimeout(() => chunkReveal($('#main'), 'var(--fog)'), 30);
  root.dataset.prevSkin = root.dataset.skin || '';
  Wall.apply(S, TOKEN);
  if (!themeSent) { themeSent = 'dark'; api('/api/window/theme', { dark: true }).catch(() => { themeSent = ''; }); }
}

// ---------- акцентный цвет в HEX ----------
// Цвет считается в OKLab: так светлота и различимость близки к тому, как их видит глаз.
const ACCENT_DEFAULT = '#B6E35A';
// Готовые цвета проверены accentNotes: ни один не путается с «напрямую», ошибкой и ожиданием.
const ACCENT_PRESETS = [['#B6E35A', 'Лайм'], ['#4FD08A', 'Изумруд'], ['#F2E05A', 'Лимон'], ['#FF9A6E', 'Коралл'], ['#F58FC0', 'Роза']];
const ACCENT_VARS = ['--tunnel', '--tunnel-soft', '--chart-tunnel', '--on-accent', '--logo', '--logo-ink'];

function parseHex(v) {
  let x = String(v || '').trim().replace(/^#/, '');
  if (/^[0-9a-f]{3}$/i.test(x)) x = x.split('').map(c => c + c).join('');
  return /^[0-9a-f]{6}$/i.test(x) ? '#' + x.toUpperCase() : '';
}
const hexRgb = hex => [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255);
const toLin = c => c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
const fromLin = c => c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055;
function linOklab([r, g, b]) {
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const q = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * q, 1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * q, 0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * q];
}
function oklabLin([L, a, b]) {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3, m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3, q = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3;
  return [4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * q, -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * q, -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * q];
}
const hexLab = hex => linOklab(hexRgb(hex).map(toLin));
// Цвет по светлоте L, насыщенности C и оттенку h; насыщенность убавляется, пока цвет не влезет в sRGB.
function lchHex(L, C, hue) {
  for (let c = C; c >= 0; c -= 0.005) {
    const lin = oklabLin([L, c * Math.cos(hue), c * Math.sin(hue)]);
    if (lin.every(v => v >= -1e-4 && v <= 1 + 1e-4) || c < 0.005) {
      return '#' + lin.map(v => Math.round(Math.min(1, Math.max(0, fromLin(Math.min(1, Math.max(0, v))))) * 255).toString(16).padStart(2, '0')).join('').toUpperCase();
    }
  }
}
const lum = hex => { const [r, g, b] = hexRgb(hex).map(toLin); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
const contrast = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
// Протанопия и дейтеранопия (Machado и др., 2009): различимость для тех, кто плохо видит красный или зелёный.
const CVD = [[[0.152286, 1.052583, -0.204868], [0.114503, 0.786281, 0.099216], [-0.003882, -0.048116, 1.051998]],
  [[0.367322, 0.860646, -0.227968], [0.280085, 0.672501, 0.047413], [-0.011820, 0.042940, 0.968881]]];
function deltaE(h1, h2, mat) {
  const lab = hex => { let lin = hexRgb(hex).map(toLin); if (mat) lin = mat.map(row => Math.max(0, Math.min(1, row[0] * lin[0] + row[1] * lin[1] + row[2] * lin[2]))); return linOklab(lin); };
  const a = lab(h1), b = lab(h2);
  return 100 * Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);
}

// accentVars — все цвета, которые выводятся из акцента.
function accentVars(hex) {
  const [L, a, b] = hexLab(hex), C = Math.hypot(a, b), hue = Math.atan2(b, a);
  const [r, g, bl] = hexRgb(hex).map(v => Math.round(v * 255));
  // Графики: светлота в полосе 0.52–0.64 и насыщенность не меньше 0.1 — так линия видна на тёмном фоне
  // и не сливается с соседними сериями.
  const chart = lchHex(Math.min(0.64, Math.max(0.52, L)), C < 0.03 ? C : Math.max(C, 0.1), hue);
  const dark = lchHex(0.2, Math.min(C, 0.05), hue);
  const ink = contrast(hex, dark) >= contrast(hex, '#FFFFFF') ? dark : '#FFFFFF';
  return { '--tunnel': hex, '--tunnel-soft': `rgba(${r}, ${g}, ${bl}, .14)`, '--chart-tunnel': chart, '--on-accent': ink, '--logo': hex, '--logo-ink': ink };
}

function applyAccent(v) {
  const root = document.documentElement;
  const hex = parseHex(v) || ACCENT_DEFAULT;
  if (root.dataset.skin || hex === ACCENT_DEFAULT) ACCENT_VARS.forEach(k => root.style.removeProperty(k));
  else for (const [k, val] of Object.entries(accentVars(hex))) root.style.setProperty(k, val);
  syncLogo();
}

// ---------- тема «Своя» ----------
// Человек задаёт пять вещей: фон, панели, текст, «напрямую» и шрифт («через сервер» — это акцент).
// Остальные оттенки (рамки, приглушённый текст, подложки) выводятся из них в OKLab — так они
// остаются согласованными при любом выборе.
const CUSTOM_DEFAULT = { bg: '#101215', panel: '#191C20', ink: '#ECEEF0', direct: '#5BB4EE', font: '' };
const CUSTOM_VARS = ['--bg', '--nav', '--layer', '--surface', '--raised', '--line', '--ink', '--on-ink', '--muted', '--faint', '--direct', '--direct-soft', '--chart-direct', '--font', '--display', 'color-scheme'];
const CUSTOM_FONTS = {
  '': ['Как в Naivka', null, null],
  segoe: ['Segoe UI', '"Segoe UI Variable Text", "Segoe UI", system-ui, sans-serif', '"Segoe UI Variable Display", "Segoe UI", system-ui, sans-serif'],
  bahn: ['Bahnschrift', 'Bahnschrift, "Segoe UI", sans-serif', 'Bahnschrift, "Segoe UI", sans-serif'],
  mono: ['Моноширинный', '"Cascadia Code", Consolas, "Courier New", monospace', '"Cascadia Code", Consolas, "Courier New", monospace'],
  serif: ['С засечками', 'Georgia, "Times New Roman", serif', 'Georgia, "Times New Roman", serif'],
};
// Заготовки — чтобы не начинать с пустого места.
const CUSTOM_PRESETS = [
  ['Графит', { bg: '#101215', panel: '#191C20', ink: '#ECEEF0', direct: '#5BB4EE', font: '' }, '#B6E35A'],
  ['Океан', { bg: '#0A1420', panel: '#11202F', ink: '#E4EEF7', direct: '#4FD1C5', font: 'segoe' }, '#7AA2FF'],
  ['Сирень', { bg: '#15111C', panel: '#1F1929', ink: '#EEE8F5', direct: '#62B6F0', font: 'bahn' }, '#C79BFF'],
  ['Терминал', { bg: '#050805', panel: '#0C120C', ink: '#B8F5B0', direct: '#5BC0EB', font: 'mono' }, '#39FF6A'],
  ['Бумага', { bg: '#F3EFE6', panel: '#FFFDF8', ink: '#24211C', direct: '#1F6FB2', font: 'serif' }, '#C2410C'],
];
function labHex(lab) {
  return '#' + oklabLin(lab).map(v => Math.round(Math.min(1, Math.max(0, fromLin(Math.min(1, Math.max(0, v))))) * 255).toString(16).padStart(2, '0')).join('').toUpperCase();
}
function mixHex(a, b, t) { const x = hexLab(a), y = hexLab(b); return labHex(x.map((v, i) => v + (y[i] - v) * t)); }
function shiftL(hex, d) { const [L, a, b] = hexLab(hex); return labHex([Math.min(1, Math.max(0, L + d)), a, b]); }
function customVars(c) {
  const bg = parseHex(c.bg) || CUSTOM_DEFAULT.bg, panel = parseHex(c.panel) || CUSTOM_DEFAULT.panel;
  const ink = parseHex(c.ink) || CUSTOM_DEFAULT.ink, direct = parseHex(c.direct) || CUSTOM_DEFAULT.direct;
  const light = lum(bg) > 0.4;
  const [r, g, b] = hexRgb(direct).map(v => Math.round(v * 255));
  const font = CUSTOM_FONTS[c.font] || CUSTOM_FONTS[''];
  const v = {
    '--bg': bg, '--nav': shiftL(bg, light ? -0.02 : -0.012), '--layer': bg, '--on-ink': bg,
    '--surface': panel, '--raised': shiftL(panel, light ? -0.035 : 0.035), '--line': mixHex(panel, ink, light ? 0.16 : 0.1),
    '--ink': ink, '--muted': mixHex(ink, bg, 0.4), '--faint': mixHex(ink, bg, 0.62),
    '--direct': direct, '--direct-soft': `rgba(${r}, ${g}, ${b}, .15)`, '--chart-direct': direct, 'color-scheme': light ? 'light' : 'dark',
  };
  if (font[1]) { v['--font'] = font[1]; v['--display'] = font[2]; }
  return v;
}
function applyCustom(c) {
  const root = document.documentElement;
  CUSTOM_VARS.forEach(k => root.style.removeProperty(k));
  if (!c) { delete root.dataset.custom; return; }
  root.dataset.custom = lum(parseHex(c.bg) || CUSTOM_DEFAULT.bg) > 0.4 ? 'light' : 'dark';
  for (const [k, val] of Object.entries(customVars(c))) root.style.setProperty(k, val);
}
// customNotes — чем выбор может мешать читать: мало контраста, «напрямую» сливается с фоном или акцентом.
function customNotes(c, accent) {
  const out = [];
  if (contrast(c.ink, c.bg) < 4.5) out.push('Текст плохо читается на фоне — сделайте его светлее или фон темнее.');
  else if (contrast(c.ink, c.panel) < 4.5) out.push('Текст плохо читается на панелях.');
  if (contrast(c.direct, c.bg) < 3) out.push('Цвет «напрямую» почти сливается с фоном.');
  const acc = parseHex(accent) || ACCENT_DEFAULT;
  if (contrast(acc, c.bg) < 3) out.push('Акцент («через сервер») почти сливается с фоном.');
  if (deltaE(acc, c.direct) < 15) out.push('«Через сервер» и «напрямую» похожи — на схеме их будет трудно различить.');
  else if (Math.min(...CVD.map(m => deltaE(acc, c.direct, m))) < 8) out.push('Людям с нарушением цветового зрения «через сервер» и «напрямую» будет трудно различить.');
  return out;
}
// Код темы: её можно отправить другу строкой и вставить у себя.
const THEME_CODE = 'NAIVKA-THEME:';
function themeCode() {
  const c = S.custom, json = JSON.stringify({ v: 1, bg: c.bg, panel: c.panel, ink: c.ink, direct: c.direct, font: c.font || '', accent: S.accent });
  return THEME_CODE + btoa(String.fromCharCode(...new TextEncoder().encode(json)));
}
function parseThemeCode(text) {
  const t = String(text || '').trim();
  if (!t.startsWith(THEME_CODE)) throw new Error('Это не код темы Naivka');
  let o;
  try { o = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(t.slice(THEME_CODE.length)), ch => ch.charCodeAt(0)))); } catch (_) { throw new Error('Код темы повреждён'); }
  const c = {};
  for (const k of ['bg', 'panel', 'ink', 'direct']) { c[k] = parseHex(o[k]); if (!c[k]) throw new Error('В коде темы неверный цвет'); }
  c.font = Object.hasOwn(CUSTOM_FONTS, o.font) ? o.font : '';
  return { custom: c, accent: parseHex(o.accent) };
}

// Значок в трее и у окна — того же цвета, что логотип на странице (с учётом стиля и своего акцента).
// Пока цвет подбирается в палитре, Naivka получает его не чаще раза в 0,3 с.
let logoSent = '', logoTimer = 0;
function syncLogo() {
  if (!S) return;
  clearTimeout(logoTimer);
  logoTimer = setTimeout(() => {
    const cs = getComputedStyle(document.documentElement);
    const bg = parseHex(cs.getPropertyValue('--logo')), ink = parseHex(cs.getPropertyValue('--logo-ink'));
    if (!bg || !ink || bg + ink === logoSent) return;
    logoSent = bg + ink;
    api('/api/window/logo', { bg, ink }).catch(() => { logoSent = ''; });
  }, 300);
}

// accentNotes — чем свой цвет может мешать: плохо виден, путается с «напрямую», ошибкой или ожиданием.
function accentNotes(hex) {
  const out = [], chart = accentVars(hex)['--chart-tunnel'];
  if (contrast(hex, '#16181B') < 3) out.push('Слишком тёмный: на тёмном фоне его будет плохо видно.');
  const cvd = Math.min(...CVD.map(m => deltaE(chart, '#3A8FCF', m)));
  if (deltaE(chart, '#3A8FCF') < 15) out.push('Похож на голубой цвет трафика «напрямую» — на схеме и графиках их будет трудно различить.');
  else if (cvd < 8) out.push('Людям с нарушением цветового зрения его будет трудно отличить от голубого «напрямую».');
  if (deltaE(hex, '#FF6B6B') < 8) out.push('Похож на красный цвет ошибок — сбой будет хуже заметен.');
  if (deltaE(hex, '#F5B44A') < 8) out.push('Похож на жёлтый цвет ожидания и zapret.');
  return out;
}

// ---------- API ----------
// Ошибки окна — в журнал Naivka (раздел «Журнал» и отчёт): иначе сбой интерфейса ничем не виден.
let uiLogN = 0;
function uiLog(msg) {
  if (++uiLogN > 30) return;
  fetch('/api/uilog', { method: 'POST', headers: { 'X-Naivka-Token': TOKEN, 'Content-Type': 'application/json' }, body: JSON.stringify({ msg: String(msg) }) }).catch(() => {});
}
// guard: сбой в перерисовке пишется в журнал, но не ломает кнопки и переходы по разделам.
const guardSeen = new Set();
function guard(fn, name) {
  try { return fn(); } catch (e) {
    const msg = `${name}: ${e && (e.stack || e.message) || e}`;
    if (!guardSeen.has(msg)) { guardSeen.add(msg); uiLog(msg); console.error(e); }
  }
}
window.addEventListener('error', e => uiLog(`${e.message} (${(e.filename || '').split('/').pop()}:${e.lineno}:${e.colno})`));
window.addEventListener('unhandledrejection', e => uiLog('promise: ' + (e.reason && (e.reason.stack || e.reason.message) || e.reason)));

async function api(path, body) {
  const opt = { method: body === undefined ? 'GET' : 'POST', headers: { 'X-Naivka-Token': TOKEN } };
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  // Ответ, который так и не пришёл, не должен навсегда оставить кнопку в «Подключение…»:
  // самые долгие действия (подключение с проверкой конфига) укладываются в пару минут.
  const ctl = new AbortController(), timer = setTimeout(() => ctl.abort(), 5 * 60 * 1000);
  opt.signal = ctl.signal;
  let res;
  try {
    res = await fetch(path, opt);
  } catch (e) {
    clearTimeout(timer);
    if (ctl.signal.aborted) { uiLog(`нет ответа за 5 минут: ${path}`); throw new Error('Naivka слишком долго не отвечает. Подробности — в журнале'); }
    setOffline(true);
    throw new Error('Naivka не отвечает');
  }
  clearTimeout(timer);
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
    $('#offlineText').textContent = 'Naivka перезапускается с правами администратора и откроет новое окно. Это окно можно закрыть.';
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
    while (box.childElementCount > 3000) box.firstElementChild.remove(); // окно журнала не растёт без конца
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
    if (view === 'diag' && diagTab === 'speed') await refreshSpeed();
    if ((APPUP && APPUP.busy) || tickN % 600 === 5) await refreshAppUpdate();
  } finally {
    setTimeout(tick, 1000);
  }
}

// ---------- состояние ----------
function phase() {
  if (!ST) return 'off';
  if (busy || ST.starting) return 'wait';
  // «Только zapret»: включено — значит работает zapret, ядра нет.
  if (zapretOnly()) return ST.zapretOn ? 'on' : 'off';
  if (ST.blocked) return 'blocked';
  if (ST.running) return 'on';
  if (ST.reviving) return 'revive';
  if (ST.error) return 'err';
  return 'off';
}

const zapretOnly = () => !!(S && S.zapretOnly);
// Начало сеанса: в «Только zapret» ядро не работает — считаем от запуска zapret.
const sessionStart = () => (zapretOnly() ? ST.zapretSince : ST.startedAt) || Date.now() / 1000;
const TITLES = { on: 'Подключено', off: 'Отключено', wait: 'Подключение…', err: 'Не удалось подключиться', blocked: 'Трафик заблокирован', revive: 'Восстанавливаю соединение…' };
const ROUTING = { 'ru-direct': 'Россия напрямую', 'global': 'Всё через сервер' };

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

// Котёнок спрыгивает из окна на панель задач Windows и возвращается (ui_pet_windows.go). Где начинается
// полоса котёнка над панелью, сообщает Naivka: window.naivkaPetZone = {top} в пикселях страницы
// (null — полосы нет: окно не на том мониторе, выключено в настройках). Не чаще раза в 3 минуты.
let petExitAt = Date.now();
const PET_EXIT = {
  ok: () => !!(window.NAIVKA_HOST && S && S.petHopOff !== true && window.naivkaPetZone && Date.now() - petExitAt > 180e3),
  cross: (x, y, w, h) => !!window.naivkaPetZone && y + h * 0.5 > window.naivkaPetZone.top,
  drop: (cx, cy) => !!(window.NAIVKA_HOST && S && S.petHopOff !== true && window.naivkaPetZone) && cy > innerHeight,
  go: (x, y, vx, vy) => { petExitAt = Date.now(); window.chrome.webview.postMessage(JSON.stringify({ cmd: 'petout', x, y, vx, vy })); },
};
// Кот вернулся с панели задач: в точку (x, y) со скоростью (vx, vy); 'jump' — выпрыгивает снизу окна
// (был на другом мониторе); null — сразу домой (окно свернули, перейти некуда).
window.naivkaPetBack = (x, y, vx, vy) => {
  if (x === 'jump') Pet.appear(innerWidth * (0.3 + Math.random() * 0.4), innerHeight + 10, 0, -1300);
  else if (x == null) Pet.recall();
  else Pet.appear(x, y, vx, vy);
};

function updateStatus() { guard(updateStatusNow, 'updateStatus'); }
function updateStatusNow() {
  if (!ST || !S) return;
  const p = phase();
  document.body.dataset.state = p;
  document.body.classList.toggle('zapret-only', zapretOnly());
  const nt = $('#navTitle'); if (nt) nt.textContent = TITLES[p];
  const ab = $('#achvBtn'); if (ab) ab.hidden = S.achievements === false;
  const cs = currentServer();
  const ns = $('#navSub'); if (ns) ns.textContent = p === 'on' ? (cs.auto ? `${cs.name}, авто` : cs.name) : (ST.mode === 'tun' || S.mode === 'tun' ? 'режим TUN' : '');
  navCounts();
  const sp0 = lastSpeeds();
  const kb = (p === 'on' ? sp0.pd + sp0.pu : 0) / 1024;
  // Котёнок переселён на панель задач (своё окно Windows) — в меню его нет.
  if (S.mascot === true && !(S.petTaskbar === true && window.NAIVKA_HOST)) { Pet.mount(document.querySelector('.side-foot'), { beta: S.mascotBeta === true, say: petSay, onPurr: () => unlockAchv('pet'), spot: S.petSpot, onMove: spot => { S.petSpot = spot || null; save(); }, exit: window.NAIVKA_HOST ? PET_EXIT : null }); Pet.update(p, kb * 1024, STATS && STATS.health); }
  else Pet.unmount();
  checkAchv();
  document.body.style.setProperty('--energy', Math.min(1, Math.log10(kb + 1) / 3.5).toFixed(2));
  const tbs = $('#tbStatus');
  if (tbs) tbs.textContent = TITLES[p] + (p === 'on' ? ' · ' + cs.name : '');

  if ($('#statusTitle')) {
    $('#statusTitle').textContent = zapretOnly() && p === 'on' ? 'zapret работает' : TITLES[p];
    let sub = zapretOnly() ? 'Режим «Только zapret»: нажмите на кнопку, чтобы включить zapret.' : 'Трафик идёт как обычно, без сервера.';
    if (p === 'on' && zapretOnly()) sub = 'Без сервера: YouTube, Discord и другие сайты из списков zapret. Остальное — как обычно.';
    else if (p === 'on') sub = (ST.mode === 'tun' ? 'Весь трафик компьютера' : `Системный прокси 127.0.0.1:${ST.mixedPort}`) + `, ${fmtDur(Date.now() / 1000 - sessionStart())}`;
    else if (p === 'wait') sub = 'Запускаю sing-box и проверяю конфиг.';
    else if (p === 'err') sub = 'Подробности ниже и в журнале.';
    else if (p === 'revive') sub = 'Связь пропала. Naivka подключится снова сама, как только получится.';
    else if (p === 'blocked') sub = 'Ядро упало, защита от утечек не выпускает трафик напрямую. Naivka пробует восстановить связь.';
    $('#statusSub').textContent = sub;
    const btn = $('#connectBtn');
    btn.disabled = p === 'wait';
    btn.setAttribute('aria-label', powerLabel(p));
    btn.title = powerLabel(p) + ' (Ctrl+Enter)';
    guard(renderTiles, 'renderTiles');
    guard(renderReady, 'renderReady');
    const n = [ST.error, ST.needsRestart, S.mode, ST.admin, ST.core.present, ST.core.naive, S.servers.length, ST.running, ST.blocked, JSON.stringify(ST.portIssue || null), S.dnsDirect, S.routing, sessionStorage.getItem('naivka-dns-ok'), JSON.stringify(ST.conflicts || []), JSON.stringify(ST.failover || null), JSON.stringify(APPUP)].join('|');
    patch('notices', n, buildNotices);
    guard(renderLanes, 'renderLanes');
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

// DNS для сайтов «напрямую» не зашифрован — значок у строки состояния (не плашка на весь экран).
const dnsNag = () => S.servers.length && !zapretOnly() && S.routing !== 'global' && !dnsEncrypted(S.dnsDirect) && !sessionStorage.getItem('naivka-dns-ok');
function dnsDialog() {
  const dlg = openDialog(h('h3', null, 'DNS не зашифрован'),
    h('p', { class: 'hint mt' }, 'Для сайтов «напрямую» адреса ищутся обычным DNS: провайдер видит, какие сайты вы открываете (сами страницы — нет). Зашифрованный DNS это скрывает.'),
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: () => { sessionStorage.setItem('naivka-dns-ok', '1'); dlg.close(); updateStatus(); } }, 'Не сейчас'),
      h('button', { class: 'btn primary', onclick: () => {
        S.dnsDirect = DNS_DIRECT[0][0]; save(0); dlg.close(); updateStatus();
        toast('Включён зашифрованный DNS Яндекса — переподключитесь, чтобы применить');
      } }, 'Зашифровать')));
}

function notice(kind, text, action) {
  return h('div', { class: 'notice ' + kind }, h('div', { class: 'grow' }, text), action);
}

function buildNotices() {
  const out = [];
  const up = appUpdateNotice();
  if (up) out.push(up);
  if (!S.servers.length && !zapretOnly()) out.push(notice('info', 'Сначала добавьте сервер.', h('button', { class: 'btn small', onclick: () => go('servers') }, 'Добавить сервер')));
  if (!ST.core.present) out.push(notice('warn', 'Ядро sing-box ещё не скачано.', h('button', { class: 'btn small', onclick: () => go('settings') }, 'Перейти к загрузке')));
  if (ST.core.present && !ST.core.naive && S.servers.some(x => x.type === 'naive')) {
    out.push(notice('warn', 'Эта сборка ядра не поддерживает Naive (так бывает на Windows 7) — будут работать серверы остальных протоколов (Reality, Hysteria2 и другие).'));
  }
  if (S.mode === 'tun' && !ST.admin && ST.os === 'windows') out.push(notice('warn', 'Для режима TUN нужны права администратора.', h('button', { class: 'btn small', onclick: elevate }, 'Перезапустить с правами')));
  if (ST.failover) out.push(notice('warn', `Сервер «${ST.failover.from}» не отвечает — временно работаю через «${ST.failover.to}». Вернусь, когда он оживёт.`));
  for (const c of ST.conflicts || []) out.push(notice('warn', h('span', null, h('b', null, `Запущен ${c.name}. `), c.hint)));
  if (ST.needsRestart) out.push(S.autoApply
    ? notice('info', 'Настройки изменены — Naivka сейчас переподключится сама.')
    : notice('info', 'Настройки изменены. Они применятся после переподключения.', h('button', { class: 'btn small', onclick: reconnect }, 'Переподключить')));
  const pi = !ST.running && ST.portIssue;
  if (pi) {
    const who = pi.kind === 'naivka' ? 'другая копия Naivka' : pi.name ? `программа ${pi.name}` : 'другая программа';
    out.push(h('div', { class: 'notice error' }, h('div', { class: 'grow' },
      h('b', null, `Порт ${pi.port} занят`),
      h('p', { style: 'margin:4px 0 0' }, `Его держит ${who}` + (pi.pid ? ` (PID ${pi.pid})` : '') + '. ' + (pi.kind === 'naivka'
        ? 'Закройте её через значок в трее (он может прятаться под стрелкой у часов) или возьмите свободный порт.'
        : 'Закройте эту программу или пусть Naivka возьмёт свободный порт — браузеры и программы, которые используют системный прокси, подхватят его сами.')),
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
  const pw = $('#connectBtn');
  if (pw) { pw.classList.remove('ripple'); void pw.offsetWidth; pw.classList.add('ripple'); }
  if (S && S.mascot === true) Pet.press();
  // «Только zapret» без прав администратора: zapret включается после перезапуска с правами.
  if (zapretOnly() && !(ST && ST.zapretOn) && needElevation()) {
    S.zapret.enabled = true; save(0); await saveNow();
    await elevateFor('zapret');
    return;
  }
  busy = true;
  try {
    updateStatus();
    if (ST && (ST.running || ST.blocked || ST.reviving || (zapretOnly() && ST.zapretOn))) await api('/api/disconnect', {});
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

// needElevation — zapret на Windows без прав администратора не запустится.
function needElevation() {
  return !!ST && ST.os === 'windows' && !ST.admin;
}

// elevateFor включает функцию, которой нужны права: настройка сохраняется, Naivka перезапускается
// с правами (Windows спросит разрешение) и сама подключается снова, если была подключена.
async function elevateFor(what) {
  toast(what === 'zapret' ? 'Windows спросит разрешение: zapret нужны права администратора' : 'Windows спросит разрешение');
  await elevate();
  // Отказ в окне Windows: функция остаётся включённой и заработает при следующем запуске с правами.
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
  railMark();
  if (!sectionOn(view)) go('overview');
}

function railMark() {
  const rail = $('#rail');
  if (!rail) return;
  const order = navOrder(), at = Math.max(0, order.indexOf(view));
  rail.style.setProperty('--nav-i', at); // список: подсвеченный отрезок линии до текущего раздела
  rail.querySelectorAll('.nav-item').forEach(b => {
    const i = order.indexOf(b.dataset.view);
    if (i < 0) return;
    b.classList.toggle('passed', i < at);
    const o = i - at; // колесо: положение лепестка относительно текущего
    b.style.setProperty('--o', o);
    b.style.setProperty('--d', Math.abs(o));
    if (Math.abs(o) > 2) b.dataset.far = ''; else delete b.dataset.far;
    b.tabIndex = 0;
  });
}

// Колёсико мыши над меню листает разделы, как барабан.
let railWheelAt = 0;
function railWheel(e) {
  if (document.documentElement.dataset.nav !== 'wheel') return;
  e.preventDefault();
  if (Math.abs(e.deltaY) < 4 || performance.now() - railWheelAt < 380) return;
  railWheelAt = performance.now();
  const order = navOrder(), at = Math.max(0, order.indexOf(view));
  const next = order[Math.min(order.length - 1, Math.max(0, at + (e.deltaY > 0 ? 1 : -1)))];
  if (next !== view) go(next);
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
    drawerBody.onscroll = null;
    drawerBody.replaceChildren(guard(VIEWS[v], 'view ' + v) || h('div', { class: 'view' }, h('h1', null, 'Не удалось открыть раздел'),
      h('p', { class: 'err-text' }, 'Ошибка записана в журнал: «Сводка» → «Журнал» → «Собрать отчёт» — пришлите его автору.')));
    drawerBody.scrollTop = 0;
    drawer.hidden = false;
    $('#scrim').hidden = false;
    requestAnimationFrame(() => { drawer.classList.add('open'); setTimeout(() => chunkReveal(drawer, 'var(--bg)'), 30); });
    main.setAttribute('inert', '');
    decodeTitle(drawerBody.querySelector('h1'));
    if (was === 'overview') setTimeout(() => $('#drawerClose').focus({ preventScroll: true }), 50);
  }
  updateStatus();
  guard(() => { renderOverviewLive(); renderIP(); refreshIP(false); refreshServerGeo(false); refreshStats(); refreshView(v); }, 'goNow ' + v);
}

// refreshView — заполнить только что построенный раздел данными.
function refreshView(v = view) {
  if (v === 'servers') { renderServers(); refreshProxies(); }
  if (v === 'own') renderOwn();
  if (v === 'rules') { renderRules(); renderLists(); renderSmart(); }
  if (v === 'bypass') refreshAddons();
  if (v === 'settings') { buildToc(); shareBox(); renderCustomEditor(); refreshSelf().then(() => patch('selfBox', JSON.stringify([SELF, APPUP]), selfBox)); refreshAddons(); }
  if (v === 'servers') Promise.all([refreshAddons(), S.wl.enabled ? refreshWL() : null]).then(renderServers);
}

// Оглавление «Настроек»: кнопки к заголовкам разделов, текущий подсвечивается при прокрутке.
function buildToc() {
  const view = drawerBody.querySelector('.view');
  const toc = view && view.querySelector('.toc');
  if (!toc) return;
  const heads = [...view.querySelectorAll(':scope > h2')];
  const btns = heads.map(hd => h('button', { type: 'button', onclick: () => {
    drawerBody.scrollTo({ top: hd.offsetTop - toc.offsetHeight - 8, behavior: REDUCE.matches ? 'auto' : 'smooth' });
  } }, hd.textContent));
  toc.replaceChildren(...btns);
  const mark = () => {
    const y = drawerBody.scrollTop + toc.offsetHeight + 24;
    let cur = 0;
    heads.forEach((hd, i) => { if (hd.offsetTop <= y) cur = i; });
    btns.forEach((b, i) => b.setAttribute('aria-current', String(i === cur)));
    const c = btns[cur];
    if (c && (c.offsetLeft < toc.scrollLeft || c.offsetLeft + c.offsetWidth > toc.scrollLeft + toc.clientWidth)) toc.scrollTo({ left: c.offsetLeft - 34 });
    toc.classList.toggle('stuck', drawerBody.scrollTop > toc.offsetTop - 4);
  };
  drawerBody.onscroll = mark;
  mark();
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

// Подтверждение перед удалением — его можно выключить в настройках («Спрашивать перед удалением»).
function ask(msg) { return S.confirm === false || confirm(msg); }

function check(key, title, desc, after) {
  return h('label', { class: 'check' },
    h('input', { type: 'checkbox', checked: S[key], onchange: e => { S[key] = e.target.checked; save(); if (after) after(); } }),
    h('span', null, h('b', null, title), desc && longText(desc)));
}

// Длинное пояснение — две строки и «подробнее»: раздел читается, подробности под рукой.
function longText(desc) {
  if (typeof desc !== 'string' || desc.length <= 150) return h('small', null, desc);
  const sm = h('small', { class: 'clamp' }, desc);
  const btn = h('button', { class: 'more-link', type: 'button', onclick: e => {
    e.preventDefault(); e.stopPropagation();
    const open = sm.classList.toggle('open');
    btn.textContent = open ? 'свернуть' : 'подробнее';
  } }, 'подробнее');
  // Широкое окно — текст и так влезает в две строки: тогда «подробнее» не нужно.
  requestAnimationFrame(() => { if (sm.isConnected && sm.scrollHeight <= sm.clientHeight + 1) btn.hidden = true; });
  return h('span', { class: 'long' }, sm, btn);
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


// Раскрытые блоки «ещё» запоминаются по заголовку: окно часто пересобирает разделы
// (обновились данные, изменилась настройка), и без этого блок закрывался сам.
const MORE_OPEN = new Set();
function more(title, note, ...content) {
  const d = h('details', { class: 'more', open: MORE_OPEN.has(title) },
    h('summary', null, h('span', null, title), note && h('small', null, note)),
    h('div', { class: 'more-body' }, ...content));
  d.addEventListener('toggle', () => { if (d.open) MORE_OPEN.add(title); else MORE_OPEN.delete(title); });
  return d;
}

function openDialog(...content) {
  const dlg = h('dialog', null, ...content);
  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  return dlg;
}

// ---------- обзор ----------
// Переключение режима: «Сервер + zapret» ↔ «Только zapret». Включённое отключаем, чтобы не осталось
// ядра без кнопки (или zapret, который пользователь не включал).
async function setZapretOnly(v) {
  const want = v === 'zap';
  if (want === zapretOnly()) return;
  const p = phase();
  if (p === 'on' || p === 'wait' || p === 'revive' || p === 'blocked') {
    busy = true; updateStatus();
    try { await api('/api/disconnect', {}); } catch (_) { /* не важно */ }
    busy = false;
  }
  S.zapretOnly = want; save(0); await saveNow();
  document.body.classList.toggle('zapret-only', want);
  await refreshState(); await refreshAddons(); updateStatus();
  toast(want ? 'Режим «Только zapret»: кнопка включает zapret без сервера' : 'Режим «Сервер + zapret»');
}

function viewOverview() {
  return h('div', { class: 'stage-inner' },
    h('div', { class: 'stage-head' },
      h('div', { class: 'stage-status' },
        h('div', { class: 'status-title', id: 'statusTitle' }, '…'),
        h('div', { class: 'sub', id: 'statusSub' }),
        // Подключено — крупная живая скорость под заголовком (подробности — в «Сводке»).
        h('button', { class: 'live-speed', id: 'liveSpeed', type: 'button', title: 'График скорости и активные сайты', onclick: () => openDiag('now') })),
      h('div', { class: 'sysline', id: 'sysLine' })),
    h('div', { id: 'notices', class: 'stage-notices' }),
    h('div', { class: 'lanes-stage', id: 'lanesCard' },
      h('div', { class: 'lanes-box', id: 'lanesBox' }, h('div', { id: 'lanesSvg' }), powerButton('connectBtn')),
      h('div', { class: 'lanes-legend', id: 'lanesLegend' }),
      h('div', { class: 'proc-shelf', id: 'procShelf' })),
    // Главный выбор — режим; как перехватывать и куда вести трафик — в меню «Путь» (меняют редко).
    h('div', { class: 'stage-controls' },
      segmented([['srv', 'Сервер + zapret'], ['zap', 'Только zapret']], () => zapretOnly() ? 'zap' : 'srv', setZapretOnly, 'Режим'),
      pathMenu(),
      h('button', { class: 'btn link stage-help', type: 'button', onclick: () => openDiag('check') }, 'Сайт не открывается?')),
    h('div', { class: 'stage-tiles' },
      h('button', { class: 'tile', type: 'button', id: 'tileSpeed', onclick: () => openDiag('now'), title: 'График скорости и активные сайты' }),
      h('button', { class: 'tile', type: 'button', id: 'tileIp', onclick: () => openDiag('now'), title: 'Как вас видят сайты и карта' }),
      h('button', { class: 'tile', type: 'button', id: 'tileSession', onclick: () => openDiag('traffic'), title: 'История трафика' })),
    // Вместо плиток с нулями, пока не подключено: что будет при подключении и как подключиться.
    h('div', { class: 'stage-ready', id: 'stageReady' }),
    // Широкое окно: справа живая колонка — что сейчас качается и каким путём (CSS прячет её в узком).
    h('aside', { class: 'live-col', id: 'liveCol', 'aria-label': 'Сейчас' }));
}

// Меню «Путь»: перехват (прокси или TUN) и маршрут (Россия напрямую или всё через сервер).
// На кнопке — что выбрано сейчас.
function pathMenu() {
  const summary = h('summary', { class: 'btn small', title: 'Как перехватывать трафик и куда его вести' });
  const label = () => summary.replaceChildren(svgIcon('route'), h('span', null, S.mode === 'tun' ? 'TUN' : 'Прокси'), h('span', { class: 'hint' }, '·'), h('span', null, ROUTING[S.routing] || ''), h('span', { class: 'caret', 'aria-hidden': 'true' }, '▾'));
  const d = h('details', { class: 'path-menu srv-only' }, summary,
    h('div', { class: 'path-pop' },
      h('label', null, h('span', null, 'Перехват'), segmented([['proxy', 'Системный прокси'], ['tun', 'Весь трафик (TUN)']], () => S.mode, v => { S.mode = v; save(); label(); updateStatus(); }, 'Перехват')),
      h('label', null, h('span', null, 'Маршрут'), segmented(Object.entries(ROUTING), () => S.routing, v => { S.routing = v; save(); label(); updateStatus(); }, 'Маршрут'))));
  label();
  // Щелчок мимо или Esc закрывают меню.
  const close = e => { if (d.open && (e.type === 'keydown' ? e.key === 'Escape' : !d.contains(e.target))) d.open = false; };
  d.addEventListener('toggle', () => {
    if (d.open) { document.addEventListener('pointerdown', close, true); document.addEventListener('keydown', close, true); }
    else { document.removeEventListener('pointerdown', close, true); document.removeEventListener('keydown', close, true); }
  });
  return d;
}

function renderReady() {
  const box = $('#stageReady');
  if (!box || !S || !ST) return;
  const cs = currentServer();
  const none = !S.servers.length && !(S.wl && S.wl.enabled);
  if (zapretOnly()) {
    const have = !!(AD && AD.zapret && AD.zapret.installed);
    box.classList.toggle('slim', have);
    patch('stageReady', ['z', have, AD && AD.zapret && AD.zapret.strategy].join('|'), () => [
      h('div', { class: 'ready-text' },
        h('b', null, have ? 'Только zapret — без сервера' : 'Сначала установите zapret'),
        h('span', null, have ? 'Замедленные YouTube, Discord и другие сайты из списков zapret откроются без сервера. Заблокированные сайты без сервера не откроются.'
          : 'zapret скачивается в разделе «Обход» за минуту.')),
      have ? h('span', { class: 'ready-keys' }, 'кнопка слева или ', h('kbd', null, 'Ctrl'), '+', h('kbd', null, 'Enter'))
        : h('button', { class: 'btn primary', onclick: () => go('bypass') }, 'Открыть «Обход»')]);
    return;
  }
  box.classList.toggle('slim', !none);
  patch('stageReady', [none, cs.name, cs.host, S.routing, S.mode, ST.core.present].join('|'), () => [
    h('div', { class: 'ready-text' },
      h('b', null, none ? 'Добавьте сервер, чтобы подключиться' : 'Готово к подключению'),
      h('span', null, none ? 'Вставьте ссылку vless://, hysteria2:// или naive+https:// — или поставьте свой сервер.'
        : `${cs.name}${cs.host ? ' · ' + cs.host : ''} · ${ROUTING[S.routing] || ''}`)),
    none ? h('button', { class: 'btn primary', onclick: () => go('servers') }, 'Добавить сервер')
      : h('span', { class: 'ready-keys' }, 'кнопка слева или ', h('kbd', null, 'Ctrl'), '+', h('kbd', null, 'Enter'))]);
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
    h('span', { class: 'tile-v' }, on ? fmtDur(Date.now() / 1000 - sessionStart()) : 'не подключено'),
    h('span', { class: 'tile-sub' }, on ? (zapretOnly() ? 'zapret, без сервера' : `через сервер ${fmtBytes(t[0] + t[1])}`) : 'щёлкните, чтобы открыть историю трафика'));
  const ls = $('#liveSpeed');
  if (ls) ls.replaceChildren(
    h('span', { class: 'ls-n' }, h('span', { class: 'ls-arrow' }, '↓'), tnum('ls-down', on ? sp.pd + sp.dd + sp.zd + sp.td : 0, fmtSpeed)),
    h('span', { class: 'ls-n up' }, h('span', { class: 'ls-arrow' }, '↑'), tnum('ls-up', on ? sp.pu + sp.du + sp.zu + sp.tu : 0, fmtSpeed)),
    h('span', { class: 'ls-spark' }, speedSpark()));
  const sl = $('#sysLine');
  if (sl) {
    // Защищён ли трафик: замок и слово; при наведении — что это значит.
    const word = { on: 'Защищено', off: 'Без защиты', wait: 'Подключаюсь…', revive: 'Восстанавливаю…', err: 'Сбой', blocked: 'Заблокировано' }[p] || '';
    const tip = { on: 'Трафик к заблокированным сайтам идёт через сервер в шифрованном канале', off: 'Naivka выключена: трафик идёт как обычно, без сервера' }[p] || '';
    sl.title = tip;
    const ms = on ? (d && d.ms ? `· ${cs.name} · ${d.ms} мс` : `· ${cs.name}`) : '';
    const nag = !!dnsNag();
    patch('sysLine', [p, ms, nag].join('|'), () => [h('span', { class: 'sl-ico ' + p }, svgIcon(on ? 'lock' : 'unlock')), h('b', { class: 'sl-val ' + p }, word),
      ms ? h('span', { class: 'sl-ms' }, ms) : null,
      nag ? h('button', { class: 'sl-warn', type: 'button', title: 'DNS для сайтов «напрямую» не зашифрован — подробнее', onclick: dnsDialog }, svgIcon('warn'), 'DNS') : null]);
  }
}

const ZERO = { pd: 0, pu: 0, dd: 0, du: 0, zd: 0, zu: 0, td: 0, tu: 0 };
function lastSpeeds() {
  const smp = STATS && STATS.samples || [];
  const l = smp[smp.length - 1];
  if (!l || Date.now() / 1000 - l.t > 4) return ZERO;
  return { ...ZERO, ...l };
}

const zapretOn = () => S.zapret && S.zapret.enabled && ((AD && AD.zapret && AD.zapret.running) || (ST && ST.zapretOn));
const tgOn = () => S.telegram && S.telegram.enabled && AD && AD.telegram && AD.telegram.running;

// Иконки 24×24, рисуются линией.
const ICONS = {
  pc: 'M4 5h16a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM9 20h6M12 16v4',
  globe: 'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18',
  server: 'M5 4h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM5 14h14a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1zM8 7h.01M8 17h.01',
  shield: 'M12 3l7 3v5c0 4.5-3 8.2-7 10-4-1.8-7-5.5-7-10V6l7-3zM9 12l2 2 4-4',
  tg: 'M21 4L3 11.5l6.5 2.2M21 4l-3 16-8.5-6.3M21 4L9.5 13.7v5.8l3.2-3.7',
  route: 'M6 19a2 2 0 1 0 0-4a2 2 0 1 0 0 4zM18 9a2 2 0 1 0 0-4a2 2 0 1 0 0 4zM8 17h7a3 3 0 0 0 0-6H9a3 3 0 0 1 0-6h7',
  lock: 'M6 11h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1zM8 11V8a4 4 0 0 1 8 0v3',
  unlock: 'M6 11h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1zM8 11V8a4 4 0 0 1 7.7-1.5',
  warn: 'M12 4l9 16H3l9-16zM12 10v4M12 17v.01',
  doc: 'M7 3h7l4 4v14H7zM14 3v4h4M10 12h5M10 16h5',
  gauge: 'M4 16a8 8 0 1 1 16 0M12 16l4-5M12 16h.01',
};
// Значок для кнопок и подписей (линией, цветом текста).
const svgIcon = name => s('svg', { viewBox: '0 0 24 24', class: 'ico', 'aria-hidden': 'true' }, s('path', { d: ICONS[name] }));
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
  const lanes = zapretOnly() ? [] : [{
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
    // AD (сведения о zapret) приходит позже состояния: при запуске zapret уже работает, а AD ещё нет.
    node: { icon: 'shield', title: 'zapret', sub: AD && AD.zapret && AD.zapret.degraded ? 'не справляется' : (AD && AD.zapret && AD.zapret.strategy) || S.zapret.strategy || '' },
  });
  lanes.push({ key: 'direct', name: 'Напрямую', color: 'var(--direct)', speed: on ? sp.dd + sp.du : 0, active: true, unmeasured: !on, node: null });
  return lanes;
}

let lastProxyNode = '';
function renderLanes() {
  const box = $('#lanesCard');
  if (!box || !ST || !S) return;
  const lanes = laneList();
  const total = lanes.reduce((a, l) => a + l.speed, 0);
  const n = lanes.length;
  const W = 1000, top = 58, bottom = n > 2 ? 70 + 82 * (n - 1) : 236, H = bottom + 92;
  // Пропорции рисунка — чтобы CSS подобрал ширину схемы под свободную высоту и главная влезала без прокрутки.
  box.style.setProperty('--lanes-ratio', (W / H).toFixed(3));
  // Свободная высота под рисунок: блок схемы минус легенда и полка программ под ней.
  const lg = $('#lanesLegend'), ps = $('#procShelf');
  const free = box.clientHeight - (lg ? lg.offsetHeight : 0) - (ps ? ps.offsetHeight : 0) - 16;
  if (free > 0) box.style.setProperty('--lanes-free', free + 'px');
  const Y0 = (top + bottom) / 2;
  const nodeX = { proxy: 500, tg: 405, zapret: 595, direct: 500 };
  const mc = document.documentElement.dataset.skin === 'blocks';
  if (!mc) {
    const lsv = $('#lanesSvg');
    if (lsv && lsv.firstChild) { lsv.replaceChildren(); delete lsv.dataset.built; flowsBegin('lane:'); flowsEnd('lane:'); }
    renderLaneLegend(lanes, total, renderBranches(lanes));
    return;
  }
  Branches.unmount();
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
        // Сменился сервер — узел «перещёлкивается», по ветке пробегает вспышка.
        const swapped = l.key === 'proxy' && lastProxyNode && lastProxyNode !== l.node.title && !REDUCE.matches;
        if (l.key === 'proxy') lastProxyNode = l.node.title;
        if (swapped && line) line.classList.add('swap-line');
        const open = () => go(l.key === 'proxy' ? 'servers' : 'bypass');
        const tip = s('title', null, l.key === 'proxy' ? 'Выбрать сервер' : 'Открыть «Обход»');
        // Флаг страны сервера — внутри узла вместо значка, чтобы не ложиться на ветку.
        const flag = l.node.cc ? flagInSvg(l.node.cc, x - 11, y - 22 / 3, 22) : null;
        // «Пиксели»: тот же круг радиусом 19, растрированный.
        if (mc) nodes.push(s('g', { class: 'clickable' + (l.active ? ' on' : ''), onclick: open }, tip, pxNode(x, y, 19, flag ? null : l.node.icon, l.active ? l.color : 'var(--faint)', l.active ? 'on' : '')));
        else {
          nodes.push(s('circle', { cx: x, cy: y, r: 19, class: 'lane-node clickable' + (l.active ? ' on' : ''), style: `stroke:${l.color}`, onclick: open }, tip));
          if (!flag) nodes.push(icon(l.node.icon, x, y, 20, l.active ? l.color : 'var(--faint)'));
        }
        if (flag) { flag.classList.add('node-flag'); if (!l.active) flag.classList.add('idle'); nodes.push(flag); }
        if (swapped) {
          nodes.push(s('circle', { cx: x, cy: y, r: 19, class: 'swap-ring', style: `stroke:${l.color}` }));
          if (flag) flag.classList.add('swap-in');
        }
        nodes.push(s('text', { x, y: y - 40, 'text-anchor': 'middle', class: 'lbl halo' + (swapped ? ' swap-text' : '') }, l.node.title));
        nodes.push(s('text', { x, y: y - 25, 'text-anchor': 'middle', class: 'sub-lbl halo' + (swapped ? ' swap-text' : '') },
          !flag && l.node.cc ? s('tspan', { class: 'cc-text' }, l.node.cc + '  ') : null, l.node.sub));
      } else {
        nodes.push(s('text', { x: l.nx, y: l.ny + 26, 'text-anchor': 'middle', class: 'sub-lbl halo', style: `fill:${l.color}` }, 'напрямую'));
      }
    });
    // Узел «Компьютер» — сама кнопка подключения (HTML поверх схемы), здесь только подписи.
    const end = (x, ic, title, sub) => ic === 'pc' ? [
      s('text', { x, y: Y0 + 88, 'text-anchor': 'middle', class: 'lbl halo' }, title),
      s('text', { x, y: Y0 + 104, 'text-anchor': 'middle', class: 'sub-lbl halo' }, sub)] : [
      ...(mc ? [pxNode(x, Y0, 25, ic, 'var(--ink)', 'end')] : [s('circle', { cx: x, cy: Y0, r: 25, class: 'lane-end' }), icon(ic, x, Y0, 24, 'var(--ink)')]),
      s('text', { x, y: Y0 + 46, 'text-anchor': 'middle', class: 'lbl halo' }, title),
      s('text', { x, y: Y0 + 62, 'text-anchor': 'middle', class: 'sub-lbl halo' }, sub)];
    return s('svg', { class: 'lanes', viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': 'Схема маршрутов трафика' },
      defs, ...paths, dots, ...nodes,
      ...end(95, 'pc', 'Компьютер', phase() === 'on' ? (S.mode === 'tun' ? 'весь трафик' : `прокси :${S.mixedPort}`) : phase() === 'revive' ? 'нажмите, чтобы отключить' : 'нажмите, чтобы подключить'),
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
  renderLaneLegend(lanes, total, {});
}

// Легенда обновляется каждую секунду отдельно — анимацию схемы это не трогает.
// over — сколько сайтов не поместилось на каждой ветке схемы.
function renderLaneLegend(lanes, total, over) {
  const leg = $('#lanesLegend');
  if (leg) leg.replaceChildren(...lanes.map(l => {
    const share = total > 0 && l.speed > 0 ? Math.round(l.speed / total * 100) : 0;
    return h('div', { class: 'leg-chip' + (l.active ? '' : ' off') },
      h('span', { class: 'leg-dot', style: `background:${l.color};color:${l.color}` }),
      h('span', { class: 'leg-name' }, l.name),
      h('b', null, !l.active ? 'выключено' : l.unmeasured ? (l.key === 'zapret' ? 'работает' : 'всё остальное') : tnum('lane-' + l.key, l.speed, fmtSpeed)),
      share ? h('span', { class: 'leg-share num' }, share + '%') : null,
      over[l.key] ? h('span', { class: 'leg-share' }, `ещё ${count(over[l.key], 'сайт', 'сайта', 'сайтов')}`) : null,
      signalBar(l.active && !l.unmeasured ? l.speed : 0));
  }));
}

// ---------- живые ветви (web/branches.js) ----------
// Сайт → его основной домен: cache12-fra1.steamcontent.com → steamcontent.com, x.co.uk → x.co.uk.
function baseDomain(host) {
  const p = host.toLowerCase().replace(/\.$/, '').split('.');
  if (p.length <= 2) return p.join('.');
  const sld = p[p.length - 2], tld = p[p.length - 1];
  return (tld.length === 2 && /^(com|net|org|co|gov|edu|ac|msk|spb)$/.test(sld) ? p.slice(-3) : p.slice(-2)).join('.');
}
const DL_DOMAINS = ['steamcontent.com', 'steamserver.net', 'steampipe.akamaized.net', 'steamcdn-a.akamaihd.net', 'steamstatic.com', 'epicgames.com', 'windowsupdate.com', 'microsoft.com'];

// Листья на схеме — не домены, а понятные сервисы: десяток адресов Twitch — один лист «Twitch».
const SERVICES = [
  ['YouTube', 'youtube.com googlevideo.com ytimg.com youtu.be ggpht.com youtube-nocookie.com'],
  ['Twitch', 'twitch.tv ttvnw.net jtvnw.net live-video.net twitchcdn.net twitchsvc.net 7tv.io 7tv.app betterttv.net frankerfacez.com'],
  ['Discord', 'discord.com discord.gg discordapp.com discordapp.net discord.media'],
  ['Telegram', 'telegram.org t.me telegram.me telesco.pe'],
  ['WhatsApp', 'whatsapp.com whatsapp.net wa.me'],
  ['Instagram', 'instagram.com cdninstagram.com'],
  ['Facebook', 'facebook.com fbcdn.net fb.com messenger.com'],
  ['X (Twitter)', 'x.com twitter.com twimg.com t.co'],
  ['ChatGPT', 'openai.com chatgpt.com oaistatic.com oaiusercontent.com'],
  ['Claude', 'claude.ai anthropic.com claude.com'],
  ['Google', 'google.com google.ru gstatic.com googleapis.com googleusercontent.com gvt1.com gvt2.com googleadservices.com'],
  ['Яндекс', 'yandex.ru yandex.net yandex.com ya.ru yastatic.net yandex.by yandex.kz yandexcloud.net yandex.st'],
  ['ВКонтакте', 'vk.com vk.ru userapi.com vkuserphoto.ru vk-portal.net vkuser.net vk.me'],
  ['Одноклассники', 'ok.ru mycdn.me'],
  ['Mail.ru', 'mail.ru imgsmail.ru mradx.net'],
  ['Steam', 'steampowered.com steamcommunity.com steamcontent.com steamstatic.com steamserver.net steamgames.com valvesoftware.com steam-chat.com'],
  ['Epic Games', 'epicgames.com epicgames.dev unrealengine.com'],
  ['Microsoft', 'microsoft.com office.com office.net live.com skype.com bing.com msn.com windows.com windowsupdate.com sfx.ms microsoftonline.com msftconnecttest.com msedge.net azureedge.net xboxlive.com'],
  ['Apple', 'apple.com icloud.com mzstatic.com cdn-apple.com'],
  ['GitHub', 'github.com githubassets.com githubusercontent.com github.io'],
  ['Spotify', 'spotify.com scdn.co spotifycdn.com'],
  ['Netflix', 'netflix.com nflxvideo.net nflximg.net'],
  ['Кинопоиск', 'kinopoisk.ru kp.ru'],
  ['Wildberries', 'wildberries.ru wbstatic.net wb.ru'],
  ['Ozon', 'ozon.ru ozone.ru'],
  ['Госуслуги', 'gosuslugi.ru'],
  ['Сбер', 'sberbank.ru sber.ru'],
  ['Т-Банк', 'tbank.ru tinkoff.ru'],
  ['Reddit', 'reddit.com redd.it redditmedia.com redditstatic.com'],
  ['Pinterest', 'pinterest.com pinimg.com'],
  ['TikTok', 'tiktok.com tiktokcdn.com tiktokv.com byteoversea.com'],
  ['Cloudflare', 'cloudflare.com cloudflare-dns.com'],
].map(([name, list]) => ({ name, domains: list.split(' ') }));
// Счётчики, реклама и фоновая телеметрия — их на схеме не показываем, только счётчиком «в фоне».
const BACKGROUND = /(^|\.)(scorecardresearch|doubleclick|google-analytics|googletagmanager|googlesyndication|adfox|criteo|taboola|outbrain|hotjar|mixpanel|segment|amplitude|appsflyer|adjust|branch|sentry|newrelic|datadoghq|cdndex|iabtechnologylab|qianxin|360safe|kaspersky-labs|drweb|avast|avcdn|msftncsi|digicert|letsencrypt|ocsp|pki|crashlytics|app-measurement|onesignal|adriver|admitad|yandexadexchange|mediascope|tns-counter|top100|liveinternet|tiqcdn|demdex|omtrdc|rubiconproject|pubmatic|openx|adnxs|casalemedia)\.[a-z]+$/;
// Значки сервисов на листьях схемы: простые, в одном стиле, встроены в программу (ничего не грузится).
const SVC_ICONS = {
  video: 'M4 7h16v10H4zM10 10l4 2-4 2z', stream: 'M5 4h15v10l-4 4h-4l-3 3v-3H5zM11 8v4M15 8v4',
  chat: 'M4 6h16v10H9l-4 3v-3H4zM9 11h.01M15 11h.01', send: 'M21 4L3 11.5l6.5 2.2M21 4l-3 16-8.5-6.3M21 4L9.5 13.7v5.8l3.2-3.7',
  social: 'M5 5h14v14H5zM12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM16.5 7.5h.01', ai: 'M12 3l2.5 5.5L20 11l-5.5 2.5L12 19l-2.5-5.5L4 11l5.5-2.5z',
  search: 'M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14zM20 20l-4-4', game: 'M6 9h12a3 3 0 0 1 3 3v2a3 3 0 0 1-5 2l-1-1H9l-1 1a3 3 0 0 1-5-2v-2a3 3 0 0 1 3-3zM8 11v3M6.5 12.5h3M16 12h.01',
  store: 'M5 8h14l-1.5 11h-11zM9 8a3 3 0 0 1 6 0', code: 'M8 8l-4 4 4 4M16 8l4 4-4 4M13.5 6l-3 12',
  music: 'M9 18V6l10-2v12M9 18a3 3 0 1 1-3-3 3 3 0 0 1 3 3zM19 16a3 3 0 1 1-3-3 3 3 0 0 1 3 3z',
  bank: 'M3 10l9-5 9 5M5 10v8M9 10v8M15 10v8M19 10v8M3 20h18', gov: 'M12 3l7 3v6c0 4-3 7-7 9-4-2-7-5-7-9V6zM9 12l2 2 4-4',
  cloud: 'M7 18a4 4 0 0 1 0-8 5 5 0 0 1 9.6-1.5A4 4 0 1 1 17 18z', mail: 'M4 6h16v12H4zM4 7l8 6 8-6',
  windows: 'M4 5h7v7H4zM13 5h7v7h-7zM4 14h7v6H4zM13 14h7v6h-7z', download: 'M12 4v11M7 10l5 5 5-5M5 20h14',
  globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18',
  app: 'M4 5h16v14H4zM4 9h16',
};
const SVC_ICON = { YouTube: 'video', Twitch: 'stream', Discord: 'chat', Telegram: 'send', WhatsApp: 'chat', Instagram: 'social', Facebook: 'social',
  'X (Twitter)': 'social', ChatGPT: 'ai', Claude: 'ai', Google: 'search', 'Яндекс': 'search', 'ВКонтакте': 'social', 'Одноклассники': 'social',
  'Mail.ru': 'mail', Steam: 'game', 'Epic Games': 'game', Microsoft: 'windows', Apple: 'cloud', GitHub: 'code', Spotify: 'music', Netflix: 'video',
  'Кинопоиск': 'video', Wildberries: 'store', Ozon: 'store', 'Госуслуги': 'gov', 'Сбер': 'bank', 'Т-Банк': 'bank', Reddit: 'social',
  Pinterest: 'social', TikTok: 'video', Cloudflare: 'cloud' };
const PROC_ICON = { 'Chrome': 'globe', 'Edge': 'globe', 'Firefox': 'globe', 'Opera': 'globe', 'Яндекс Браузер': 'globe', Steam: 'game', Discord: 'chat',
  Telegram: 'send', Spotify: 'music', 'Epic Games': 'game', qbittorrent: 'download', utorrent: 'download' };

function serviceOf(base) {
  for (const sv of SERVICES) for (const d of sv.domains) if (base === d || base.endsWith('.' + d)) return sv;
  return null;
}
const PROC_NAME = { 'chrome.exe': 'Chrome', 'msedge.exe': 'Edge', 'firefox.exe': 'Firefox', 'opera.exe': 'Opera', 'browser.exe': 'Яндекс Браузер',
  'steam.exe': 'Steam', 'steamwebhelper.exe': 'Steam', 'discord.exe': 'Discord', 'telegram.exe': 'Telegram', 'spotify.exe': 'Spotify',
  'epicgameslauncher.exe': 'Epic Games', 'svchost.exe': 'служба Windows', 'msedgewebview2.exe': 'встроенный браузер' };
const procName = p => { const k = (p || '').toLowerCase(); return PROC_NAME[k] || (k ? k.replace(/\.exe$/, '') : ''); };

// Решения автоопределения (раз в 30 с) и сводка листьев за последний опрос: ключ → { label, domains, procs }.
let smartMap = {}, smartAt = 0;
let branchLeaves = {};
function branchWhy(key, b) {
  const e = branchLeaves[key] || { domains: [key], procs: [] };
  const r = S.rules.find(r => r.kind === 'domain' && e.domains.some(h => h === r.value.toLowerCase() || h.endsWith('.' + r.value.toLowerCase())));
  let why;
  if (r) why = `Ваше правило: ${{ proxy: 'через сервер', direct: 'напрямую', zapret: 'через zapret', block: 'блокировать' }[r.action]}.`;
  else if (b === 'zapret') why = 'Из списков zapret: его замедляют, zapret обходит это без сервера.';
  else if (S.routing === 'global' && b === 'proxy') why = 'Режим «Всё через сервер».';
  else {
    const sm = e.domains.map(h => smartMap[h] ?? smartMap['.' + h]).find(v => v !== undefined);
    if (sm !== undefined) why = sm ? 'Автоопределение: открывается из России нормально — напрямую быстрее.' : 'Автоопределение: напрямую не открывается или закрыт для России — через сервер.';
    else if (b === 'direct' && S.downloadsDirect && e.domains.some(h => DL_DOMAINS.some(d => h === d || h.endsWith('.' + d)))) why = 'Загрузки игр и обновлений — сразу напрямую, чтобы закачка не забивала сервер.';
    else why = b === 'direct' ? 'Напрямую: открывается из России без сервера.'
      : 'Через сервер: заблокирован в России, закрыт для российских IP или ещё проверяется.';
  }
  // Части подсказки — отдельными строками: так их переводит английский интерфейс.
  return [why,
    e.domains.length > 1 || e.label !== e.domains[0] ? 'Адреса: ' + e.domains.slice(0, 4).map(displayDomain).join(', ') + (e.domains.length > 4 ? ' …' : '') + '.' : '',
    e.procs.length ? 'Программа: ' + e.procs.slice(0, 2).join(', ') + '.' : ''].filter(Boolean);
}
// Свои правила применяются на ходу (ядро перечитывает их списки) — подсказка говорит, когда это случится.
function ruleNote(action) {
  if (!(ST && ST.running)) return 'Заработает при подключении.';
  return action === 'zapret' ? 'Уже работает — zapret перезапустился на секунду.' : 'Уже работает, переподключаться не нужно.';
}
function branchDrop(key, b) {
  if (b === 'zapret' && !zapretOn()) { toast('zapret выключен — на эту ветку сейчас нельзя'); return false; }
  const e = branchLeaves[key] || { label: key, domains: [key] };
  // Для известного сервиса правило ставится на все его адреса — иначе видео пошло бы одним путём, а сайт другим.
  const sv = SERVICES.find(x => x.name === key);
  const doms = sv ? sv.domains : e.domains;
  S.rules = S.rules.filter(r => !(r.kind === 'domain' && doms.includes(r.value.toLowerCase())));
  for (const d of doms) S.rules.push({ kind: 'domain', value: d, action: b });
  save(0);
  toast(`${e.label} — ${{ proxy: 'через сервер', direct: 'напрямую', zapret: 'через zapret' }[b]}. ${ruleNote(b)}`);
  unlockAchv('route');
  return true;
}
// Сайты с трафиком — понятные сервисы: адреса одного сервиса сводятся в одну группу; незнакомые сайты
// с заметным трафиком — по основному домену; счётчики и фоновая мелочь не показываются.
// Нужны схеме веток (листья) и колонке «Сейчас» на широкой главной.
function siteGroups() {
  const groups = new Map();
  for (const t of (STATS && STATS.top) || []) {
    if (!t.host || /^[\d.:[\]]+$/.test(t.host) || t.route === 'telegram') continue;
    const base = baseDomain(t.host), sv = serviceOf(base);
    // Мелочь (до 20 КБ/с и 2 МБ за сеанс) — фоновые запросы и телеметрия: на схеме только шум.
    if (BACKGROUND.test(base) || (t.speed < 20 * 1024 && t.total < 2 * 1024 * 1024)) continue;
    const key = sv ? sv.name : base;
    const g = groups.get(key) || { key, label: sv ? sv.name : displayDomain(base), domains: [], procs: [], routes: {}, speed: 0, total: 0 };
    if (!g.domains.includes(base)) g.domains.push(base);
    const pn = procName((t.proc || '').split(/[\\/]/).pop()); if (pn && !g.procs.includes(pn)) g.procs.push(pn);
    const rb = t.route === 'direct' || t.route === 'zapret' ? t.route : 'proxy';
    g.routes[rb] = (g.routes[rb] || 0) + t.speed + 1;
    g.speed += t.speed || 0; g.total += t.total || 0;
    groups.set(key, g);
  }
  return [...groups.values()];
}
function renderBranches(lanes) {
  const lb = $('#lanesBox');
  if (!lb) return {};
  renderProcShelf();
  const p = phase();
  if (p === 'on' && Date.now() - smartAt > 30000 && S.smartDirect) {
    smartAt = Date.now();
    api('/api/smart').then(v => { smartMap = {}; for (const x of v.recent || []) smartMap[x.host] = x.direct; }).catch(() => {});
  }
  branchLeaves = {};
  const top = siteGroups().map(g => {
    branchLeaves[g.key] = g;
    const route = Object.entries(g.routes).sort((a, b) => b[1] - a[1])[0][0];
    return { key: g.key, label: g.label, route, icon: SVC_ICONS[SVC_ICON[g.key] || 'globe'] };
  });
  const sp = lanes.map(l => ({ ...l,
    thin: l.key === 'direct' && p === 'on' && S.routing === 'global',
    wilt: l.key === 'zapret' && AD && AD.zapret && AD.zapret.degraded }));
  return Branches.update(lb, {
    phase: p, offline: !!(STATS && STATS.health === 'offline' && p === 'on'), lanes: sp, top,
    endSub: ROUTING[S.routing],
    compSub: zapretOnly() ? (p === 'on' ? 'zapret включён' : 'нажмите, чтобы включить zapret') : p === 'on' ? (S.mode === 'tun' ? 'весь трафик' : `прокси :${S.mixedPort}`) : p === 'revive' ? 'нажмите, чтобы отключить' : 'нажмите, чтобы подключить',
    flag: code => flag(code), displayDomain, why: branchWhy, onDrop: branchDrop, onPick: sitePick,
    onNode: k => go(k === 'proxy' ? 'servers' : 'bypass'), toast,
  }) || {};
}

// ---------- выбор пути: меню по щелчку и программы целиком ----------
const ROUTE_OPTS = [['proxy', 'Через сервер', 'var(--tunnel)'], ['direct', 'Напрямую', 'var(--direct)'], ['zapret', 'Через zapret', 'var(--warn)'], ['auto', 'Как решит Naivka', 'var(--faint)']];
const ROUTE_SAY = { proxy: 'через сервер', direct: 'напрямую', zapret: 'через zapret' };
let routePop = null;
function closeRoutePop() { if (routePop) { routePop.remove(); routePop = null; } }
// routePicker — небольшое меню у элемента: куда пускать сайт или программу. Тянуть не обязательно.
function routePicker(anchor, { title, sub, current, onPick }) {
  closeRoutePop();
  const opts = ROUTE_OPTS.filter(([k]) => k !== 'zapret' || zapretOn());
  const pop = h('div', { class: 'route-pop', role: 'menu', 'aria-label': title },
    h('b', null, title), sub ? h('small', null, sub) : null,
    ...opts.map(([k, name, col]) => h('button', { role: 'menuitem', 'aria-current': String(current === k), onclick: () => { closeRoutePop(); onPick(k); anchor.focus && anchor.focus(); } },
      h('i', { style: `background:${col}` }), name)));
  document.body.append(pop);
  const r = anchor.getBoundingClientRect(), pw = pop.offsetWidth, ph = pop.offsetHeight;
  let x = Math.min(Math.max(8, r.left), innerWidth - pw - 8), y = r.bottom + 6;
  if (y + ph > innerHeight - 8) y = Math.max(8, r.top - ph - 6);
  pop.style.left = x + 'px'; pop.style.top = y + 'px';
  routePop = pop;
  pop.querySelector('button').focus();
}
document.addEventListener('pointerdown', e => { if (routePop && !routePop.contains(e.target)) closeRoutePop(); }, true);
document.addEventListener('keydown', e => { if (e.key === 'Escape' && routePop) { e.stopPropagation(); closeRoutePop(); } }, true);

// Сайт (лист на схеме): меню вместо обязательного перетаскивания.
function sitePick(item, el) {
  const label = branchLeaves[item.key] ? branchLeaves[item.key].label : item.label;
  routePicker(el, { title: label, sub: 'Этот сервис — все его адреса', current: item.current, onPick: k => {
    if (k === 'auto') { siteClear(item.key); return; }
    if (k !== item.current && branchDrop(item.key, k)) Branches.moveLeaf(item.key, k);
  } });
}
function siteClear(key) {
  const e = branchLeaves[key] || { label: key, domains: [key] };
  const sv = SERVICES.find(x => x.name === key);
  const doms = sv ? sv.domains : e.domains;
  const before = S.rules.length;
  S.rules = S.rules.filter(r => !(r.kind === 'domain' && doms.includes(r.value.toLowerCase())));
  save(0);
  toast(before === S.rules.length ? `${e.label} — и так решает Naivka` : `${e.label} — снова как решит Naivka. ${ruleNote('')}`);
}

// Программы с трафиком — полка под схемой. Программа отправляется целиком (правило по процессу).
const PROC_SKIP = /^(svchost|msedgewebview2|naivka.*|sing-box|system|lsass|services|winws|tg-ws-proxy|xray)\.exe$/i;
function procRule(exe) { return S.rules.find(r => r.kind === 'process' && r.value.toLowerCase() === exe.toLowerCase()); }
function procList() {
  const m = new Map();
  for (const t of (STATS && STATS.top) || []) {
    const exe = (t.proc || '').split(/[\\/]/).pop();
    if (!exe || PROC_SKIP.test(exe)) continue;
    const x = m.get(exe.toLowerCase()) || { exe, speed: 0, total: 0 };
    x.speed += t.speed || 0; x.total += t.total || 0; m.set(exe.toLowerCase(), x);
  }
  for (const r of S.rules) if (r.kind === 'process' && !m.has(r.value.toLowerCase())) m.set(r.value.toLowerCase(), { exe: r.value, speed: 0, total: 0 });
  return [...m.values()].filter(x => procRule(x.exe) || x.speed >= 20 * 1024 || x.total >= 2 * 1024 * 1024)
    .sort((a, b) => b.speed - a.speed).slice(0, 8);
}
function setProcRoute(exe, k) {
  const name = procName(exe.toLowerCase()) || exe;
  if (k === 'zapret' && !zapretOn()) { toast('zapret выключен — на эту ветку сейчас нельзя'); return; }
  S.rules = S.rules.filter(r => !(r.kind === 'process' && r.value.toLowerCase() === exe.toLowerCase()));
  if (k !== 'auto') S.rules.push({ kind: 'process', value: exe, action: k });
  save(0);
  toast(k === 'auto' ? `${name} — снова как решит Naivka. ${ruleNote('')}` : `Вся программа ${name} — ${ROUTE_SAY[k]}. ${ruleNote(k)}`);
  if (k !== 'auto') unlockAchv('route');
  renderProcShelf(true);
}
function renderProcShelf(force) {
  const box = $('#procShelf');
  if (!box || !S) return;
  const list = phase() === 'on' && !zapretOnly() ? procList() : [];
  const sig = JSON.stringify(list.map(x => [x.exe, (procRule(x.exe) || {}).action]));
  if (!force && box.dataset.sig === sig) return;
  if (liveDrag) return; // тащат программу с полки — не пересоздаём её из-под курсора
  box.dataset.sig = sig;
  if (!list.length) { box.replaceChildren(); return; }
  box.replaceChildren(h('span', { class: 'proc-title' }, 'Программы'),
    ...list.map(x => procChip(x)),
    h('span', { class: 'proc-hint' }, 'Перетащите на ветку или нажмите — программа пойдёт этим путём целиком'));
}
function procChip(x) {
  const name = procName(x.exe.toLowerCase()) || x.exe, rule = procRule(x.exe);
  const col = rule ? (ROUTE_OPTS.find(o => o[0] === rule.action) || [])[2] : '';
  const ic = SVC_ICONS[PROC_ICON[name] || PROC_ICON[name.toLowerCase()] || 'app'];
  const el = h('button', { class: 'proc-chip' + (rule ? ' ruled' : ''), type: 'button', style: col ? `--c:${col}` : '',
    'aria-label': `${name}: ${rule ? ROUTE_SAY[rule.action] : 'как решит Naivka'}. Нажмите, чтобы выбрать путь` },
    h('span', { class: 'svc' }, s('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' }, s('path', { d: ic }))), name);
  ghostDrag(el, () => { const g = el.cloneNode(true); g.classList.add('proc-ghost'); return g; },
    () => routePicker(el, { title: name, sub: 'Вся программа целиком', current: rule ? rule.action : 'auto', onPick: k => setProcRoute(x.exe, k) }),
    k => { if (k !== (rule && rule.action)) setProcRoute(x.exe, k); });
  return el;
}

// Перетаскивание программы или сайта на ветку схемы: короткое нажатие — tap(), перетаскивание — призрак
// под курсором и drop(ветка). Призрак убирается при любом исходе: отпустили, отменили, элемент
// пересоздали или окно потеряло фокус — иначе он «зависал» над схемой, а ветки оставались дорожками.
let liveDrag = 0;
// Страховка: отпустили мышь, а тащить уже нечего (строку убрали вместе с разделом) — убираем осиротевший призрак.
addEventListener('pointerup', () => setTimeout(() => {
  if (!document.querySelector('.proc-ghost') || document.querySelector('.lifted')) return;
  document.querySelectorAll('.proc-ghost').forEach(g => g.remove());
  liveDrag = 0; Branches.endHover();
}), true);
function ghostDrag(el, makeGhost, tap, drop) {
  let sx, sy, ghost = null, id = null;
  const finish = () => {
    id = null;
    if (!ghost) return null;
    ghost.remove(); ghost = null; el.classList.remove('lifted');
    liveDrag = Math.max(0, liveDrag - 1);
    removeEventListener('blur', cancel);
    return Branches.endHover();
  };
  const cancel = () => { if (id !== null || ghost) finish(); };
  el.addEventListener('pointerdown', e => { if (e.button !== 0) return; id = e.pointerId; el.setPointerCapture(id); sx = e.clientX; sy = e.clientY; });
  el.addEventListener('pointermove', e => {
    if (e.pointerId !== id) return;
    if (!ghost && Math.hypot(e.clientX - sx, e.clientY - sy) < 6) return;
    if (!ghost) {
      ghost = makeGhost(); document.body.append(ghost); el.classList.add('lifted');
      liveDrag++; addEventListener('blur', cancel);
    }
    ghost.style.left = (e.clientX - 20) + 'px'; ghost.style.top = (e.clientY - 14) + 'px';
    Branches.hoverAt(e.clientX, e.clientY);
  });
  el.addEventListener('pointerup', e => {
    if (e.pointerId !== id) return;
    if (!ghost) { id = null; tap(); return; }
    const k = finish();
    if (k) drop(k);
  });
  el.addEventListener('pointercancel', e => { if (e.pointerId === id) finish(); });
  el.addEventListener('lostpointercapture', cancel);
}

// ---------- колонка «Сейчас» (широкая главная) ----------
const ROUTE_COLOR = { proxy: 'var(--tunnel)', direct: 'var(--direct)', zapret: 'var(--warn)', block: 'var(--danger)' };
function siteRule(g) {
  const sv = SERVICES.find(x => x.name === g.key);
  const doms = sv ? sv.domains : g.domains;
  return S.rules.find(r => r.kind === 'domain' && doms.includes(r.value.toLowerCase()));
}
addEventListener('resize', () => { if (view === 'overview') guard(renderLive, 'renderLive'); });
function renderLive() {
  const box = $('#liveCol');
  if (!box || !S) return;
  if (getComputedStyle(box).display === 'none') { box.dataset.sig = ''; return; } // узкое окно — колонки нет
  const on = phase() === 'on';
  const sites = on ? siteGroups().sort((a, b) => b.speed - a.speed).slice(0, 12) : [];
  const procs = on && !zapretOnly() ? procList() : [];
  // Перерисовываем по смыслу, а скорость обновляем в уже стоящих строках — без мигания и сброса перетаскивания.
  const sig = JSON.stringify([on, sites.map(g => [g.key, groupRoute(g), (siteRule(g) || {}).action]), procs.map(x => [x.exe, (procRule(x.exe) || {}).action])]);
  if (box.dataset.sig !== sig && !liveDrag) { // тащат строку — пересоберём после того, как отпустят
    box.dataset.sig = sig;
    box.replaceChildren(
      h('div', { class: 'live-head' }, h('b', null, 'Сейчас'), h('small', null, on ? 'что качается и каким путём' : 'появится после подключения')),
      !on ? h('p', { class: 'live-empty' }, 'Подключитесь — здесь будут сайты и программы с трафиком. Любой можно нажать или перетащить на ветку схемы, чтобы он шёл другим путём.')
        : !sites.length && !procs.length ? h('p', { class: 'live-empty' }, 'Пока тихо: ничего заметного не качается.')
          : null,
      sites.length ? h('div', { class: 'live-sec' }, 'Сайты') : null,
      ...sites.map(g => liveRow({
        key: 'site:' + g.key, label: g.label, icon: SVC_ICONS[SVC_ICON[g.key] || 'globe'], route: groupRoute(g), rule: siteRule(g),
        sub: g.procs.slice(0, 2).join(', '),
        pick: el => sitePick({ key: g.key, label: g.label, current: (siteRule(g) || {}).action || 'auto' }, el),
        drop: k => { if (branchDrop(g.key, k)) Branches.moveLeaf(g.key, k); },
      })),
      procs.length ? h('div', { class: 'live-sec' }, 'Программы целиком') : null,
      ...procs.map(x => {
        const name = procName(x.exe.toLowerCase()) || x.exe, rule = procRule(x.exe);
        return liveRow({
          key: 'proc:' + x.exe, label: name, icon: SVC_ICONS[PROC_ICON[name] || PROC_ICON[name.toLowerCase()] || 'app'], route: rule ? rule.action : '', rule,
          pick: el => routePicker(el, { title: name, sub: 'Вся программа целиком', current: rule ? rule.action : 'auto', onPick: k => setProcRoute(x.exe, k) }),
          drop: k => { if (k !== (rule && rule.action)) setProcRoute(x.exe, k); },
        });
      }),
      on && (sites.length || procs.length) ? h('p', { class: 'live-hint' }, 'Нажмите или перетащите на ветку — пойдёт этим путём.') : null);
  }
  const speeds = new Map([...sites.map(g => ['site:' + g.key, g.speed]), ...procs.map(x => ['proc:' + x.exe, x.speed])]);
  for (const el of box.querySelectorAll('.live-row')) {
    const v = speeds.get(el.dataset.key) || 0;
    el.querySelector('.live-speed').textContent = v >= 1024 ? fmtSpeed(v) : '';
  }
}
// ---------- талисман и достижения ----------
function petSay(mood) {
  const sp = lastSpeeds(), cs = currentServer();
  if (mood === 'off') return 'Сплю… Нажмите кнопку питания — проснусь.';
  if (mood === 'wait') return 'Подключаюсь, секунду…';
  if (mood === 'err') return 'Ой, связь пропала. Уже чиню!';
  if (mood === 'run') return `Качаем: ${fmtSpeed(sp.pd + sp.pu)}`;
  const lines = [`Всё работает через ${cs.name || 'сервер'}.`, 'Тихо и спокойно.', 'Погладьте меня — проведите мышью туда-сюда.', 'Сайты видят адрес сервера, а не ваш.'];
  return lines[Math.floor(Date.now() / 7000) % lines.length];
}

// Значки: id, название, условие, путь значка (24×24). check возвращает true или прогресс [сколько, из скольки].
const GB = 1024 ** 3;
const ACHV = [
  ['connect', 'Первый шаг', 'Подключиться в первый раз.', 'M12 3v8M7.5 6.5a7 7 0 1 0 9 0', c => c.on],
  ['gb1', 'Первый гигабайт', '1 ГБ через сервер.', 'M4 17l5-5 4 4 7-8M15 8h5v5', c => [c.proxy, GB]],
  ['gb100', 'Сотня', '100 ГБ через сервер.', 'M5 4h14v6a7 7 0 0 1-14 0zM9 20h6M12 17v3', c => [c.proxy, 100 * GB]],
  ['saver', 'Экономист', '10 ГБ напрямую и через zapret — сервер не тратился.', 'M12 3v18M17 7.5c0-2-2-3-5-3s-5 1.2-5 3.3c0 4.7 10 2.2 10 7 0 2.2-2.2 3.4-5 3.4s-5-1.2-5-3.2', c => [c.direct, 10 * GB]],
  ['streak7', 'Неделя в строю', '7 дней подряд с трафиком.', 'M4 6h16v14H4zM4 10h16M8 3v5M16 3v5', c => [c.streak, 7]],
  ['rocket', 'Ракета', 'Скорость больше 10 МБ/с.', 'M12 2c4 3 5 8 3 13H9C7 10 8 5 12 2zM9 15l-3 4h4M15 15l3 4h-4M12 8.5v.01', c => [c.speed, 10 * 1024 * 1024]],
  ['travel', 'Путешественник', 'Трафик в 10 стран за один сеанс.', 'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18M3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18', c => [c.countries, 10]],
  ['night', 'Ночная сова', 'В сети между 3 и 5 часами утра.', 'M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z', c => c.night],
  ['route', 'Регулировщик', 'Отправить сайт или программу другим путём.', 'M6 3v18M6 7h9l3 3-3 3H6', null],
  ['zapret', 'Без сервера', 'Включить zapret.', 'M13 2L4 14h7l-1 8 9-12h-7z', c => c.zapret],
  ['own', 'Свой сервер', 'Поставить свой сервер через Naivka.', 'M4 5h16v6H4zM4 13h16v6H4zM8 8h.01M8 16h.01', c => c.own],
  ['friend', 'Не жадина', 'Пригласить друга на свой сервер.', 'M8 11a3.5 3.5 0 1 0 0-7a3.5 3.5 0 0 0 0 7zM2 20c0-3.5 2.7-6 6-6s6 2.5 6 6M16 8h6M19 5v6', null],
  ['style', 'Стилист', 'Своя тема или свой фон окна.', 'M12 3a9 9 0 0 0 0 18c1.5 0 2-1 2-2s-1-1.5-1-2.5 1-1.5 2-1.5h2a4 4 0 0 0 4-4c0-4.4-4-8-9-8zM7.5 11h.01M10 7h.01M15 7h.01', c => c.style],
  ['globe', 'Картограф', 'Покрутить глобус трафика.', 'M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18M3 12h18M12 3c3 3 3 15 0 18', null],
  ['tg', 'Почтальон', 'Telegram через tg-ws-proxy — без сервера.', 'M21 4L3 11l6 2 2 6 3-4 5 4zM9 13l12-9', c => c.tg],
  ['marathon', 'Марафон', 'Сутки на связи без перерыва.', 'M12 7v5l3 2M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18', c => [c.session, 86400]],
  ['pet', 'Лучший друг', 'Погладить талисмана.', 'M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10z', null],
];
let achvTraffic = null, achvTrafficAt = 0, sessionCountries = new Set(), bestSpeed = 0, achvQueue = [], achvShowing = false, achvBoot = true;
async function refreshAchvTraffic() {
  try { achvTraffic = await api('/api/traffic'); achvTrafficAt = Date.now(); } catch (_) { return; }
  checkAchv();
  if (view === 'diag' && diagTab === 'achv') renderAchv();
}
function achvCtx() {
  const life = (achvTraffic && achvTraffic.lifetime) || {};
  const sum = k => ((life[k] || [0, 0])[0] + (life[k] || [0, 0])[1]);
  const on = phase() === 'on', sp = lastSpeeds(), hr = new Date().getHours();
  if (on) { bestSpeed = Math.max(bestSpeed, sp.pd + sp.pu); for (const t of (STATS && STATS.top) || []) if (t.cc) sessionCountries.add(t.cc); }
  return {
    on, proxy: sum('proxy'), direct: sum('direct') + sum('zapret'), streak: (achvTraffic && achvTraffic.streak) || 0, speed: bestSpeed,
    countries: sessionCountries.size, night: on && sp.pd + sp.pu > 1024 && hr >= 3 && hr < 5, zapret: !!zapretOn(),
    tg: !!(tgOn() && AD.telegram.conns > 0), session: on ? Date.now() / 1000 - sessionStart() : 0,
    own: !!((S.ownHosts || []).length || (S.panels || []).length), style: S.theme === 'custom' || !!(S.wall && S.wall.kind),
  };
}
const achvDone = (r) => r === true || (Array.isArray(r) && r[0] >= r[1]);
function checkAchv() {
  if (!S || !ST || S.achievements === false) return;
  if (Date.now() - achvTrafficAt > 120000) { achvTrafficAt = Date.now(); refreshAchvTraffic(); return; }
  if (!achvTraffic) return; // без истории трафика часть условий не посчитать — ждём её
  const c = achvCtx();
  const fresh = [];
  for (const [id, , , , test] of ACHV) if (test && !(S.achv || {})[id] && achvDone(test(c))) fresh.push(id);
  if (!fresh.length) { achvBoot = false; return; }
  S.achv = { ...(S.achv || {}) };
  const now = Math.floor(Date.now() / 1000);
  for (const id of fresh) S.achv[id] = now;
  save();
  // После обновления Naivka многое уже выполнено — одна сводка вместо очереди из десятка окошек.
  if (achvBoot && fresh.length > 2) achvToast(null, fresh.length); else fresh.forEach(id => achvToast(id));
  achvBoot = false;
  if (view === 'diag' && diagTab === 'achv') renderAchv();
}
function unlockAchv(id) {
  if (!S || S.achievements === false || (S.achv || {})[id]) return;
  S.achv = { ...(S.achv || {}), [id]: Math.floor(Date.now() / 1000) };
  save();
  achvToast(id);
}
function achvIcon(d, cls = '') {
  return s('svg', { viewBox: '0 0 24 24', class: 'achv-ic ' + cls, 'aria-hidden': 'true' }, s('path', { d }));
}
function achvToast(id, many) {
  achvQueue.push([id, many]);
  if (achvShowing) return;
  const next = () => {
    const it = achvQueue.shift();
    if (!it) { achvShowing = false; return; }
    achvShowing = true;
    const a = it[0] && ACHV.find(x => x[0] === it[0]);
    const el = h('div', { class: 'achv-pop', role: 'status' },
      h('div', { class: 'achv-medal' }, achvIcon(a ? a[3] : 'M12 2l3 6.5 7 .8-5.2 4.8 1.4 7L12 17.6 5.8 21.1l1.4-7L2 9.3l7-.8z')),
      h('div', null, h('small', null, 'Достижение'), h('b', null, a ? a[1] : `Открыто достижений: ${it[1]}`), h('span', null, a ? a[2] : 'Загляните в «Сводку» → «Достижения».')));
    el.addEventListener('click', () => { el.remove(); openDiag('achv'); });
    document.body.append(el);
    if (S.mascot === true) Pet.cheer(a ? `Ура! «${a[1]}»` : 'Сколько всего открыто!');
    setTimeout(() => { el.classList.add('out'); setTimeout(() => { el.remove(); next(); }, 400); }, 4200);
  };
  next();
}
function renderAchv() {
  const box = $('#achvBox');
  if (!box) return;
  const c = achvCtx(), got = S.achv || {};
  const n = ACHV.filter(a => got[a[0]]).length;
  box.replaceChildren(
    h('p', { class: 'sub' }, `Открыто ${n} из ${ACHV.length}. Значки только для вас — Naivka никуда их не отправляет.`),
    h('div', { class: 'achv-grid' }, ACHV.map(([id, title, desc, icon, test]) => {
      const at = got[id];
      const r = !at && test ? test(c) : null;
      const prog = Array.isArray(r) ? Math.min(1, r[0] / r[1]) : 0;
      return h('div', { class: 'achv' + (at ? ' got' : '') },
        h('div', { class: 'achv-medal' }, achvIcon(icon)),
        h('div', { class: 'achv-text' }, h('b', null, title), h('span', null, desc),
          at ? h('small', null, 'открыто ' + new Date(at * 1000).toLocaleDateString(LOCALE))
            : prog > 0 ? h('div', { class: 'achv-bar', title: Math.round(prog * 100) + '%' }, h('i', { style: `width:${(prog * 100).toFixed(1)}%` })) : h('small', null, 'ещё не открыто')));
    })));
}

// ---------- глобус трафика («Сводка» → «Сейчас») ----------
const REGION = (() => { try { return new Intl.DisplayNames([LOCALE], { type: 'region' }); } catch (_) { return null; } })();
const countryName = cc => (REGION && REGION.of(cc)) || cc;
function renderGlobe() {
  const box = $('#globeBox');
  if (!box) { Globe.unmount(); return; }
  Globe.mount(box, { onPick: globePick, onSpin: () => unlockAchv('globe') });
  const on = phase() === 'on';
  const ends = mapEnds();
  // Страны сайтов: по каждому пути отдельно (через сервер / напрямую / zapret), сайты — сервисами.
  const flows = new Map();
  if (on) for (const t of (STATS && STATS.top) || []) {
    if (!t.cc || !COUNTRY_POS[t.cc] || t.route === 'telegram') continue;
    const route = t.route === 'direct' || t.route === 'zapret' ? t.route : 'proxy';
    const k = route + ':' + t.cc;
    const f = flows.get(k) || { cc: t.cc, route, ll: COUNTRY_POS[t.cc], name: countryName(t.cc), speed: 0, sites: new Map() };
    f.speed += t.speed || 0;
    if (t.host && !/^[\d.:[\]]+$/.test(t.host)) {
      const base = baseDomain(t.host), sv = serviceOf(base), key = sv ? sv.name : base;
      const x = f.sites.get(key) || { key, label: sv ? sv.name : displayDomain(base), domains: [], procs: [], routes: {}, speed: 0, total: 0 };
      if (!x.domains.includes(base)) x.domains.push(base);
      x.routes[route] = (x.routes[route] || 0) + (t.speed || 0) + 1;
      x.speed += t.speed || 0; x.total += t.total || 0;
      f.sites.set(key, x);
    }
    flows.set(k, f);
  }
  const list = [...flows.values()].map(f => ({ ...f, sites: [...f.sites.values()].sort((a, b) => b.speed - a.speed).map(x => ({ ...x, say: ROUTE_SAY[f.route] })) }))
    .sort((a, b) => b.speed - a.speed).slice(0, 24);
  Globe.update({ me: ends.me, srv: ends.srv, flows: list, on });
  const note = $('#globeNote');
  if (note) note.textContent = !on ? 'появится после подключения' : STATS && !STATS.geoReady ? 'загружаю базу стран…' : list.length ? count(new Set(list.map(f => f.cc)).size, 'страна', 'страны', 'стран') : 'пока тихо';
  patch('globeLegend', String(on), () => [
    ...[['proxy', 'через сервер'], ['direct', 'напрямую'], ['zapret', 'через zapret']].map(([k, t]) => h('span', null, h('i', { style: `background:${ROUTE_COLOR[k]}` }), t)),
    h('small', null, 'Линии идут от вас — через ваш сервер или напрямую — в страну, где стоят сайты; стрелки показывают направление. Наведите на страну — её путь подсветится. Щелчок по стране — её сайты и выбор пути. Крутите мышью, колесо — масштаб, двойной щелчок — к вам.')]);
}
// Щелчок по стране: один сайт — сразу меню пути, несколько — сначала список сайтов.
function globePick(flow, rect) {
  let a = $('#globeAnchor');
  if (!a) { a = h('span', { id: 'globeAnchor', class: 'globe-anchor', 'aria-hidden': 'true' }); document.body.append(a); }
  Object.assign(a.style, { left: rect.left + 'px', top: rect.top + 'px', width: rect.width + 'px', height: rect.height + 'px' });
  const pickSite = (x, anchor) => {
    branchLeaves[x.key] = x;
    const rule = siteRule(x);
    sitePick({ key: x.key, label: x.label, current: rule ? rule.action : 'auto' }, anchor);
  };
  if (flow.sites.length === 1) { pickSite(flow.sites[0], a); return; }
  closeRoutePop();
  const pop = h('div', { class: 'route-pop', role: 'menu', 'aria-label': flow.name },
    h('b', null, flow.name), h('small', null, `${ROUTE_SAY[flow.route]} · выберите сайт`),
    ...flow.sites.slice(0, 8).map(x => h('button', { role: 'menuitem', onclick: () => {
      const r = pop.getBoundingClientRect();
      Object.assign(a.style, { left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: '0px' });
      pickSite(x, a);
    } }, h('i', { style: `background:${ROUTE_COLOR[flow.route]}` }), x.label, h('span', { class: 'num', style: 'margin-left:auto;color:var(--muted);font-size:12px' }, x.speed >= 1024 ? fmtSpeed(x.speed) : ''))));
  document.body.append(pop);
  const pw = pop.offsetWidth, ph = pop.offsetHeight;
  let x = Math.min(Math.max(8, rect.left), innerWidth - pw - 8), y = rect.bottom + 6;
  if (y + ph > innerHeight - 8) y = Math.max(8, rect.top - ph - 6);
  pop.style.left = x + 'px'; pop.style.top = y + 'px';
  routePop = pop;
  pop.querySelector('button').focus();
}

// Каким путём группа идёт сейчас (по самому быстрому трафику).
const groupRoute = g => Object.entries(g.routes).sort((a, b) => b[1] - a[1])[0][0];
function liveRow({ key, label, icon, route, rule, sub, pick, drop }) {
  const say = rule ? ROUTE_SAY[rule.action] || 'блокируется' : route ? ROUTE_SAY[route] : 'как решит Naivka';
  const el = h('button', { class: 'live-row', type: 'button', 'data-key': key, style: `--c:${ROUTE_COLOR[rule ? rule.action : route] || 'var(--faint)'}`,
    'aria-label': `${label}: ${say}. Нажмите, чтобы выбрать путь` },
    h('span', { class: 'svc' }, s('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' }, s('path', { d: icon }))),
    h('span', { class: 'live-name' }, h('b', null, label), h('small', null, h('i', { class: 'live-dot' }), say + (rule ? ' · правило' : '') + (sub ? ' · ' + sub : ''))),
    h('span', { class: 'live-speed num' }));
  // Как у программ на полке: короткое нажатие — меню, перетаскивание — на ветку схемы.
  ghostDrag(el, () => h('div', { class: 'proc-chip proc-ghost' }, label), () => pick(el), drop);
  return el;
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

const ROUTE_STYLE = { direct: ['direct', 'напрямую'], zapret: ['zapret', 'через zapret'], telegram: ['tg', 'tg-ws-proxy'] };

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
    h('div', { class: 'fact' }, h('b', { class: health === 'fail' || health === 'offline' ? 'delay bad' : '' }, !running ? '—' : d && d.ms ? `${d.ms} мс` : health === 'offline' ? 'нет интернета' : health === 'fail' ? 'нет связи' : '…'),
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

function renderOverviewLive() { guard(renderOverviewLiveNow, 'renderOverviewLive'); }
function renderOverviewLiveNow() {
  renderLive();
  renderGlobe();
  renderTiles();
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
  renderTiles();
}
// ---------- диагностика ----------
function routeOf(c) {
  const chains = c.chains || [];
  const srv = chains.find(t => t.startsWith('s-') || t.startsWith('w-'));
  if (srv) { const sv = serverByTag(srv); return { cls: 'proxy', text: sv ? sv.name : srv }; }
  if (chains.includes('direct')) return { cls: 'direct', text: 'Напрямую' };
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
        h('section', { class: 'card globecard mt' },
          h('div', { class: 'card-head' }, h('h3', null, 'Куда идёт трафик'), h('small', { id: 'globeNote' })),
          h('div', { class: 'globe-box', id: 'globeBox' }),
          h('div', { class: 'globe-legend', id: 'globeLegend' })));
      renderOverviewLive();
      renderIP();
    } else if (diagTab === 'conns') {
      body.replaceChildren(
        h('p', { class: 'sub' }, 'Что сейчас открыто и каким маршрутом идёт.'),
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
    } else if (diagTab === 'achv') {
      body.replaceChildren(h('div', { id: 'achvBox' }));
      renderAchv();
      refreshAchvTraffic();
    } else {
      const box = h('pre', { class: 'log', id: 'logBox' }, logBuf.map(logLine));
      body.replaceChildren(
        h('div', { class: 'row', style: 'margin:0 0 12px' },
          h('label', { class: 'check', style: 'padding:0' }, h('input', { type: 'checkbox', id: 'logFollow', checked: true }), h('span', null, 'Прокручивать к новым')),
          h('span', { class: 'spacer' }),
          h('button', { class: 'btn small', onclick: async () => { try { await navigator.clipboard.writeText(logBuf.join('\n')); toast('Журнал скопирован'); } catch (e) { toast('Не удалось скопировать', true); } } }, 'Скопировать'),
          h('button', { class: 'btn small', onclick: () => { logBuf = []; box.replaceChildren(); } }, 'Очистить окно'),
          h('button', { class: 'btn small', title: 'Состояние, мешающие программы, настройки и журнал одним файлом — пароли, ключи и адреса серверов в него не попадают. В журнале могут остаться адреса открытых сайтов: посмотрите файл перед отправкой.', onclick: async () => {
            try { const r = await api('/api/report'); download(r.name, r.text, 'text/plain'); toast('Отчёт сохранён в «Загрузки»'); } catch (e) { toast(e.message, true); }
          } }, 'Собрать отчёт')),
        box);
      setTimeout(() => { box.scrollTop = box.scrollHeight; });
      refreshLogs();
    }
  };
  // Пять вкладок; журнал — кнопкой справа (нужен при сбоях), достижения — кнопкой в меню слева.
  const title = h('h1', null, '');
  const pick = v => { diagTab = v; sessionStorage.setItem('naivka-diag', v); render(); mark(); };
  const tabs = segmented([['now', 'Сейчас'], ['conns', 'Соединения'], ['traffic', 'Трафик'], ['check', 'Проверить сайт'], ['speed', 'Скорость']],
    () => diagTab, pick, 'Раздел диагностики');
  const logBtn = h('button', { class: 'btn small diag-log', type: 'button', title: 'Журнал Naivka и ядра; там же — «Собрать отчёт»', onclick: () => pick('logs') }, svgIcon('doc'), 'Журнал');
  const mark = () => { logBtn.setAttribute('aria-pressed', String(diagTab === 'logs')); title.textContent = diagTab === 'achv' ? 'Достижения' : 'Сводка'; };
  render(); mark();
  return h('div', { class: 'view', style: 'max-width:none' }, h('div', { class: 'title-row' }, title,
    h('div', { class: 'row' }, tabs, logBtn)), body);
}

function viewCheck() {
  const input = h('input', { type: 'text', class: 'grow-input', placeholder: 'например, youtube.com или ссылка на страницу', spellcheck: 'false', value: lastCheck ? lastCheck.host : '' });
  const out = h('div', { id: 'checkOut' });
  const run = async () => {
    const target = input.value.trim();
    if (!target) { input.focus(); return; }
    btn.disabled = true; btn.textContent = 'Проверяю…';
    out.replaceChildren(h('p', { class: 'hint mt' }, 'Проверяю… это займёт до 15 секунд.'));
    try { lastCheck = await api('/api/check', { target }); renderCheck(out, lastCheck); }
    catch (e) { out.replaceChildren(h('p', { class: 'err-text mt' }, e.message)); }
    finally { btn.disabled = false; btn.textContent = 'Проверить'; }
  };
  input.addEventListener('keydown', e => { if (e.key === 'Enter') run(); });
  const btn = h('button', { class: 'btn primary', onclick: run }, 'Проверить');
  if (lastCheck) renderCheck(out, lastCheck);
  return h('div', null,
    h('p', { class: 'sub' }, 'Не открывается сайт? Naivka проверит, заблокирован ли он и как его открыть.'),
    h('div', { class: 'row' }, input, btn), out,
    leakBlock());
}

// ---------- проверка утечек ----------
// WebRTC (звонки в браузере) может выдать настоящий адрес мимо прокси: проверяем прямо в окне —
// оно на том же Chromium, что Chrome и Edge. DNS проверяет Naivka через сервис bash.ws.
async function webrtcIPs() {
  if (typeof RTCPeerConnection === 'undefined') return null;
  const ips = new Set();
  let pc;
  try {
    pc = new RTCPeerConnection({ iceServers: [{ urls: ['stun:stun.l.google.com:19302', 'stun:stun.cloudflare.com:3478'] }] });
    pc.createDataChannel('leak');
    pc.onicecandidate = e => {
      const m = e.candidate && / ([0-9.]+|[0-9a-f:]+) \d+ typ srflx/i.exec(e.candidate.candidate);
      if (m) ips.add(m[1]);
    };
    await pc.setLocalDescription(await pc.createOffer());
    await new Promise(r => setTimeout(r, 4000));
  } catch (_) { /* WebRTC выключен — утечки через него нет */ }
  finally { try { pc && pc.close(); } catch (_) { /* не важно */ } }
  return [...ips];
}

let lastLeak = null;
function leakBlock() {
  const out = h('div');
  const btn = h('button', { class: 'btn', onclick: async () => {
    btn.disabled = true; btn.textContent = 'Проверяю…';
    out.replaceChildren(h('p', { class: 'hint mt' }, 'Проверяю DNS и WebRTC… это займёт до 20 секунд.'));
    try {
      const [r, rtc] = await Promise.all([api('/api/leaktest', {}), webrtcIPs()]);
      lastLeak = { ...r, rtc };
      renderLeak(out, lastLeak);
    } catch (e) { out.replaceChildren(h('p', { class: 'err-text mt' }, e.message)); }
    finally { btn.disabled = false; btn.textContent = 'Проверить утечки'; }
  } }, 'Проверить утечки');
  if (lastLeak) renderLeak(out, lastLeak);
  return h('div', { class: 'mt' },
    h('h2', null, 'Утечки'),
    h('p', { class: 'sub' }, 'Не выдаёт ли что-то ваш настоящий адрес или список сайтов мимо сервера: DNS-запросы и WebRTC в браузере.'),
    h('div', { class: 'row' }, btn), out);
}

function renderLeak(out, r) {
  const real = r.direct && r.direct.ip;
  const exit = r.exit && r.exit.ip;
  const items = [];
  if (!r.running) items.push(notice('info', 'Naivka не подключена — проверка покажет, что видно без неё. Подключитесь и проверьте снова.'));
  // DNS
  if (r.error) items.push(notice('warn', 'DNS: ' + r.error));
  else if (!r.resolvers.length) items.push(notice('warn', 'DNS: сервис проверки не увидел ни одного запроса. Попробуйте ещё раз.'));
  else {
    const list = h('ul', { class: 'leak-list' }, r.resolvers.map(x => h('li', { class: x.leak ? 'bad' : '' },
      h('code', null, x.ip), ` — ${x.country}${x.asn ? ', ' + x.asn : ''}`, x.leak ? h('b', null, ' — DNS вашего провайдера') : null)));
    if (!r.dnsLeak) items.push(notice('ok', h('span', null, h('b', null, 'DNS: утечки нет. '), 'Адреса сайтов узнаёт не ваш провайдер.'), null), list);
    else items.push(notice('error', h('span', null, h('b', null, 'DNS: утечка. '),
      r.running ? (r.mode === 'tun'
        ? 'Часть запросов уходит к DNS провайдера. Проверьте «DNS через сервер» в настройках и правила «Напрямую».'
        : 'Часть запросов уходит к DNS провайдера. Включите режим «Весь трафик (TUN)»: тогда Naivka перехватит DNS всех программ.')
        : 'Без подключения адреса сайтов узнаёт провайдер — так и должно быть.')), list);
  }
  // WebRTC
  if (r.rtc === null) items.push(notice('ok', h('span', null, h('b', null, 'WebRTC: '), 'выключен — через него ничего не утекает.')));
  else if (!r.rtc.length) items.push(notice('ok', h('span', null, h('b', null, 'WebRTC: '), 'внешний адрес не виден.')));
  else {
    const leaked = r.running && real && r.rtc.includes(real);
    items.push(notice(leaked ? 'error' : 'ok', h('span', null, h('b', null, leaked ? 'WebRTC: утечка. ' : 'WebRTC: утечки нет. '),
      leaked ? `Браузер через WebRTC показывает сайтам ваш настоящий адрес ${real}` + (exit ? ` вместо ${exit}` : '') + '. ' +
        (r.mode === 'tun' ? 'Включите «UDP через сервер» в «Особых случаях» (нужен сервер VLESS, Hysteria2, TUIC, Trojan или Shadowsocks) или поставьте в браузер расширение WebRTC Leak Prevent.'
          : 'В режиме «Системный прокси» звонки в браузере идут мимо него. Включите режим «Весь трафик (TUN)» или поставьте в браузер расширение WebRTC Leak Prevent.')
        : `Виден адрес ${r.rtc.join(', ')}` + (r.running ? ' — это не ваш настоящий.' : ' — без подключения это ваш обычный адрес.'))));
  }
  out.replaceChildren(h('div', { class: 'mt' }, ...items));
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
  // Сделать правилом можно любой путь; рабочие подсвечены, рекомендованный — главной кнопкой.
  const rule = act => async () => {
    const res = mergeRules(S.rules, [{ kind: 'domain', value: r.host, action: act }]);
    save(0);
    if (await saveNow()) toast(res.added ? `${displayDomain(r.host)} — ${ROUTE_SAY[act]}. ${ruleNote(act)}` : 'Действие правила изменено');
  };
  const works = { direct: r.direct.ok && !r.zapretCovers, zapret: r.zapretOn && r.zapretCovers && r.direct.ok, proxy: r.proxy.ok };
  const best = r.suggest || (works.zapret ? 'zapret' : works.direct ? 'direct' : works.proxy ? 'proxy' : '');
  const actions = ['zapret', 'proxy', 'direct'].filter(k => k !== 'zapret' || r.zapretOn).map(k =>
    h('button', { class: 'btn' + (k === best ? ' primary' : ''), onclick: rule(k) }, `Всегда ${ROUTE_SAY[k]}`));
  const zapretCard = h('div', { class: 'card probe' }, h('div', { class: 'hint' }, 'Через zapret'),
    !r.zapretOn ? h('div', { class: 'cell-sub', style: 'margin-top:6px' }, 'zapret выключен')
      : r.zapretCovers ? h('div', { style: 'margin-top:4px' }, r.direct.ok ? h('b', { style: 'color:var(--warn)' }, `открывается · ${r.direct.ms} мс`) : h('b', { style: 'color:var(--danger)' }, 'не помог'))
        : h('div', { class: 'cell-sub', style: 'margin-top:6px' }, 'сайта нет в списках zapret — добавьте правилом «Всегда через zapret», чтобы попробовать'));
  out.replaceChildren(
    h('div', { class: 'verdict' }, r.verdict),
    h('div', { class: 'check-grid' },
      probeCard('Напрямую', r.direct),
      zapretCard,
      probeCard('Через сервер', r.proxy),
      h('div', { class: 'card probe' }, h('div', { class: 'hint' }, 'Сейчас идёт'), h('div', { style: 'margin:4px 0' }, routeTag),
        h('div', { class: 'cell-sub trunc', title: r.rule || '' }, r.rule ? 'правило: ' + r.rule : (r.routeKind ? 'без особого правила, по режиму' : '')))),
    h('div', { class: 'row mt' }, ...actions));
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

// Страна сервера по его названию — пока нет данных о его IP: флаг в начале названия или название страны.
const COUNTRY_NAMES = { NL: 'нидерланд|голланд|netherland|holland|amsterdam', FI: 'финлянд|finland|helsinki', DE: 'германи|germany|frankfurt|berlin', PL: 'польш|poland|warsaw',
  LV: 'латви|latvia|riga', LT: 'литв|lithuania|vilnius', EE: 'эстони|estonia|tallinn', SE: 'швеци|sweden|stockholm', NO: 'норвеги|norway|oslo', US: 'сша|америк|usa|united states|new york',
  FR: 'франци|france|paris', GB: 'великобритан|англи|britain|england|london|united kingdom|\\buk\\b', TR: 'турци|turkey|türkiye|istanbul', KZ: 'казахстан|kazakhstan|almaty',
  JP: 'япони|japan|tokyo', SG: 'сингапур|singapore', CH: 'швейцари|switzerland|zurich', AT: 'австри|austria|vienna', CZ: 'чехия|czech|prague', IT: 'итали|italy|milan',
  ES: 'испани|spain|madrid', CA: 'канад|canada|toronto', RO: 'румыни|romania', BG: 'болгари|bulgaria', HU: 'венгри|hungary', MD: 'молдов|moldova', AM: 'армени|armenia',
  GE: 'грузия|georgia|tbilisi', RS: 'серби|serbia', IL: 'израил|israel', AE: 'оаэ|эмират|emirates|dubai', HK: 'гонконг|hong kong', RU: 'росси|russia|москв|moscow',
  IE: 'ирланди|ireland|dublin', PT: 'португали|portugal|lisbon', DK: 'дания|denmark|copenhagen', BE: 'бельги|belgium', IS: 'исланди|iceland', CY: 'кипр|cyprus', GR: 'греци|greece',
  IN: 'индия|india', BR: 'бразили|brazil', AU: 'австрали|australia', KR: 'корея|korea|seoul', UA: 'украин|ukraine', BY: 'беларус|belarus' };
function nameCountry(name) {
  const f = splitFlag(name || '');
  if (f.cc) return f.cc;
  const n = (name || '').toLowerCase();
  for (const [cc, re] of Object.entries(COUNTRY_NAMES)) if (new RegExp(re).test(n)) return cc;
  return '';
}

function delayEl(id) {
  const d = delayFor(id);
  if (!d) return null;
  if (d.pending) return h('span', { class: 'delay cell-sub' }, 'проверяю…');
  if (d.error) return h('span', { class: 'delay bad' }, d.error);
  const cls = d.ms < 300 ? 'good' : d.ms < 800 ? 'mid' : 'bad';
  return h('span', { class: 'delay ' + cls, title: d.kind === 'tls' ? 'TLS-рукопожатие без подключения' : 'Реальный запрос через сервер' }, `${d.ms} мс` + (d.kind === 'tls' ? ' TLS' : ''));
}

function viewServers() {
  return h('div', { class: 'view' },
    h('div', { class: 'title-row' }, h('h1', null, 'Серверы'),
      h('div', { class: 'row' },
        h('button', { class: 'btn', id: 'testAll', onclick: () => testServers(''), title: 'Проверить пинг всех серверов' }, 'Проверить все'),
        h('button', { class: 'btn', onclick: importLinksDialog }, 'Вставить ссылки'),
        h('button', { class: 'btn primary', onclick: () => editServer(null) }, 'Добавить сервер'))),
    h('div', { class: 'scards', id: 'srvList', role: 'radiogroup', 'aria-label': 'Сервер' }),
    h('p', { class: 'hint mt' }, 'Карточки можно перетаскивать, звёздочка поднимает сервер наверх.'),
    h('div', { id: 'wlBox' }));
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
      const cc = (geo && geo.countryCode) || nameCountry(sv.name);
      const where = geo ? [geo.city || geo.country, geo.isp].filter(Boolean).join(', ') : '';
      const hist = STATS && STATS.delays && STATS.delays[sv.id];
      const off = noNaive && sv.type === 'naive';
      const d = delayFor(sv.id);
      const note = off ? 'Naive не поддерживается этим ядром'
        : sv.transport === 'xhttp' && !xr ? 'нужен Xray — установите его в «Настройках» → «О программе»' : where;
      const star = h('button', {
        class: 'star' + (sv.fav ? ' on' : ''), type: 'button', title: sv.fav ? 'Убрать из избранного' : 'В избранное',
        'aria-label': sv.fav ? 'Убрать из избранного' : 'В избранное', 'aria-pressed': sv.fav ? 'true' : 'false',
        onclick: () => { sv.fav = !sv.fav; save(); renderServers(true); },
      }, s('svg', { viewBox: '0 0 24 24' }, s('path', { d: 'M12 3.5l2.6 5.3 5.9.9-4.2 4.1 1 5.8-5.3-2.8-5.3 2.8 1-5.8-4.2-4.1 5.9-.9z' })));
      cards.push(srvCardBase(sv.id, '', [
        h('span', { class: 'scard-grip', title: 'Перетащите, чтобы поменять порядок', 'aria-hidden': 'true' }, '⋮⋮'),
        h('div', { class: 'scard-top' },
          h('span', { class: 'scard-flag' }, cc ? flag(cc, true) : h('span', { class: 'scard-ico' }, s('svg', { viewBox: '0 0 24 24' }, s('path', { d: ICONS.globe })))),
          h('div', { class: 'scard-title' }, h('b', { title: sv.name }, sv.name),
            h('small', { title: `${sv.host}:${sv.port}` }, h('span', { class: 'proto' }, protoName(sv)))),
          // Действия — значками, появляются при наведении: карточка компактнее, адрес — во всплывающей подсказке.
          h('div', { class: 'scard-tools' },
            h('button', { class: 'tool', type: 'button', title: 'Проверить', 'aria-label': 'Проверить', onclick: () => testServers(sv.id) }, btnIcon('test')),
            h('button', { class: 'tool', type: 'button', title: 'QR-код', 'aria-label': 'QR-код', onclick: () => qrDialog(sv) }, btnIcon('qr')),
            h('button', { class: 'tool', type: 'button', title: 'Изменить', 'aria-label': 'Изменить', onclick: () => editServer(sv) }, btnIcon('edit'))),
          star),
        h('div', { class: 'scard-mid' },
          h('div', { class: 'scard-delay' }, pingBars(d), delayEl(sv.id) || h('span', { class: 'delay cell-sub' }, 'не проверен')),
          nowTag === 's-' + sv.id ? h('span', { class: 'live-tag' }, 'сейчас') : null,
          h('div', { class: 'scard-spark' }, spark(hist, 130, 26))),
        note ? h('div', { class: 'scard-sub trunc', title: note }, note) : null],
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
  // Naivka проверяет все серверы сразу; ответы забираем каждые 0,4 с и показываем, как только они готовы,
  // а не после самого медленного.
  try {
    await api('/api/test', { id, async: true });
    const left = new Set(ids);
    const t0 = Date.now();
    while (left.size && Date.now() - t0 < 20000) {
      await new Promise(r => setTimeout(r, 400));
      const v = await api('/api/test/results');
      for (const r of v.done) if (left.has(r.id)) { delays[r.id] = { ms: r.ms, error: r.error, kind: r.kind }; left.delete(r.id); }
      renderServers();
    }
    left.forEach(i => { delete delays[i]; });
  } catch (e) { ids.forEach(i => { if (delays[i] && delays[i].pending) delete delays[i]; }); toast(e.message, true); }
  if (btn) btn.disabled = false;
  renderServers(); updateStatus();
}

async function qrDialog(sv) {
  await saveNow();
  let link;
  try { link = (await api('/api/link?id=' + encodeURIComponent(sv.id))).link; } catch (e) { toast(e.message, true); return; }
  const dlg = openDialog(
    h('h2', null, `${sv.name}: QR-код`),
    h('p', { class: 'hint' }, 'Отсканируйте в приложении на телефоне. В коде пароль сервера — не показывайте его посторонним.'),
    h('div', { class: 'qr-box' }, qrSvg(link)),
    h('div', { class: 'link-box' }, link),
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: async () => { try { await navigator.clipboard.writeText(link); toast('Ссылка скопирована'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Скопировать ссылку'),
      h('button', { class: 'btn primary', autofocus: true, onclick: () => dlg.close() }, 'Готово')));
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
  // Маскировка начала соединения: для серверов на TLS поверх TCP (Reality, TLS, Trojan, AnyTLS).
  const fragSel = () => h('div', null,
    sel('fragment', 'Маскировка начала соединения', [['', 'нет'], ['record', 'лёгкая'], ['full', 'сильная']]),
    h('p', { class: 'hint' }, 'Если провайдер обрывает соединение с этим сервером. Лёгкая почти не замедляет; сильная — на крайний случай.'));
  const insecureBox = () => box('insecure', 'Не проверять сертификат', 'Только для своего сервера с самоподписанным сертификатом: иначе соединение можно перехватить.');
  const renderFields = () => {
    if (d.type === 'trojan' && d.transport && d.transport !== 'tcp') {
      fields.replaceChildren(
        h('div', { class: 'grid2' }, inp('name', 'Название')),
        fragSel(),
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
        h('p', { class: 'hint mt' }, 'Где-то провайдеры режут UDP — держите рядом сервер Reality.'));
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
        null);
    } else if (d.type === 'trojan') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды Trojan' }),
          inp('host', 'Домен сервера', { placeholder: 'tr.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'SNI, отпечаток, маскировка',
          h('div', { class: 'grid2' }, inp('sni', 'SNI', { placeholder: 'как домен' }), inp('fingerprint', 'Отпечаток браузера', { placeholder: 'chrome' }), inp('alpn', 'ALPN', { placeholder: 'h2,http/1.1' })),
          fragSel(),
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
        h('p', { class: 'hint mt' }, 'Обычный Shadowsocks блокируют чаще других: выбирайте шифры 2022 и держите рядом сервер Reality.'));
    } else if (d.type === 'anytls') {
      fields.replaceChildren(
        h('div', { class: 'grid2' },
          inp('name', 'Название', { placeholder: 'Нидерланды AnyTLS' }),
          inp('host', 'Домен сервера', { placeholder: 'any.example.com' }),
          inp('port', 'Порт', { type: 'number' }),
          inp('password', 'Пароль', { type: 'password' })),
        more('Дополнительно', 'SNI, отпечаток, маскировка',
          h('div', { class: 'grid2' }, inp('sni', 'SNI', { placeholder: 'как домен' }), inp('fingerprint', 'Отпечаток браузера', { placeholder: 'chrome' })),
          fragSel(),
          insecureBox()),
        h('p', { class: 'hint mt' }, 'Нужен сервер на sing-box 1.12 или новее.'));
    } else if (d.type === 'vless' && ((d.security && d.security !== 'reality') || (d.transport && d.transport !== 'tcp'))) {
      fields.replaceChildren(
        h('div', { class: 'grid2' }, inp('name', 'Название')),
        d.security !== 'none' && d.transport !== 'xhttp' ? fragSel() : null,
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
        more('Дополнительно', 'SNI, отпечаток, маскировка',
          h('div', { class: 'grid2' }, inp('sni', 'SNI', { placeholder: 'как домен' }), inp('fingerprint', 'Отпечаток браузера', { placeholder: 'chrome' })),
          fragSel()));
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
        if (!ask(`Удалить сервер «${d.name}»?`)) return;
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
        const at = S.servers.findIndex(x => x.id === d.id);
        if (isNew || at < 0) S.servers.push(d); else S.servers[at] = d;
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
        h('p', { style: 'margin:6px 0 0' }, 'Naivka подключится к серверу по SSH с правами root и изменит его настройки. На сервере, где уже работает что-то важное (сайт, другой VPN, панель управления), установка может ему помешать.'))),
      h('h3', { class: 'mt' }, 'Что может быть сделано на сервере (вы выберете на следующем шаге)'),
      h('ul', { class: 'steps' },
        h('li', null, 'sing-box из официального репозитория SagerNet (apt): VLESS + Reality на TCP 443 или 8443 и, по желанию, Hysteria2 на UDP. Нужен Debian 11+ или Ubuntu 20.04+.'),
        h('li', null, 'NaiveProxy: Caddy с модулем forwardproxy — готовая сборка автора NaiveProxy (проверяется по контрольной сумме) или сборка из исходников прямо на сервере. Нужен свой домен; Caddy займёт порты 80 и 443 и получит сертификат Let\'s Encrypt.'),
        h('li', null, 'Веб-панель 3X-UI или s-ui — их официальными установщиками с GitHub.'),
        h('li', null, 'Файл /etc/sing-box/config.json будет перезаписан — прежний сохранится рядом с пометкой .bak. Службы включатся в автозапуск; если работает ufw, в нём откроются нужные порты; по желанию — BBR.')),
      h('h3', null, 'Что останется как было'),
      h('ul', { class: 'steps' },
        h('li', null, 'SSH, пароли, пользователи, другие программы и их порты.'),
        h('li', null, 'Пароль и ключ SSH Naivka не сохраняет — они нужны только на время установки (и потом — чтобы открыть панель).')),
      h('p', { class: 'hint' }, 'Перед вводом пароля Naivka покажет отпечаток ключа сервера — сверьте его с панелью хостинга или консолью. Скрипты, которые выполнятся на сервере, можно прочитать заранее. Откатить протоколы: на сервере bash install-server.sh --remove; панели удаляются их же меню (x-ui uninstall, s-ui uninstall).'),
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
      box('hy2', 'Ещё и Hysteria2 (UDP)', 'Запасной протокол: быстрый на плохом мобильном интернете, но где-то UDP за границу режут. Свой сертификат, Naivka проверяет его по отпечатку.'),
      box('bbr', 'Включить BBR', 'Обычно ускоряет загрузку через далёкий сервер. Меняет настройку ядра сервера.'));

    // панель
    const panelBody = h('div');
    const renderPanel = () => {
      if (!f.panel) { panelBody.replaceChildren(); return; }
      const agree = h('input', { type: 'checkbox', checked: !!f.panelConfirm, onchange: e => { f.panelConfirm = e.target.checked; } });
      panelBody.replaceChildren(
        f.panel === '3x-ui'
          ? choices('panelAccess', [
            ['tunnel', 'Только через Naivka (рекомендуется)', 'Панель слушает только сам сервер — из интернета её не найти и не взломать. Naivka открывает её через SSH-туннель (спросит пароль SSH).'],
            ['public', 'Из интернета по HTTPS', 'Откроется по адресу https://IP:порт/секретный-путь с сертификатом Let\'s Encrypt на IP (нужен свободный порт 80). Удобно с телефона, но панель видна всему интернету — её будут пробовать подобрать.'],
          ], () => f.panelAccess, v => { f.panelAccess = v; })
          : h('p', { class: 'hint' }, 's-ui работает без HTTPS, поэтому открывается только через Naivka по SSH-туннелю — из интернета её не видно.'),
        h('div', { class: 'notice warn mt' }, h('div', { class: 'grow' },
          h('b', null, 'Панель — сторонняя программа с правами root.'),
          h('ul', { class: 'steps', style: 'margin-bottom:0' },
            h('li', null, `Ставится официальный установщик ${PANEL_TITLE[f.panel]} с GitHub (последний релиз): его код выполняется на сервере от root. Naivka не может за него ручаться.`),
            f.panel === '3x-ui' ? h('li', null, 'Установщик 3X-UI заодно ставит fail2ban и cron для обновления сертификатов.') : null,
            h('li', null, 'Логин и длинный пароль панели Naivka придумает сама и сохранит у вас в зашифрованном виде.'),
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
      box('protocols', 'Поставить протоколы Naivka', 'Снимите, если нужна только панель.', renderProto),
      protoBody,
      h('h3', { class: 'mt' }, 'Веб-панель (необязательно)'),
      h('p', { class: 'hint' }, 'Панель нужна, чтобы раздавать доступ друзьям и ставить лимиты. Для себя одного не нужна.'),
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
    h('p', { class: 'sub' }, 'Naivka сама поставит протоколы и панель управления на ваш VPS по SSH (Debian или Ubuntu) и добавит готовые серверы в список.'),
    h('div', { class: 'card own-hero' },
      h('div', { class: 'own-protos' },
        proto('VLESS Reality', 'основной: маскируется под чужой сайт, ставится всегда'),
        proto('Hysteria2', 'быстрый запасной по UDP'),
        proto('NaiveProxy', 'HTTPS как у браузера, нужен свой домен'),
        proto('Панель 3X-UI или s-ui', 'пользователи, лимиты, подписки — по желанию')),
      h('div', { class: 'row mt' },
        h('button', { class: 'btn primary', disabled: !can, onclick: () => installServerDialog() }, 'Начать установку'),
        h('span', { class: 'hint' }, can ? 'Перед началом Naivka покажет, что именно сделает на сервере, и попросит подтвердить.' : 'Недоступно в этой сборке (Windows 7).'))),
    h('h2', null, 'Друзья'),
    h('p', { class: 'sub' }, 'Отдельный вход на ваш сервер для каждого: друга можно приостановить, продлить или убрать, не трогая остальных и себя.'),
    h('div', { id: 'friendsBox' }),
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
  if (ST && ST.install) patch('friendsBox', JSON.stringify([S.ownHosts || [], FRIENDS]), friendsBox);
  patch('panelsBox', JSON.stringify(S.panels || []), () => (S.panels || []).length ? panelsBox()
    : h('div', { class: 'empty' }, 'Панелей пока нет. Её можно поставить вместе с протоколами — отметьте панель в окне установки.'));
}

// ---------- друзья ----------
// Список друзей живёт на сервере; здесь — последний полученный (до перезапуска Naivka).
const FRIENDS = {}; // id своего сервера → { list, err, busy }
const FRIEND_DAYS = [[7, 'неделя'], [30, 'месяц'], [90, '3 месяца'], [365, 'год'], [0, 'бессрочно']];
const friendLeft = f => {
  if (f.paused) return ['paused', 'на паузе'];
  if (f.expired) return ['expired', 'срок истёк'];
  if (!f.until) return ['ok', 'бессрочно'];
  const days = Math.ceil((f.until * 1000 - Date.now()) / 864e5);
  return ['ok', days <= 1 ? 'до завтра' : `ещё ${count(days, 'день', 'дня', 'дней')}`];
};

function friendsBox() {
  const hosts = S.ownHosts || [];
  const addOld = h('button', { class: 'btn small', onclick: () => ownHostDialog() }, 'Указать сервер, поставленный раньше');
  if (!hosts.length) return h('div', { class: 'empty' },
    h('p', null, 'Друзей можно завести на сервере, на который протоколы ставила Naivka. Поставьте их кнопкой выше — сервер появится здесь сам.'), addOld);
  return h('div', { class: 'friends' }, hosts.map(hostCard), h('div', { class: 'row mt' }, addOld));
}

function hostCard(o) {
  const st = FRIENDS[o.id];
  const head = h('div', { class: 'friends-head' },
    h('div', { class: 'grow' }, h('div', { class: 'cell-main' }, o.name || o.host), h('div', { class: 'cell-sub' }, `${o.host} · SSH ${o.sshUser}@${o.sshPort}`)),
    st && st.list ? h('button', { class: 'btn small primary', onclick: () => inviteDialog(o) }, 'Пригласить друга') : null,
    h('button', { class: 'btn small', disabled: st && st.busy, onclick: () => friendsCall(o, { action: 'list' }) }, st && st.list ? 'Обновить' : 'Открыть список'),
    h('button', { class: 'tool', title: 'Убрать сервер из списка', 'aria-label': 'Убрать сервер из списка', onclick: async () => {
      if (!ask(`Убрать ${o.name || o.host} из списка? Друзья на сервере останутся и продолжат работать.`)) return;
      api('/api/friends/close', { host: o.id }).catch(() => {});
      delete FRIENDS[o.id];
      S.ownHosts = S.ownHosts.filter(x => x.id !== o.id); save(0); await saveNow(); renderOwn();
    } }, btnIcon('close')));
  let body = null;
  if (st && st.busy && !st.list) body = h('p', { class: 'hint' }, 'Подключаюсь к серверу…');
  else if (st && st.err) body = h('div', { class: 'err-text' }, st.err);
  else if (st && st.list && !st.list.length) body = h('div', { class: 'empty' }, 'Друзей пока нет. Нажмите «Пригласить друга» — Naivka создаст ему отдельный вход и покажет ссылки и QR-код.');
  else if (st && st.list) body = h('div', { class: 'panel' }, h('table', { class: 'table' }, h('tbody', null, st.list.map(f => {
    const [kind, left] = friendLeft(f);
    return h('tr', null,
      h('td', null, h('div', { class: 'cell-main' }, f.name), h('div', { class: 'cell-sub' }, h('span', { class: 'friend-state ' + kind }, left),
        f.until && !f.paused ? ' · до ' + new Date(f.until * 1000).toLocaleDateString('ru-RU') : '')),
      h('td', { class: 'r' }, h('div', { class: 'row end', style: 'flex-wrap:nowrap' },
        h('button', { class: 'btn small', disabled: !f.links.length, onclick: () => friendLinks(o, f) }, 'Ссылки'),
        f.paused
          ? h('button', { class: 'btn small', disabled: st.busy, onclick: () => friendsCall(o, { action: 'resume', fid: f.id }, `${f.name}: доступ снова открыт`) }, 'Возобновить')
          : h('button', { class: 'btn small', disabled: st.busy, onclick: () => friendsCall(o, { action: 'pause', fid: f.id }, `${f.name}: доступ приостановлен`) }, 'Пауза'),
        h('button', { class: 'btn small', disabled: st.busy, onclick: () => renewDialog(o, f) }, 'Продлить'),
        h('button', { class: 'btn small', disabled: st.busy, onclick: () => {
          if (!ask(`Удалить вход ${f.name}? Его ссылки перестанут работать сразу.`)) return;
          friendsCall(o, { action: 'remove', fid: f.id }, `${f.name}: вход удалён`);
        } }, 'Удалить'))));
  }))));
  return h('div', { class: 'card friends-card' }, head, body);
}

// friendsCall — команда серверу; без открытого соединения сначала спросит пароль или ключ SSH.
async function friendsCall(o, req, done, creds) {
  const st = FRIENDS[o.id] || (FRIENDS[o.id] = {});
  st.busy = true; st.err = ''; renderOwn();
  try {
    const r = await api('/api/friends', { host: o.id, ...req, ...(creds || {}) });
    if (r.needAuth) { st.busy = false; renderOwn(); return sshAuthDialog(o, c => friendsCall(o, req, done, c)); }
    st.list = r.friends; st.busy = false; renderOwn();
    if (done) toast(done);
    return r.friends;
  } catch (e) {
    st.busy = false;
    if (creds) { renderOwn(); throw e; } // ошибку покажет окно с паролем
    st.err = e.message; renderOwn(); toast(e.message, true);
  }
}

function sshAuthDialog(o, run) {
  return new Promise(resolve => {
    const pass = h('input', { type: 'password', placeholder: 'Пароль SSH', autocomplete: 'current-password' });
    const key = h('textarea', { placeholder: 'или закрытый ключ SSH', spellcheck: 'false', style: 'min-height:70px' });
    const phrase = h('input', { type: 'password', placeholder: 'пароль ключа (если есть)' });
    const err = h('div', { class: 'err-text' });
    const go = h('button', { class: 'btn primary' }, 'Подключиться');
    const d = openDialog(h('h2', null, `Вход на ${o.name || o.host}`),
      h('p', { class: 'hint' }, `Naivka подключится к ${o.host} по SSH. Пароль не сохраняется; соединение закроется через 20 минут без действий.`),
      pass, h('details', { class: 'more' }, h('summary', null, h('span', null, 'Вход по ключу')), h('div', { class: 'more-body' }, key, phrase)), err,
      h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Отмена'), go));
    d.addEventListener('close', () => resolve(null));
    const submit = async () => {
      err.textContent = ''; go.disabled = true; go.textContent = 'Подключаюсь…';
      try { const r = await run({ password: pass.value, key: key.value, passphrase: phrase.value }); d.close(); resolve(r); }
      catch (e) { err.textContent = e.message; go.disabled = false; go.textContent = 'Подключиться'; }
    };
    go.addEventListener('click', submit);
    pass.addEventListener('keydown', e => { if (e.key === 'Enter') submit(); });
    pass.focus();
  });
}

function daysSelect(def) {
  return h('select', null, FRIEND_DAYS.map(([v, t]) => h('option', { value: v, selected: v === def }, t)));
}

function inviteDialog(o) {
  const name = h('input', { type: 'text', placeholder: 'Например, Петя', maxlength: 40, spellcheck: 'false' });
  const days = daysSelect(30);
  const err = h('div', { class: 'err-text' });
  const go = h('button', { class: 'btn primary' }, 'Создать вход');
  const d = openDialog(h('h2', null, 'Пригласить друга'),
    h('p', { class: 'hint' }, `На ${o.name || o.host} появится отдельный вход со своими ключами. Через него идёт трафик друга — по вашему серверу и вашему трафику у хостера.`),
    h('div', { class: 'grid2' },
      h('label', { class: 'field' }, h('span', null, 'Имя'), name),
      h('label', { class: 'field' }, h('span', null, 'Срок'), days, h('small', { class: 'hint' }, 'Когда срок выйдет, сервер сам закроет вход. Продлить можно в любой момент.'))),
    err, h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Отмена'), go));
  const submit = async () => {
    if (!name.value.trim()) { err.textContent = 'Введите имя'; name.focus(); return; }
    err.textContent = ''; go.disabled = true; go.textContent = 'Создаю…';
    const before = new Set(((FRIENDS[o.id] || {}).list || []).map(f => f.id));
    const list = await friendsCall(o, { action: 'add', name: name.value.trim(), days: parseInt(days.value, 10) });
    go.disabled = false; go.textContent = 'Создать вход';
    if (!list) { err.textContent = (FRIENDS[o.id] || {}).err || ''; return; }
    d.close();
    const f = list.find(x => !before.has(x.id));
    if (f) { friendLinks(o, f, true); unlockAchv('friend'); }
  };
  go.addEventListener('click', submit);
  name.addEventListener('keydown', e => { if (e.key === 'Enter') submit(); });
  name.focus();
}

function renewDialog(o, f) {
  const days = daysSelect(30);
  const d = openDialog(h('h2', null, `Продлить: ${f.name}`),
    h('p', { class: 'hint' }, 'Срок считается с сегодняшнего дня. Если вход был на паузе — он снова откроется.'),
    h('label', { class: 'field' }, h('span', null, 'Новый срок'), days),
    h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Отмена'),
      h('button', { class: 'btn primary', onclick: () => { d.close(); friendsCall(o, { action: 'renew', fid: f.id, days: parseInt(days.value, 10) }, `${f.name}: срок обновлён`); } }, 'Продлить')));
}

const linkProto = l => l.startsWith('vless:') ? 'Reality' : l.startsWith('hysteria2:') ? 'Hysteria2' : l.startsWith('naive') ? 'NaiveProxy' : l.split(':')[0];

function friendLinks(o, f, fresh) {
  let cur = 0;
  const qr = h('div', { class: 'qr-box' }), text = h('div', { class: 'link-box' });
  const tabs = h('div', { class: 'seg' });
  const show = () => {
    qr.replaceChildren(qrSvg(f.links[cur])); text.textContent = f.links[cur];
    tabs.replaceChildren(...f.links.map((l, i) => h('button', { 'aria-pressed': String(i === cur), onclick: () => { cur = i; show(); } }, linkProto(l))));
  };
  const copy = async v => { try { await navigator.clipboard.writeText(v); toast('Скопировано'); } catch (_) { toast('Не удалось скопировать', true); } };
  const d = openDialog(h('h2', null, fresh ? `Готово: вход «${f.name}»` : `Ссылки: ${f.name}`),
    h('p', { class: 'hint' }, 'Отправьте другу все ссылки — он вставит их в Naivka («Серверы» → «Вставить ссылки») или отсканирует QR-код в Hiddify, v2rayNG, sing-box на телефоне. Ссылки — это ключи: не публикуйте их.'),
    f.links.length > 1 ? tabs : null, qr, text,
    h('div', { class: 'row end mt' },
      h('button', { class: 'btn', onclick: () => copy(f.links[cur]) }, 'Скопировать эту'),
      h('button', { class: 'btn', onclick: () => copy(f.links.join('\n')) }, 'Скопировать все'),
      h('button', { class: 'btn primary', autofocus: true, onclick: () => d.close() }, 'Готово')));
  show();
}

// Сервер, поставленный до версии, которая запоминает свои серверы: адрес + подтверждённый ключ.
function ownHostDialog() {
  const f = { host: '', port: 22, user: 'root', name: '' };
  let fp = '';
  const err = h('div', { class: 'err-text' }), fpBox = h('div');
  const addBtn = h('button', { class: 'btn primary', disabled: true }, 'Добавить');
  const inp = (key, label, type) => h('label', { class: 'field' }, h('span', null, label),
    h('input', { type: type || 'text', value: f[key], spellcheck: 'false', autocomplete: 'off', oninput: e => { f[key] = e.target.value; if (key !== 'name') { fp = ''; fpBox.replaceChildren(); addBtn.disabled = true; } } }));
  const check = async () => {
    err.textContent = ''; fpBox.replaceChildren(h('p', { class: 'hint' }, 'Получаю ключ сервера…'));
    try {
      const r = await api('/api/server/hostkey', { host: f.host, port: parseInt(f.port, 10) || 22 });
      fp = r.fingerprint;
      const agree = h('input', { type: 'checkbox', onchange: e => { addBtn.disabled = !e.target.checked; } });
      fpBox.replaceChildren(h('div', { class: 'card mt' }, h('p', null, 'Отпечаток ключа сервера (', r.type, '):'), h('code', { class: 'fp' }, fp),
        h('p', { class: 'hint' }, 'Сверьте его с выводом на сервере: ', h('code', null, 'ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub'), '. Не совпадает — не продолжайте.'),
        h('label', { class: 'check' }, agree, h('span', null, h('b', null, 'Отпечаток совпадает — это мой сервер')))));
    } catch (e) { fpBox.replaceChildren(); err.textContent = e.message; }
  };
  addBtn.addEventListener('click', async () => {
    try {
      const r = await api('/api/ownhost', { host: f.host, port: parseInt(f.port, 10) || 22, user: f.user, name: f.name, fingerprint: fp });
      S = r.settings; d.close(); renderOwn(); toast('Сервер добавлен');
    } catch (e) { err.textContent = e.message; }
  });
  const d = openDialog(h('h2', null, 'Сервер, поставленный раньше'),
    h('p', { class: 'hint' }, 'Подойдёт сервер, на который протоколы ставила Naivka (вкладка «Свой сервер»). Пароль SSH здесь не нужен — его спросят при открытии списка друзей.'),
    h('div', { class: 'grid2' }, inp('host', 'IP или домен сервера'), inp('port', 'Порт SSH', 'number'), inp('user', 'Пользователь'), inp('name', 'Название (необязательно)')),
    h('div', { class: 'row' }, h('button', { class: 'btn', onclick: check }, 'Проверить ключ сервера')), fpBox, err,
    h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Отмена'), addBtn));
}

function panelsBox() {
  const list = S.panels || [];
  if (!list.length) return null;
  return h('div', null,
    h('div', { class: 'panel' }, h('table', { class: 'table' }, h('tbody', null, list.map(p => h('tr', null,
      h('td', null, h('div', { class: 'cell-main' }, PANEL_TITLE[p.type] || p.type, p.version ? h('span', { class: 'ver' }, ' ', p.version) : null),
        h('div', { class: 'cell-sub' }, p.host + ' · ' + (p.access === 'public' ? 'из интернета по HTTPS' : 'через SSH-туннель'))),
      h('td', { class: 'r' }, h('div', { class: 'row end', style: 'flex-wrap:nowrap' },
        h('button', { class: 'btn small', onclick: () => openPanel(p) }, 'Открыть'),
        h('button', { class: 'btn small', onclick: () => panelCreds(p) }, 'Логин и пароль'),
        h('button', { class: 'btn small', onclick: async () => {
          if (!ask(`Убрать панель ${PANEL_TITLE[p.type]} на ${p.host} из списка? На сервере она останется.`)) return;
          S.panels = S.panels.filter(x => x.id !== p.id); save(0); await saveNow(); renderServers(); renderOwn();
        } }, 'Убрать')))))))),
    h('p', { class: 'hint' }, 'Панель откроется в браузере через защищённый SSH-туннель.'));
}

function panelCreds(p) {
  const row = (label, value) => h('div', { class: 'row', style: 'flex-wrap:nowrap' }, h('span', { class: 'hint', style: 'min-width:80px' }, label), h('code', { class: 'fp grow' }, value),
    h('button', { class: 'btn small', onclick: async () => { try { await navigator.clipboard.writeText(value); toast('Скопировано'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Копировать'));
  const d = openDialog(h('h2', null, `${PANEL_TITLE[p.type]} на ${p.host}`),
    row('Логин', p.user), row('Пароль', p.pass),
    p.access === 'public' ? row('Адрес', p.url) : h('p', { class: 'hint' }, `Открывается только через Naivka.`),
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
    h('p', { class: 'hint' }, `Naivka подключится к ${p.host} по SSH и откроет панель в браузере. Пароль SSH не сохраняется.`),
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
    h('p', { class: 'hint' }, 'По одной в строке: vless://, hysteria2://, naive+https:// и другие.'),
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
  // Разделы — отдельными блоками: заголовок, пояснение, кнопки — каждый на своей строке, ничего не наезжает.
  const section = (title, sub, ...tools) => h('div', { class: 'sec-head' },
    h('h2', { class: 'flush' }, title),
    sub ? h('p', { class: 'sub', id: sub.id || null }, sub.text || '') : null,
    tools.length ? h('div', { class: 'row sec-tools' }, ...tools) : null);
  return h('div', { class: 'view' },
    h('h1', null, 'Правила'),
    h('p', { class: 'sub' }, 'Что идёт через сервер, а что напрямую. Свои правила работают сразу, режим и списки — после переподключения.'),

    h('section', { class: 'card rules-card' },
      h('h2', { class: 'flush' }, 'Режим'),
      segmented(Object.entries(ROUTING), () => S.routing, v => { S.routing = v; save(); $('#modeDesc').textContent = MODE_DESC[v]; renderSmart(); }, 'Режим'),
      h('p', { class: 'hint mode-desc', id: 'modeDesc' }, MODE_DESC[S.routing]),
      h('div', { id: 'smartBox' })),

    // Особые случаи — сразу на виду, а не в свёрнутом блоке.
    h('section', { class: 'card rules-card' },
      h('h2', { class: 'flush' }, 'Особые случаи'),
      h('div', { class: 'toggle-grid' },
        check('ruInsideDirect', 'Сайты, которые работают только из России, всегда напрямую', 'Госуслуги, банки и другие сервисы, которые не пускают иностранные IP.'),
        check('blockQuic', 'Блокировать QUIC', 'Нужно для серверов Naive.'),
        check('udpDirect', 'Остальной UDP напрямую', 'Игры и звонки — мимо сервера.'),
        check('udpProxy', 'UDP через сервер — голос Discord, игры', 'Для серверов VLESS, Hysteria2, TUIC, Trojan и Shadowsocks. Работает в режиме TUN.'),
        check('adBlock', 'Блокировать рекламу и трекеры', 'Баннеры, счётчики и рекламные сети. Рекламу в видео YouTube так не убрать.'))),

    h('section', { class: 'card rules-card' },
      h('h2', { class: 'flush' }, 'Куда идут соединения'),
      h('div', { id: 'catBox' })),

    section('Свои правила', { id: 'ruleSummary' },
      h('button', { class: 'btn primary', onclick: () => addRulesDialog('') }, 'Добавить'),
      h('button', { class: 'btn', onclick: pickProcesses }, 'Выбрать программы'),
      h('button', { class: 'btn', onclick: importRulesFile }, 'Импорт'),
      h('button', { class: 'btn', onclick: exportRulesDialog }, 'Экспорт')),
    h('div', { id: 'ruleSearch' }),
    h('div', { class: 'panel' }, h('table', { class: 'table rules' }, h('tbody', { id: 'ruleBody' }))),
    h('div', { id: 'ruleFoot' }),

    section('Списки по ссылке', { text: 'Готовые списки сайтов обновляются сами раз в сутки.' },
      h('button', { class: 'btn', onclick: addListDialog }, 'Добавить список'),
      h('button', { class: 'btn', id: 'listRefreshBtn', onclick: refreshListsNow }, 'Обновить')),
    h('div', { id: 'listBox' }),

    h('div', { class: 'mt-lg' }),
    more('Правила sing-box вручную', S.extraRules.trim() ? 'есть свои правила' : 'для опытных',
      h('p', { class: 'hint' }, 'JSON-массив для route.rules, вставляется сразу после ваших правил. Можно ссылаться на outbound proxy, direct, auto и на свои outbounds из настроек.'),
      jsonArea('extraRules', '[\n  { "domain_keyword": ["openai"], "outbound": "proxy" }\n]', true)));
}

// ---------- списки по ссылке ----------
const LIST_PRESETS = [
  ['Заблокированное в России (itdoginfo)', 'https://github.com/itdoginfo/allow-domains/releases/latest/download/russia_inside.srs', 'proxy',
    'Сайты, недоступные из России: их — через сервер.'],
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
    box.replaceChildren(h('div', { class: 'empty' }, 'Списков нет. Добавьте готовый или свой по ссылке.'));
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
        if (!ask(`Удалить список «${l.name}»?`)) return;
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
    h('p', { class: 'hint' }, 'Ссылка на список сайтов. Добавляйте только списки из источников, которым доверяете: список решает, куда пойдёт ваш трафик.'),
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
  'global': 'Весь трафик через сервер, кроме локальной сети и ваших правил «Напрямую».',
};

const CATS = [
  ['mine', 'Ваши правила', 'var(--warn)'],
  ['blocked', 'Списки блокировок — через сервер', 'var(--tunnel)'],
  ['ru', 'Российские сайты — напрямую', 'var(--direct)'],
  ['inside', 'Только из России — напрямую', 'color-mix(in srgb, var(--direct) 60%, var(--ink))'],
  ['downloads', 'Загрузки игр и обновлений — напрямую', 'color-mix(in srgb, var(--direct) 80%, var(--warn))'],
  ['smart', 'Автоопределение — напрямую', 'color-mix(in srgb, var(--direct) 70%, var(--accent))'],
  ['smartp', 'Автоопределение — через сервер', 'color-mix(in srgb, var(--tunnel) 70%, var(--accent))'],
  ['final', 'Остальное — по режиму', 'var(--faint)'],
];

function ruleCategory(rule) {
  if (rule === 'final') return 'final';
  if (/inbound=\[?probe/.test(rule) || rule.includes('ip_is_private') || rule.includes('network=udp') || rule.includes('protocol=dns')) return null;
  if (rule.includes('steamcontent.com')) return 'downloads';
  if (rule.includes('smart-direct')) return 'smart';
  if (rule.includes('smart-proxy')) return 'smartp';
  if (rule.includes('ru-blocked') || rule.includes('geoblock')) return 'blocked';
  if (rule.includes('ru-inside')) return 'inside';
  if (rule.includes('category-ru') || rule.includes('geoip-ru') || /domain_suffix=\[?ru\b/.test(rule)) return 'ru';
  return 'mine';
}

// ---------- автоопределение ----------
let SMART = null;
async function renderSmart() {
  const box = $('#smartBox');
  if (!box) return;
  if (S.routing !== 'ru-direct') { box.replaceChildren(); return; }
  const parts = [check('downloadsDirect', 'Загрузки игр и обновлений — сразу напрямую',
    'Steam, Epic Games, обновления Windows. Они доступны из России, а через сервер большая закачка занимает весь канал до него — растёт пинг и тормозит остальной интернет.'),
  check('smartDirect', 'Автоопределение: что открывается из России — напрямую',
    'Незнакомый сайт сначала идёт через сервер, а Naivka в фоне проверяет, открывается ли он напрямую так же хорошо. Если да — дальше он идёт напрямую, без потери скорости на сервере; если начнёт обрываться — вернётся на сервер. Уже открытые соединения при этом не рвутся. Для проверки адрес сайта открывается напрямую — провайдер видит его имя, даже если сайт потом пойдёт через сервер.',
    renderSmart)];
  if (S.smartDirect) {
    try { SMART = await api('/api/smart'); } catch (_) { /* окно переподключится */ }
    const v = SMART || { direct: 0, proxy: 0, recent: [] };
    const forget = async host => { try { SMART = await api('/api/smart/forget', { host }); } catch (e) { toast(e.message, 'error'); } renderSmart(); };
    const rows = v.recent.map(x => h('tr', null,
      h('td', null, x.host.startsWith('.') ? '*' + displayDomain(x.host) : displayDomain(x.host)),
      h('td', null, h('span', { class: 'tag' }, x.direct ? 'напрямую' : 'через сервер')),
      h('td', { class: 'r' }, h('button', { class: 'btn small', title: 'Проверить заново, когда сайт откроют', onclick: () => forget(x.host) }, 'Забыть'))));
    parts.push(more('Что определилось', `напрямую ${v.direct}, через сервер ${v.proxy}`,
      rows.length
        ? h('div', { class: 'panel' }, h('table', { class: 'table' }, h('tbody', null, ...rows)))
        : h('p', { class: 'hint' }, 'Пока пусто: сайты появятся здесь, когда вы начнёте их открывать.'),
      h('p', { class: 'hint' }, 'Ваши правила, zapret, заблокированное в России и сервисы, закрытые для России, автоопределение не трогает. Нужно, чтобы сайт всегда шёл определённым путём, — добавьте правило.'),
      h('div', { class: 'row mt' }, h('button', { class: 'btn', onclick: () => { if (ask('Забыть всё, что определилось? Сайты будут проверены заново.')) forget(''); } }, 'Сбросить всё'))));
  }
  box.replaceChildren(...parts);
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
    h('div', { class: 'cat-list' }, CATS.filter(([k]) => sum[k]).map(([k, label, col]) =>
      h('div', { class: 'cat-row' }, h('i', { style: 'background:' + col }), h('span', null, label), h('b', { class: 'num' }, String(sum[k]))))),
    h('p', { class: 'hint', style: 'margin-top:8px' }, `${count(total, 'соединение', 'соединения', 'соединений')} за сеанс.`));
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
  // Таблицу (до 300 строк) пересобираем, только когда изменились правила, поиск или счётчики —
  // а не каждую секунду: иначе терялся клик по «×» и фокус, а английский перевод шёл заново.
  const tableSig = JSON.stringify([S.rules, q, visible.slice(0, LIMIT).map(([r]) => hitsOf(r))]);
  const same = body.dataset.sig === tableSig;
  body.dataset.sig = tableSig;
  if (same) { /* без изменений */ } else if (!S.rules.length) {
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
        if (!ask(`Удалить все ${count(S.rules.length, 'правило', 'правила', 'правил')}? Перед этим можно сделать экспорт.`)) return;
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
      ? `${count(data.json.rules.length, 'правило', 'правила', 'правил')}. Файл JSON можно импортировать обратно в Naivka. Текст удобно читать и править: строки в квадратных скобках задают действие.`
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
    return notice('warn', 'Для TUN нужны права администратора. Включите автозапуск — тогда Naivka будет стартовать с правами без запроса.',
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
  out.push(h('p', { class: 'hint mt' }, (ST.portable ? 'Портативный режим. Данные: ' : 'Данные: ') + ST.dataDir));
  return out;
}

// Темы — плитками с маленьким видом каждой: длинное название не переносится внутри переключателя.
const THEMES = [['dark', 'Обычная', 'Спокойная, с вашим акцентом'], ['gold', 'Чёрное с золотом', 'Тонкие заглавные буквы, огни от трафика'], ['blocks', 'Пиксели', 'Ступеньки, клетки, пиксельный шрифт'], ['custom', 'Своя', 'Ваши цвета и шрифт']];
function themeTiles() {
  const box = h('div', { class: 'theme-tiles', role: 'radiogroup', 'aria-label': 'Тема' });
  const paint = () => box.querySelectorAll('.theme-tile').forEach(b => b.setAttribute('aria-checked', b.dataset.theme === (S.theme || 'dark') ? 'true' : 'false'));
  for (const [k, name, sub] of THEMES) {
    const prev = h('span', { class: 'theme-prev', 'aria-hidden': 'true' }, h('i'), h('i'), h('i'));
    if (k === 'custom') paintCustomPrev(prev);
    box.append(h('button', { class: 'theme-tile', type: 'button', role: 'radio', 'data-theme': k, onclick: () => { S.theme = k; save(); applyTheme(); paint(); renderCustomEditor(); } },
      prev,
      h('b', null, name), h('small', null, sub)));
  }
  paint();
  return box;
}

function paintCustomPrev(el) {
  const c = S.custom || CUSTOM_DEFAULT;
  el.style.background = c.bg;
  el.style.setProperty('--p1', c.ink); el.style.setProperty('--p2', c.panel); el.style.setProperty('--p3', parseHex(S.accent) || ACCENT_DEFAULT);
}

// Редактор темы «Своя»: виден, пока она выбрана. Меняется сразу, сохраняется, когда цвет выбран.
function customEditor() { return h('div', { id: 'customEditor', class: 'custom-editor' }); }
function renderCustomEditor() {
  const box = $('#customEditor');
  if (!box) return;
  if (S.theme !== 'custom') { box.replaceChildren(); return; }
  S.custom = { ...CUSTOM_DEFAULT, ...(S.custom || {}) };
  const notes = h('div', { class: 'custom-notes' });
  const refresh = () => {
    applyTheme();
    notes.replaceChildren(...customNotes(S.custom, S.accent).map(t => h('p', { class: 'hint warn-text' }, t)));
    const prev = document.querySelector('.theme-tile[data-theme="custom"] .theme-prev');
    if (prev) paintCustomPrev(prev);
  };
  const color = (key, label, hint) => {
    const pick = h('input', { type: 'color', value: S.custom[key].toLowerCase(), 'aria-label': label });
    const text = h('input', { type: 'text', value: S.custom[key], maxlength: 7, spellcheck: 'false', 'aria-label': label + ' (HEX)' });
    const set = v => { const x = parseHex(v); if (!x) return false; S.custom[key] = x; pick.value = x.toLowerCase(); text.value = x; refresh(); return true; };
    pick.addEventListener('input', () => set(pick.value));
    pick.addEventListener('change', () => save());
    text.addEventListener('change', () => { if (set(text.value)) save(); else text.value = S.custom[key]; });
    return h('label', { class: 'custom-color' }, pick, h('span', null, h('b', null, label), h('small', null, hint)), text);
  };
  const font = h('select', { 'aria-label': 'Шрифт', onchange: e => { S.custom.font = e.target.value; refresh(); save(); } },
    Object.entries(CUSTOM_FONTS).map(([k, [name]]) => h('option', { value: k, selected: (S.custom.font || '') === k }, name)));
  const presets = h('div', { class: 'custom-presets' }, h('span', { class: 'hint' }, 'Начать с:'),
    CUSTOM_PRESETS.map(([name, c, acc]) => h('button', { class: 'custom-preset', type: 'button', style: `--a:${c.bg};--b:${c.panel};--c:${acc};--d:${c.direct};--e:${c.ink}`,
      onclick: () => { S.custom = { ...c }; S.accent = acc; applyTheme(); save(); renderCustomEditor(); renderAccentPicker(); toast(`Тема «${name}» — меняйте под себя`); } },
      h('i'), name)));
  const share = async () => { try { await navigator.clipboard.writeText(themeCode()); toast('Код темы скопирован — отправьте его другу'); } catch (_) { toast('Не удалось скопировать', true); } };
  const paste = () => {
    const ta = h('textarea', { placeholder: THEME_CODE + '…', spellcheck: 'false', style: 'min-height:80px' });
    const err = h('div', { class: 'err-text' });
    const d = openDialog(h('h2', null, 'Вставить код темы'), h('p', { class: 'hint' }, 'Код темы — строка, которую даёт кнопка «Поделиться» в чужой Naivka. В нём только цвета и шрифт.'), ta, err,
      h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Отмена'), h('button', { class: 'btn primary', onclick: () => {
        try {
          const r = parseThemeCode(ta.value);
          S.custom = r.custom; if (r.accent) S.accent = r.accent; S.theme = 'custom';
          applyTheme(); save(); renderCustomEditor(); renderAccentPicker(); d.close(); toast('Тема применена');
        } catch (e) { err.textContent = e.message; }
      } }, 'Применить')));
    ta.focus();
  };
  box.replaceChildren(
    h('h3', null, 'Своя тема'),
    presets,
    h('div', { class: 'custom-grid' },
      color('bg', 'Фон', 'окно и меню'),
      color('panel', 'Панели', 'карточки и списки'),
      color('ink', 'Текст', 'остальные оттенки — из него'),
      color('direct', '«Напрямую»', 'ветка и графики'),
      h('label', { class: 'custom-color' }, h('span', { class: 'custom-font-ic', 'aria-hidden': 'true' }, 'Aa'), h('span', null, h('b', null, 'Шрифт'), h('small', null, 'системные шрифты Windows')), font)),
    h('p', { class: 'hint' }, '«Через сервер» — это акцент ниже. Фото или видео фоном работают и со своей темой.'),
    notes,
    h('div', { class: 'row' }, h('button', { class: 'btn small', onclick: share }, 'Поделиться кодом'), h('button', { class: 'btn small', onclick: paste }, 'Вставить код'),
      h('button', { class: 'btn small', onclick: () => { S.custom = { ...CUSTOM_DEFAULT }; applyTheme(); save(); renderCustomEditor(); } }, 'Сбросить')));
  refresh();
}

const FIT_HINT = {
  cover: 'На весь экран, лишнее по краям обрезается.',
  contain: 'Картинка видна полностью, свободные края — её размытая копия.',
  fill: 'На весь экран без обрезки — пропорции могут исказиться.',
};
// Фон окна: своё фото или видео. Пока файла нет — поле «перетащите или выберите»; есть — миниатюра,
// ползунки затемнения и размытия (видно сразу, на настоящем фоне) и переключатели.
function wallBox() {
  const box = h('div', { class: 'wall-box', id: 'wallBox' });
  const input = h('input', { type: 'file', accept: 'image/jpeg,image/png,image/webp,image/gif,video/mp4,video/webm', hidden: true });
  const upload = async file => {
    if (!file) return;
    if (file.size > 80 * 1024 * 1024) { toast('Файл больше 80 МБ — возьмите поменьше', true); return; }
    box.classList.add('busy');
    try {
      const res = await fetch('/api/wallpaper', { method: 'POST', headers: { 'X-Naivka-Token': TOKEN, 'Content-Type': 'application/octet-stream' }, body: file });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || 'Не удалось загрузить');
      S.wall = data;
      await Wall.apply(S, TOKEN);
      toast(data.kind === 'video' ? 'Видео поставлено фоном' : data.ext === '.gif' ? 'GIF поставлен фоном' : 'Фото поставлено фоном');
    } catch (e) { toast(e.message, true); }
    finally { box.classList.remove('busy'); render(); }
  };
  input.addEventListener('change', () => { upload(input.files[0]); input.value = ''; });
  const slider = (key, label, max, unit, min = 0) => {
    const out = h('b', { class: 'num' }, S.wall[key] + unit);
    const r = h('input', { type: 'range', min, max, step: 1, value: S.wall[key], 'aria-label': label,
      oninput: () => { S.wall[key] = +r.value; out.textContent = r.value + unit; Wall.apply(S, TOKEN); },
      onchange: () => save() });
    return h('label', { class: 'wall-slider' }, h('span', null, label), r, out);
  };
  const flag = (key, title, desc) => h('label', { class: 'check' },
    h('input', { type: 'checkbox', checked: !!S.wall[key], onchange: e => { S.wall[key] = e.target.checked; save(); Wall.apply(S, TOKEN); } }),
    h('span', null, h('b', null, title), desc && h('small', null, desc)));
  function render() {
    const w = S.wall || {};
    if (!w.kind) {
      const drop = h('button', { class: 'wall-drop', type: 'button', onclick: () => input.click() },
        s('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' }, s('path', { d: 'M4 16l4.5-5 3.5 4 2.5-3L20 16M4 5h16v14H4zM15.5 8.5h.01' })),
        h('b', null, 'Свой фон окна'),
        h('small', null, 'Перетащите сюда фото или видео, или нажмите, чтобы выбрать. JPG, PNG, WEBP, GIF, MP4, WEBM — до 80 МБ.'));
      drop.addEventListener('dragover', e => { e.preventDefault(); drop.classList.add('over'); });
      drop.addEventListener('dragleave', () => drop.classList.remove('over'));
      drop.addEventListener('drop', e => { e.preventDefault(); drop.classList.remove('over'); upload(e.dataTransfer.files[0]); });
      box.replaceChildren(h('h3', null, 'Фон окна'), drop, input);
      return;
    }
    const fitHint = h('p', { class: 'hint wall-fit-hint' }, FIT_HINT[w.fit || 'cover']);
    const thumb = h('div', { class: 'wall-thumb' });
    const u = Wall.url();
    if (u) thumb.append(w.kind === 'video' ? h('video', { src: u, muted: true, autoplay: true, loop: true, playsinline: true }) : h('img', { src: u, alt: '' }));
    box.replaceChildren(
      h('h3', null, 'Фон окна'),
      h('div', { class: 'wall-row' }, thumb,
        h('div', { class: 'wall-side' },
          h('p', { class: 'hint' }, w.kind === 'video' ? 'Видео без звука, по кругу. В свёрнутом окне — на паузе.'
            : w.ext === '.gif' ? 'GIF под интерфейсом. Размер — «Целиком», чтобы гифка была видна полностью.' : 'Фото под интерфейсом. Файл хранится только у вас.'),
          h('div', { class: 'row' },
            h('button', { class: 'btn', onclick: () => input.click() }, 'Заменить'),
            h('button', { class: 'btn', onclick: async () => {
              try { const res = await fetch('/api/wallpaper', { method: 'DELETE', headers: { 'X-Naivka-Token': TOKEN } }); S.wall = await res.json(); }
              catch (e) { toast(e.message, true); }
              Wall.apply(S, TOKEN); render(); toast('Фон убран');
            } }, 'Убрать')))),
      h('div', { class: 'wall-fit' }, h('span', null, 'Размер'),
        segmented([['cover', 'Заполнить'], ['contain', 'Целиком'], ['fill', 'Растянуть']], () => w.fit || 'cover',
          v => { S.wall.fit = v; save(); Wall.apply(S, TOKEN); fitHint.textContent = FIT_HINT[v]; }, 'Размер фона')),
      fitHint,
      slider('scale', 'Масштаб', 200, '%', 50),
      slider('dim', 'Затемнение', 90, '%'),
      slider('blur', 'Размытие', 30, ' px'),
      flag('react', 'Подсвечивать по состоянию', 'Зелёный оттенок — подключено, жёлтый — подключение, красный — ошибка.'),
      S.theme === 'blocks' ? h('p', { class: 'hint' }, 'В теме «Пиксели» фон всегда пиксельный.') : flag('pixel', 'Пиксельный фон', 'Крупные клетки и упрощённые цвета, как в теме «Пиксели».'),
      input);
  }
  render();
  return box;
}

// Акцент: готовые цвета, палитра Windows/Chromium и поле HEX. Пока цвет подбирается, он только
// показывается; сохраняется — когда выбран.
function accentPicker() {
  const cur = () => parseHex(S.accent) || ACCENT_DEFAULT;
  const notes = h('div', { class: 'accent-notes' });
  const text = h('input', { type: 'text', class: 'hex-input', value: cur(), maxlength: '7', spellcheck: 'false', 'aria-label': 'Цвет в формате HEX', placeholder: '#B6E35A' });
  const wheel = h('input', { type: 'color', class: 'hex-wheel', value: cur().toLowerCase(), 'aria-label': 'Выбрать цвет', title: 'Выбрать цвет' });
  const sw = h('div', { class: 'swatches', role: 'radiogroup', 'aria-label': 'Готовые цвета' });
  const show = (hex, keep) => {
    applyAccent(hex);
    if (keep !== 'text') text.value = hex;
    if (keep !== 'wheel') wheel.value = hex.toLowerCase();
    text.removeAttribute('aria-invalid');
    sw.querySelectorAll('.swatch').forEach(b => b.setAttribute('aria-checked', b.dataset.hex === hex ? 'true' : 'false'));
    notes.replaceChildren(...accentNotes(hex).map(t => h('p', { class: 'hint warn-text' }, t)));
  };
  const commit = hex => { S.accent = hex; save(); show(hex); if (S.theme === 'custom') renderCustomEditor(); };
  for (const [hex, name] of ACCENT_PRESETS) {
    const b = h('button', { class: 'swatch', type: 'button', role: 'radio', 'data-hex': hex, title: `${name} ${hex}`, 'aria-label': `${name} ${hex}`, onclick: () => commit(hex) });
    b.style.background = hex;
    sw.append(b);
  }
  text.addEventListener('input', () => { const hex = parseHex(text.value); if (hex) show(hex, 'text'); });
  text.addEventListener('change', () => {
    const hex = parseHex(text.value);
    if (hex) commit(hex);
    else { text.setAttribute('aria-invalid', 'true'); notes.replaceChildren(h('p', { class: 'err-text' }, 'Нужен цвет вида #B6E35A: решётка и шесть знаков 0–9, A–F.')); }
  });
  text.addEventListener('keydown', e => { if (e.key === 'Enter') text.dispatchEvent(new Event('change')); });
  wheel.addEventListener('input', () => show(parseHex(wheel.value), 'wheel'));
  wheel.addEventListener('change', () => commit(parseHex(wheel.value)));
  show(cur());
  return h('div', { class: 'accent-box' },
    h('div', { class: 'accent-row' }, h('span', { class: 'hint' }, 'Акцент'), sw,
      h('label', { class: 'hex-field' }, wheel, text),
      h('button', { class: 'btn small', type: 'button', onclick: () => commit(ACCENT_DEFAULT) }, 'Сбросить')),
    h('p', { class: 'hint' }, 'Этим цветом показан трафик через сервер.'),
    notes);
}

// Акцент сменился не из самого выбора цвета (заготовка или код темы) — перерисовать выбор.
function renderAccentPicker() {
  const old = document.querySelector('.accent-box');
  if (old) old.replaceWith(accentPicker());
}

function viewSettings() {
  const cfgOut = h('textarea', { readonly: true, style: 'min-height:320px', hidden: true });
  return settingsCards(h('div', { class: 'view settings-view' },
    h('h1', null, 'Настройки'),
    h('div', { class: 'set-search' }, s('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' }, s('circle', { cx: 11, cy: 11, r: 7 }), s('path', { d: 'M20 20l-4-4' })),
      h('input', { type: 'search', id: 'setSearch', placeholder: 'Найти настройку: dns, запуск, утечки…', 'aria-label': 'Найти настройку', oninput: e => filterSettings(e.target.value) })),

    h('h2', null, 'Язык · Language'),
    segmented([['ru', 'Русский'], ['en', 'English']], () => S.lang === 'en' ? 'en' : 'ru', async v => {
      S.lang = v;
      save();
      await saveNow();
      try { localStorage.setItem('naivka-lang', v); } catch (_) { /* перезагрузка всё равно возьмёт из настроек */ }
      location.reload();
    }, 'Язык'),

    h('h2', null, 'Внешний вид'),
    themeTiles(),
    customEditor(),
    wallBox(),
    h('p', { class: 'hint gold-only' }, 'У этой темы свои цвета.'),
    h('p', { class: 'hint blocks-only' }, 'У этой темы свои цвета.'),
    accentPicker(),
    h('div', { class: 'oled-check' }, check('oled', 'Чистый чёрный (OLED)', '', applyTheme)),
    check('mascot', 'Талисман', 'Пиксельный серый котёнок внизу меню: спит, когда Naivka отключена, бегает вместе с трафиком, пугается сбоев. Его можно погладить. По умолчанию выключен.', updateStatus),
    window.NAIVKA_HOST ? h('label', { class: 'check' },
      h('input', { type: 'checkbox', checked: S.petHopOff !== true, onchange: e => { S.petHopOff = !e.target.checked; save(); } }),
      h('span', null, h('b', null, 'Котёнок спрыгивает на панель задач'), h('small', null, 'Пока окно Naivka открыто, котёнок иногда спрыгивает с его низа на панель задач Windows, бегает там под окном и запрыгивает обратно. Его можно унести мышью на панель задач другого монитора, а отпустить над окном Naivka — вернуть домой.'))) : null,
    window.NAIVKA_HOST ? check('petTaskbar', 'Котёнок живёт на панели задач', 'Котёнок переселяется из меню на панель задач Windows насовсем: гуляет вдоль неё поверх всех окон, даже когда окно Naivka закрыто, и прячется, когда открыта игра или видео во весь экран. Его можно гладить и переносить мышью, в том числе на другой монитор; щелчки мимо кота проходят к окнам как обычно. Работает, когда талисман включён.', updateStatus) : null,
    check('mascotBeta', 'Новый котёнок (бета)', 'Котёнок из нового набора анимаций: ходит и бегает по-новому, играет с мячиком, прячется в коробку, ест и пьёт. Пока бета — что-то может выглядеть непривычно. Работает, когда талисман включён.', updateStatus),
    check('achievements', 'Достижения', 'Значки за первый гигабайт, неделю без перерыва, свой сервер и другое — в «Сводке».'),
    h('div', { class: 'row mt' }, h('span', { class: 'hint' }, 'Меню'),
      segmented([['list', 'Список'], ['wheel', 'Колесо']], () => S.nav === 'wheel' ? 'wheel' : 'list', v => { S.nav = v; save(); applyTheme(); railMark(); }, 'Меню')),
    h('h2', null, 'Разделы'),
    check('ownServer', 'Свой сервер', 'Установка своего сервера на VPS. Выключите, если не нужно.', applySections),

    window.NAIVKA_HOST ? h('h2', null, 'Окно') : null,
    window.NAIVKA_HOST ? check('trayMini', 'Мини-окно по щелчку в трее', 'Если выключить, щелчок по значку сразу открывает окно.') : null,
    window.NAIVKA_HOST ? check('nativeFrame', 'Классическое окно Windows', 'Применится после перезапуска.') : null,

    h('h2', null, 'Перехват трафика'),
    choices('mode', [
      ['proxy', 'Системный прокси', 'Без прав администратора. Браузеры и большинство программ используют его сами.'],
      ['tun', 'Весь трафик (TUN)', 'Ловит трафик всех программ, включая те, что игнорируют прокси. Нужны права администратора.'],
    ], () => S.mode, v => { S.mode = v; save(); updateStatus(); }),
    h('div', { id: 'adminBox' }),
    window.NAIVKA_HOST ? check('proxyPac', 'Без Naivka — интернет напрямую (PAC)', 'Системный прокси скриптом настройки: если компьютер выключили или Naivka упала, сайты откроются и без неё. Некоторые программы скрипт не понимают и идут мимо сервера — если что-то стало медленнее или отваливается, выключите.') : null,

    h('h2', null, 'Надёжность'),
    check('killSwitch', 'Защита от утечек', 'Если соединение упадёт, трафик не пойдёт мимо сервера.'),
    check('watchdog', 'Следить за соединением', 'Переподключаться после сбоев и смены сети.'),
    check('autoApply', 'Применять изменения сразу', 'Если изменению нужно переподключение, Naivka переподключится сама — без кнопки «Переподключить».'),
    check('confirm', 'Спрашивать перед удалением', 'Подтверждение перед удалением серверов, списков, правил и перед восстановлением копии.'),
    check('failover', 'Переходить на рабочий сервер', 'Если выбранный сервер перестал отвечать — временно работать через другой свой, потом вернуться.'),
    check('sleepReconnect', 'Отключаться перед сном', 'После пробуждения компьютера Naivka подключится снова.'),
    check('notify', 'Уведомления Windows', 'О сбоях, восстановлении связи и смене сервера автовыбором.'),

    h('h2', null, 'Запуск'),
    check('autoStart', 'Запускать вместе с Windows', 'Naivka запустится в трее без окна. Для режима TUN и zapret без запроса Windows — только если папка Naivka лежит в Program Files; иначе Windows спросит разрешение при входе (так подменить программу нельзя).'),
    check('autoConnect', 'Подключаться при запуске Naivka'),
    check('exitWithWindow', 'Закрывать Naivka вместе с окном', 'Иначе при закрытии окна Naivka уходит в трей и соединение остаётся.'),
    check('showIp', 'Показывать мой IP и страну', 'Как вас видят сайты. Узнаётся через ipwho.is и ipinfo.io.', () => { ip.key = ''; srvGeoKey = ''; }),

    h('h2', null, 'Раздача на другие устройства'),
    h('p', { class: 'hint' }, 'Телевизор, приставка или второй компьютер в той же сети смогут пользоваться подключением Naivka как прокси.'),
    h('div', { id: 'shareBox' }),
    h('h2', null, 'Ядро sing-box'),
    h('div', { id: 'coreBox' }),
    check('updateCheck', 'Проверять обновления', 'Устанавливаются только по вашей кнопке.'),

    h('h2', null, 'Резервная копия'),
    h('p', { class: 'hint' }, 'Серверы, правила и настройки в одном файле. Пароль от копии не восстановить — не забудьте его.'),
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

    // «О программе» — в конце: сюда заглядывают редко. Здесь же версии компонентов (раньше были в «Обходе»).
    h('h2', null, 'О программе'),
    h('div', { id: 'selfBox' }),
    h('div', { class: 'row mt' }, h('button', { class: 'btn', onclick: openGuide }, 'Как пользоваться'),
      h('button', { class: 'btn', onclick: openFeedback }, 'Написать автору в Telegram'),
      h('span', { class: 'hint' }, 'Вопросы, ошибки и идеи — @mixazvv.')),
    h('div', { id: 'versionsBox' }, AD ? versionsCard(AD.updates) : null),

    h('div', { class: 'exit-row' },
      h('span', { class: 'hint' }, 'Отключит соединение, вернёт настройки прокси Windows и закроет приложение.'),
      h('button', { class: 'btn danger', onclick: async () => {
        try { await saveNow(); await api('/api/quit', {}); } catch (_) { /* уже закрывается */ }
        setTimeout(() => window.close(), 300);
      } }, 'Выйти из Naivka'))));
}

// Настройки карточками: каждый раздел (заголовок h2 и всё до следующего) — отдельная карточка,
// блоки «Для опытных» собраны в одну. Поиск прячет карточки и строки, где нет искомого.
const SET_ICONS = {
  'О программе': 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 11v5M12 8h.01', 'Язык · Language': 'M4 5h10M9 3v2M6 5c1 4 4 7 8 8M12 5c-1 4-4 7-8 8M14 21l4-9 4 9M15.5 18h5',
  'Внешний вид': 'M12 3a9 9 0 0 0 0 18c1.5 0 2-1 2-2s-1-1.5-1-2.5S14 15 15 15h2a4 4 0 0 0 4-4c0-4.4-4-8-9-8z', 'Разделы': 'M4 5h16v14H4zM9 5v14',
  'Окно': 'M4 5h16v14H4zM4 9h16', 'Перехват трафика': 'M13 3L4 14h7l-1 7 9-11h-7z', 'Надёжность': 'M12 3l7 3v6c0 4-3 7-7 9-4-2-7-5-7-9V6z',
  'Запуск': 'M5 12h14M13 6l6 6-6 6', 'Раздача на другие устройства': 'M5 12a7 7 0 0 1 14 0M8.5 12a3.5 3.5 0 0 1 7 0M12 12v.01',
  'Ядро sing-box': 'M4 7l8-4 8 4v10l-8 4-8-4zM4 7l8 4 8-4M12 11v10', 'Резервная копия': 'M5 4h11l3 3v13H5zM8 4v5h7V4M8 20v-6h8v6',
  'Для опытных': 'M8 8l-4 4 4 4M16 8l4 4-4 4',
};
function settingsCards(view) {
  const grid = h('div', { class: 'set-grid' });
  const mores = h('section', { class: 'set-card' }, h('h2', null, 'Для опытных'));
  let card = null;
  for (const el of [...view.children]) {
    if (el.tagName === 'H1' || el.classList.contains('set-search')) continue;
    if (el.classList.contains('exit-row')) continue;
    if (el.tagName === 'DETAILS') { mores.append(el); continue; }
    if (el.tagName === 'H2') { card = h('section', { class: 'set-card' }); grid.append(card); }
    if (card) card.append(el); else continue;
  }
  if (mores.children.length > 1) grid.append(mores);
  for (const c of grid.children) {
    const t = c.querySelector('h2'), ic = t && SET_ICONS[t.textContent];
    if (ic) t.prepend(h('span', { class: 'set-ic' }, s('svg', { viewBox: '0 0 24 24', 'aria-hidden': 'true' }, s('path', { d: ic }))));
  }
  const exit = view.querySelector(':scope > .exit-row');
  view.querySelector('.set-search').after(grid);
  if (exit) grid.after(exit);
  for (const el of [...view.children]) if (el.tagName !== 'H1' && el !== grid && el !== exit && !el.classList.contains('set-search')) el.remove();
  return view;
}
function filterSettings(q) {
  q = q.trim().toLowerCase();
  // Русские окончания: «утечки» должно найти «утечек», «запуска» — «запуск».
  const stems = q.length >= 5 ? [q, q.slice(0, -1), q.slice(0, -2)] : [q];
  const has = t => stems.some(x => t.includes(x));
  const grid = drawerBody.querySelector('.set-grid');
  if (!grid) return;
  let any = false;
  for (const card of grid.children) {
    const title = (card.querySelector('h2') || {}).textContent || '';
    const titleHit = !q || has(title.toLowerCase());
    let hit = titleHit;
    for (const el of card.children) {
      if (el.tagName === 'H2') continue;
      if (el.tagName === 'DETAILS' && q) el.open = has(el.textContent.toLowerCase()) || el.open;
      const m = titleHit || has(el.textContent.toLowerCase());
      el.hidden = !!q && !m;
      if (m) hit = true;
    }
    card.hidden = !hit;
    if (hit) any = true;
  }
  let empty = drawerBody.querySelector('.set-empty');
  if (!any && !empty) grid.after(h('p', { class: 'set-empty hint' }, 'Ничего не нашлось. Попробуйте другое слово.'));
  if (any && empty) empty.remove();
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
    h('p', { class: 'hint' }, 'В копии все серверы с паролями. С паролем файл зашифрован: открыть его можно только зная пароль.'),
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
    if (!ask('Заменить текущие серверы, правила и настройки содержимым копии?')) return;
    let password = '', advanced = '';
    for (let attempt = 0; ; attempt++) {
      try {
        S = await api('/api/restore', { text, password, advanced });
        applyTheme();
        toast('Настройки восстановлены');
        go('settings');
        return;
      } catch (e) {
        if (e.message === 'needAdvanced') {
          advanced = confirm('В копии другие настройки, через которые трафик можно увести мимо сервера или на чужой: ручные правки конфига, свои правила и списки по ссылке, DNS, защита от утечек, раздача по сети, свои серверы и панели.\n\nВзять их из копии? Нажимайте «ОК», только если копию сделали вы сами. «Отмена» — восстановить всё остальное, а эти настройки оставить свои.') ? 'keep' : 'drop';
          continue;
        }
        if (e.message !== 'needPassword') { toast(e.message, true); return; }
        password = await askBackupPassword(attempt > 0);
        if (password === null) return;
      }
    }
  };
  input.click();
}

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
  $('#rail').addEventListener('wheel', railWheel, { passive: false });
  $('#drawerClose').addEventListener('click', () => go('overview'));
  $('#scrim').addEventListener('click', () => go('overview'));
  $('#feedbackBtn').addEventListener('click', openFeedback);
  $('#achvBtn').addEventListener('click', () => openDiag('achv'));
  $('#helpBtn').addEventListener('click', openGuide);
  try { S = await api('/api/settings'); }
  catch (e) { setOffline(true); setOffline(true); setOffline(true); return; }
  // Язык хранится в настройках; страница узнаёт его до загрузки из localStorage, чтобы не мигать русским.
  if ((S.lang === 'en' ? 'en' : 'ru') !== LANG) {
    try { localStorage.setItem('naivka-lang', S.lang === 'en' ? 'en' : 'ru'); location.reload(); return; } catch (_) { /* остаётся как есть */ }
  }
  applyTheme();
  applySections();
  // Сервер могли сменить в мини-окне или меню трея — подтягиваем настройки, когда окно снова на экране.
  const resync = async () => {
    if (savePending || document.hidden) return;
    try {
      const fresh = await api('/api/settings');
      // Ничего не изменилось — оставляем прежний объект: на него ссылаются кнопки открытого раздела.
      if (savePending || JSON.stringify(fresh) === JSON.stringify(S)) return;
      S = fresh;
      applyTheme(); applySections(); updateStatus();
      // Раздел пересобираем с новыми настройками (раскрытые блоки и прокрутка сохраняются),
      // иначе его кнопки меняли бы уже ненужную копию настроек.
      if (view !== 'overview' && VIEWS[view] && !drawerBody.contains(document.activeElement)) {
        const top = drawerBody.scrollTop;
        Object.keys(sigs).forEach(k => delete sigs[k]);
        drawerBody.replaceChildren(VIEWS[view]());
        drawerBody.scrollTop = top;
        refreshView();
      }
    } catch (_) { /* не важно */ }
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
  refreshAppUpdate();
  let seen = true;
  try { seen = !!localStorage.getItem('naivka-guide'); } catch (_) { /* не показываем */ }
  if (!seen && !S.servers.length && !zapretOnly()) setTimeout(openOnboarding, 300);
  autoCoreDownload();
}

// Без ядра Naivka не подключится — при первом запуске качаем его сразу, не отправляя человека в настройки.
// Один раз за запуск: если загрузка не удалась, повтор — кнопкой (в «Настройках» или на экране первого запуска).
let coreAutoTried = false;
function autoCoreDownload() {
  if (coreAutoTried || !ST || ST.core.present || ST.download.active) return;
  coreAutoTried = true;
  api('/api/core/download', {}).then(refreshState).catch(() => {});
}


// ---------- обход без сервера ----------
let AD = null;

async function refreshAddons() {
  const wasPicking = !!(AD && AD.zapret && AD.zapret.pick && AD.zapret.pick.active);
  try { AD = await api('/api/addons' + (view === 'bypass' ? '?full=1' : '')); } catch (_) { return; }
  // Автоподбор сам сохранил стратегию — берём её, иначе следующее сохранение настроек из окна вернуло бы старую.
  if (wasPicking && !AD.zapret.pick.active && !savePending) {
    try { S.zapret = (await api('/api/settings')).zapret; } catch (_) { /* не важно */ }
  }
  const u = AD.updates;
  const newer = x => x.installed && x.latest && x.installed !== x.latest;
  const badge = $('#navBadge');
  if (badge) badge.hidden = !(newer(u.zapret) || newer(u.telegram) || newer(u.core) || (AD.zapret.degraded));
  if (view === 'bypass') renderBypass();
  if (view === 'settings') renderVersions();
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

// Подбор стратегии zapret: быстрый — до первой, открывшей всё; полный — все по очереди.
async function pickStrategy(full) {
  try { await api('/api/zapret/pick', { full }); } catch (e) { toast(e.message.split('\n')[0], true); }
  refreshAddons();
}

async function act(path, okText) {
  try { await api(path, {}); if (okText) toast(okText); } catch (e) { toast(e.message.split('\n')[0], true); }
  refreshAddons();
}

function viewBypass() {
  return h('div', { class: 'view', style: 'max-width:960px' },
    h('h1', null, 'Обход без сервера'),
    h('p', { class: 'sub' }, 'Обход блокировок без вашего сервера: не тратит его трафик.'),
    h('div', { id: 'bypassBox' }, h('p', { class: 'hint' }, 'Загружаю…')));
}

function renderBypass() {
  if (!AD) return;
  const z = AD.zapret, t = AD.telegram;
  // Счётчик соединений Telegram меняется постоянно, а в этом блоке не показывается.
  const sig = JSON.stringify([z, { ...t, conns: 0 }, AD.updates, AD.conflict, ST && ST.admin, S.zapret, S.telegram]);
  patch('bypassBox', sig, () => [
    needElevation() ? notice('info', 'zapret нужны права администратора. При включении Windows спросит разрешение, и Naivka сама перезапустится с правами — подключение восстановится.') : null,
    AD.conflict ? notice('error', AD.conflict) : null,
    zapretCard(z),
    telegramCard(t),
  ]);
}

function renderVersions() {
  if (AD) patch('versionsBox', JSON.stringify(AD.updates), () => [versionsCard(AD.updates)]);
}

function zapretCard(z) {
  const zs = S.zapret;
  if (ST && ST.legacy) {
    return h('section', { class: 'card addon' }, h('div', { class: 'addon-head' }, h('div', null, h('h3', null, 'zapret'),
      h('p', null, 'В Naivka zapret пока работает только на Windows 10 и 11: на Windows 7 ему нужен другой драйвер WinDivert. Заблокированные сайты на этом компьютере идут через ваш сервер Reality.'))));
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
      h('p', { class: 'hint' }, 'Пока идёт подбор, YouTube и Discord могут подтормаживать.'))
    : null;
  const results = !pick.active && !trial.active && pick.results && pick.results.length ? pickTable(pick, zs) : null;
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' },
      h('div', null, h('h3', null, 'zapret', verLabel(z)),
        h('p', null, 'Обход DPI для YouTube, Discord и сайтов из ваших правил «Через zapret». Эти сайты идут напрямую — быстрее и мимо сервера.')),
      installed ? sw(zs.enabled, z.pick.active, v => { S.zapret.enabled = v; save(0); if (v && needElevation()) elevateFor('zapret'); }, zs.enabled ? 'Включён' : 'Выключен') : null),
    status,
    !installed ? h('div', { class: 'row mt' },
      h('button', { class: 'btn primary', disabled: !!z.busy, onclick: () => act('/api/zapret/install') }, 'Установить zapret'),
      h('span', { class: 'hint' }, 'Антивирус может ругаться на драйвер WinDivert — это не вирус.')) : null,
    busyLine(z),
    z.error ? h('p', { class: 'err-text' }, z.error) : null,
    installed && trial.active ? trialPanel(z) : null,
    installed && !trial.active ? h('div', { class: 'strat-row' },
      h('span', { class: 'hint' }, 'Стратегия'),
      h('select', { disabled: pick.active, onchange: e => { S.zapret.strategy = e.target.value; save(0); } },
        z.strategies.map(n => h('option', { value: n, selected: n === zs.strategy }, n))),
      h('button', { class: 'btn', disabled: pick.active || (ST && ST.os === 'windows' && !ST.admin), onclick: () => pickStrategy(false), title: 'Останавливается на первой стратегии, которая открыла всё' }, 'Подобрать автоматически'),
      h('button', { class: 'btn', disabled: pick.active || (ST && ST.os === 'windows' && !ST.admin), onclick: () => pickStrategy(true), title: 'Проверяет все стратегии и выбирает самую быструю' }, 'Проверить все стратегии'),
      h('button', { class: 'btn', disabled: pick.active || (ST && ST.os === 'windows' && !ST.admin), onclick: () => startTrial(z) }, 'Проверить вручную'),
      z.latest && z.installed !== z.latest ? h('button', { class: 'btn', disabled: !!z.busy, onclick: () => act('/api/zapret/install') }, `Обновить до ${z.latest}`) : null) : null,
    pickBox,
    installed && !pick.active && !trial.active ? sitesBox(z) : null,
    results,
    installed ? more('Настройки zapret', [zs.gameFilter !== 'off' && 'режим игр', zs.ipset !== 'none' && 'ipset ' + zs.ipset, zs.routeDirect ? 'сайты напрямую' : 'сайты через сервер'].filter(Boolean).join(', '),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: zs.routeDirect, onchange: e => { S.zapret.routeDirect = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Сайты из списков zapret — напрямую'), h('small', null, 'YouTube, Discord и другие — мимо сервера.'))),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: zs.fallback, onchange: e => { S.zapret.fallback = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Подстраховка сервером'), h('small', null, 'Если zapret перестанет справляться, эти сайты временно пойдут через сервер.'))),
      h('div', { class: 'row mt' }, h('span', { class: 'hint', style: 'min-width:120px' }, 'Режим для игр'),
        segmented([['off', 'Выключен'], ['all', 'TCP и UDP'], ['tcp', 'TCP'], ['udp', 'UDP']], () => S.zapret.gameFilter, v => { S.zapret.gameFilter = v; save(0); }, 'Режим для игр')),
      h('p', { class: 'hint' }, 'Античиты иногда ругаются — включайте, только если нужно.'),
      h('div', { class: 'row mt' }, h('span', { class: 'hint', style: 'min-width:120px' }, 'Список IP'),
        segmented([['none', 'Не использовать'], ['loaded', 'Список Flowseal']], () => S.zapret.ipset, v => { S.zapret.ipset = v; save(0); }, 'Список IP')),
      h('p', { class: 'hint' }, 'zapret обрабатывает только сайты из своих списков (и адреса из списка IP, если он включён) — всё остальное идёт как обычно.'),
      h('p', { class: 'hint mt' }, 'Папка: ' + z.path)) : null);
}

function telegramCard(t) {
  const ts = S.telegram;
  const installed = !!t.installed;
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' },
      h('div', null, h('h3', null, 'Telegram без сервера', verLabel(t)),
        h('p', null, 'Прокси для Telegram Desktop: работает даже без вашего сервера.')),
      installed ? sw(ts.enabled, false, v => { S.telegram.enabled = v; save(0); }, ts.enabled ? 'Включён' : 'Выключен') : null),
    installed ? h('div', { class: 'addon-status' }, h('span', { class: 'k ' + (t.running ? 'on' : t.lastError ? 'err' : '') }),
      t.running ? `Работает на 127.0.0.1:${t.port}` : t.lastError || 'Выключен.') : null,
    !installed ? h('div', { class: 'row mt' },
      h('button', { class: 'btn', disabled: !!t.busy, onclick: () => act('/api/tg/install') }, 'Установить tg-ws-proxy'),
      h('span', { class: 'hint' }, 'Антивирусы иногда ошибочно ругаются на неё.')) : null,
    busyLine(t),
    t.error ? h('p', { class: 'err-text' }, t.error) : null,
    installed ? h('div', { class: 'row mt' },
      h('button', { class: 'btn', disabled: !t.running, onclick: () => act('/api/tg/open', 'Telegram должен предложить подключить прокси') }, 'Подключить Telegram'),
      h('button', { class: 'btn', onclick: async () => { try { await navigator.clipboard.writeText(t.link); toast('Ссылка скопирована'); } catch (_) { toast('Не удалось скопировать', true); } } }, 'Скопировать ссылку'),
      t.latest && t.installed !== t.latest ? h('button', { class: 'btn', disabled: !!t.busy, onclick: () => act('/api/tg/install') }, `Обновить до ${t.latest}`) : null) : null,
    installed ? h('p', { class: 'hint mt' }, 'Откроется Telegram Desktop — нажмите «Подключить».') : null,
    installed ? more('Порт', String(ts.port),
      h('div', { class: 'grid2' }, h('label', { class: 'field' }, h('span', null, 'Порт прокси'),
        h('input', { type: 'number', value: ts.port, min: 1, max: 65535, onchange: e => { S.telegram.port = parseInt(e.target.value, 10) || 1443; save(0); } }))),
      h('p', { class: 'hint' }, 'После смены порта заново нажмите «Подключить Telegram».')) : null,
    installed ? h('label', { class: 'check mt' },
      h('input', { type: 'checkbox', checked: ts.viaServer !== false, onchange: e => { S.telegram.viaServer = e.target.checked; save(0); } }),
      h('span', null, h('b', null, 'Пока Naivka подключена — через сервер'),
        h('small', null, 'Если провайдер режет прямые соединения с серверами Telegram, без этого Telegram не загрузится. Без подключения tg-ws-proxy работает напрямую, как раньше.'))) : null);
}

function versionsCard(u) {
  const row = (name, st) => h('tr', null, h('td', null, name), h('td', { class: 'num' }, st.installed || '—'),
    h('td', { class: 'num ' + (st.latest && st.installed && st.latest !== st.installed ? 'delay mid' : 'cell-sub') }, st.latest || '—'));
  return h('div', { class: 'versions-box mt' },
    h('div', { class: 'addon-head' }, h('div', null, h('h3', null, 'Версии компонентов'),
      h('p', { class: 'hint' }, u.checked ? 'Проверено в ' + fmtTime(u.checked) + '. Ядро обновляется в «Ядре sing-box», zapret и tg-ws-proxy — в «Обходе».' : 'Ещё не проверялось.')),
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
    pick.early ? h('p', { class: 'hint' }, 'Быстрый подбор остановился на первой стратегии, которая открыла всё. Чтобы сравнить все, нажмите «Проверить все стратегии».') : null,
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
    h('p', { class: 'hint' }, 'Свои сайты добавляются правилом «Через zapret».'),
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
  const upd = w.fetched ? 'обновлена ' + new Date(w.fetched * 1000).toLocaleString(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }) : 'ещё не загружена';
  const src = w.source ? w.source.replace(/^https:\/\/([^/]+).*$/, '$1') : '';
  return h('section', { class: 'card addon' },
    h('div', { class: 'addon-head' },
      h('div', null, h('h3', null, 'Запасная подписка для белых списков', h('span', { class: 'ver' }, 'zieng2/wl')),
        h('p', null, 'Запасные серверы на случай белых списков на мобильном интернете. Включаются сами, когда ваши серверы не отвечают.')),
      sw(on, false, v => { S.wl.enabled = v; save(0); if (v) setTimeout(() => act('/api/wl/refresh'), 600); }, on ? 'Включена' : 'Выключена')),
    on ? h('div', { class: 'addon-status' }, h('span', { class: 'k ' + (w.error ? 'err' : w.active ? 'warn' : w.used ? 'on' : '') }),
      w.busy ? 'Загружаю подписку…' : w.error ? w.error :
        `${count(w.used || 0, 'сервер', 'сервера', 'серверов')} в работе из ${w.total || 0}, ${upd}` + (src ? ` (${src})` : '') + '.') : null,
    on && w.active ? notice('warn', 'Сейчас трафик идёт через подписку: ваши серверы не отвечали. Naivka проверяет их каждые 2 минуты.') : null,
    on && w.ru && !S.wl.includeRu ? h('p', { class: 'hint mt' }, `Российские серверы (${w.ru}) не используются — через них заблокированное не открывается.`) : null,
    on ? h('div', { class: 'row mt' },
      h('button', { class: 'btn', disabled: !!w.busy, onclick: () => act('/api/wl/refresh', 'Подписка обновлена').then(refreshWL) }, 'Обновить сейчас'),
      h('button', { class: 'btn', disabled: S.selected === 'wl', onclick: async () => { S.selected = 'wl'; try { await api('/api/select', { id: 'wl' }); toast('Трафик через сервер теперь идёт через подписку'); } catch (e) { toast(e.message, true); } renderServers(); } }, 'Использовать сейчас')) : null,
    on ? xrayBlock(w) : null,
    on && w.list && w.list.length ? wlList(w) : null,
    on ? more('Настройки подписки', `${S.wl.max} серверов` + (S.wl.fallback ? ', запасной вариант' : '') + (S.wl.includeRu ? ', с российскими' : ''),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: S.wl.fallback, onchange: e => { S.wl.fallback = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Включать, когда свои серверы не отвечают'), h('small', null, 'Naivka вернётся на ваши серверы, как только они ответят.'))),
      h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: S.wl.includeRu, onchange: e => { S.wl.includeRu = e.target.checked; save(0); } }),
        h('span', null, h('b', null, 'Использовать и российские серверы'), h('small', null, 'Заблокированное через них не откроется.'))),
      h('div', { class: 'grid2' }, h('label', { class: 'field' }, h('span', null, 'Сколько серверов брать'),
        h('input', { type: 'number', min: 5, max: 100, value: S.wl.max, onchange: e => { S.wl.max = Math.max(5, Math.min(100, parseInt(e.target.value, 10) || 30)); save(0); } })))) : null,
    h('p', { class: 'hint mt' }, 'Это чужие серверы: они видят, на какие сайты вы ходите. Поэтому — только запасной вариант.'));
}

// ---------- история трафика ----------
let TRAFFIC = null;
const tsum = v => v ? v[0] + v[1] : 0;
const aroundOf = k => tsum(k.direct) + tsum(k.zapret) + tsum(k.telegram);
const fmtDay = d => { const [y, m, dd] = d.split('-'); return `${dd}.${m}`; };
const fmtDayLong = d => new Date(d + 'T12:00:00').toLocaleDateString(LOCALE, { day: 'numeric', month: 'long' });

async function refreshTraffic() {
  try { TRAFFIC = await api('/api/traffic'); } catch (_) { return; }
  renderTraffic();
}

function viewTraffic() {
  return h('div', null,
    h('p', { class: 'sub' }, 'Сколько прошло через сервер и мимо него — по дням.'),
    h('div', { id: 'trafficBox', 'aria-label': 'Загружаю…' },
      h('div', { class: 'skel', style: 'width:180px;height:12px' }),
      h('div', { class: 'skel', style: 'width:240px;height:30px;margin-top:8px' }),
      h('div', { class: 'skel', style: 'width:100%;height:180px;margin-top:18px;border-radius:12px' })),
    // Сколько израсходовано — полоской вверху; здесь только настройка, свёрнутая.
    h('div', { class: 'mt' }, more('Настроить лимит', S.trafficLimitGB ? `${S.trafficLimitGB} ГБ в месяц` : 'без лимита',
    h('p', { class: 'hint' }, 'Naivka предупредит, когда израсходуете 80% и 100% лимита.'),
    h('div', { class: 'grid2' },
      h('label', { class: 'field' }, h('span', null, 'Лимит в месяц, ГБ'),
        h('input', { type: 'number', min: 0, value: S.trafficLimitGB || '', placeholder: 'без лимита', onchange: e => { S.trafficLimitGB = Math.max(0, parseInt(e.target.value, 10) || 0); save(0); setTimeout(refreshTraffic, 500); } })),
      h('label', { class: 'field' }, h('span', null, 'Месяц начинается'),
        h('select', { onchange: e => { S.trafficResetDay = parseInt(e.target.value, 10); save(0); setTimeout(refreshTraffic, 500); } },
          Array.from({ length: 28 }, (_, i) => h('option', { value: i + 1, selected: (S.trafficResetDay || 1) === i + 1 }, `${i + 1}-го числа`))),
        h('small', { class: 'hint' }, 'Как у тарифа: например, в день оплаты сервера.'))))));
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
    h('p', { class: 'sub' }, 'Скорость и задержка напрямую и через каждый ваш сервер.'),
    h('div', { class: 'row' },
      h('button', { class: 'btn primary', id: 'speedBtn', onclick: async () => {
        try { await api('/api/speedtest', {}); } catch (e) { toast(e.message, true); }
        refreshSpeed();
      } }, 'Запустить тест'),
      h('span', { class: 'hint', id: 'speedNote' }, ST && !ST.running ? 'Сейчас Naivka отключена — будет замер только напрямую.' : '')),
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
  const note = $('#speedNote');
  if (note && SPEED && SPEED.at && rows.length) note.textContent = (SPEED.active ? 'Замер начат в ' : 'Последний замер — в ') + fmtTime(SPEED.at);
  if (!rows.length) {
    body.replaceChildren(h('tr', null, h('td', { class: 'speed-empty', colspan: 4 },
      svgIcon('gauge'),
      h('b', null, 'Какой путь быстрее?'),
      h('span', { class: 'hint' }, 'За минуту Naivka измерит задержку и скорость загрузки напрямую и через каждый ваш сервер — и покажет, насколько каждый медленнее прямого.'),
      h('button', { class: 'btn primary', onclick: () => $('#speedBtn') && $('#speedBtn').click() }, 'Запустить тест'))));
    return;
  }
  const max = Math.max(1, ...rows.map(r => r.mbps || 0));
  const direct = rows.find(r => r.kind === 'direct');
  body.replaceChildren(...rows.map(r => {
    const vs = r.kind !== 'direct' && direct && direct.mbps > 0 && r.mbps > 0 ? Math.round((r.mbps / direct.mbps - 1) * 100) : null;
    return h('tr', null,
      h('td', null, h('div', { class: 'cell-main' }, r.name), h('div', { class: 'cell-sub' }, r.kind === 'direct' ? 'без сервера' : r.kind === 'wl' ? 'подписка' : 'ваш сервер')),
      h('td', { class: 'r num' }, r.state === 'done' || r.state === 'ping' ? `${r.ping} мс` : ''),
      h('td', null, r.state === 'wait' || r.state === 'ping' ? h('span', { class: 'hint' }, 'в очереди')
        : r.state === 'run' ? h('span', { class: 'hint' }, 'меряю…')
        : r.state === 'error' ? h('span', { class: 'err-text' }, r.error)
        : h('div', { class: 'row', style: 'flex-wrap:nowrap' },
          h('div', { class: 'score-bar', style: 'flex:1;height:8px' }, h('span', { style: `width:${r.mbps / max * 100}%;background:${r.kind === 'direct' ? 'var(--direct)' : 'var(--tunnel)'}` })),
          h('b', { class: 'num', style: 'min-width:92px;text-align:right' }, `${r.mbps.toLocaleString(LOCALE, { maximumFractionDigits: 1 })} Мбит/с`))),
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
    h('p', { class: 'hint' }, 'Сверху — программы, которые выходили в сеть. Правило по программе надёжнее всего работает в режиме TUN.'),
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
// shareRefresh — перерисовать блок, но не пока человек печатает в его поле (иначе поле исчезло бы
// у него из-под рук вместе с набранным): дождёмся, когда он из него выйдет.
function shareRefresh() {
  const box = $('#shareBox');
  if (!box) return;
  const f = document.activeElement;
  if (f && box.contains(f) && /^(INPUT|SELECT|TEXTAREA)$/.test(f.tagName) && f.type !== 'checkbox') {
    box.addEventListener('focusout', () => setTimeout(shareRefresh, 300), { once: true });
    return;
  }
  shareBox();
}

async function shareBox() {
  const box = $('#shareBox');
  if (!box) return;
  let info;
  try { info = await api('/api/share'); } catch (_) { return; }
  const sh = S.share;
  box.replaceChildren(
    sw(sh.enabled, false, v => { S.share.enabled = v; save(0); setTimeout(shareRefresh, 500); }, sh.enabled ? 'Раздача включена' : 'Раздача выключена'),
    sh.enabled ? h('div', { class: 'card mt' },
      h('p', null, 'На телевизоре, приставке или другом компьютере в той же сети укажите прокси (HTTP или SOCKS5):'),
      info.ips.length ? h('div', { class: 'share-addr' }, info.ips.map(ip => h('code', null, `${ip}:${info.port}`)))
        : h('p', { class: 'err-text' }, 'Компьютер не подключён к домашней сети.'),
      sh.auth ? h('p', { class: 'mt' }, 'Логин ', h('code', null, info.username), ' пароль ', h('code', null, info.password)) : h('p', { class: 'hint mt' }, 'Без пароля — подключиться может любое устройство в этой сети.'),
      h('p', { class: 'hint' }, ST && ST.running ? 'Устройства получат то же, что этот компьютер: те же серверы и правила.' : 'Раздача работает, пока Naivka подключена.'),
      h('div', { class: 'row mt' },
        h('button', { class: 'btn', onclick: () => act('/api/share/firewall', 'Брандмауэр Windows разрешил подключения из домашней сети') }, 'Разрешить в брандмауэре'),
        h('span', { class: 'hint' }, 'Один раз: иначе Windows не пустит другие устройства. Попросит права администратора.')),
      more('Настройки раздачи', `порт ${sh.port}` + (sh.auth ? ', с паролем' : ', без пароля'),
        h('label', { class: 'check' }, h('input', { type: 'checkbox', checked: sh.auth, onchange: e => { S.share.auth = e.target.checked; save(0); setTimeout(shareRefresh, 500); } }),
          h('span', null, h('b', null, 'Требовать логин и пароль'), h('small', null, 'Многие телевизоры не умеют прокси с паролем — тогда выключите.'))),
        h('div', { class: 'grid2' },
          h('label', { class: 'field' }, h('span', null, 'Порт'), h('input', { type: 'number', value: sh.port, onchange: e => { S.share.port = parseInt(e.target.value, 10) || 2090; save(0); setTimeout(shareRefresh, 500); } })),
          h('label', { class: 'field' }, h('span', null, 'Пароль'), h('input', { type: 'text', value: sh.password, spellcheck: 'false', onchange: e => { S.share.password = e.target.value.trim(); save(0); setTimeout(shareRefresh, 500); } }))))) : null);
}

function xrayBlock(w) {
  const x = (AD && AD.updates && AD.updates.xray) || w.xray || {};
  if (!w.xhttp) return null;
  if (x.installed) {
    return h('p', { class: 'hint mt' }, `Серверы xHTTP (${w.xhttp}) работают через Xray ${x.installed}` + (w.xray && w.xray.running ? `, сейчас через него ${count(w.xray.servers, 'сервер', 'сервера', 'серверов')}.` : '.'));
  }
  return h('div', { class: 'notice info' }, h('div', { class: 'grow' },
      h('b', null, `${w.xhttp} серверов подписки — на транспорте xHTTP`),
      h('p', { style: 'margin:4px 0 0' }, 'Для них Naivka поставит рядом ядро Xray (около 10 МБ).'),
      x.busy ? h('p', { class: 'hint num' }, `Скачиваю… ${fmtBytes(x.done)}` + (x.total > 0 ? ` из ${fmtBytes(x.total)}` : '')) : null,
      x.error ? h('p', { class: 'err-text' }, x.error) : null),
    h('button', { class: 'btn small', disabled: !!x.busy, onclick: async () => { await act('/api/xray/install'); toast('Xray ставится — после установки переподключитесь'); } }, 'Установить Xray'));
}

function wlList(w) {
  const delays = (PX && PX.delays) || {};
  const now = PX && PX.selected === 'wl' ? PX.wlNow : PX && PX.selected && PX.selected.startsWith('w-') ? PX.selected : '';
  const items = w.list.slice().sort((a, b) => (b.used - a.used) || ((delays[a.tag] || 1e9) - (delays[b.tag] || 1e9)));
  return more('Серверы подписки', `${w.used} в работе из ${w.total}` + (now && wlName(now) ? `, сейчас: ${splitFlag(wlName(now)).name}` : ''),
    h('div', { class: 'row', style: 'margin-bottom:6px' },
      h('button', { class: 'btn small', id: 'wlTestBtn', disabled: !(ST && ST.running), onclick: testWL }, 'Проверить задержки'),
      h('span', { class: 'hint' }, ST && ST.running ? 'Займёт несколько секунд.' : 'Подключитесь, чтобы проверить серверы.')),
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

// ---------- о программе и обновление ----------
function selfBox() {
  if (!SELF) return h('p', { class: 'hint' }, 'Загружаю…');
  if (SELF.version === 'dev') return h('p', null, 'Naivka, сборка для разработки.');
  const u = APPUP;
  let state = 'Новые версии проверяются сами раз в 12 часов, если включено «Проверять обновления».';
  if (u && u.busy) state = u.busy === 'install' ? 'Устанавливаю обновление…' : `Скачиваю Naivka ${u.latest}…`;
  else if (u && u.newer) state = `Вышла Naivka ${u.latest}. Настройки и серверы сохранятся.`;
  else if (u && u.latest) state = 'Установлена последняя версия.';
  return h('div', null,
    h('p', null, h('b', null, `Naivka ${SELF.version}`), '. ', state),
    u && u.error ? h('p', { class: 'err-text' }, u.error) : null,
    h('div', { class: 'row' },
      u && u.newer
        ? h('button', { class: 'btn primary', disabled: !!u.busy, onclick: installAppUpdate }, `Обновить до ${u.latest}`)
        : h('button', { class: 'btn', onclick: async e => {
          e.target.disabled = true;
          try { APPUP = await api('/api/app/update/check', {}); toast(APPUP.newer ? `Вышла Naivka ${APPUP.latest}` : 'Установлена последняя версия'); }
          catch (err) { toast(err.message, true); }
          finally { e.target.disabled = false; refreshAppUpdate(); }
        } }, 'Проверить обновления'),
      h('button', { class: 'btn', onclick: () => api('/api/open', { link: 'releases' }).catch(() => {}) }, 'Что нового')));
}

function appUpdateNotice() {
  const u = APPUP;
  if (!u || !(u.newer || u.busy)) return null;
  if (u.busy) {
    const pct = u.total > 0 ? ` ${Math.round(u.done / u.total * 100)}%` : '';
    return notice('info', u.busy === 'install' ? 'Устанавливаю обновление и перезапускаю Naivka…' : `Скачиваю Naivka ${u.latest}…${pct}`);
  }
  if (u.error) {
    return notice('error', h('span', null, h('b', null, 'Не удалось обновить. '), u.error.charAt(0).toUpperCase() + u.error.slice(1)),
      h('div', { class: 'row', style: 'flex-wrap:nowrap' },
        h('button', { class: 'btn small', onclick: () => api('/api/open', { link: 'releases' }).catch(() => {}) }, 'Скачать вручную'),
        h('button', { class: 'btn small primary', onclick: installAppUpdate }, 'Повторить')));
  }
  return notice('info', h('span', null, h('b', null, `Вышла Naivka ${u.latest}. `), `У вас ${u.installed}. Настройки и серверы сохранятся.`),
    h('div', { class: 'row', style: 'flex-wrap:nowrap' },
      h('button', { class: 'btn small', onclick: () => api('/api/open', { link: 'releases' }).catch(() => {}) }, 'Что нового'),
      h('button', { class: 'btn small primary', onclick: installAppUpdate }, 'Обновить')));
}

async function installAppUpdate() {
  try {
    await saveNow();
    APPUP = await api('/api/app/update/install', {});
    toast('Скачиваю обновление — Naivka перезапустится сама');
  } catch (e) { toast(e.message, true); }
  refreshAppUpdate();
}

// ---------- первый запуск ----------
// Вместо всего интерфейса сразу — три шага на весь экран: привет → ссылка на сервер → подключить.
// Без сервера можно сразу перейти в режим «Только zapret».
function openOnboarding() {
  try { localStorage.setItem('naivka-guide', '1'); } catch (_) { /* не важно */ }
  const box = h('div', { class: 'onb-in' });
  const ov = h('div', { class: 'onb', role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Первый запуск Naivka' }, box);
  const close = () => { ov.classList.add('out'); setTimeout(() => ov.remove(), 250); };
  const dots = n => h('div', { class: 'onb-dots', 'aria-hidden': 'true' }, ...[0, 1, 2].map(i => h('i', { class: i <= n ? 'on' : '' })));
  const skip = h('button', { class: 'btn link onb-skip', onclick: close }, 'Пропустить');
  const zapretLink = h('button', { class: 'btn link onb-zap', onclick: async () => { close(); await setZapretOnly('zap'); if (!(AD && AD.zapret && AD.zapret.installed)) go('bypass'); } }, 'Нет своего сервера? Включить только zapret');
  const step = n => {
    if (n === 0) box.replaceChildren(dots(0), s('svg', { class: 'onb-logo', viewBox: '0 0 32 32', 'aria-hidden': 'true' }, s('rect', { class: 'logo-bg', width: 32, height: 32, rx: 8 }), s('path', { class: 'logo-ink', d: 'M9 23.5V9l14 14.5V12.5' }), s('circle', { class: 'logo-dot', cx: 23, cy: 9.3, r: 3 })),
      h('h2', null, 'Привет, это Naivka'),
      h('p', null, 'Открывает заблокированное через ваш сервер, а российские сайты оставляет напрямую — быстро и без лишнего трафика.'),
      h('button', { class: 'btn primary', onclick: () => step(1) }, 'Начать'), zapretLink);
    if (n === 1) {
      const ta = h('textarea', { class: 'onb-paste', placeholder: 'Вставьте сюда ссылку (Ctrl+V): naive+https://…, vless://…, hysteria2://…', spellcheck: 'false' });
      const err = h('p', { class: 'err-text' });
      const add = async () => {
        if (!ta.value.trim()) { ta.focus(); return; }
        try {
          const res = await api('/api/import', { text: ta.value });
          S = res.settings;
          if (!res.added) { err.textContent = 'Не удалось разобрать ссылку. ' + (res.errors || []).join(' '); return; }
          step(2);
        } catch (e) { err.textContent = e.message; }
      };
      ta.addEventListener('paste', () => setTimeout(add, 50));
      box.replaceChildren(dots(1), h('h2', null, 'Вставьте ссылку на сервер'),
        h('p', null, 'Ту, что вам дали: она начинается с naive+https://, vless://, hysteria2:// и т. п.'), ta, err,
        h('div', { class: 'row', style: 'justify-content:center' }, h('button', { class: 'btn primary', onclick: add }, 'Добавить'), h('button', { class: 'btn', onclick: () => step(0) }, 'Назад')),
        zapretLink);
      ta.focus();
    }
    if (n === 2) {
      const sv = S.servers[S.servers.length - 1] || {};
      box.replaceChildren(dots(2), h('h2', null, 'Готово'),
        h('p', null, 'Сервер добавлен. Нажмите кнопку — и всё заработает.'),
        h('div', { class: 'onb-srv' }, h('b', null, sv.name || 'Сервер'), h('small', null, sv.type || '')),
        // Маршрут — сразу здесь: по умолчанию российские сайты напрямую, остальное через сервер.
        h('div', { class: 'onb-route' }, h('span', { class: 'hint' }, 'Что пускать через сервер'),
          segmented(Object.entries(ROUTING), () => S.routing, v => { S.routing = v; save(0); }, 'Маршрут'),
          h('small', { class: 'hint' }, 'Можно поменять потом: меню «Путь» на главной.')),
        h('button', { class: 'btn primary onb-go', disabled: !coreReady(), onclick: () => { close(); toggleConnect(); } }, coreReady() ? 'Подключить' : 'Скачиваю ядро…'));
      coreSig = '';
    }
    if (!REDUCE.matches) box.animate([{ opacity: 0, transform: 'translateY(10px)' }, { opacity: 1, transform: 'none' }], { duration: 320, easing: 'cubic-bezier(.2,.8,.2,1)' });
  };
  // Строка о ядре: оно качается само, пока человек вставляет ссылку.
  const core = h('div', { class: 'onb-core', role: 'status' });
  const coreReady = () => !!(ST && ST.core.present);
  let coreSig = '';
  const paintCore = () => {
    if (!ov.isConnected) { clearInterval(coreTimer); return; }
    const d = ST && ST.download, sig = JSON.stringify([coreReady(), d]);
    if (sig === coreSig) return;
    coreSig = sig;
    const connect = ov.querySelector('.onb-go');
    if (connect) { connect.disabled = !coreReady(); connect.textContent = coreReady() ? 'Подключить' : 'Скачиваю ядро…'; }
    if (coreReady()) { core.replaceChildren(h('i', { class: 'onb-core-dot ok' }), 'Ядро sing-box установлено'); return; }
    if (d && d.error) {
      core.replaceChildren(h('i', { class: 'onb-core-dot bad' }), 'Ядро не скачалось: ' + d.error.split('\n')[0] + ' ',
        h('button', { class: 'btn link', onclick: () => api('/api/core/download', {}).then(refreshState).catch(e => toast(e.message, true)) }, 'Повторить'));
      return;
    }
    const pct = d && d.total > 0 ? Math.min(100, Math.round(d.done / d.total * 100)) : 0;
    core.replaceChildren(h('i', { class: 'onb-core-dot' }), d && d.active ? `Скачиваю ядро sing-box… ${d.total > 0 ? pct + '%' : fmtBytes(d.done)}` : 'Готовлю загрузку ядра…',
      h('span', { class: 'onb-core-bar' }, h('span', { style: `width:${pct}%` })));
  };
  const coreTimer = setInterval(paintCore, 500);
  autoCoreDownload();
  ov.append(skip, core);
  document.body.append(ov);
  step(0);
  paintCore();
  ov.addEventListener('keydown', e => { if (e.key === 'Escape') close(); });
}

// ---------- как пользоваться ----------
function openGuide() {
  try { localStorage.setItem('naivka-guide', '1'); } catch (_) { /* не важно */ }
  let dlg;
  const goto = v => () => { dlg.close(); go(v); };
  const step = (title, text, action) => h('li', null, h('b', null, title), h('p', null, text), action || null);
  const needCore = ST && !ST.core.present;
  dlg = openDialog(
    h('h2', null, 'Как пользоваться Naivka'),
    h('ol', { class: 'guide' },
      needCore ? step('Скачайте ядро.', 'Один раз после установки: «Настройки» → «Ядро sing-box» → «Скачать ядро». Это движок, через который идёт трафик.',
        h('button', { class: 'btn small', onclick: goto('settings') }, 'Открыть «Настройки»')) : null,
      step('Добавьте сервер.', 'Скопируйте ссылку, которую вам дали (начинается с naive+https://, vless://, hysteria2:// и т. п.). Откройте «Серверы» → «Вставить ссылки» и вставьте её.',
        h('button', { class: 'btn small', onclick: goto('servers') }, 'Открыть «Серверы»')),
      step('Нажмите большую кнопку на главном экране.', 'Когда она светится и написано «Подключено» — всё работает. Российские сайты идут напрямую, заблокированные — через сервер, Naivka решает это сама. Хотите иначе — нажмите на сайт или программу под схемой и выберите путь.'),
      step('YouTube и Discord без сервера — по желанию.', '«Обход» → включите zapret. Windows спросит разрешение — нажмите «Да». Нет своего сервера — переключите на главном экране режим «Только zapret».',
        h('button', { class: 'btn small', onclick: goto('bypass') }, 'Открыть «Обход»')),
      step('Закройте другие VPN.', 'Karing, Hiddify, v2rayN, Happ, AmneziaVPN и подобные нельзя запускать вместе с Naivka — две такие программы ломают интернет друг другу. Naivka предупредит, если найдёт такую.'),
      step('Если что-то не работает.', '«Сводка» → «Журнал» → «Собрать отчёт» и отправьте файл автору через «Обратную связь».'),
      step('Обновления.', 'Когда выйдет новая версия, на главном экране появится кнопка «Обновить». Нажмите — Naivka скачает её и перезапустится сама, настройки и серверы сохранятся.')),
    h('div', { class: 'row end mt' }, h('button', { class: 'btn primary', onclick: () => dlg.close() }, 'Понятно')));
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
