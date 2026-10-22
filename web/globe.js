// Глобус трафика («Сводка» → «Сейчас»): объёмная Земля из точек суши, её можно крутить мышью
// и приближать колесом. Дуги идут от вас к странам, где стоят сайты: «через сервер» — через
// страну сервера, «напрямую» и «через zapret» — сразу. Толщина и частицы — по скорости.
// Точка страны — кнопка: по ней app.js показывает сайты этой страны и меню выбора пути.
const Globe = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const RAD = Math.PI / 180;
  let box = null, cv = null, ctx = null, tip = null, ro = null, raf = 0;
  let W = 0, H = 0, R = 0, dpr = 1;
  // Вид: центр (долгота, широта), масштаб; цель — куда плавно доворачиваем.
  const view = { lon: 37, lat: 30, zoom: 1, tlon: null, tlat: null, tzoom: null };
  let drag = null, idleAt = 0, spin = 0; // spin — скорость вращения после броска, град/с
  let data = { me: null, srv: null, flows: [], on: false };
  let nodes = []; // точки стран на экране для наведения и щелчка
  let land = null;
  let opts = { onPick: null, onSpin: null };
  let last = 0, parts = new Map(); // частицы по ключу дуги

  function decodeLand() {
    if (land) return land;
    const { step, top, cols, rows, data: b64 } = WORLD_DOTS;
    const B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
    const out = [];
    for (let r = 0, i = 0; r < rows; r++) {
      const lat = top - r * step, off = (r % 2) * step / 2;
      for (let c = 0; c < cols; c++, i++) {
        if (!((B64.indexOf(b64[i / 6 | 0]) >> (5 - i % 6)) & 1)) continue;
        // Сетка равнопромежуточная: у полюсов точек слишком много — прореживаем по cos(широты),
        // иначе вокруг полюса видны кольца.
        const keep = ((i * 2654435761) >>> 0) / 4294967296;
        if (keep > Math.cos(lat * RAD) * 1.7) continue;
        out.push(vec(lat, -180 + step / 2 + c * step + off - step / 4));
      }
    }
    land = out;
    return land;
  }

  const vec = (lat, lon) => [Math.cos(lat * RAD) * Math.sin(lon * RAD), Math.sin(lat * RAD), Math.cos(lat * RAD) * Math.cos(lon * RAD)];
  // Поворот точки сферы к зрителю: сначала по долготе, потом по наклону.
  function rot(p) {
    const a = -view.lon * RAD, b = view.lat * RAD;
    const x = p[0] * Math.cos(a) + p[2] * Math.sin(a), z0 = -p[0] * Math.sin(a) + p[2] * Math.cos(a);
    const y = p[1] * Math.cos(b) - z0 * Math.sin(b), z = p[1] * Math.sin(b) + z0 * Math.cos(b);
    return [x, y, z];
  }
  const scr = q => [W / 2 + q[0] * R, H / 2 - q[1] * R];

  function slerp(a, b, t) {
    const d = Math.acos(Math.max(-1, Math.min(1, a[0] * b[0] + a[1] * b[1] + a[2] * b[2])));
    if (d < 1e-6) return a.slice();
    const s1 = Math.sin((1 - t) * d) / Math.sin(d), s2 = Math.sin(t * d) / Math.sin(d);
    return [a[0] * s1 + b[0] * s2, a[1] * s1 + b[1] * s2, a[2] * s1 + b[2] * s2];
  }
  // Дуга — дуга большого круга, приподнятая над поверхностью тем выше, чем она длиннее.
  function arcPts(a, b, n = 48) {
    const d = Math.acos(Math.max(-1, Math.min(1, a[0] * b[0] + a[1] * b[1] + a[2] * b[2])));
    const lift = 0.04 + d * 0.16;
    const out = [];
    for (let i = 0; i <= n; i++) {
      const t = i / n, p = slerp(a, b, t), k = 1 + lift * Math.sin(Math.PI * t);
      out.push([p[0] * k, p[1] * k, p[2] * k]);
    }
    return out;
  }
  // Точка видна, если она на ближней стороне или выступает за край диска.
  const visible = q => q[2] >= 0 || q[0] * q[0] + q[1] * q[1] > 1;

  function css(name, fb) { const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim(); return v || fb; }
  function colors() {
    return {
      land: css('--muted', '#8F959B'), ink: css('--ink', '#ECEEEA'), line: css('--line', '#272A2F'), bg: css('--surface', '#16181B'),
      proxy: css('--tunnel', '#B6E35A'), direct: css('--direct', '#5BB4EE'), zapret: css('--warn', '#F5B44A'), me: css('--ink', '#ECEEEA'),
      pixel: document.documentElement.dataset.skin === 'blocks',
    };
  }

  function resize() {
    if (!box) return;
    dpr = Math.min(2, devicePixelRatio || 1);
    W = box.clientWidth; H = box.clientHeight;
    cv.width = Math.max(1, W * dpr); cv.height = Math.max(1, H * dpr);
    cv.style.width = W + 'px'; cv.style.height = H + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    kick();
  }

  function mount(container, o) {
    if (box === container) { opts = { ...opts, ...o }; return; }
    unmount();
    box = container; opts = { ...opts, ...o };
    cv = document.createElement('canvas');
    cv.className = 'globe-cv';
    cv.setAttribute('role', 'img');
    cv.setAttribute('aria-label', 'Глобус: куда идёт трафик');
    tip = document.createElement('div');
    tip.className = 'globe-tip';
    tip.hidden = true;
    box.replaceChildren(cv, tip);
    ctx = cv.getContext('2d');
    if (window.ResizeObserver) { ro = new ResizeObserver(resize); ro.observe(box); }
    cv.addEventListener('pointerdown', down);
    cv.addEventListener('pointermove', move);
    cv.addEventListener('pointerup', up);
    cv.addEventListener('pointercancel', up);
    cv.addEventListener('pointerleave', () => { if (!drag) hideTip(); });
    cv.addEventListener('wheel', wheel, { passive: false });
    cv.addEventListener('dblclick', () => { focusMe(); });
    document.addEventListener('visibilitychange', kick);
    resize();
  }

  function unmount() {
    cancelAnimationFrame(raf); raf = 0;
    if (ro) ro.disconnect();
    ro = null; box = null; cv = null; ctx = null; tip = null; parts.clear();
    document.removeEventListener('visibilitychange', kick);
  }

  // ---------- управление ----------
  function down(e) {
    if (e.button !== 0) return;
    cv.setPointerCapture(e.pointerId);
    drag = { x: e.clientX, y: e.clientY, sx: e.clientX, sy: e.clientY, t: performance.now(), vx: 0, moved: false };
    view.tlon = view.tlat = view.tzoom = null; spin = 0; touched = true;
    cv.classList.add('grab');
  }
  function move(e) {
    if (!drag) { hover(e); return; }
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y, now = performance.now();
    const k = 180 / (Math.PI * R);
    view.lon -= dx * k; view.lat = Math.max(-70, Math.min(70, view.lat + dy * k));
    drag.vx = -dx * k / Math.max(1, now - drag.t) * 1000;
    drag.x = e.clientX; drag.y = e.clientY; drag.t = now;
    if (Math.hypot(e.clientX - drag.sx, e.clientY - drag.sy) > 4) drag.moved = true;
    hideTip();
    kick();
  }
  function up(e) {
    if (!drag) return;
    const d = drag; drag = null; idleAt = performance.now();
    cv.classList.remove('grab');
    if (!d.moved) { const n = nodeAt(e); if (n && opts.onPick) opts.onPick(n.flow, n.rect()); return; }
    spin = REDUCE.matches ? 0 : Math.max(-120, Math.min(120, d.vx));
    if (opts.onSpin) opts.onSpin();
    kick();
  }
  function wheel(e) {
    e.preventDefault();
    view.tzoom = null;
    view.zoom = Math.max(0.8, Math.min(3, view.zoom * (e.deltaY > 0 ? 0.9 : 1.1)));
    idleAt = performance.now();
    kick();
  }
  function nodeAt(e) {
    const r = cv.getBoundingClientRect(), x = e.clientX - r.left, y = e.clientY - r.top;
    let best = null, bd = 1e9;
    for (const n of nodes) { const d = Math.hypot(n.x - x, n.y - y); if (d < Math.max(12, n.r + 6) && d < bd) { best = n; bd = d; } }
    return best;
  }
  function hover(e) {
    const n = nodeAt(e);
    cv.classList.toggle('pick', !!n);
    if (!n) { hideTip(); return; }
    tip.hidden = false;
    tip.replaceChildren();
    const f = n.flow;
    const b = document.createElement('b'); b.textContent = f.name; tip.append(b);
    for (const s of f.sites.slice(0, 4)) { const i = document.createElement('span'); i.textContent = s.label + (s.say ? ' · ' + s.say : ''); tip.append(i); }
    if (f.sites.length > 4) { const i = document.createElement('small'); i.textContent = '…и ещё ' + (f.sites.length - 4); tip.append(i); }
    const sm = document.createElement('small'); sm.textContent = 'Нажмите, чтобы выбрать путь'; tip.append(sm);
    tip.style.left = Math.min(W - 220, n.x + 14) + 'px';
    tip.style.top = Math.max(6, n.y - 10) + 'px';
  }
  function hideTip() { if (tip) tip.hidden = true; }

  function focusOn(ll) { if (!ll) return; view.tlon = ll[1]; view.tlat = Math.max(-60, Math.min(60, ll[0])); kick(); }
  // К вам: центр между вами и сервером, масштаб — чтобы все точки поместились, но не мельчили.
  function focusMe() {
    const a = data.me && data.me.ll, b = data.srv && data.srv.ll;
    const c = a && b ? [(a[0] + b[0]) / 2, a[1] + (((b[1] - a[1] + 540) % 360) - 180) / 2] : a || b;
    if (!c) return;
    const cv0 = vec(c[0], c[1]);
    // Масштаб — по ближним точкам (до ~55°): дальние дуги (в Америку, Азию) уходят за горизонт,
    // а Европа не сливается в одну точку.
    let far = 0.2;
    for (const ll of [a, b, ...data.flows.map(f => f.ll)]) {
      if (!ll) continue;
      const p = vec(ll[0], ll[1]);
      const d = Math.acos(Math.max(-1, Math.min(1, p[0] * cv0[0] + p[1] * cv0[1] + p[2] * cv0[2])));
      if (d < 0.96) far = Math.max(far, d);
    }
    view.tzoom = Math.max(1, Math.min(2, 0.7 / Math.sin(far)));
    focusOn(c);
  }

  // ---------- данные ----------
  let first = true, touched = false, shown = 0;
  function update(d) {
    const had = data.me;
    data = d;
    // Первые данные — сразу к вам; новые страны (пока человек не крутил глобус) — масштаб под них.
    if (first || (!had && d.me)) { first = false; view.lon = (d.me && d.me.ll[1]) || 37; view.lat = d.me ? Math.max(-60, Math.min(60, d.me.ll[0])) : 25; focusMe(); }
    else if (!touched && d.flows.length !== shown) focusMe();
    shown = d.flows.length;
    kick();
  }

  // ---------- рисование ----------
  function kick() { if (!raf && box && !document.hidden) raf = requestAnimationFrame(frame); }

  function frame(now) {
    raf = 0;
    if (!box || !ctx || !W) return;
    const dt = Math.min(0.1, (now - (last || now)) / 1000);
    last = now;
    let busy = false;
    // Плавный доворот к цели, бросок с затуханием, медленное вращение в покое.
    if (view.tzoom != null) {
      view.zoom += (view.tzoom - view.zoom) * Math.min(1, dt * 4);
      if (Math.abs(view.tzoom - view.zoom) < 0.005) view.tzoom = null;
      busy = true;
    }
    if (view.tlon != null) {
      const dl = ((view.tlon - view.lon + 540) % 360) - 180, dla = view.tlat - view.lat;
      view.lon += dl * Math.min(1, dt * 4); view.lat += dla * Math.min(1, dt * 4);
      if (Math.abs(dl) < 0.05 && Math.abs(dla) < 0.05) view.tlon = view.tlat = null;
      busy = true;
    } else if (!drag && Math.abs(spin) > 0.5) {
      view.lon += spin * dt; spin *= Math.pow(0.12, dt); busy = true;
    } else if (!drag && !REDUCE.matches && now - idleAt > 5000) {
      view.lon += 2.2 * dt; busy = true; // «дышит»: оборот за 2,5 минуты
    }
    draw(now, dt);
    if (data.on && !REDUCE.matches && data.flows.some(f => f.speed > 0)) busy = true;
    if (busy || drag) raf = requestAnimationFrame(frame);
  }

  // Цвет из CSS (HEX, rgb() — любой) с прозрачностью: холст сам приводит его к одному виду.
  function alpha(col, a) {
    ctx.fillStyle = col;
    const v = ctx.fillStyle;
    if (v[0] === '#') { const n = parseInt(v.slice(1), 16); return `rgba(${n >> 16 & 255}, ${n >> 8 & 255}, ${n & 255}, ${a})`; }
    return v.replace(/rgba?\(([^)]+)\)/, (_, x) => `rgba(${x.split(',').slice(0, 3).join(',')}, ${a})`);
  }

  function draw(now, dt) {
    const cs = colors();
    R = Math.min(W, H) * 0.42 * view.zoom;
    ctx.clearRect(0, 0, W, H);
    const cx = W / 2, cy = H / 2;
    // Атмосфера и диск.
    const g = ctx.createRadialGradient(cx, cy, R * 0.92, cx, cy, R * 1.18);
    g.addColorStop(0, alpha(cs.proxy, 0.14)); g.addColorStop(1, alpha(cs.proxy, 0));
    ctx.fillStyle = g; ctx.beginPath(); ctx.arc(cx, cy, R * 1.18, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = cs.bg; ctx.beginPath(); ctx.arc(cx, cy, R, 0, Math.PI * 2); ctx.fill();
    ctx.strokeStyle = cs.line; ctx.lineWidth = 1; ctx.stroke();
    // Суша: ближе к зрителю — ярче, к краю — тусклее.
    const dot = Math.max(1.2, Math.min(3, R / 190));
    ctx.fillStyle = cs.land;
    for (const p of decodeLand()) {
      const q = rot(p);
      if (q[2] <= 0.02) continue;
      ctx.globalAlpha = 0.22 + 0.6 * q[2];
      const [x, y] = scr(q);
      if (cs.pixel) ctx.fillRect(Math.round(x / 3) * 3 - 1, Math.round(y / 3) * 3 - 1, 2.4, 2.4);
      else ctx.fillRect(x - dot / 2, y - dot / 2, dot, dot);
    }
    ctx.globalAlpha = 1;

    // Дуги: «через сервер» — вы → сервер → страна сайта; остальное — вы → страна сайта.
    const me = data.me && vec(...data.me.ll), srv = data.srv && vec(...data.srv.ll);
    const arcs = [];
    const proxyTotal = data.flows.filter(f => f.route === 'proxy').reduce((a, f) => a + f.speed, 0);
    if (me && srv) arcs.push({ key: 'me-srv', pts: arcPts(me, srv), col: cs.proxy, speed: data.on ? proxyTotal : 0, dim: !data.on });
    nodes = [];
    for (const f of data.flows) {
      const to = vec(...f.ll), from = f.route === 'proxy' && srv ? srv : me;
      if (!from) continue;
      const col = cs[f.route] || cs.proxy;
      arcs.push({ key: f.route + ':' + f.cc, pts: arcPts(from, to), col, speed: f.speed });
      const q = rot(to);
      if (q[2] > 0) {
        const [x, y] = scr(q), r = 3 + Math.min(7, Math.log10(1 + f.speed / 1024) * 2.2);
        nodes.push({ x, y, r, flow: f, col, rect: () => { const b = cv.getBoundingClientRect(); return { left: b.left + x - 8, top: b.top + y - 8, right: b.left + x + 8, bottom: b.top + y + 8, width: 16, height: 16 }; } });
      }
    }
    const seen = new Set();
    for (const a of arcs) {
      seen.add(a.key);
      const w = a.speed > 0 ? 1.2 + Math.min(3.2, Math.log10(1 + a.speed / 1024) * 0.9) : 1;
      ctx.strokeStyle = a.col; ctx.lineWidth = w; ctx.globalAlpha = a.dim ? 0.35 : 0.85;
      ctx.lineCap = cs.pixel ? 'butt' : 'round';
      if (a.dim) ctx.setLineDash([4, 5]); else ctx.setLineDash([]);
      ctx.beginPath();
      let pen = false;
      for (const p of a.pts) {
        const q = rot(p);
        if (!visible(q)) { pen = false; continue; }
        let [x, y] = scr(q);
        if (cs.pixel) { x = Math.round(x / 3) * 3; y = Math.round(y / 3) * 3; }
        if (pen) ctx.lineTo(x, y); else { ctx.moveTo(x, y); pen = true; }
      }
      ctx.stroke();
      ctx.setLineDash([]);
      // Частицы: чем быстрее, тем их больше и тем быстрее бегут.
      let ps = parts.get(a.key);
      if (!ps) { ps = []; parts.set(a.key, ps); }
      const want = a.speed > 0 && !REDUCE.matches ? Math.min(8, 1 + Math.round(Math.log10(1 + a.speed / 1024) * 2)) : 0;
      while (ps.length < want) ps.push(Math.random());
      ps.length = want;
      const v = 0.18 + Math.min(0.5, Math.log10(1 + a.speed / 1024) * 0.12);
      ctx.fillStyle = a.col; ctx.globalAlpha = 1;
      for (let i = 0; i < ps.length; i++) {
        ps[i] = (ps[i] + v * dt) % 1;
        const p = a.pts[Math.min(a.pts.length - 1, Math.round(ps[i] * (a.pts.length - 1)))], q = rot(p);
        if (!visible(q)) continue;
        const [x, y] = scr(q);
        ctx.beginPath();
        if (cs.pixel) ctx.rect(Math.round(x / 3) * 3 - 2, Math.round(y / 3) * 3 - 2, 4, 4); else ctx.arc(x, y, w + 0.8, 0, Math.PI * 2);
        ctx.fill();
      }
    }
    for (const k of parts.keys()) if (!seen.has(k)) parts.delete(k);
    ctx.globalAlpha = 1;

    // Точки: страны сайтов, сервер, вы.
    for (const n of nodes) {
      ctx.fillStyle = n.col;
      ctx.beginPath();
      if (cs.pixel) ctx.rect(Math.round(n.x) - n.r, Math.round(n.y) - n.r, n.r * 2, n.r * 2); else ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = cs.bg; ctx.lineWidth = 2; ctx.stroke();
    }
    const pin = (ll, col, label, big) => {
      if (!ll) return;
      const q = rot(vec(...ll));
      if (q[2] <= 0) return;
      const [x, y] = scr(q);
      const pulse = REDUCE.matches ? 0 : (now / 1000) % 1.6 / 1.6;
      ctx.strokeStyle = col; ctx.globalAlpha = 0.6 * (1 - pulse); ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(x, y, 5 + pulse * 12, 0, Math.PI * 2); ctx.stroke();
      ctx.globalAlpha = 1; ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(x, y, big ? 5 : 4, 0, Math.PI * 2); ctx.fill();
      ctx.strokeStyle = cs.bg; ctx.lineWidth = 2; ctx.stroke();
      ctx.font = `600 12px ${getComputedStyle(document.body).fontFamily}`;
      ctx.lineWidth = 4; ctx.strokeStyle = cs.bg; ctx.strokeText(label, x + 9, y - 8);
      ctx.fillStyle = cs.ink; ctx.fillText(label, x + 9, y - 8);
    };
    if (data.srv) pin(data.srv.ll, data.on ? cs.proxy : cs.land, data.srv.label);
    if (data.me) pin(data.me.ll, cs.me, data.me.label, true);
  }

  // points — где на холсте точки стран (для проверок и подсказок).
  return { mount, unmount, update, focusMe, kick, points: () => nodes.map(n => ({ x: n.x, y: n.y, cc: n.flow.cc, route: n.flow.route })) };
})();
