// Талисман внизу меню — пиксельный серый котёнок (лист кадров web/cat-sheet.png).
// Живёт состоянием подключения: отключено — спит; подключается — оглядывается; идёт трафик — подпрыгивает
// (чем быстрее, тем бодрее) и машет хвостом; сбой — пугается, хвост трубой; связь вернулась — радуется.
// Сам по себе моргает, машет хвостом и дёргает ухом; его можно погладить
// (провести мышью несколько раз) и потыкать. Выключается в «Настройках» → «Внешний вид».
const Pet = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const NS = 'http://www.w3.org/2000/svg';
  let root = null, svg = null, bubble = null;
  let state = '', prev = '', joyTimer = 0, bubbleTimer = 0, strokes = 0, strokeAt = 0, lastX = null;
  let say = () => '', onPurr = null;

  const el = (tag, attrs = {}, ...kids) => {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
    e.append(...kids);
    return e;
  };

  // Лист кадров web/cat-sheet.png: 8 кадров по 53×74 — [обычный, хвост вверх, хвост вверх и вбок, ухо дёрнулось],
  // каждый с открытыми и закрытыми глазами. Рисуется один к одному: на экранах с масштабом — чёткими пикселями.
  const SW = 53, SH = 74, POSE = { base: 0, tail1: 1, tail2: 2, ear: 3 };
  let lastStep = '', sprite = null, closed = false, pose = 'base', animTimer = 0, blinkTimer = 0, runTimer = 0;
  function draw() { if (sprite) sprite.setAttribute('x', 6 - (POSE[pose] * 2 + (closed ? 1 : 0)) * SW); }
  const frame = c => { closed = c; draw(); };
  const setPose = p => { pose = p; draw(); };
  function body() {
    sprite = el('image', { class: 'pet-sprite', href: 'cat-sheet.png', x: 6, y: 6, width: SW * 8, height: SH, preserveAspectRatio: 'none' });
    return el('g', { 'clip-path': 'url(#petClip)' }, el('clipPath', { id: 'petClip' }, el('rect', { x: 6, y: 6, width: SW, height: SH })), sprite);
  }
  // Последовательность поз с паузой ms между кадрами; новая прерывает прежнюю.
  function play(seq, ms) {
    clearTimeout(animTimer);
    let i = 0;
    const step = () => { if (!root) return; setPose(seq[i]); if (++i < seq.length) animTimer = setTimeout(step, ms); };
    step();
  }
  const wag = (ms = 140) => play(['tail1', 'tail2', 'tail1', 'base', 'tail1', 'tail2', 'tail1', 'base'], ms);
  const twitch = () => play(['ear', 'base', 'ear', 'base'], 110);
  // Сам по себе: иногда машет хвостом или дёргает ухом (во сне — реже и медленнее).
  function idleLoop() {
    clearTimeout(blinkTimer);
    blinkTimer = setTimeout(() => {
      if (!root) return;
      if (!REDUCE.matches && state !== 'run' && state !== 'err') {
        const r = Math.random();
        if (state === 'off') { if (r < 0.35) wag(260); else if (r < 0.6) twitch(); }
        else if (r < 0.45) wag();
        else if (r < 0.7) twitch();
        else if (!root.classList.contains('purr')) { frame(true); setTimeout(() => { if (root && state !== 'off' && !root.classList.contains('purr')) frame(false); }, 150); }
      }
      idleLoop();
    }, state === 'off' ? 6000 + Math.random() * 8000 : 2500 + Math.random() * 3500);
  }
  // Бежит: хвост машет без остановки в такт шагам.
  function runLoop() {
    clearInterval(runTimer);
    if (state !== 'run' || REDUCE.matches) return;
    let i = 0;
    const seq = ['tail1', 'tail2', 'tail1', 'base'];
    const ms = Math.max(90, parseFloat(getComputedStyle(root).getPropertyValue('--pet-step')) * 1000 / 4 || 150);
    runTimer = setInterval(() => { if (!root || state !== 'run') { clearInterval(runTimer); setPose('base'); return; } setPose(seq[i++ % seq.length]); }, ms);
  }

  function extras() {
    return el('g', { class: 'pet-fx' },
      el('g', { class: 'pet-zzz' }, el('text', { x: 56, y: 16 }, 'z'), el('text', { x: 61, y: 9 }, 'z'), el('text', { x: 66, y: 3 }, 'z')),
      el('path', { class: 'pet-sweat', d: 'M58 18c2 3 3 5 1.5 6.5s-4 0-3.5-2 2-4.5 2-4.5z' }),
      el('g', { class: 'pet-hearts' },
        el('path', { d: 'M4 16c-2-3 2-5 3-2 1-3 5-1 3 2l-3 3z' }),
        el('path', { d: 'M54 6c-2-3 2-5 3-2 1-3 5-1 3 2l-3 3z' })),
      el('g', { class: 'pet-spark' }, el('path', { d: 'M4 40l2-1-2-1-1-2-1 2-2 1 2 1 1 2zM62 48l2-1-2-1-1-2-1 2-2 1 2 1 1 2z' })),
      el('path', { class: 'pet-dust', d: 'M2 74h4M0 70h3M1 78h3' }));
  }

  function mount(container, opts = {}) {
    if (root) return;
    say = opts.say || say;
    onPurr = opts.onPurr || onPurr;
    root = document.createElement('div');
    root.className = 'pet';
    root.setAttribute('role', 'img');
    root.setAttribute('aria-label', 'Талисман Naivka');
    svg = el('svg', { viewBox: '0 0 64 84', 'aria-hidden': 'true' }, el('ellipse', { class: 'pet-shadow', cx: 33, cy: 80, rx: 18, ry: 2.6 }),
      el('g', { class: 'pet-jump' }, el('g', { class: 'pet-bob' }, body())), extras());
    bubble = document.createElement('div');
    bubble.className = 'pet-bubble';
    bubble.hidden = true;
    root.append(svg, bubble);
    container.prepend(root);
    root.addEventListener('click', poke);
    root.addEventListener('pointermove', pet);
    idleLoop();
  }

  function unmount() {
    if (!root) return;
    clearTimeout(blinkTimer); clearTimeout(animTimer); clearInterval(runTimer);
    root.remove();
    root = svg = bubble = null; state = prev = '';
  }

  // Погладить: несколько проходов мышью туда-обратно.
  function pet(e) {
    if (lastX != null && Math.sign(e.movementX || e.clientX - lastX) !== 0) {
      const now = performance.now();
      if (Math.abs(e.clientX - lastX) > 3) {
        if (now - strokeAt > 900) strokes = 0;
        strokes++; strokeAt = now;
        if (strokes >= 6) {
          strokes = 0; flash('purr', 1800); talk('Мррр…'); if (onPurr) onPurr();
          if (state !== 'run' && state !== 'err') wag(220);
          frame(true); setTimeout(() => { if (state !== 'off') frame(false); }, 1800);
        }
      }
    }
    lastX = e.clientX;
  }

  function poke() {
    flash('hop', 650);
    if (state !== 'run' && state !== 'err') twitch();
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
    if (s !== state) {
      prev = state; state = s; root.dataset.mood = s; frame(s === 'off');
      clearTimeout(animTimer);
      // Испуг — хвост трубой; бег — машет; иначе обычная поза.
      setPose(s === 'err' ? 'tail2' : 'base');
      runLoop();
    }
    // Чем быстрее трафик, тем чаще шаги: от 0,7 до 0,22 с на шаг.
    const kb = Math.max(0, bps) / 1024;
    const step = (0.7 - Math.min(0.48, Math.log10(1 + kb) * 0.13)).toFixed(2);
    if (step !== lastStep) { lastStep = step; root.style.setProperty('--pet-step', step + 's'); if (state === 'run') runLoop(); }
  }

  return { mount, unmount, update, talk, cheer: (t) => { flash('hop', 650); if (root) { root.classList.add('joy'); setTimeout(() => root && root.classList.remove('joy'), 2400); } talk(t, 3600); } };
})();
