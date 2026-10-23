// Живые ветви — главная схема: от компьютера к интернету растут ветки «через сервер», «напрямую»,
// «zapret» и «Telegram». Толщина ветки — скорость, по ней летят «семена» трафика. На ветках висят
// сайты, которые сейчас открыты; лист можно перетащить на другую ветку — это станет правилом.
// Рисуется на canvas (ветки, частицы) с HTML поверх (узлы, листья): так работают перетаскивание,
// подсказки и клавиатура. Анимация идёт только пока схема на экране.

const Branches = (() => {
  let box = null, cv = null, ctx = null, nodesEl = null, leavesEl = null, toastFn = null;
  let W = 0, H = 0, dpr = 1, raf = 0, ro = null;
  let data = null;             // последнее состояние из app.js
  let dragOver = null;
  let dragging = false;        // что-то тащат (лист или программу с полки) — ветки показываются широкими дорожками
  const br = {};               // ветки: key → { grow, speed, color, active }
  const parts = {};            // частицы по веткам
  const leaves = new Map();    // host → { host, b, pref, side, t, pos, base, el, seen, pending, over }
  const LEAF_TTL = 45000;      // лист без трафика живёт 45 с
  const MAX_LEAVES = 10;
  const reduce = () => typeof REDUCE !== 'undefined' && REDUCE.matches;

  // ---------- геометрия ----------
  const narrow = () => W < 620;
  // Справа оставляем место под подпись «Интернет» сбоку: так её не пересекают сходящиеся ветки.
  // Кнопка подключения (100 px, в узком окне 72) целиком в окне.
  const cx0 = () => Math.max(W * 0.1, narrow() ? 48 : 68), cx1 = () => narrow() ? Math.min(W * 0.9, W - 34) : W - 150;
  function geom(key) {
    const c = [cx0(), H * 0.5], n = [cx1(), H * 0.5], top = 64, bot = H - 64;
    switch (key) {
      case 'proxy': return [c, [W * 0.3, top - 44], [W * 0.7, top - 44], n];
      case 'direct': return [c, [W * 0.38, H * 0.6], [W * 0.62, H * 0.4], n];
      case 'zapret': return [c, [W * 0.3, bot + 30], [W * 0.7, bot + 30], n];
      // Telegram — короткая веточка вверх-влево, над веткой сервера: не задевает подписи «Компьютер».
      case 'tg': return [c, [c[0] - 14, H * 0.22], [c[0] + 4, H * 0.1], [c[0] + 64, H * 0.09]];
    }
  }
  const bez = (p, t) => { const u = 1 - t; return [0, 1].map(i => u*u*u*p[0][i] + 3*u*u*t*p[1][i] + 3*u*t*t*p[2][i] + t*t*t*p[3][i]); };
  const tan = (p, t) => { const u = 1 - t; return [0, 1].map(i => 3*u*u*(p[1][i]-p[0][i]) + 6*u*t*(p[2][i]-p[1][i]) + 3*t*t*(p[3][i]-p[2][i])); };
  const norm = (p, t) => { const d = tan(p, t), l = Math.hypot(d[0], d[1]) || 1; return [-d[1] / l, d[0] / l]; };

  // ---------- построение ----------
  function mount(container, opts) {
    if (box === container) return;
    unmount();
    box = container; toastFn = opts.toast;
    box.classList.add('branches');
    cv = document.createElement('canvas'); cv.className = 'br-canvas'; cv.setAttribute('aria-hidden', 'true');
    ctx = cv.getContext('2d');
    nodesEl = document.createElement('div'); nodesEl.className = 'br-nodes';
    leavesEl = document.createElement('div'); leavesEl.className = 'br-leaves';
    box.prepend(cv); box.append(nodesEl, leavesEl);
    // Экран собран заново (ушли в другой раздел и вернулись) — всё рисуем с нуля:
    // иначе узлы считались бы уже нарисованными и пропадали.
    leaves.clear(); nodesSig = ''; growKey = ''; data = null;
    for (const k in br) delete br[k];
    ro = new ResizeObserver(resize); ro.observe(box);
    resize();
    raf = requestAnimationFrame(frame);
  }
  function unmount() {
    if (!box) return;
    cancelAnimationFrame(raf); raf = 0;
    if (ro) ro.disconnect();
    cv.remove(); nodesEl.remove(); leavesEl.remove();
    box.classList.remove('branches');
    box = null; leaves.clear();
  }
  function resize() {
    if (!box) return;
    const r = box.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) return; // окно скрыто (в трее) — размер узнаем, когда покажется
    W = r.width; H = r.height; dpr = Math.min(2, devicePixelRatio || 1);
    cv.width = Math.round(W * dpr); cv.height = Math.round(H * dpr); ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    box.classList.toggle('small', narrow());
    // Кнопка подключения стоит на месте узла «Компьютер».
    box.style.setProperty('--pcx', cx0() + 'px'); box.style.setProperty('--pcy', H * 0.5 + 'px');
    placeNodes(); layout();
  }

  // Узлы — HTML поверх canvas.
  let nodesSig = '', popNodes = false;
  function renderNodes() {
    const d = data, sig = JSON.stringify([d.phase, d.lanes.map(l => [l.key, l.node && [l.node.title, l.node.sub, l.node.cc], l.active]), d.endSub, d.compSub, narrow()]);
    // Узлы на месте и подпись та же — ничего не делаем. Пропали (окно пересобрали, сбой) — рисуем заново.
    if (sig === nodesSig && nodesEl.isConnected && nodesEl.querySelector('[data-key="comp"]')) return;
    const mk = (key, cls, iconPath, title, sub, extra, click) => {
      const el = document.createElement(click ? 'button' : 'div');
      if (click) { el.type = 'button'; el.addEventListener('click', click); el.title = key === 'proxy' ? 'Выбрать сервер' : 'Открыть «Обход»'; }
      el.className = 'br-node ' + cls; el.dataset.key = key;
      const disc = document.createElement('span'); disc.className = 'br-disc';
      if (extra) disc.append(extra);
      else if (iconPath) disc.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="${iconPath}"/></svg>`;
      const txt = document.createElement('span'); txt.className = 'br-txt';
      const b = document.createElement('b'); b.textContent = title;
      txt.append(b);
      if (sub) { const s = document.createElement('small'); s.textContent = sub; txt.append(s); }
      el.append(disc, txt);
      return el;
    };
    const out = [];
    for (const l of d.lanes) {
      if (!l.node) continue;
      const flag = l.node.cc && d.flag ? d.flag(l.node.cc) : null;
      const el = mk(l.key, l.key === 'tg' ? 'side' : '', ICONS[l.node.icon], l.node.title, l.node.sub, flag, () => d.onNode(l.key));
      if (d.phase !== 'on' || !l.active) el.dataset.off = '';
      out.push(el);
    }
    out.push(mk('net', narrow() ? 'end' : 'side', ICONS.globe, 'Интернет', d.endSub));
    const comp = document.createElement('div'); comp.className = 'br-node comp'; comp.dataset.key = 'comp';
    comp.innerHTML = '<span class="br-txt"><b>Компьютер</b><small></small></span>';
    comp.querySelector('small').textContent = d.compSub;
    out.push(comp);
    nodesEl.replaceChildren(...out);
    if (popNodes) {
      popNodes = false;
      for (const el of out) {
        const delay = (GROW_DELAY[el.dataset.key] ?? 0) + 380;
        if (el.dataset.key !== 'comp' && el.dataset.key !== 'net') { el.classList.add('pop-in'); el.style.animationDelay = delay + 'ms'; }
      }
    }
    nodesSig = sig; // только после успешной сборки: при сбое на следующем обновлении соберём снова
    placeNodes();
  }
  function placeNodes() {
    if (!nodesEl) return;
    for (const el of nodesEl.children) {
      const k = el.dataset.key;
      let p;
      if (k === 'comp') p = [cx0(), H * 0.5 + (narrow() ? 44 : 60)]; // подпись под кнопкой
      else if (k === 'net') p = [cx1(), H * 0.5];
      else if (k === 'tg') p = geom('tg')[3];
      else p = bez(geom(k), .5);
      el.style.left = p[0] + 'px'; el.style.top = p[1] + 'px';
    }
    measureHoles();
  }

  // Ветки не проходят сквозь кружки: под каждым кружком (и кнопкой подключения) линия вырезается,
  // но без зазора — ветка входит в обводку кружка и выходит из неё с другой стороны (с прозрачным
  // фоном окна зазор был виден как разрыв). Места кружков меряем по самим элементам.
  const GAP = -1;
  let holes = [];
  function measureHoles() {
    if (!box || !nodesEl) return;
    const sr = box.getBoundingClientRect(), out = [];
    const add = (el, extra = 0) => {
      const r = el.getBoundingClientRect();
      if (r.width < 2) return false;
      out.push({ x: r.left - sr.left + r.width / 2, y: r.top - sr.top + r.height / 2, r: r.width / 2 + GAP + extra,
        square: el instanceof HTMLElement && getComputedStyle(el).borderRadius === '0px' });
      return true;
    };
    for (const d of nodesEl.querySelectorAll('.br-disc')) add(d);
    // Кнопка подключения: кольцо нарисовано внутри неё, меньше самого элемента, — меряем по кольцу
    // (его рамка без толщины линии, поэтому +2 — до середины обводки). В пиксельной теме кольца нет.
    const pw = box.querySelector('.power');
    if (pw) {
      const ring = pw.querySelector('.pw-track.pw-c');
      if (!ring || !add(ring, 2)) add(pw);
    }
    holes = out;
  }
  function cutHoles() {
    ctx.save();
    ctx.globalCompositeOperation = 'destination-out';
    ctx.beginPath();
    for (const c of holes) {
      if (c.square) ctx.rect(c.x - c.r, c.y - c.r, c.r * 2, c.r * 2);
      else { ctx.moveTo(c.x + c.r, c.y); ctx.arc(c.x, c.y, c.r, 0, Math.PI * 2); }
    }
    ctx.fill();
    ctx.restore();
  }

  // ---------- листья ----------
  const LEAF = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M14 2C7 2 2 5.5 2 11c0 1.2.3 2.2.8 3 .6-3.4 3-6.4 6.7-8-3 2.2-4.8 5-5.2 8C11 14 14 9 14 2z"/></svg>';
  const routeBranch = r => r === 'direct' ? 'direct' : r === 'zapret' ? 'zapret' : r === 'telegram' ? null : 'proxy';
  function syncLeaves() {
    const now = Date.now(), d = data;
    if (d.phase !== 'on') { for (const lf of leaves.values()) lf.el.remove(); leaves.clear(); return; }
    for (const t of d.top) {
      const host = t.key;
      const b = routeBranch(t.route);
      if (!b || !host || !br[b] || !br[b].active) continue;
      let lf = leaves.get(host);
      if (!lf) {
        if (leaves.size >= MAX_LEAVES) {
          // вытесняем самый давний
          let old = null; for (const x of leaves.values()) if (!old || x.seen < old.seen) old = x;
          if (old && now - old.seen > 4000) { old.el.remove(); leaves.delete(old.host); } else continue;
        }
        lf = { host, label: t.label || host, icon: t.icon, b, pref: .2 + Math.random() * .6, side: Math.random() < .5 ? -1 : 1, seen: now, fresh: true };
        makeLeaf(lf); leaves.set(host, lf);
      }
      lf.seen = now;
      // Пользователь перетащил лист — держим его на новой ветке, пока трафик не пойдёт так же (или 10 минут).
      if (lf.pending && (lf.pending.b === b || now - lf.pending.at > 600000)) lf.pending = null;
      const want = lf.pending ? lf.pending.b : b;
      if (lf.b !== want) { lf.b = want; lf.pref = .25 + Math.random() * .5; }
      paintLeaf(lf);
    }
    for (const lf of [...leaves.values()]) if (now - lf.seen > LEAF_TTL && !lf.el.classList.contains('drag')) { lf.el.remove(); leaves.delete(lf.host); }
  }
  function makeLeaf(lf) {
    const el = document.createElement('button');
    el.type = 'button'; el.className = 'br-leaf';
    el.innerHTML = (lf.icon ? `<span class="svc"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="${lf.icon}"/></svg></span>` : LEAF) + '<span class="h"></span><span class="why"></span>';
    el.querySelector('.h').textContent = lf.label;
    lf.el = el; leavesEl.append(el); bindDrag(lf);
  }
  const BRANCH_NAME = { proxy: 'через сервер', direct: 'напрямую', zapret: 'через zapret' };
  function paintLeaf(lf) {
    lf.el.style.setProperty('--c', br[lf.b] ? br[lf.b].color : 'var(--faint)');
    const why = lf.pending ? `Ваше новое правило: ${BRANCH_NAME[lf.pending.b]}. Заработает после переподключения.` : data.why(lf.host, lf.b);
    const w = lf.el.querySelector('.why');
    const parts = [...[why].flat(), 'Перетащите на другую ветку, чтобы изменить.'], sig = parts.join('|');
    if (w.dataset.t !== sig) {
      // Каждая часть — отдельным узлом: так их переводит английский интерфейс.
      w.replaceChildren(...parts.map(t => { const e = document.createElement('span'); e.textContent = t; return e; }));
      w.dataset.t = sig;
    }
    lf.el.setAttribute('aria-label', `${lf.label}: ${BRANCH_NAME[lf.b]}. Enter — выбрать путь`);
  }

  // Раскладка: каждый лист — в ближайшее свободное место у своей ветки, без наложений
  // на соседей, узлы и чужие ветки. Совсем некуда — прячется в счётчик «ещё N».
  const hit = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
  function layout() {
    if (!box || !data) return;
    measureHoles();
    const sr = box.getBoundingClientRect(), pad = 6, blocked = [];
    for (const el of nodesEl.children) { const r = el.getBoundingClientRect(); blocked.push({ x: r.left - sr.left - pad, y: r.top - sr.top - pad, w: r.width + pad * 2, h: r.height + pad * 2 }); }
    const pw = box.querySelector('.power');
    if (pw) { const r = pw.getBoundingClientRect(); blocked.push({ x: r.left - sr.left - pad, y: r.top - sr.top - pad, w: r.width + pad * 2, h: r.height + pad * 2 }); }
    const curves = {};
    for (const k in br) if (br[k].grow > .5) { const p = geom(k), pts = []; for (let i = 0; i <= 60; i++) pts.push(bez(p, i / 60)); curves[k] = pts; }
    const placed = [], over = {};
    const list = [...leaves.values()].sort((a, b) => (b.drop ? 1 : 0) - (a.drop ? 1 : 0) || a.host.localeCompare(b.host));
    for (const lf of list) {
      const w = lf.el.offsetWidth || 110, h = lf.el.offsetHeight || 24, p = geom(lf.b);
      if (!br[lf.b] || br[lf.b].grow < .6) { lf.over = true; continue; }
      let best = null, bestScore = Infinity;
      const ts = []; for (let t = 0.12; t <= 0.88; t += 0.02) ts.push(+t.toFixed(2));
      ts.sort((a, b) => Math.abs(a - lf.pref) - Math.abs(b - lf.pref));
      outer: for (const t of ts) for (const side of [lf.side, -lf.side]) for (const off of [20, 38]) {
        const base = bez(p, t), n = norm(p, t);
        const cx = base[0] + n[0] * side * (off + h / 2), cy = base[1] + n[1] * side * (off + h / 2);
        const r = { x: cx - w / 2 - 3, y: cy - h / 2 - 3, w: w + 6, h: h + 6 };
        let score = Math.abs(t - lf.pref) * 10 + (off > 20 ? 1 : 0);
        if (r.x < 4 || r.y < 4 || r.x + r.w > W - 4 || r.y + r.h > H - 4) score += 100;
        for (const b of blocked) if (hit(r, b)) score += 80;
        for (const b of placed) if (hit(r, b)) score += 80;
        for (const k in curves) for (const q of curves[k]) if (q[0] > r.x && q[0] < r.x + r.w && q[1] > r.y && q[1] < r.y + r.h) { score += k === lf.b ? 6 : 30; break; }
        if (score < bestScore) { bestScore = score; best = { t, side, cx, cy, r, base }; }
        if (score < 1.2) break outer;
      }
      lf.over = bestScore >= 80 && !lf.drop;
      if (!lf.over) placed.push(best.r);
      Object.assign(lf, { t: best.t, side: best.side, pos: [best.cx, best.cy], base: best.base });
      delete lf.drop;
      if (lf.over) over[lf.b] = (over[lf.b] || 0) + 1;
    }
    for (const lf of leaves.values()) {
      if (lf.pos && !lf.el.classList.contains('drag')) {
        const tr = `translate(${lf.pos[0] - lf.el.offsetWidth / 2}px, ${lf.pos[1] - lf.el.offsetHeight / 2}px)`;
        if (lf.fresh) {
          // Новый лист появляется сразу на своём месте (а не вылетает из угла) и проявляется.
          lf.el.classList.add('born'); lf.el.style.transform = tr; void lf.el.offsetWidth;
          requestAnimationFrame(() => lf.el.classList.remove('born'));
          lf.fresh = false;
        } else lf.el.style.transform = tr;
      }
      lf.el.classList.toggle('up', !!lf.pos && lf.pos[1] > H * 0.6);
      lf.el.dataset.hidden = String(!!lf.over || !br[lf.b] || br[lf.b].grow < .6);
      lf.el.dataset.dead = String(!!data.offline);
    }
    overflow = over;
  }
  let overflow = {};

  // ---------- перетаскивание ----------
  function nearestBranch(x, y) {
    let best = null, bd = Infinity, bt = .5;
    for (const k of ['proxy', 'direct', 'zapret']) {
      if (!br[k] || br[k].grow < .6) continue;
      const p = geom(k);
      for (let i = 4; i <= 56; i++) { const t = i / 60, q = bez(p, t), d = Math.hypot(q[0] - x, q[1] - y); if (d < bd) { bd = d; best = k; bt = t; } }
    }
    return bd < 110 ? { k: best, t: bt } : null;
  }
  function bindDrag(lf) {
    let sx, sy, ox, oy, moved = false, id = null;
    const el = lf.el;
    el.addEventListener('pointerdown', e => {
      if (e.button !== 0 || !lf.pos) return;
      id = e.pointerId; el.setPointerCapture(id); moved = false;
      const sr = box.getBoundingClientRect(); sx = e.clientX; sy = e.clientY;
      ox = e.clientX - sr.left - lf.pos[0]; oy = e.clientY - sr.top - lf.pos[1];
    });
    el.addEventListener('pointermove', e => {
      if (e.pointerId !== id) return;
      if (!moved && Math.hypot(e.clientX - sx, e.clientY - sy) < 5) return;
      moved = true; el.classList.add('drag'); dragging = true;
      const sr = box.getBoundingClientRect(), x = e.clientX - sr.left - ox, y = e.clientY - sr.top - oy;
      el.style.transform = `translate(${x - el.offsetWidth / 2}px, ${y - el.offsetHeight / 2}px)`;
      const nb = nearestBranch(x, y); dragOver = nb && nb.k;
    });
    const end = e => {
      if (e.pointerId !== id) return;
      id = null;
      // Просто нажали — меню с выбором пути (тянуть не обязательно).
      if (!moved) { if (data.onPick) data.onPick({ kind: 'site', key: lf.host, label: lf.label, icon: lf.icon, current: lf.pending ? lf.pending.b : lf.b }, el); return; }
      el.classList.remove('drag'); dragging = false;
      const sr = box.getBoundingClientRect(), x = e.clientX - sr.left - ox, y = e.clientY - sr.top - oy;
      const nb = nearestBranch(x, y); dragOver = null;
      if (nb) moveTo(lf, nb.k, nb.t, y); else layout();
    };
    el.addEventListener('pointerup', end); el.addEventListener('pointercancel', end);
    el.addEventListener('keydown', e => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      e.preventDefault();
      if (data.onPick) data.onPick({ kind: 'site', key: lf.host, label: lf.label, icon: lf.icon, current: lf.pending ? lf.pending.b : lf.b }, el);
    });
  }
  function moveTo(lf, k, t, y) {
    if (k === lf.b) { lf.pref = t; lf.drop = true; layout(); return; }
    if (!data.onDrop(lf.host, k)) { layout(); return; }
    lf.b = k; lf.pref = t; lf.drop = true; lf.pending = { b: k, at: Date.now() };
    const base = bez(geom(k), t); lf.side = y !== undefined && y < base[1] ? -1 : 1;
    paintLeaf(lf); layout();
  }

  // ---------- рисование ----------
  const cssCache = {}; let cssAt = 0;
  function css(v, time) {
    if (time - cssAt > 700) { for (const k in cssCache) delete cssCache[k]; cssAt = time; }
    const name = v.startsWith('var(') ? v.slice(4, -1) : v;
    return cssCache[name] ??= getComputedStyle(box).getPropertyValue(name).trim() || '#888';
  }
  function shape(k, time) {
    const b = br[k], p = geom(k), steps = 80, until = Math.max(2, Math.floor(steps * b.grow));
    const kb = Math.max(0, b.speed) / 1024;
    const w0 = b.thin ? 2.2 : b.active ? 3 + Math.min(12, Math.sqrt(kb) * 0.55) : 2.5, w1 = Math.max(1.8, w0 * 0.45);
    const L = [], R = [], off = data.offline;
    for (let i = 0; i <= until; i++) {
      const t = i / steps, pt = bez(p, t), n = norm(p, t);
      const droop = off ? t * t * 24 + Math.sin(t * 8 + time * .002) * 2 : (reduce() ? 0 : Math.sin(t * 6 + time * .0011) * 1.2);
      const w = (w0 + (w1 - w0) * t) / 2;
      L.push([pt[0] + n[0] * w, pt[1] + n[1] * w + droop]);
      R.push([pt[0] - n[0] * w, pt[1] - n[1] * w + droop]);
    }
    return { L, R, w0 };
  }
  function drawBranch(k, time) {
    const b = br[k];
    if (!b || b.grow < 0.01) return;
    const dead = data.offline || !b.active || b.wilt, col = css(dead ? 'var(--faint)' : b.color, time);
    const { L, R, w0 } = shape(k, time);
    ctx.save();
    ctx.beginPath(); ctx.moveTo(L[0][0], L[0][1]);
    for (const q of L) ctx.lineTo(q[0], q[1]);
    for (let i = R.length - 1; i >= 0; i--) ctx.lineTo(R[i][0], R[i][1]);
    ctx.closePath(); ctx.fillStyle = col;
    if (!dead) { ctx.shadowColor = col; ctx.shadowBlur = dragOver === k ? 26 : 12; }
    // «Всё через сервер»: прямая ветка остаётся целой, но тонкой и бледной — по ней идёт только локальная сеть и ваши правила.
    ctx.globalAlpha = dead ? .55 : b.thin ? .35 : (dragOver && dragOver !== k ? .4 : 1);
    ctx.fill(); ctx.restore();
    if (!dead && w0 > 6) {
      ctx.beginPath();
      for (let i = 0; i < L.length; i++) { const x = (L[i][0] + R[i][0]) / 2, y = (L[i][1] + R[i][1]) / 2 - 1; i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      ctx.strokeStyle = 'rgba(255,255,255,.25)'; ctx.lineWidth = 1.1; ctx.stroke();
    }
    if (b.grow > .6) for (const lf of leaves.values()) if (lf.b === k && lf.base && !lf.over && lf.pos && !lf.el.classList.contains('drag')) {
      const tip = lf.pos, by = lf.base[1] + (data.offline ? lf.t * lf.t * 24 : 0);
      ctx.beginPath(); ctx.moveTo(lf.base[0], by);
      ctx.quadraticCurveTo(lf.base[0] + (tip[0] - lf.base[0]) * .2, tip[1], tip[0], tip[1]);
      ctx.strokeStyle = col; ctx.globalAlpha = .5; ctx.lineWidth = 1.3; ctx.stroke(); ctx.globalAlpha = 1;
    }
    if (dead || reduce() || b.grow < .98 || b.speed < 50) { parts[k] = []; return; }
    const list = parts[k] || (parts[k] = []), kb = b.speed / 1024;
    const want = Math.min(60, Math.round(Math.log2(kb + 1) * 5) + 2), p = geom(k);
    while (list.length < want) list.push({ t: Math.random(), v: .0014 + Math.random() * .0014, r: 1.2 + Math.random() * 1.5 });
    list.length = Math.min(list.length, want);
    const pace = 0.6 + Math.min(2.4, Math.log10(kb + 1) / 1.6);
    ctx.fillStyle = '#fff';
    for (const q of list) {
      q.t += q.v * pace; if (q.t > 1) q.t -= 1;
      const pt = bez(p, q.t), d = tan(p, q.t);
      ctx.save(); ctx.translate(pt[0], pt[1] + Math.sin(q.t * 6 + time * .0011) * 1.2); ctx.rotate(Math.atan2(d[1], d[0]));
      ctx.globalAlpha = .85 * Math.sin(Math.PI * q.t);
      ctx.beginPath(); ctx.ellipse(0, 0, q.r * 2.1, q.r * .8, 0, 0, 7); ctx.fill(); ctx.restore();
    }
    ctx.globalAlpha = 1;
  }
  // Пока что-то тащат, ветки — широкие подписанные дорожки: в них легко попасть.
  const BAND_NAME = { proxy: 'ЧЕРЕЗ СЕРВЕР', direct: 'НАПРЯМУЮ', zapret: 'ЧЕРЕЗ ZAPRET' };
  function drawBands(time) {
    for (const k of ['proxy', 'direct', 'zapret']) {
      const b = br[k];
      if (!b || b.grow < .6 || !b.active) continue;
      const p = geom(k), col = css(b.color, time);
      ctx.save();
      ctx.beginPath();
      for (let i = 0; i <= 40; i++) { const q = bez(p, i / 40); i ? ctx.lineTo(q[0], q[1]) : ctx.moveTo(q[0], q[1]); }
      ctx.strokeStyle = col; ctx.lineWidth = 64; ctx.lineCap = 'round'; ctx.lineJoin = 'round';
      ctx.globalAlpha = dragOver === k ? .28 : .1; ctx.stroke();
      const q = bez(p, .8);
      ctx.globalAlpha = dragOver === k ? 1 : .75; ctx.fillStyle = col;
      ctx.font = '600 11px ' + (getComputedStyle(box).fontFamily || 'sans-serif');
      ctx.textAlign = 'center'; ctx.fillText(BAND_NAME[k], q[0], q[1] + (k === 'proxy' ? 26 : k === 'zapret' ? -20 : 24));
      ctx.restore();
    }
  }

  let growKey = '';
  // Подключение — одно движение: ветки вырастают из кнопки по очереди, узлы раскрываются следом.
  const GROW_DELAY = { proxy: 250, tg: 450, zapret: 650, direct: 0 };
  let onAt = 0, lastPhase = '';
  function frame(time) {
    raf = requestAnimationFrame(frame);
    if (!box || !box.isConnected || document.hidden || !data) return;
    ctx.clearRect(0, 0, W, H);
    const since = performance.now() - onAt;
    for (const k in br) {
      const b = br[k];
      const target = b.target > 0 && since < (GROW_DELAY[k] || 0) && !reduce() ? 0 : b.target;
      b.grow += (target - b.grow) * (reduce() ? 1 : 0.07);
      if (Math.abs(target - b.grow) < .002) b.grow = target;
      b.speed += (b.want - b.speed) * 0.08;
    }
    const key = Object.keys(br).map(k => k + (br[k].grow > .6 ? 1 : 0)).join('');
    if (key !== growKey) { growKey = key; layout(); }
    if (dragging) drawBands(time);
    for (const k of ['tg', 'zapret', 'proxy', 'direct']) drawBranch(k, time);
    cutHoles();
  }

  // ---------- вход из app.js раз в секунду ----------
  function update(container, d) {
    mount(container, d);
    if (d.phase === 'on' && lastPhase && lastPhase !== 'on') { onAt = performance.now(); popNodes = true; }
    lastPhase = d.phase;
    data = d;
    // Размер схемы сверяем каждую секунду, а не только по ResizeObserver: после сворачивания в трей
    // и разворачивания WebView не всегда сообщает о новом размере — ветки и узлы съезжали.
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height > 0 && (Math.abs(r.width - W) > 1 || Math.abs(r.height - H) > 1)) resize();
    const keys = new Set(d.lanes.map(l => l.key));
    for (const l of d.lanes) {
      const b = br[l.key] || (br[l.key] = { grow: 0, speed: 0 });
      Object.assign(b, { color: l.color, active: l.active, want: l.active ? l.speed : 0, wilt: l.wilt,
        thin: !!l.thin, target: d.phase === 'on' || l.key === 'direct' ? 1 : 0 });
    }
    for (const k in br) if (!keys.has(k)) br[k].target = 0;
    try { renderNodes(); } catch (e) { nodesSig = ''; console.error('branches: nodes', e); }
    placeNodes();
    syncLeaves();
    layout();
    return overflow;
  }

  // Перетаскивание программы с полки (она вне схемы): app.js сообщает, где указатель.
  function hoverAt(cx, cy) {
    if (!box) return null;
    const r = box.getBoundingClientRect();
    dragging = true;
    const nb = nearestBranch(cx - r.left, cy - r.top);
    dragOver = nb && nb.k;
    return dragOver;
  }
  function endHover() { dragging = false; const k = dragOver; dragOver = null; return k; }
  // Путь выбран в меню — лист переезжает на новую ветку (правило уже сохранено в app.js).
  function moveLeaf(host, k) {
    const lf = leaves.get(host);
    if (!lf || !br[k]) return;
    lf.b = k; lf.pref = .3 + Math.random() * .4; lf.drop = true; lf.pending = { b: k, at: Date.now() };
    paintLeaf(lf); layout();
  }

  return { update, unmount, hoverAt, endHover, moveLeaf };
})();
