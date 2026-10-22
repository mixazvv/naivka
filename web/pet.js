// Талисман внизу меню: живёт состоянием подключения. Отключено — спит; подключается — оглядывается;
// идёт трафик — бежит (чем быстрее, тем бодрее); сбой — пугается; связь вернулась — радуется.
// Глаза следят за мышью, его можно погладить (провести мышью несколько раз) и потыкать.
// В «Пикселях» он пиксельный. Выключается в «Настройках» → «Внешний вид».
const Pet = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const NS = 'http://www.w3.org/2000/svg';
  let root = null, svg = null, pupils = [], bubble = null;
  let state = '', prev = '', joyTimer = 0, bubbleTimer = 0, strokes = 0, strokeAt = 0, lastX = null;
  let say = () => '', onPurr = null;

  const el = (tag, attrs = {}, ...kids) => {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
    e.append(...kids);
    return e;
  };

  // Круглый зверёк: тело-капля с ушками и хвостом. Цвета — из темы (акцент и фон).
  function roundBody() {
    return el('g', { class: 'pet-round' },
      el('path', { class: 'pet-tail', d: 'M47 44c7 0 10-6 8-11' }),
      el('path', { class: 'pet-ear l', d: 'M19 21l-3-12 11 7z' }),
      el('path', { class: 'pet-ear r', d: 'M45 21l3-12-11 7z' }),
      el('ellipse', { class: 'pet-body', cx: 32, cy: 36, rx: 19, ry: 17 }),
      el('ellipse', { class: 'pet-belly', cx: 32, cy: 42, rx: 10, ry: 7 }),
      el('g', { class: 'pet-eyes' },
        el('g', { class: 'pet-eye l' }, el('ellipse', { class: 'pet-white', cx: 25, cy: 33, rx: 4.2, ry: 4.6 }), el('circle', { class: 'pet-pupil', cx: 25, cy: 33.5, r: 2.2 })),
        el('g', { class: 'pet-eye r' }, el('ellipse', { class: 'pet-white', cx: 39, cy: 33, rx: 4.2, ry: 4.6 }), el('circle', { class: 'pet-pupil', cx: 39, cy: 33.5, r: 2.2 }))),
      el('path', { class: 'pet-lid l', d: 'M21 33.5q4 3 8 0' }),
      el('path', { class: 'pet-lid r', d: 'M35 33.5q4 3 8 0' }),
      el('ellipse', { class: 'pet-cheek', cx: 20.5, cy: 39.5, rx: 2.6, ry: 1.6 }),
      el('ellipse', { class: 'pet-cheek', cx: 43.5, cy: 39.5, rx: 2.6, ry: 1.6 }),
      el('path', { class: 'pet-mouth', d: 'M29.5 40q2.5 2.4 5 0' }),
      el('ellipse', { class: 'pet-mouth-o', cx: 32, cy: 41, rx: 2, ry: 2.4 }),
      el('path', { class: 'pet-feet', d: 'M24 52.5h5M35 52.5h5' }));
  }

  // Пиксельный: 16×16 клеток. # — тело, o — глаз, . — живот, ^ — уши.
  const PIX = [
    '...^........^...',
    '..^^^......^^^..',
    '..^##########^..',
    '.##############.',
    '.##############.',
    '.###oo####oo###.',
    '.###oo####oo###.',
    '.##############.',
    '.#####....#####.',
    '.####......####.',
    '.####......####.',
    '..############..',
    '...##......##...',
  ];
  function pixelBody() {
    const g = el('g', { class: 'pet-pixel', 'shape-rendering': 'crispEdges', transform: 'translate(8 12) scale(3)' });
    PIX.forEach((row, y) => [...row].forEach((ch, x) => {
      if (ch === '.' && (y < 8 || y > 10)) return;
      const cls = ch === '#' || ch === '^' ? 'px-body' : ch === 'o' ? 'px-eye' : 'px-belly';
      g.append(el('rect', { x, y, width: 1, height: 1, class: cls }));
    }));
    return g;
  }

  function extras() {
    return el('g', { class: 'pet-fx' },
      el('g', { class: 'pet-zzz' }, el('text', { x: 48, y: 18 }, 'z'), el('text', { x: 53, y: 11 }, 'z'), el('text', { x: 57, y: 5 }, 'z')),
      el('path', { class: 'pet-sweat', d: 'M50 22c2 3 3 5 1.5 6.5s-4 0-3.5-2 2-4.5 2-4.5z' }),
      el('g', { class: 'pet-hearts' },
        el('path', { d: 'M14 14c-2-3 2-5 3-2 1-3 5-1 3 2l-3 3z' }),
        el('path', { d: 'M48 10c-2-3 2-5 3-2 1-3 5-1 3 2l-3 3z' })),
      el('g', { class: 'pet-spark' }, el('path', { d: 'M10 30l2-1-2-1-1-2-1 2-2 1 2 1 1 2zM56 36l2-1-2-1-1-2-1 2-2 1 2 1 1 2z' })),
      el('path', { class: 'pet-dust', d: 'M6 52h4M2 48h3M4 56h3' }));
  }

  function mount(container, opts = {}) {
    if (root) return;
    say = opts.say || say;
    onPurr = opts.onPurr || onPurr;
    root = document.createElement('div');
    root.className = 'pet';
    root.setAttribute('role', 'img');
    root.setAttribute('aria-label', 'Талисман Naivka');
    svg = el('svg', { viewBox: '0 0 64 64', 'aria-hidden': 'true' }, el('ellipse', { class: 'pet-shadow', cx: 32, cy: 55, rx: 15, ry: 2.6 }),
      el('g', { class: 'pet-jump' }, el('g', { class: 'pet-bob' }, roundBody(), pixelBody())), extras());
    bubble = document.createElement('div');
    bubble.className = 'pet-bubble';
    bubble.hidden = true;
    root.append(svg, bubble);
    container.prepend(root);
    pupils = [...svg.querySelectorAll('.pet-pupil')];
    root.addEventListener('click', poke);
    root.addEventListener('pointermove', pet);
    document.addEventListener('pointermove', look, { passive: true });
  }

  function unmount() {
    if (!root) return;
    document.removeEventListener('pointermove', look);
    root.remove();
    root = svg = bubble = null; state = prev = '';
  }

  // Глаза смотрят на мышь (не дальше 1,6 единицы от центра).
  let lookRaf = 0, lookE = null;
  function look(e) {
    lookE = e;
    if (lookRaf || !svg) return;
    lookRaf = requestAnimationFrame(() => {
      lookRaf = 0;
      if (!svg || !lookE) return;
      const r = svg.getBoundingClientRect(), cx = r.left + r.width / 2, cy = r.top + r.height * 0.5;
      const dx = lookE.clientX - cx, dy = lookE.clientY - cy, d = Math.hypot(dx, dy) || 1;
      const k = Math.min(1.6, d / 60);
      for (const p of pupils) p.style.transform = `translate(${(dx / d * k).toFixed(2)}px, ${(dy / d * k).toFixed(2)}px)`;
    });
  }

  // Погладить: несколько проходов мышью туда-обратно.
  function pet(e) {
    if (lastX != null && Math.sign(e.movementX || e.clientX - lastX) !== 0) {
      const now = performance.now();
      if (Math.abs(e.clientX - lastX) > 3) {
        if (now - strokeAt > 900) strokes = 0;
        strokes++; strokeAt = now;
        if (strokes >= 6) { strokes = 0; flash('purr', 1800); talk('Мррр…'); if (onPurr) onPurr(); }
      }
    }
    lastX = e.clientX;
  }

  function poke() {
    flash('hop', 650);
    talk(say(state) || '…');
  }

  function flash(cls, ms) {
    if (!root || REDUCE.matches) return;
    root.classList.remove(cls);
    void root.offsetWidth;
    root.classList.add(cls);
    setTimeout(() => root && root.classList.remove(cls), ms);
  }

  function talk(text, ms = 2600) {
    if (!bubble || !text) return;
    bubble.textContent = text;
    bubble.hidden = false;
    clearTimeout(bubbleTimer);
    bubbleTimer = setTimeout(() => { if (bubble) bubble.hidden = true; }, ms);
  }

  // update — фаза подключения (off | wait | on | revive | err | blocked) и скорость в байтах/с.
  function update(phase, bps) {
    if (!root) return;
    const s = phase === 'on' ? (bps > 30 * 1024 ? 'run' : 'on') : phase === 'wait' || phase === 'revive' ? 'wait' : phase === 'err' || phase === 'blocked' ? 'err' : 'off';
    // Связь вернулась после сбоя — праздник на пару секунд.
    if ((prev === 'err' || prev === 'wait') && (s === 'on' || s === 'run') && state !== s) {
      clearTimeout(joyTimer);
      root.classList.add('joy');
      joyTimer = setTimeout(() => root && root.classList.remove('joy'), 2400);
      if (prev === 'err') talk('Ура, связь вернулась!');
    }
    if (s !== state) { prev = state; state = s; root.dataset.mood = s; }
    // Чем быстрее трафик, тем чаще шаги: от 0,7 до 0,22 с на шаг.
    const kb = Math.max(0, bps) / 1024;
    root.style.setProperty('--pet-step', (0.7 - Math.min(0.48, Math.log10(1 + kb) * 0.13)).toFixed(2) + 's');
  }

  return { mount, unmount, update, talk, cheer: (t) => { flash('hop', 650); if (root) { root.classList.add('joy'); setTimeout(() => root && root.classList.remove('joy'), 2400); } talk(t, 3600); } };
})();
