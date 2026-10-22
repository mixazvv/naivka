// Талисман внизу меню — серый котёнок (лист кадров web/cat-sheet.webp).
// Живёт состоянием подключения: отключено — ложится и спит; подключается — озирается; подключено —
// сидит, а в простое умывается, потягивается, зевает и прогуливается по меню; идёт трафик — бежит
// (чем быстрее, тем чаще шаги); сбой — пугается, спина дугой; защита заблокировала — грустит;
// связь вернулась — прыгает от радости. Его можно погладить (провести мышью несколько раз) и потыкать.
// Выключается в «Настройках» → «Внешний вид».
const Pet = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const NS = 'http://www.w3.org/2000/svg';
  let root = null, svg = null, bubble = null, body = null;
  let state = '', prev = '', joyTimer = 0, bubbleTimer = 0, strokes = 0, strokeAt = 0, lastX = null;
  let say = () => '', onPurr = null;

  const el = (tag, attrs = {}, ...kids) => {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
    e.append(...kids);
    return e;
  };

  // Лист кадров: ячейки 120×130, по 9 в ряд, кот стоит на нижнем крае ячейки. На экран ячейка
  // ложится в 64 единицы ширины, «пол» — на y = 80.
  const FRAMES = ['sit_1', 'sit_2', 'sit_3', 'sit_4', 'sleep_1', 'sleep_2', 'sleep_3', 'liedown_1', 'liedown_2',
    'yawn_1', 'yawn_2', 'look_1', 'look_2', 'run_1', 'run_2', 'run_3', 'run_4', 'scared_1',
    'scared_2', 'jump_1', 'jump_2', 'jump_3', 'purr_1', 'purr_2', 'poke_1', 'wash_1', 'wash_2',
    'wash_3', 'stretch_1', 'stretch_2', 'stretch_3', 'sad_1', 'walk_1', 'walk_2', 'walk_3', 'walk_4'];
  const COLS = 9, K = 64 / 120, CW = 64, CH = 130 * K, TOP = 80 - 130 * K;
  let sprite = null;
  function show(name) {
    const i = FRAMES.indexOf(name);
    if (!sprite || i < 0) return;
    sprite.setAttribute('x', -(i % COLS) * CW);
    sprite.setAttribute('y', TOP - Math.floor(i / COLS) * CH);
  }
  function makeBody() {
    sprite = el('image', { class: 'pet-sprite', href: 'cat-sheet.webp', width: COLS * CW, height: Math.ceil(FRAMES.length / COLS) * CH, preserveAspectRatio: 'none' });
    body = el('g', { class: 'pet-body' }, el('g', { 'clip-path': 'url(#petClip)' }, sprite));
    return [el('clipPath', { id: 'petClip' }, el('rect', { x: 0, y: TOP, width: CW, height: CH })), body];
  }

  // ---------- проигрыватель кадров ----------
  // Шаг последовательности — имя кадра или [имя, мс]. Новая последовательность прерывает прежнюю.
  let animTimer = 0, busy = false;
  function stopAnim() { clearTimeout(animTimer); busy = false; }
  function play(seq, ms, done) {
    stopAnim();
    busy = true;
    let i = 0;
    const step = () => {
      if (!root) return;
      if (i >= seq.length) { busy = false; if (done) done(); return; }
      const s = seq[i++];
      const [name, t] = Array.isArray(s) ? s : [s, ms];
      show(name);
      animTimer = setTimeout(step, t);
    };
    step();
  }
  // Бесконечная анимация: next() возвращает следующую последовательность.
  function loop(next, ms) {
    const run = () => play(next(), typeof ms === 'function' ? ms() : ms, run);
    run();
    busy = false; // фон состояния, не мешает делам в простое
  }

  // ---------- состояния ----------
  const sleepSeq = () => Math.random() < 0.15 ? ['sleep_1', 'sleep_2', ['sleep_3', 500], 'sleep_1'] : ['sleep_1', 'sleep_2'];
  const runMs = () => Math.max(70, Math.min(170, parseFloat(getComputedStyle(root).getPropertyValue('--pet-step')) * 1000 / 3 || 150));

  // enter включает анимацию состояния s. Из сна кот сначала встаёт и зевает, в сон — сначала ложится.
  function enter(s, from) {
    stopStroll();
    if (REDUCE.matches) {
      show({ off: 'sleep_1', wait: 'look_1', run: 'run_2', err: 'scared_1', sad: 'sad_1' }[s] || 'sit_1');
      return;
    }
    if (from === 'off' && s !== 'off') {
      play(['liedown_2', 'liedown_1', ['yawn_1', 220], ['yawn_2', 650], ['yawn_1', 220]], 260, () => enterNow(s));
      return;
    }
    if (s === 'off' && from && from !== 'off') {
      play(['liedown_1', 'liedown_2'], 380, () => enterNow(s));
      return;
    }
    enterNow(s);
  }
  function enterNow(s) {
    if (!root || s !== state) return;
    setFlip(false);
    switch (s) {
      case 'off': loop(sleepSeq, 1400); break;
      case 'wait': loop(() => ['look_1', 'look_2'], 900); break;
      case 'run': setFlip(false); loop(() => ['run_1', 'run_2', 'run_3', 'run_4'], runMs); break;
      case 'err': loop(() => ['scared_1', 'scared_2'], 170); break;
      case 'sad': stopAnim(); show('sad_1'); break;
      default: stopAnim(); show('sit_1');
    }
  }
  const back = () => enterNow(state);

  // ---------- дела в простое ----------
  let idleTimer = 0;
  function idleLoop() {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      if (!root) return;
      if (!REDUCE.matches && !busy && !strolling) {
        const r = Math.random();
        if (state === 'on') {
          if (r < 0.22) play(['sit_2', 'sit_3', 'sit_4', 'sit_1'], 520, back);
          else if (r < 0.42) play(['wash_1', 'wash_2', 'wash_3', 'wash_2', 'wash_3', 'wash_1'], 330, back);
          else if (r < 0.56) play([['stretch_1', 450], ['stretch_2', 1000], ['stretch_3', 450]], 0, back);
          else if (r < 0.68) play([['yawn_1', 250], ['yawn_2', 750], ['yawn_1', 250]], 0, back);
          else if (r < 0.8) play(['look_1', 'look_2', 'look_1'], 750, back);
          else stroll();
        } else if (state === 'sad' && r < 0.3) {
          play(['look_1', 'sad_1'], 900, back);
        }
      }
      idleLoop();
    }, 4000 + Math.random() * 6000);
  }

  // Прогулка: идёт шагом в случайное место по ширине меню и садится там.
  let strolling = false, strollRaf = 0, posX = 0, flip = false;
  function setFlip(v) { flip = v; if (body) body.setAttribute('transform', v ? 'translate(64 0) scale(-1 1)' : ''); }
  function stopStroll() { cancelAnimationFrame(strollRaf); strolling = false; }
  function stroll() {
    const host = root.parentElement;
    const room = host ? host.clientWidth - root.offsetWidth - 8 : 0;
    if (room < 30) return;
    let to = Math.random() * room;
    if (Math.abs(to - posX) < 25) to = posX < room / 2 ? room : 0;
    setFlip(to < posX);
    strolling = true; busy = true;
    loop(() => ['walk_1', 'walk_2', 'walk_3', 'walk_4'], 170);
    busy = true;
    const dir = Math.sign(to - posX);
    let t0 = performance.now();
    const step = now => {
      if (!root || !strolling) return;
      posX += dir * 28 * Math.min(0.05, (now - t0) / 1000); // 28 px/с
      t0 = now;
      if ((dir > 0 && posX >= to) || (dir < 0 && posX <= to)) {
        posX = to; root.style.setProperty('--pet-x', posX + 'px');
        strolling = false; busy = false; back();
        return;
      }
      root.style.setProperty('--pet-x', posX + 'px');
      strollRaf = requestAnimationFrame(step);
    };
    strollRaf = requestAnimationFrame(step);
  }

  function extras() {
    return el('g', { class: 'pet-fx' },
      el('g', { class: 'pet-zzz' }, el('text', { x: 50, y: 40 }, 'z'), el('text', { x: 55, y: 33 }, 'z'), el('text', { x: 60, y: 26 }, 'z')),
      el('path', { class: 'pet-sweat', d: 'M58 22c2 3 3 5 1.5 6.5s-4 0-3.5-2 2-4.5 2-4.5z' }),
      el('g', { class: 'pet-hearts' },
        el('path', { d: 'M4 20c-2-3 2-5 3-2 1-3 5-1 3 2l-3 3z' }),
        el('path', { d: 'M54 10c-2-3 2-5 3-2 1-3 5-1 3 2l-3 3z' })),
      el('g', { class: 'pet-spark' }, el('path', { d: 'M4 40l2-1-2-1-1-2-1 2-2 1 2 1 1 2zM62 48l2-1-2-1-1-2-1 2-2 1 2 1 1 2z' })),
      el('path', { class: 'pet-dust', d: 'M2 76h4M0 72h3M1 80h3' }));
  }

  function mount(container, opts = {}) {
    if (root) return;
    say = opts.say || say;
    onPurr = opts.onPurr || onPurr;
    root = document.createElement('div');
    root.className = 'pet';
    root.setAttribute('role', 'img');
    root.setAttribute('aria-label', 'Талисман Naivka');
    svg = el('svg', { viewBox: '0 0 64 84', 'aria-hidden': 'true' }, el('ellipse', { class: 'pet-shadow', cx: 32, cy: 80, rx: 20, ry: 2.6 }),
      el('g', { class: 'pet-jump' }, ...makeBody()), extras());
    bubble = document.createElement('div');
    bubble.className = 'pet-bubble';
    bubble.hidden = true;
    root.append(svg, bubble);
    container.prepend(root);
    posX = 0;
    root.addEventListener('click', poke);
    root.addEventListener('pointermove', pet);
    show('sit_1');
    idleLoop();
  }

  function unmount() {
    if (!root) return;
    stopAnim(); stopStroll(); clearTimeout(idleTimer);
    root.remove();
    root = svg = bubble = body = sprite = null; state = prev = '';
  }

  // Погладить: несколько проходов мышью туда-обратно.
  function pet(e) {
    if (lastX != null && Math.abs(e.clientX - lastX) > 3) {
      const now = performance.now();
      if (now - strokeAt > 900) strokes = 0;
      strokes++; strokeAt = now;
      if (strokes >= 6) {
        strokes = 0; flash('purr', 2000); talk('Мррр…'); if (onPurr) onPurr();
        if (state !== 'run' && state !== 'err') {
          stopStroll();
          play(state === 'off' ? ['sleep_3', 'sleep_1', 'sleep_3', 'sleep_1'] : ['purr_1', 'purr_2', 'purr_1', 'purr_2', 'purr_1'], 400, back);
        }
      }
    }
    lastX = e.clientX;
  }

  function poke() {
    if (state !== 'run' && state !== 'err') {
      stopStroll();
      if (state === 'off') play([['sleep_3', 600]], 0, back);
      else { flash('hop', 650); play([['poke_1', 700]], 0, back); }
    }
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
    // Кот ушёл вправо — облачко слева от него, чтобы не вылезать за меню.
    root.classList.toggle('bubble-left', posX > 40);
    bubble.hidden = false;
    clearTimeout(bubbleTimer);
    bubbleTimer = setTimeout(() => { if (bubble) bubble.hidden = true; }, ms);
  }

  // Радость: прыжок и искры, потом обратно к своему состоянию.
  function joy() {
    if (!root) return;
    clearTimeout(joyTimer);
    root.classList.add('joy');
    joyTimer = setTimeout(() => root && root.classList.remove('joy'), 2400);
    if (!REDUCE.matches && state !== 'off') {
      stopStroll();
      play(['jump_1', 'jump_2', 'jump_3', 'jump_2', 'jump_1'], 180, back);
    }
  }

  // update — фаза подключения (off | wait | on | revive | err | blocked) и скорость в байтах/с.
  function update(phase, bps) {
    if (!root) return;
    const s = phase === 'on' ? (bps > 30 * 1024 ? 'run' : 'on') : phase === 'wait' || phase === 'revive' ? 'wait'
      : phase === 'err' ? 'err' : phase === 'blocked' ? 'sad' : 'off';
    // Чем быстрее трафик, тем чаще шаги: от 0,7 до 0,22 с на шаг.
    const kb = Math.max(0, bps) / 1024;
    root.style.setProperty('--pet-step', (0.7 - Math.min(0.48, Math.log10(1 + kb) * 0.13)).toFixed(2) + 's');
    if (s === state) return;
    // Связь вернулась после сбоя — праздник на пару секунд.
    const revived = (state === 'err' || state === 'wait' || state === 'sad') && (s === 'on' || s === 'run');
    prev = state; state = s; root.dataset.mood = s;
    enter(s, prev);
    if (revived && prev !== 'wait') { joy(); talk('Ура, связь вернулась!'); }
  }

  return { mount, unmount, update, talk, cheer: (t) => { flash('hop', 650); joy(); talk(t, 3600); } };
})();
