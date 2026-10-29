'use strict';
// Оформление: окно (своя строка заголовка, Mica), частицы трафика, карта, кнопка питания,
// плавные цифры и мини-окно из трея. Функции app.js (h, s, api…) вызываются только во время работы,
// когда app.js уже загружен.

// ---------- окно ----------
// NAIVKA_HOST задаёт Naivka до загрузки страницы: frame — своя строка заголовка (Windows 11),
// mica — прозрачный фон окна, mini — это мини-окно из трея. В браузере объекта нет.
// Те же признаки приходят и в адресе (?host=frame,mica): на случай, если скрипт до загрузки не сработал.
const HOST = (() => {
  const q = new URLSearchParams(location.search).get('host');
  if (q !== null) sessionStorage.setItem('naivka-host', q);
  const flags = (q !== null ? q : sessionStorage.getItem('naivka-host') || '').split(',');
  const h = { ...(window.NAIVKA_HOST || {}) };
  for (const f of ['frame', 'mica', 'mini']) if (flags.includes(f)) h[f] = true;
  return h;
})();
const MINI = !!HOST.mini;
const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');

function hostCmd(cmd, extra) {
  try { window.chrome.webview.postMessage(JSON.stringify({ cmd, ...extra })); } catch (_) { /* не в окне Naivka */ }
}

// Naivka сообщает о состоянии окна: развёрнуто, активно, мини-окно показано.
window.naivkaHost = {
  update(st) {
    const root = document.documentElement;
    if ('max' in st) {
      root.classList.toggle('maxed', !!st.max);
      const b = document.getElementById('tbMax');
      if (b) {
        b.title = st.max ? 'Восстановить' : 'Развернуть';
        b.setAttribute('aria-label', b.title);
      }
    }
    if ('active' in st) root.classList.toggle('inactive', !st.active);
    if (st.shown && MINI) miniRefresh(true);
  },
};

function setupChrome() {
  const root = document.documentElement;
  if (HOST.mica) root.classList.add('mica');
  if (!HOST.frame || MINI) return;
  root.classList.add('frame');
  if (HOST.max) window.naivkaHost.update({ max: true });
  const tb = document.getElementById('titlebar');
  tb.hidden = false;
  const drag = document.getElementById('tbDrag');
  // Перетаскивание начинаем при движении мыши, а не при нажатии: так работает двойной щелчок.
  let armed = false;
  drag.addEventListener('mousedown', e => { armed = e.button === 0 && e.detail === 1; });
  drag.addEventListener('mousemove', e => { if (armed && (e.buttons & 1)) { armed = false; hostCmd('drag'); } });
  window.addEventListener('mouseup', () => { armed = false; });
  drag.addEventListener('dblclick', () => hostCmd('max'));
  drag.addEventListener('contextmenu', e => { e.preventDefault(); hostCmd('menu'); });
  tb.querySelectorAll('[data-cmd]').forEach(b => b.addEventListener('click', () => hostCmd(b.dataset.cmd)));
  // Клавиши окна: меню окна и закрытие (системного меню у окна со своей рамкой нет).
  window.addEventListener('keydown', e => {
    if (e.altKey && e.code === 'Space') { e.preventDefault(); hostCmd('menu'); }
    if (e.altKey && e.key === 'F4') { e.preventDefault(); hostCmd('close'); }
  });
  tb.querySelectorAll('[data-edge]').forEach(el => el.addEventListener('mousedown', e => {
    if (e.button === 0) { e.preventDefault(); hostCmd('resize', { edge: el.dataset.edge }); }
  }));
}

// ---------- плавные цифры ----------
const TWEEN = new Map();
// tnum — число, которое плавно «докручивается» от прошлого значения с тем же ключом.
function tnum(key, value, fmt, cls) {
  const el = document.createElement('span');
  el.className = 'num' + (cls ? ' ' + cls : '');
  const prev = TWEEN.get(key);
  TWEEN.set(key, value);
  if (prev === undefined || prev === value || REDUCE.matches || document.hidden) {
    el.textContent = fmt(value);
    return el;
  }
  el.textContent = fmt(prev);
  const t0 = performance.now();
  const step = now => {
    if (!el.isConnected) return;
    const k = Math.min(1, (now - t0) / 650);
    const e = 1 - Math.pow(1 - k, 3);
    el.textContent = fmt(prev + (value - prev) * e);
    if (k < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
  return el;
}

// ---------- частицы трафика ----------
// Поток — частицы, бегущие по SVG-пути. Чем больше скорость, тем чаще и быстрее они идут.
// Схема перерисовывается, а потоки продолжаются с того же места: частица помнит долю пути t.
const FX = { flows: new Map(), raf: 0, last: 0 };

function flowTargets(bps) {
  if (!bps || bps < 50) return { rate: 0, tps: 0.2 };
  const lg = Math.log10(bps / 1024 + 1); // 0 … ~5
  return { rate: Math.min(7, 0.6 + lg * 1.35), tps: 1 / Math.max(1.3, 5.2 - lg * 1.05) };
}

function flowsBegin(prefix) {
  for (const [k, f] of FX.flows) if (k.startsWith(prefix)) f.stale = true;
}

function flowsEnd(prefix) {
  for (const [k, f] of FX.flows) {
    if (k.startsWith(prefix) && f.stale) { f.parts.forEach(p => p.el.remove()); FX.flows.delete(k); }
  }
}

function particleEl(f) {
  const r = f.size;
  if (document.documentElement.dataset.skin === 'blocks') {
    // «Блоки»: квадратные пиксельные частицы без свечения.
    const q = (k, cls) => s('rect', { x: -r * k, y: -r * k, width: 2 * r * k, height: 2 * r * k, class: cls });
    const g = s('g', { class: 'pt', style: `color:${f.color}` }, q(0.7, 'pt-tail'), q(0.8, 'pt-tail'), q(1.15, 'pt-core'));
    g.children[0].dataset.k = 3; g.children[1].dataset.k = 2;
    f.layer.append(g);
    return g;
  }
  const g = s('g', { class: 'pt', style: `color:${f.color}` },
    s('circle', { r: r * 2.6, class: 'pt-glow' }),
    s('circle', { r: r * 0.75, class: 'pt-tail', 'data-k': 3 }),
    s('circle', { r: r * 0.85, class: 'pt-tail', 'data-k': 2 }),
    s('circle', { r, class: 'pt-core' }));
  f.layer.append(g);
  return g;
}

// flowBind привязывает поток к пути. size — радиус частицы в единицах SVG.
function flowBind(key, path, layer, color, size) {
  let f = FX.flows.get(key);
  if (!f) { f = { rate: 0, tps: 0.2, target: 0, acc: 0, parts: [] }; FX.flows.set(key, f); }
  const moved = f.layer !== layer || f.size !== size || f.color !== color;
  Object.assign(f, { path, layer, color, size, len: path.getTotalLength(), stale: false, grid: document.documentElement.dataset.skin === 'blocks' ? 6 : 0 });
  if (moved) f.parts.forEach(p => { p.el.remove(); p.el = particleEl(f); });
  return f;
}

function flowSpeed(key, bps) {
  const f = FX.flows.get(key);
  if (!f) return;
  f.target = bps;
  if (bps >= 50) fxStart();
}

function fxStart() {
  if (FX.raf || REDUCE.matches) return;
  FX.last = performance.now();
  FX.raf = requestAnimationFrame(fxFrame);
}

function fxFrame(now) {
  const dt = Math.min(0.1, (now - FX.last) / 1000);
  FX.last = now;
  let busy = false;
  for (const f of FX.flows.values()) {
    const tg = flowTargets(f.target);
    const k = Math.min(1, dt * 1.6);
    f.rate += (tg.rate - f.rate) * k;
    f.tps += (tg.tps - f.tps) * k;
    if (f.rate > 0.05 && f.len > 0) {
      f.acc += f.rate * dt;
      while (f.acc >= 1 && f.parts.length < 24) { f.acc -= 1; f.parts.push({ t: 0, el: particleEl(f) }); }
      if (f.acc >= 1) f.acc = 0;
    }
    const lag = 0.022 * 900 / Math.max(300, f.len); // хвост — одинаковой длины на коротких и длинных путях
    f.parts = f.parts.filter(p => {
      p.t += f.tps * dt;
      if (p.t >= 1) { p.el.remove(); return false; }
      const at = t => f.path.getPointAtLength(Math.max(0, t) * f.len);
      const head = at(p.t);
      if (f.grid) { head.x = Math.floor(head.x / f.grid) * f.grid + f.grid / 2; head.y = Math.floor(head.y / f.grid) * f.grid + f.grid / 2; }
      p.el.setAttribute('transform', `translate(${head.x.toFixed(1)} ${head.y.toFixed(1)})`);
      // Появляется и гаснет у концов пути, чтобы не «выпрыгивать» из узлов.
      p.el.style.opacity = Math.min(1, p.t * 12, (1 - p.t) * 12).toFixed(2);
      for (const tail of p.el.querySelectorAll('.pt-tail')) {
        const pt = at(p.t - lag * tail.dataset.k);
        if (tail.tagName === 'rect') {
          const w = +tail.getAttribute('width');
          tail.setAttribute('x', (pt.x - head.x - w / 2).toFixed(1));
          tail.setAttribute('y', (pt.y - head.y - w / 2).toFixed(1));
        } else {
          tail.setAttribute('cx', (pt.x - head.x).toFixed(1));
          tail.setAttribute('cy', (pt.y - head.y).toFixed(1));
        }
      }
      return true;
    });
    if (f.parts.length || f.rate > 0.05) busy = true;
  }
  FX.raf = busy ? requestAnimationFrame(fxFrame) : 0;
}

// ---------- кнопка питания ----------
function powerButton(id, size) {
  return h('button', { class: 'power' + (size ? ' ' + size : ''), id, type: 'button', onclick: toggleConnect, 'aria-label': 'Подключить' },
    s('svg', { viewBox: '0 0 120 120', 'aria-hidden': 'true' },
      s('circle', { class: 'pw-halo pw-c', cx: 60, cy: 60, r: 57 }),
      s('circle', { class: 'pw-track pw-c', cx: 60, cy: 60, r: 52 }),
      s('circle', { class: 'pw-ring pw-c', cx: 60, cy: 60, r: 52, pathLength: 100 }),
      s('circle', { class: 'pw-core pw-c', cx: 60, cy: 60, r: 45 }),
      // Квадратный вариант — для тем «Чёрное с золотом» и «Блоки» (круги там спрятаны).
      s('rect', { class: 'pw-track pw-h', x: 9, y: 9, width: 102, height: 102 }),
      s('rect', { class: 'pw-ring pw-h', x: 9, y: 9, width: 102, height: 102, pathLength: 100 }),
      s('rect', { class: 'pw-core pw-h', x: 18, y: 18, width: 84, height: 84 }),
      s('path', { class: 'pw-ticks pw-h', d: 'M1 17V1h16M103 1h16v16M119 103v16h-16M17 119H1v-16' }),
      pxPowerParts(),
      s('path', { class: 'pw-icon', d: 'M60 37v21M46 44.5a21 21 0 1 0 28 0' })));
}

// Маленькие значки на кнопках: так сразу видно, что это кнопка и что она делает.
const BTN_ICONS = {
  test: 'M3 12h4l3-7 4 14 3-7h4',
  qr: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h2v2h-2zM18 18h2v2h-2zM14 18h2M18 14h2',
  edit: 'M4 20h4L19 9l-4-4L4 16v4zM13.5 6.5l4 4',
  chevron: 'M9 6l6 6-6 6',
  close: 'M6 6l12 12M18 6L6 18',
};
function btnIcon(name) {
  return s('svg', { viewBox: '0 0 24 24', class: 'bi', 'aria-hidden': 'true' }, s('path', { d: BTN_ICONS[name] }));
}

function powerLabel(p) {
  return p === 'on' || p === 'blocked' || p === 'revive' ? 'Отключить' : p === 'wait' ? 'Подключение…' : 'Подключить';
}

// ---------- карта ----------
function geoLatLon(info, cc) {
  if (info && (info.lat || info.lon)) return [info.lat, info.lon];
  const c = ((info && info.countryCode) || cc || '').toUpperCase();
  return COUNTRY_POS[c] || null;
}

// Точки маршрута: где вы и через какой сервер выходите в интернет.
function mapEnds() {
  const p = phase();
  const d = ip.data;
  const me = d && d.direct && d.direct.info;
  const cs = currentServer();
  let srv = null, srvName = cs.name || '';
  if (p === 'on' && d && d.proxy && d.proxy.info) srv = { info: d.proxy.info };
  if (!srv && cs.id && srvGeo[cs.id] && srvGeo[cs.id].info) srv = { info: srvGeo[cs.id].info };
  if (!srv && cs.cc) srv = { cc: cs.cc };
  const a = me ? geoLatLon(me) : null;
  const b = srv ? geoLatLon(srv.info, srv.cc) : null;
  return {
    me: a && { ll: a, label: 'Вы' + (me.city ? ', ' + me.city : me.country ? ', ' + me.country : '') },
    srv: b && { ll: b, label: srvName, cc: (srv.info && srv.info.countryCode) || srv.cc },
  };
}

// ---------- мини-окно из трея ----------
let miniTimer = 0;
let miniBusy = false;

async function miniBoot() {
  document.body.classList.add('mini-mode');
  document.querySelector('.shell').hidden = true;
  $('#mini').hidden = false;
  $('#mini').replaceChildren(h('div', { id: 'miniBody' }));
  document.addEventListener('keydown', e => { if (e.key === 'Escape') hostCmd('hide'); });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) miniRefresh(true); });
  if (window.ResizeObserver) {
    new ResizeObserver(() => hostCmd('size', { h: Math.ceil($('#mini').getBoundingClientRect().height) })).observe($('#mini'));
  }
  await miniRefresh(true);
}

// miniRefresh опрашивает Naivka, пока мини-окно видно; скрытое окно ничего не делает.
async function miniRefresh(full) {
  clearTimeout(miniTimer);
  if (document.hidden && !full) return;
  if (!miniBusy) {
    miniBusy = true;
    try {
      if (full || !S) S = await api('/api/settings');
      if ((S.lang === 'en' ? 'en' : 'ru') !== LANG) { try { localStorage.setItem('naivka-lang', S.lang === 'en' ? 'en' : 'ru'); } catch (_) { /* как есть */ } location.reload(); return; }
      ST = await api('/api/state');
      if (ST.running) { PX = await api('/api/proxies'); if (PX && PX.running === false) PX = null; } else PX = null;
      STATS = await api('/api/stats?n=40');
      if (full && S.showIp && S.servers.length) { try { srvGeo = await api('/api/geo/servers'); } catch (_) { /* не важно */ } }
      applyTheme();
      renderMini();
    } catch (_) { /* Naivka перезапускается */ }
    finally { miniBusy = false; }
  }
  miniTimer = setTimeout(() => miniRefresh(false), 1000);
}

function miniServerList() {
  const favs = S.servers.filter(x => x.fav), rest = S.servers.filter(x => !x.fav);
  return [...favs, ...rest];
}

function renderMini() {
  const box = $('#mini');
  if (!box || !S || !ST) return;
  const p = phase();
  document.body.dataset.state = p;
  const cs = currentServer();
  const sp = lastSpeeds();
  const on = p === 'on';
  const d = cs.id ? delayFor(cs.id) : null;
  const sig = JSON.stringify([p, S.selected, S.servers.map(x => [x.id, x.name, x.fav, x.type]), PX && PX.delays, PX && PX.autoNow, PX && PX.selected, Object.keys(srvGeo).length]);
  patch('miniBody', sig, () => {
    const sel = id => async () => {
      S.selected = id;
      try { await api('/api/select', { id }); } catch (e) { toast(e.message, true); }
      miniRefresh(false);
    };
    const row = (id, title, sub, cc, ms) => h('button', { class: 'mrow' + (S.selected === id ? ' sel' : ''), type: 'button', onclick: sel(id), 'aria-pressed': S.selected === id ? 'true' : 'false' },
      h('span', { class: 'mflag' }, cc ? flag(cc) : id === 'auto' ? h('span', { class: 'mauto' }, 'A') : null),
      h('span', { class: 'mrow-main' }, h('b', null, title), sub ? h('small', null, sub) : null),
      ms !== undefined ? h('span', { class: 'mping' }, pingBars(ms)) : null);
    const list = [];
    if (S.servers.length > 1) list.push(row('auto', 'Автовыбор', PX && PX.autoNow && serverByTag(PX.autoNow) ? 'сейчас: ' + serverByTag(PX.autoNow).name : 'самый быстрый', '', undefined));
    for (const sv of miniServerList().slice(0, 6)) {
      const g = srvGeo[sv.id] && srvGeo[sv.id].info;
      list.push(row(sv.id, (sv.fav ? '★ ' : '') + sv.name, protoName(sv), g && g.countryCode, delayFor(sv.id)));
    }
    return [
      h('div', { class: 'mini-head' },
        h('div', { class: 'mini-power' }, powerButton('miniPower', 'small')),
        h('div', { class: 'mini-status' },
          h('div', { class: 'mini-title', id: 'miniTitle' }),
          h('div', { class: 'mini-sub', id: 'miniSub' }))),
      h('div', { class: 'mini-speed', id: 'miniSpeed' }),
      list.length ? h('div', { class: 'mini-list', role: 'group', 'aria-label': 'Сервер' }, ...list)
        : h('div', { class: 'empty' }, 'Серверов пока нет.'),
      h('div', { class: 'mini-foot' },
        h('button', { class: 'btn small', type: 'button', onclick: () => hostCmd('open') }, 'Открыть Naivka'),
        S.servers.length > 6 ? h('span', { class: 'hint' }, `ещё ${S.servers.length - 6} в окне`) : null)];
  });
  $('#miniTitle').textContent = TITLES[p];
  $('#miniSub').replaceChildren(on || S.selected !== 'auto' ? (cs.cc || (cs.id && srvGeo[cs.id] && srvGeo[cs.id].info) ? flag(cs.cc || srvGeo[cs.id].info.countryCode) : '') : '',
    cs.auto ? cs.name + ' (авто)' : cs.name, d && d.ms && on ? h('span', { class: 'hint' }, ` · ${d.ms} мс`) : null);
  const btn = $('#miniPower');
  btn.disabled = p === 'wait';
  btn.setAttribute('aria-label', powerLabel(p));
  btn.title = powerLabel(p);
  $('#miniSpeed').replaceChildren(
    h('div', null, h('span', { class: 'hint' }, '↓ загрузка'), tnum('m-down', on ? sp.pd + sp.dd + sp.zd + sp.td : 0, fmtSpeed, 'mini-num')),
    h('div', null, h('span', { class: 'hint' }, '↑ отдача'), tnum('m-up', on ? sp.pu + sp.du + sp.zu + sp.tu : 0, fmtSpeed, 'mini-num')),
    h('div', { class: 'mini-spark' }, speedSpark()));
}

// Крошечный график скорости через сервер за последние 40 секунд.
function speedSpark() {
  const smp = ((STATS && STATS.samples) || []).slice(-40);
  if (smp.length < 2) return null;
  const vals = smp.map(p => (p.pd || 0) + (p.dd || 0) + (p.zd || 0) + (p.td || 0));
  const max = Math.max(...vals, 32 * 1024);
  const W = 120, H = 34;
  const x = i => i / (vals.length - 1) * W;
  const y = v => H - 2 - v / max * (H - 4);
  const line = vals.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join('');
  return s('svg', { viewBox: `0 0 ${W} ${H}`, class: 'mspark', preserveAspectRatio: 'none', 'aria-hidden': 'true' },
    s('path', { d: line + `L${W} ${H}L0 ${H}Z`, class: 'mspark-area' }),
    s('path', { d: line, class: 'mspark-line', 'vector-effect': 'non-scaling-stroke' }));
}

// ---------- задержка полосками ----------
function pingBars(d) {
  let n = 0, cls = '';
  if (d && !d.pending) {
    if (d.error) cls = 'bad';
    else if (d.ms) { n = d.ms < 150 ? 4 : d.ms < 300 ? 3 : d.ms < 600 ? 2 : 1; cls = d.ms < 300 ? 'good' : d.ms < 800 ? 'mid' : 'bad'; }
  }
  return h('span', { class: 'bars4 ' + cls, 'aria-hidden': 'true' }, ...[1, 2, 3, 4].map(i => h('i', { class: i <= n ? 'on' : '' })));
}

// ---------- «Пиксели»: плитки с пиксельной рамкой, ступенчатые ветви, мозаичная прогрузка ----------
function mcRand(seed) { let x = seed; return () => (x = (x * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff; }

// Растр: фигура обычной темы (те же кривые и круги, та же геометрия), закрашенная клетками сетки G.
function pxCellsPath(cells, G) {
  let d = '';
  for (const k of cells) { const [cx, cy] = k.split(',').map(Number); d += `M${cx * G} ${cy * G}h${G}v${G}h${-G}z`; }
  return d;
}

// Ветвь — та же кривая Безье, что в обычной теме (lanePath), толщиной thick клеток.
function pxLaneCells(x0, y0, nx, ny, x1, y1, G, thick) {
  const a = x0 + (nx - x0) * 0.5, b = nx + (x1 - nx) * 0.5;
  const bez = (p0, p1, p2, p3, t) => { const u = 1 - t; return u * u * u * p0 + 3 * u * u * t * p1 + 3 * u * t * t * p2 + t * t * t * p3; };
  const cells = new Set();
  const add = (x, y) => { const cx = Math.floor(x / G), cy = Math.floor(y / G); for (let k = 0; k < thick; k++) cells.add(`${cx},${cy + k - (thick >> 1)}`); };
  for (let i = 0; i <= 400; i++) { const t = i / 400; add(bez(x0, a, a, nx, t), bez(y0, y0, ny, ny, t)); add(bez(nx, b, b, x1, t), bez(ny, ny, y1, y1, t)); }
  return pxCellsPath(cells, G);
}

// Кольцо (или круг, если r0 < 0) по клеткам: берутся клетки, центр которых между r0 и r1.
function pxRingCells(cx, cy, r0, r1, G) {
  const cells = [];
  for (let gy = Math.floor((cy - r1) / G); gy <= Math.ceil((cy + r1) / G); gy++) {
    for (let gx = Math.floor((cx - r1) / G); gx <= Math.ceil((cx + r1) / G); gx++) {
      const d = Math.hypot(gx * G + G / 2 - cx, gy * G + G / 2 - cy);
      if (d <= r1 && d > r0) cells.push([gx, gy, Math.atan2(gx * G + G / 2 - cx, -(gy * G + G / 2 - cy))]);
    }
  }
  return cells;
}

// Узел схемы в «Пикселях»: круг обычной темы (тот же радиус), растрированный, со значком.
function pxNode(cx, cy, r, ic, color, cls) {
  const G = 3;
  const ring = pxRingCells(cx, cy, r - 3, r + 1, G), core = pxRingCells(cx, cy, -1, r - 3, G);
  // Без зазора вокруг: ветка доходит до кольца, входит в узел и выходит с другой стороны
  // (зазор цвета фона был виден тёмной рамкой поверх своего фона окна).
  return s('g', { class: 'px-node ' + (cls || ''), style: `color:${color}` },
    s('path', { d: pxCellsPath(core.map(c => c[0] + ',' + c[1]), G), class: 'px-node-bg' }),
    s('path', { d: pxCellsPath(ring.map(c => c[0] + ',' + c[1]), G), class: 'px-node-ring' }),
    ic ? icon(ic, cx, cy, r * 1.05, 'currentColor') : null);
}

// Кнопка подключения в «Пикселях»: те же круги, что у обычной кнопки (ореол, дорожка, ядро, значок), по клеткам 4×4.
function pxPowerParts() {
  const G = 4, path = cells => pxCellsPath(cells.map(c => c[0] + ',' + c[1]), G);
  const ring = pxRingCells(60, 60, 50, 56, G); // как обводка r=52 у обычной кнопки; ядро r≤41 — с зазором в 2 клетки
  // значок питания: вертикальная черта и дуга с разрывом сверху
  const icon = pxRingCells(60, 60, 17, 23, G).filter(c => Math.abs(c[2]) > 0.75);
  for (let gy = 9; gy <= 14; gy++) for (const gx of [14]) icon.push([gx, gy]);
  return s('g', { class: 'pw-px' },
    // Подложка между ядром и кольцом (до внешнего края кольца): ветка не просвечивает внутри кнопки,
    // а снаружи доходит прямо до кольца.
    s('path', { d: path(pxRingCells(60, 60, 40, 56, G)), class: 'pw-px-gap' }),
    s('path', { d: path(pxRingCells(60, 60, 57, 61, G)), class: 'pw-px-halo' }),
    ...ring.sort((p, q) => p[2] - q[2]).map((c, i, arr) => s('rect', { x: c[0] * G, y: c[1] * G, width: G, height: G, class: 'pw-px-ring', style: `--i:${Math.round(i / arr.length * 28)}` })),
    s('path', { d: path(pxRingCells(60, 60, -1, 41, G)), class: 'pw-px-core' }),
    s('path', { d: path(icon), class: 'pw-px-icon' }));
}

// Мозаика: новый экран проступает квадратами в случайном порядке, как прогрузка мира в кубической игре.
function chunkReveal(el, fog) {
  if (!el || REDUCE.matches || document.documentElement.dataset.skin !== 'blocks') return;
  const r = el.getBoundingClientRect();
  if (r.width < 50 || r.height < 50) return;
  const size = 32;
  const cols = Math.ceil(r.width / size), rows = Math.ceil(Math.min(r.height, window.innerHeight) / size);
  const grid = h('div', { class: 'chunks', 'aria-hidden': 'true' });
  grid.style.cssText = `left:${r.left}px;top:${r.top}px;width:${cols * size}px;height:${rows * size}px;grid-template-columns:repeat(${cols},${size}px)`;
  const rnd = mcRand(Date.now() & 0xffff);
  for (let i = 0; i < cols * rows; i++) {
    const c = document.createElement('i');
    // Мозаика: соседние квадраты чуть разной яркости, исчезают в случайном порядке.
    c.style.cssText = `background:${fog};filter:brightness(${(0.85 + rnd() * 0.9).toFixed(2)});animation-delay:${(rnd() * 0.5).toFixed(2)}s`;
    grid.append(c);
  }
  document.body.append(grid);
  setTimeout(() => grid.remove(), 1200);
}
