// Талисман внизу меню — серый котёнок: живёт состоянием подключения. Отключено — спит; подключается — оглядывается;
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
    for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
    e.append(...kids);
    return e;
  };

  // Кот — по образцу настоящего: дымчато-серый пушистый котёнок, «М» из полосок на лбу, янтарные глаза,
  // розовый нос, светлая мордочка, большие уши с кисточками. Голова чуть наклонена. Ошейник — цвета акцента.
  const fur = (d, cls = 'pet-fur') => el('path', { class: cls, d });
  function roundBody() {
    return el('g', { class: 'pet-round' },
      el('defs', {},
        el('linearGradient', { id: 'petFurG', x1: 0, y1: 0, x2: 0, y2: 1 },
          el('stop', { offset: 0, 'stop-color': '#B9BEC6' }), el('stop', { offset: 1, 'stop-color': '#858B95' })),
        el('radialGradient', { id: 'petIrisG', cx: 0.45, cy: 0.4, r: 0.6 },
          el('stop', { offset: 0, 'stop-color': '#E8C27A' }), el('stop', { offset: 0.65, 'stop-color': '#B98436' }), el('stop', { offset: 1, 'stop-color': '#7A5320' }))),
      // Пушистый хвост с тёмными кольцами.
      el('g', { class: 'pet-tail' },
        el('path', { class: 'pet-tail-fur', d: 'M43 51c8 1.5 15-3 14.5-11.5-.2-3-1.6-5.5-3.6-7' }),
        el('path', { class: 'pet-tail-ring', d: 'M43 51c8 1.5 15-3 14.5-11.5-.2-3-1.6-5.5-3.6-7' })),
      // Тело сидя: спинка, грудка светлее, лапки.
      fur('M18.5 54c-1.5-8 2-17 13.5-17s15 9 13.5 17c-.4 2-2 3-4 3H22.5c-2 0-3.6-1-4-3z', 'pet-body'),
      fur('M26 41c2-1.6 10-1.6 12 0 1.2 4 .8 9-1 13h-10c-1.8-4-2.2-9-1-13z', 'pet-chest'),
      el('ellipse', { class: 'pet-paw', cx: 27, cy: 56, rx: 3.6, ry: 2.2 }),
      el('ellipse', { class: 'pet-paw', cx: 37, cy: 56, rx: 3.6, ry: 2.2 }),
      el('path', { class: 'pet-toes', d: 'M25.6 55.6v1.3M28.3 55.6v1.3M35.7 55.6v1.3M38.4 55.6v1.3' }),
      el('path', { class: 'pet-collar', d: 'M22 38.5q10 4.5 20 0' }),
      el('circle', { class: 'pet-tag', cx: 32, cy: 41.2, r: 1.6 }),
      el('g', { class: 'pet-head' },
        // Уши: большие, с розовым внутри и кисточками.
        fur('M14.5 24.5L13 8.5c0-1 1-1.4 1.7-.8l10.8 9.6z', 'pet-ear l'),
        fur('M49.5 24.5L51 8.5c0-1-1-1.4-1.7-.8l-10.8 9.6z', 'pet-ear r'),
        el('path', { class: 'pet-ear-in', d: 'M16.5 21.5l-1-10 7.5 6.8zM47.5 21.5l1-10-7.5 6.8z' }),
        el('path', { class: 'pet-tuft', d: 'M17 13.5l2.5 3M15.8 16l3 1.8M47 13.5l-2.5 3M48.2 16l-3 1.8' }),
        // Голова с пушистыми щеками.
        fur('M32 13c9.5 0 16.5 6.2 16.5 14.3 0 2.2-.6 4.2-1.6 6l2.6 1.4-3.6.6c-.4.6-.9 1.1-1.4 1.6l1.8 2-3.4-.8C40 40 36.2 41 32 41s-8-1-10.9-2.9l-3.4.8 1.8-2c-.5-.5-1-1-1.4-1.6l-3.6-.6 2.6-1.4c-1-1.8-1.6-3.8-1.6-6C15.5 19.2 22.5 13 32 13z', 'pet-face'),
        // «М» из полосок на лбу и полоски у глаз.
        el('path', { class: 'pet-stripe', d: 'M28.5 15.5l1.2 5M32 14.6v5.6M35.5 15.5l-1.2 5M23.5 18.5l2.8 3.2M40.5 18.5l-2.8 3.2M18 28.5l3.5.6M46 28.5l-3.5.6' }),
        // Светлая мордочка.
        el('ellipse', { class: 'pet-muzzle', cx: 29.4, cy: 33.8, rx: 3.6, ry: 2.8 }),
        el('ellipse', { class: 'pet-muzzle', cx: 34.6, cy: 33.8, rx: 3.6, ry: 2.8 }),
        el('ellipse', { class: 'pet-muzzle', cx: 32, cy: 36.6, rx: 3, ry: 2 }),
        el('g', { class: 'pet-eyes' },
          el('g', { class: 'pet-eye l' },
            el('path', { class: 'pet-white', d: 'M20.8 26.6c1.6-2.8 6-3 7.6.2-1.2 2.9-6.1 3-7.6-.2z' }),
            el('circle', { class: 'pet-iris', cx: 24.6, cy: 26.7, r: 2.7 }),
            el('ellipse', { class: 'pet-pupil', cx: 24.6, cy: 26.7, rx: 1, ry: 2.2 }),
            el('circle', { class: 'pet-glint', cx: 25.6, cy: 25.6, r: 0.7 })),
          el('g', { class: 'pet-eye r' },
            el('path', { class: 'pet-white', d: 'M35.6 26.8c1.6-3.2 6-3 7.6-.2-1.5 3.2-6.4 3.1-7.6.2z' }),
            el('circle', { class: 'pet-iris', cx: 39.4, cy: 26.7, r: 2.7 }),
            el('ellipse', { class: 'pet-pupil', cx: 39.4, cy: 26.7, rx: 1, ry: 2.2 }),
            el('circle', { class: 'pet-glint', cx: 40.4, cy: 25.6, r: 0.7 }))),
        el('path', { class: 'pet-lid l', d: 'M21 26.8q3.6 2.4 7.2 0' }),
        el('path', { class: 'pet-lid r', d: 'M35.8 26.8q3.6 2.4 7.2 0' }),
        el('ellipse', { class: 'pet-cheek', cx: 21.5, cy: 32.5, rx: 2.3, ry: 1.3 }),
        el('ellipse', { class: 'pet-cheek', cx: 42.5, cy: 32.5, rx: 2.3, ry: 1.3 }),
        el('path', { class: 'pet-nose', d: 'M30.4 31.6h3.2c.4 0 .6.4.4.7l-1.4 1.6c-.3.3-.9.3-1.2 0L30 32.3c-.2-.3 0-.7.4-.7z' }),
        el('path', { class: 'pet-mouth', d: 'M32 34v1.2M29.8 35.6q1.2 1.2 2.2-.4q1 1.6 2.2.4' }),
        el('ellipse', { class: 'pet-mouth-o', cx: 32, cy: 36.4, rx: 1.4, ry: 1.8 }),
        el('path', { class: 'pet-whisker', d: 'M26.5 34.2l-8.5-1.4M26.5 35.6l-8 .8M37.5 34.2l8.5-1.4M37.5 35.6l8 .8' })));
  }

  // Пиксельный кот: # — шерсть, s — полоска, p — розовое в ушах и нос, o — глаз, w — мордочка и грудка.
  const PIX = [
    '.##..........##.',
    '.#p#........#p#.',
    '.#pp#s#ss#s#pp#.',
    '.##############.',
    '.##oo######oo##.',
    '.##oo######oo##.',
    '.#####wpw######.',
    '..####wwww####..',
    '...##########...',
    '..###wwwwww###..',
    '..##wwwwwwww##..',
    '..##w##ww##w##..',
  ];
  function pixelBody() {
    const g = el('g', { class: 'pet-pixel', 'shape-rendering': 'crispEdges', transform: 'translate(8 18) scale(3)' });
    const cls = { '#': 'px-body', s: 'px-stripe', p: 'px-pink', o: 'px-eye', w: 'px-belly' };
    PIX.forEach((row, y) => [...row].forEach((ch, x) => {
      if (cls[ch]) g.append(el('rect', { x, y, width: 1, height: 1, class: cls[ch] }));
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
    svg = el('svg', { viewBox: '0 0 64 64', 'aria-hidden': 'true' }, el('ellipse', { class: 'pet-shadow', cx: 32, cy: 58, rx: 16, ry: 2.6 }),
      el('g', { class: 'pet-jump' }, el('g', { class: 'pet-bob' }, roundBody(), pixelBody())), extras());
    bubble = document.createElement('div');
    bubble.className = 'pet-bubble';
    bubble.hidden = true;
    root.append(svg, bubble);
    container.prepend(root);
    pupils = [...svg.querySelectorAll('.pet-iris, .pet-pupil, .pet-glint')];
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
      const k = Math.min(1.1, d / 80);
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
