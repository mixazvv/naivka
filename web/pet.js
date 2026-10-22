// Талисман внизу меню — пиксельный серый котёнок (web/cat.png, и cat-sleep.png — тот же с закрытыми глазами).
// Живёт состоянием подключения: отключено — спит; подключается — оглядывается; идёт трафик — подпрыгивает
// (чем быстрее, тем бодрее); сбой — пугается; связь вернулась — радуется. Моргает, его можно погладить
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

  // Спрайт 53×74 рисуется один к одному (на экранах с масштабом — крупными чёткими пикселями).
  const SW = 53, SH = 74;
  let sprite = null, blinkTimer = 0;
  const frame = closed => { if (sprite) sprite.setAttribute('href', closed ? 'cat-sleep.png' : 'cat.png'); };
  function body() {
    sprite = el('image', { class: 'pet-sprite', href: 'cat.png', x: 6, y: 6, width: SW, height: SH, preserveAspectRatio: 'none' });
    return sprite;
  }
  // Моргание: раз в 3–6 секунд на 0,15 с, пока кот не спит и не мурчит.
  function blinkLoop() {
    clearTimeout(blinkTimer);
    blinkTimer = setTimeout(() => {
      if (!root) return;
      if ((state === 'on' || state === 'run' || state === 'wait') && !root.classList.contains('purr')) {
        frame(true);
        setTimeout(() => { if (root && state !== 'off' && !root.classList.contains('purr')) frame(false); }, 150);
      }
      blinkLoop();
    }, 3000 + Math.random() * 3000);
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
    blinkLoop();
  }

  function unmount() {
    if (!root) return;
    clearTimeout(blinkTimer);
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
          frame(true); setTimeout(() => { if (state !== 'off') frame(false); }, 1800);
        }
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
    if (s !== state) { prev = state; state = s; root.dataset.mood = s; frame(s === 'off'); }
    // Чем быстрее трафик, тем чаще шаги: от 0,7 до 0,22 с на шаг.
    const kb = Math.max(0, bps) / 1024;
    root.style.setProperty('--pet-step', (0.7 - Math.min(0.48, Math.log10(1 + kb) * 0.13)).toFixed(2) + 's');
  }

  return { mount, unmount, update, talk, cheer: (t) => { flash('hop', 650); if (root) { root.classList.add('joy'); setTimeout(() => root && root.classList.remove('joy'), 2400); } talk(t, 3600); } };
})();
