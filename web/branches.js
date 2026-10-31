// Живые ветви — главная схема: от компьютера к интернету растут ветки «через сервер», «напрямую»,
// «zapret» и «Telegram». Толщина ветки — скорость, по ней летят «семена» трафика. На ветках висят
// приложения и сайты в компактных метках; метку можно перенести на другой маршрут.
// Рисуется на canvas (ветки, частицы) с HTML поверх (узлы, метки): так работают перетаскивание,
// подсказки и клавиатура. Анимация идёт только пока схема на экране.

const Branches = (() => {
  let box = null, cv = null, ctx = null, nodesEl = null, leavesEl = null, toastFn = null;
  let W = 0, H = 0, dpr = 1, raf = 0, ro = null;
  let frameRect = null;
  let data = null;             // последнее состояние из app.js
  let dragOver = null;
  let dragging = false;        // что-то тащат (лист или программу с полки) — ветки показываются широкими дорожками
  const br = {};               // ветки: key → { grow, speed, color, active }
  const parts = {};            // частицы по веткам
  const leaves = new Map();    // host → { host, b, pref, side, t, pos, base, el, seen, pending, over }
  const LEAF_TTL = 45000;      // лист без трафика живёт 45 с
  const MAX_LEAVES = 12;
  let untracked = {};
  const reduce = () => typeof REDUCE !== 'undefined' && REDUCE.matches;

  // ---------- геометрия ----------
  const narrow = () => W < 620;
  // Две кривые с общей точкой-узлом: вход и выход касаются кружка по одной касательной.
  const cx0 = () => Math.max(W * .095, narrow() ? 40 : 52);
  const netPoint = () => [narrow() ? W - 40 : W - 155, H * .5];
  const rootPoint = () => [cx0(), H * .5];
  function geom(key) {
    const c = rootPoint(), end = netPoint();
    const telegram = !!(br.tg?.active && br.tg.target > 0);
    const y = { proxy: H * (telegram ? .32 : .24), direct: H * .54, zapret: H * .83, tg: H * .1 }[key];
    // Одинаковые X узлов и управляющих точек сохраняют порядок маршрутов на всём пути.
    const x = W * .5;
    const reach = Math.min(W * .16, (x - c[0]) * .55, (end[0] - x) * .5);
    return [c, [c[0] + reach, c[1]], [x - reach, y], [x, y], [x + reach, y], [end[0] - reach, end[1]], end];
  }
  const cubic = (p, t) => { const u = 1 - t; return [0, 1].map(i => u*u*u*p[0][i] + 3*u*u*t*p[1][i] + 3*u*t*t*p[2][i] + t*t*t*p[3][i]); };
  const cubicTan = (p, t) => { const u = 1 - t; return [0, 1].map(i => 3*u*u*(p[1][i]-p[0][i]) + 6*u*t*(p[2][i]-p[1][i]) + 3*t*t*(p[3][i]-p[2][i])); };
  const bez = (p, t) => t <= .5 ? cubic(p.slice(0, 4), t * 2) : cubic(p.slice(3), (t - .5) * 2);
  const tan = (p, t) => t <= .5 ? cubicTan(p.slice(0, 4), t * 2) : cubicTan(p.slice(3), (t - .5) * 2);
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
    leaves.clear(); nodesSig = ''; growKey = ''; data = null; lastPhase = ''; lastFrame = 0; pulses.length = 0;
    for (const k in parts) delete parts[k];
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
    box.querySelectorAll('.br-more').forEach(el => el.remove());
    box.classList.remove('branches');
    box = null; leaves.clear(); dragging = false; dragOver = null;
    for (const k in parts) delete parts[k];
  }
  function resize() {
    if (!box) return;
    const r = box.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) return; // окно скрыто (в трее) — размер узнаем, когда покажется
    W = r.width; H = r.height; dpr = Math.min(2, devicePixelRatio || 1);
    cv.width = Math.round(W * dpr); cv.height = Math.round(H * dpr); ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    box.classList.toggle('small', narrow());
    box.classList.toggle('tight', H < 390);
    // Кнопка подключения стоит на месте узла «Компьютер».
    box.style.setProperty('--pcx', cx0() + 'px'); box.style.setProperty('--pcy', H * 0.5 + 'px');
    placeNodes(); layout();
  }

  // Узлы — HTML поверх canvas.
  let nodesSig = '', popNodes = false;
  function renderNodes() {
    const d = data, sig = JSON.stringify([d.phase, d.lanes.map(l => [l.key, l.node && [l.node.title, l.node.sub, l.node.cc], l.active]), d.endSub, d.compSub, d.controlLabel, narrow()]);
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
      const title = { proxy: 'Сервер', direct: 'Напрямую', zapret: 'zapret', tg: 'Telegram' }[l.key];
      const extra = l.node?.cc && d.flag ? d.flag(l.node.cc) : null;
      const el = mk(l.key, 'route-node', ICONS[l.node?.icon] || ICONS.route || ICONS.globe, l.node?.title || title, l.node?.sub || '', extra, () => d.onNode(l.key));
      el.title = l.node ? [l.node.title, l.node.sub].filter(Boolean).join(' · ') : title;
      if (d.phase !== 'on' || !l.active) el.dataset.off = '';
      out.push(el);
    }
    out.push(mk('net', narrow() ? 'end' : 'side', ICONS.globe, 'Интернет', d.endSub));
    const comp = document.createElement('div'); comp.className = 'br-node comp'; comp.dataset.key = 'comp';
    comp.innerHTML = '<span class="br-txt"><b></b><small></small></span>';
    comp.querySelector('b').textContent = d.controlLabel;
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
      else if (k === 'net') p = netPoint();
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
  const LEAF = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8"/><path d="M4 12h16M12 4c4 4 4 12 0 16-4-4-4-12 0-16"/></svg>';
  const routeBranch = r => r === 'direct' ? 'direct' : r === 'zapret' ? 'zapret' : r === 'telegram' ? 'tg' : 'proxy';
  function syncLeaves() {
    const now = Date.now(), d = data;
    untracked = {};
    if (!['on', 'revive', 'blocked'].includes(d.phase)) { for (const lf of leaves.values()) lf.el.remove(); leaves.clear(); return; }
    for (const t of d.top) {
      const host = t.key;
      const b = routeBranch(t.route);
      if (!b || !host || !br[b] || (!br[b].active && d.phase === 'on')) continue;
      let lf = leaves.get(host);
      if (!lf) {
        if (leaves.size >= MAX_LEAVES) {
          // вытесняем самый давний
          let old = null; for (const x of leaves.values()) if (!old || x.seen < old.seen) old = x;
          if (old && now - old.seen > 4000) { old.el.remove(); leaves.delete(old.host); } else { untracked[b] = (untracked[b] || 0) + 1; continue; }
        }
        lf = { host, label: t.label || host, icon: t.icon, kind: t.kind || 'site', exe: t.exe, b, pref: .6 + Math.random() * .2, side: Math.random() < .5 ? -1 : 1, seen: now, fresh: true };
        makeLeaf(lf); leaves.set(host, lf);
      }
      if (t.speed > 0 || t.speed === undefined) lf.seen = now;
      lf.el.dataset.flow = String(t.speed > 50);
      // Пользователь перетащил лист — держим его на новой ветке, пока трафик не пойдёт так же (или 10 минут).
      if (lf.pending && (lf.pending.b === b || now - lf.pending.at > 600000)) lf.pending = null;
      const want = lf.pending ? lf.pending.b : b;
      if (!lf.saving && lf.b !== want) { lf.b = want; lf.pref = .65 + Math.random() * .25; }
      paintLeaf(lf);
    }
    for (const lf of [...leaves.values()]) {
      const age = d.phase === 'on' ? now - lf.seen : 0;
      lf.el.classList.toggle('quiet', age > 10000);
      lf.el.style.setProperty('--leaf-life', Math.max(0, 1 - Math.max(0, age - LEAF_TTL + 6000) / 6000));
      if (age > LEAF_TTL && !lf.saving && !lf.el.classList.contains('drag')) { lf.el.remove(); leaves.delete(lf.host); }
    }
  }
  function makeLeaf(lf) {
    const el = document.createElement('button');
    el.type = 'button'; el.className = 'br-leaf';
    el.innerHTML = '<span class="br-leaf-body">' + (lf.icon ? `<span class="svc"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="${lf.icon}"/></svg></span>` : LEAF) + '<span class="h"></span></span><span class="why"></span>';
    el.dataset.host = lf.host; el.dataset.kind = lf.kind; el.title = lf.label;
    el.querySelector('.h').textContent = lf.label;
    lf.el = el; leavesEl.append(el); bindDrag(lf);
  }
  const BRANCH_NAME = { proxy: 'через сервер', direct: 'напрямую', zapret: 'через zapret', tg: 'Telegram' };
  function paintLeaf(lf) {
    lf.el.style.setProperty('--c', br[lf.b] ? br[lf.b].color : 'var(--faint)');
    const why = lf.saving ? 'Сохраняю правило…' : lf.pending ? `Ваше новое правило: ${BRANCH_NAME[lf.pending.b]}. Правило сохранено.` : data.why(lf.host, lf.b, { kind: lf.kind, exe: lf.exe });
    const w = lf.el.querySelector('.why');
    const parts = [...[why].flat(), 'Перетащите на другую ветку, чтобы изменить.'], sig = parts.join('|');
    if (w.dataset.t !== sig) {
      // Каждая часть — отдельным узлом: так их переводит английский интерфейс.
      w.replaceChildren(...parts.map(t => { const e = document.createElement('span'); e.textContent = t; return e; }));
      w.dataset.t = sig;
    }
    lf.el.dataset.branch = lf.b;
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
    const placed = [], over = { ...untracked };
    const list = [...leaves.values()].sort((a, b) => (b.drop ? 1 : 0) - (a.drop ? 1 : 0) || a.host.localeCompare(b.host));
    for (const lf of list) {
      const w = lf.el.offsetWidth || 110, h = lf.el.offsetHeight || 24, p = geom(lf.b);
      if (!br[lf.b] || br[lf.b].grow < .6) { lf.over = true; continue; }
      let best = null, bestScore = Infinity;
      const ts = []; for (let t = 0.14; t <= 0.88; t += 0.02) ts.push(+t.toFixed(2));
      ts.sort((a, b) => Math.abs(a - lf.pref) - Math.abs(b - lf.pref));
      outer: for (const t of ts) for (const side of [lf.side, -lf.side]) for (const off of [6, 14, 24]) {
        const base = bez(p, t), n = norm(p, t);
        const cx = base[0] + n[0] * side * (off + h / 2), cy = base[1] + n[1] * side * (off + h / 2);
        const r = { x: cx - w / 2 - 3, y: cy - h / 2 - 3, w: w + 6, h: h + 6 };
        let score = Math.abs(t - lf.pref) * 10 + (off > 6 ? 1 : 0);
        if (r.x < 4 || r.y < 4 || r.x + r.w > W - 4 || r.y + r.h > H - 4) score += 100;
        for (const b of blocked) if (hit(r, b)) score += 80;
        for (const b of placed) if (hit(r, b)) score += 80;
        for (const k in curves) for (const q of curves[k]) if (q[0] > r.x && q[0] < r.x + r.w && q[1] > r.y && q[1] < r.y + r.h) { score += k === lf.b ? 6 : 30; break; }
        if (score < bestScore) { bestScore = score; best = { t, side, cx, cy, r, base }; }
        if (score < 1.2) break outer;
      }
      lf.over = bestScore >= 80;
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
          lf.el.classList.add('born', 'sprouting'); lf.el.style.transform = tr; void lf.el.offsetWidth;
          requestAnimationFrame(() => lf.el.classList.remove('born'));
          setTimeout(() => lf.el.classList.remove('sprouting'), 750);
          lf.fresh = false;
        } else lf.el.style.transform = tr;
      }
      if (lf.pos) lf.el.style.setProperty('--tip-shift', (Math.max(115, Math.min(W - 115, lf.pos[0])) - lf.pos[0]) + 'px');
      lf.el.classList.toggle('up', !!lf.pos && lf.pos[1] > H * 0.6);
      lf.el.dataset.side = lf.side > 0 ? 'down' : 'up';
      lf.el.dataset.hidden = String(!!lf.over || !br[lf.b] || br[lf.b].grow < .6);
      lf.el.dataset.dead = String(!!data.offline || data.phase === 'revive' || data.phase === 'blocked');
    }
    overflow = over;
    for (const k in br) {
      let badge = box.querySelector(`.br-more[data-branch="${k}"]`);
      if (!over[k] || br[k].grow < .9) { if (badge) badge.remove(); continue; }
      if (!badge) {
        badge = document.createElement('button'); badge.className = 'br-more'; badge.type = 'button'; badge.dataset.branch = k;
        badge.title = 'Показать соединения'; badge.setAttribute('aria-label', 'Показать соединения');
        badge.addEventListener('click', () => { if (data.onMore) data.onMore(k); }); box.append(badge);
      }
      const end = bez(geom(k), .72);
      badge.textContent = '+' + over[k]; badge.style.color = br[k].color;
      badge.style.left = Math.min(W - 36, end[0] + 12) + 'px'; badge.style.top = end[1] - 12 + 'px';
    }
  }
  let overflow = {};

  // ---------- перетаскивание ----------
  function nearestBranch(x, y) {
    let best = null, bd = Infinity, bt = .5;
    for (const k of ['proxy', 'direct', 'zapret']) {
      if (!br[k] || br[k].grow < .6 || !br[k].active || br[k].wilt || data.offline || data.phase !== 'on') continue;
      const p = geom(k);
      for (let i = 4; i <= 56; i++) { const t = i / 60, q = bez(p, t), d = Math.hypot(q[0] - x, q[1] - y); if (d < bd) { bd = d; best = k; bt = t; } }
    }
    return bd < 110 ? { k: best, t: bt } : null;
  }
  function bindDrag(lf) {
    let sx, sy, ox, oy, moved = false, id = null;
    const el = lf.el;
    el.addEventListener('pointerdown', e => {
      if (e.button !== 0 || !lf.pos || lf.saving) return;
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
      if (e.type === 'pointercancel') { el.classList.remove('drag'); dragging = false; dragOver = null; layout(); return; }
      if (!moved) { if (data.onPick) data.onPick({ kind: lf.kind, exe: lf.exe, key: lf.host, label: lf.label, icon: lf.icon, current: lf.pending ? lf.pending.b : lf.b }, el); return; }
      el.classList.remove('drag'); dragging = false;
      const sr = box.getBoundingClientRect(), x = e.clientX - sr.left - ox, y = e.clientY - sr.top - oy;
      const nb = nearestBranch(x, y); dragOver = null;
      if (nb) moveTo(lf, nb.k, nb.t, y); else layout();
    };
    el.addEventListener('pointerup', end); el.addEventListener('pointercancel', end);
    // Захват мыши потерян без «отпустили» (окно ушло в фон и т. п.) — лист возвращается на место, а не висит.
    el.addEventListener('lostpointercapture', () => {
      if (id === null) return;
      id = null;
      if (moved) { el.classList.remove('drag'); dragging = false; dragOver = null; layout(); }
    });
    el.addEventListener('keydown', e => {
      if (lf.saving || e.key !== 'Enter' && e.key !== ' ') return;
      e.preventDefault();
      if (data.onPick) data.onPick({ kind: lf.kind, exe: lf.exe, key: lf.host, label: lf.label, icon: lf.icon, current: lf.pending ? lf.pending.b : lf.b }, el);
    });
  }
  async function moveTo(lf, k, t, y) {
    if (lf.saving || !br[k] || !br[k].active) { layout(); return; }
    if (k === lf.b) { lf.pref = t; lf.drop = true; layout(); return; }
    const before = { b: lf.b, pref: lf.pref, side: lf.side, pending: lf.pending };
    lf.b = k; lf.pref = t; lf.drop = true; lf.pending = { b: k, at: Date.now() };
    const base = bez(geom(k), t); lf.side = y !== undefined && y < base[1] ? -1 : 1;
    lf.saving = true; lf.el.setAttribute('aria-busy', 'true'); paintLeaf(lf); layout();
    let saved = false;
    try { saved = await data.onDrop(lf.host, k, { kind: lf.kind, exe: lf.exe }); } catch (_) { /* возвращаем лист */ }
    lf.saving = false; lf.el.removeAttribute('aria-busy');
    if (leaves.get(lf.host) !== lf) return;
    if (!saved) Object.assign(lf, before);
    else pulse(k, t);
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
    const w0 = b.thin ? 1.5 : b.active ? 2 + Math.min(1.6, Math.log2(kb + 1) * .2) : 1.5, w1 = Math.max(1, w0 * .5);
    const L = [], R = [];
    for (let i = 0; i <= until; i++) {
      const t = i / steps, pt = bez(p, t), n = norm(p, t);
      // Геометрия остаётся стабильной; движение трафика показывают частицы.
      const droop = b.droop * Math.sin(Math.PI * t * 2) ** 2 * 14;
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
    if (!dead) { ctx.shadowColor = col; ctx.shadowBlur = dragOver === k ? 18 : document.documentElement.dataset.skin === 'gold' ? 12 : 0; }
    // «Всё через сервер»: прямая ветка остаётся целой, но тонкой и бледной — по ней идёт только локальная сеть и ваши правила.
    ctx.globalAlpha = dead ? .55 : b.thin ? .35 : (dragOver && dragOver !== k ? .4 : 1);
    ctx.fill(); ctx.restore();
    if (!dead && w0 > 4) {
      ctx.beginPath();
      for (let i = 0; i < L.length; i++) { const x = (L[i][0] + R[i][0]) / 2, y = (L[i][1] + R[i][1]) / 2 - 1; i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      ctx.strokeStyle = 'rgba(255,255,255,.25)'; ctx.lineWidth = 1.1; ctx.stroke();
    }
    if (b.grow > .6) for (const lf of leaves.values()) if (lf.b === k && lf.base && !lf.over && lf.pos && !lf.el.classList.contains('drag')) {
      // Короткая связь следует за меткой, включая CSS-переход при переносе.
      const rect = lf.el.getBoundingClientRect();
      const tip = [rect.left - frameRect.left + rect.width / 2, rect.top - frameRect.top + (lf.side > 0 ? 0 : rect.height)];
      const by = lf.base[1] + b.droop * Math.sin(Math.PI * lf.t * 2) ** 2 * 14;
      ctx.beginPath(); ctx.moveTo(lf.base[0], by);
      ctx.quadraticCurveTo((lf.base[0] + tip[0]) / 2, by, tip[0], tip[1]);
      ctx.strokeStyle = col; ctx.globalAlpha = .5; ctx.lineWidth = 1.3; ctx.stroke(); ctx.globalAlpha = 1;
    }
    if (dead || reduce() || b.grow < .98) { parts[k] = []; return; }
    // Загрузка течёт к компьютеру (t уменьшается), отдача — к интернету.
    const list = parts[k] || (parts[k] = []), p = geom(k);
    for (const direction of [-1, 1]) {
      const speed = direction < 0 ? b.down : b.up;
      const want = speed < 50 ? 0 : Math.min(16, Math.ceil(Math.log2(speed / 1024 + 1) * 1.7));
      let stream = list.filter(q => q.direction === direction);
      while (stream.length < want) { const q = { t: Math.random(), direction }; list.push(q); stream.push(q); }
      for (const q of stream.slice(want)) list.splice(list.indexOf(q), 1);
      const pace = .12 + Math.min(.18, Math.log10(speed / 1024 + 1) * .04);
      for (const q of stream.slice(0, want)) {
        q.t = (q.t + direction * pace * frameDt + 1) % 1;
        const pt = bez(p, q.t), tangent = tan(p, q.t);
        ctx.save(); ctx.translate(pt[0], pt[1]);
        ctx.rotate(Math.atan2(tangent[1], tangent[0]) + (direction < 0 ? Math.PI : 0));
        ctx.fillStyle = col; ctx.globalAlpha = Math.sin(Math.PI * q.t) * .9;
        ctx.beginPath(); ctx.ellipse(0, 0, 4, 1.7, 0, 0, Math.PI * 2); ctx.fill();
        ctx.fillStyle = '#fff'; ctx.globalAlpha *= .7; ctx.beginPath(); ctx.arc(2, 0, 1, 0, Math.PI * 2); ctx.fill(); ctx.restore();
      }
    }
  }
  const pulses = [];
  function pulse(k, t = 0) {
    if (reduce()) return;
    pulses.push({ k, t, at: performance.now() });
    if (pulses.length > 8) pulses.shift();
  }
  function drawPulses(time) {
    for (let i = pulses.length - 1; i >= 0; i--) {
      const q = pulses[i], progress = (time - q.at) / 1300;
      if (progress >= 1 || reduce()) { pulses.splice(i, 1); continue; }
      const b = br[q.k]; if (!b || b.grow < .9) continue;
      const t = q.t + (1 - q.t) * progress, pos = bez(geom(q.k), t);
      ctx.save(); ctx.strokeStyle = css(b.color, time); ctx.globalAlpha = (1 - progress) * .7; ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.arc(pos[0], pos[1], 3 + progress * 7, 0, Math.PI * 2); ctx.stroke(); ctx.restore();
    }
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
  let onAt = 0, lastPhase = '', lastFrame = 0, frameDt = 1 / 60;
  function frame(time) {
    raf = requestAnimationFrame(frame);
    if (!box || !box.isConnected || document.hidden || box.closest('[inert]') || !data) { lastFrame = 0; return; }
    frameDt = lastFrame ? Math.min(.05, (time - lastFrame) / 1000) : 1 / 60; lastFrame = time;
    frameRect = box.getBoundingClientRect();
    ctx.clearRect(0, 0, W, H);
    const since = performance.now() - onAt;
    for (const k in br) {
      const b = br[k];
      const target = b.target > 0 && since < (GROW_DELAY[k] || 0) && !reduce() ? 0 : b.target;
      b.grow += (target - b.grow) * (reduce() ? 1 : 1 - Math.exp(-frameDt * 7));
      if (Math.abs(target - b.grow) < .002) b.grow = target;
      const ease = reduce() ? 1 : 1 - Math.exp(-frameDt * 6);
      b.speed += (b.want - b.speed) * ease;
      b.down += (b.wantDown - b.down) * ease; b.up += (b.wantUp - b.up) * ease;
      b.droop += ((data.offline || data.phase === 'revive' || data.phase === 'blocked' ? 1 : 0) - b.droop) * ease;
    }
    const key = Object.keys(br).map(k => k + (br[k].grow > .6 ? 1 : 0)).join('');
    if (key !== growKey) { growKey = key; layout(); }
    if (dragging) drawBands(time);
    for (const k of ['tg', 'zapret', 'proxy', 'direct']) drawBranch(k, time);
    drawPulses(time);
    cutHoles();
  }

  // ---------- вход из app.js раз в секунду ----------
  function update(container, d) {
    mount(container, d);
    if (d.phase === 'on' && lastPhase !== 'on') { if (!['revive', 'blocked'].includes(lastPhase)) onAt = performance.now(); popNodes = true; pulse('proxy'); }
    else if (d.phase === 'on' && data && (data.offline && !d.offline || data.lanes.find(l => l.key === 'proxy')?.node?.title !== d.lanes.find(l => l.key === 'proxy')?.node?.title)) pulse('proxy', .4);
    box.dataset.phase = d.phase;
    lastPhase = d.phase;
    data = d;
    // Размер схемы сверяем каждую секунду, а не только по ResizeObserver: после сворачивания в трей
    // и разворачивания WebView не всегда сообщает о новом размере — ветки и узлы съезжали.
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height > 0 && (Math.abs(r.width - W) > 1 || Math.abs(r.height - H) > 1)) resize();
    const keys = new Set(d.lanes.map(l => l.key));
    for (const l of d.lanes) {
      const b = br[l.key] || (br[l.key] = { grow: 0, speed: 0, down: 0, up: 0, droop: 0 });
      Object.assign(b, { color: l.color, active: l.active, want: l.active ? l.speed : 0, wilt: l.wilt,
        wantDown: l.active ? Math.max(0, l.down ?? l.speed) : 0, wantUp: l.active ? Math.max(0, l.up || 0) : 0,
        thin: !!l.thin, target: ['on', 'revive', 'blocked'].includes(d.phase) || l.key === 'direct' ? 1 : 0 });
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
    lf.b = k; lf.pref = .65 + Math.random() * .25; lf.drop = true; lf.pending = { b: k, at: Date.now() };
    pulse(k, lf.pref); paintLeaf(lf); layout();
  }

  function clearLeaf(host) {
    const lf = leaves.get(host); if (lf) lf.pending = null;
  }
  return { update, unmount, hoverAt, endHover, moveLeaf, clearLeaf };
})();
