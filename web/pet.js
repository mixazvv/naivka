// Талисман — серый котёнок (лист кадров web/cat-sheet.webp). Живёт внизу меню, но его можно
// схватить мышью и унести куда угодно: пока несут — болтает лапами, отпустили — падает и садится
// на ближайшую карточку или кнопку (брошенный — отлетает от краёв окна). Двойной щелчок — домой.
// Место запоминается в настройках.
// Живёт состоянием подключения: отключено — ложится и спит; подключается — озирается; подключено —
// сидит, а в простое умывается, потягивается, зевает и прогуливается; идёт трафик — бежит
// (чем быстрее, тем чаще шаги); сбой — пугается, спина дугой; защита заблокировала — грустит;
// связь вернулась — прыгает от радости. Его можно погладить (провести мышью несколько раз) и потыкать.
// Выключается в «Настройках» → «Внешний вид».
const Pet = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const NS = 'http://www.w3.org/2000/svg';
  let root = null, svg = null, bubble = null, body = null, home = null;
  let state = '', prev = '', joyTimer = 0, bubbleTimer = 0, strokes = 0, strokeAt = 0, lastX = null;
  let say = () => '', onPurr = null, onMove = null;

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
  const inAir = () => !!(held || falling);

  // enter включает анимацию состояния s. Из сна кот сначала встаёт и зевает, в сон — сначала ложится.
  function enter(s, from) {
    stopStroll();
    if (inAir()) return; // в руках или в полёте — состояние покажется после приземления
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
    if (!root || s !== state || inAir()) return;
    setFlip(false);
    switch (s) {
      case 'off': loop(sleepSeq, 1400); break;
      case 'wait': loop(() => ['look_1', 'look_2'], 900); break;
      case 'run': loop(() => ['run_1', 'run_2', 'run_3', 'run_4'], runMs); break;
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
      if (!REDUCE.matches && !busy && !strolling && !inAir()) {
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

  // ---------- где стоит ----------
  // Дома (внизу меню) кот сдвигается по ширине меню (--pet-x). Унесённый — висит над окном
  // (position: fixed) в точке fx, fy (левый верхний угол) и стоит на «опоре» — карточке, кнопке или низе окна.
  let free = false, fx = 0, fy = 0, posX = 0, support = null;
  const FOOT = () => root.offsetHeight * 4 / 84; // от низа рамки до лап
  const feet = () => fy + root.offsetHeight - FOOT();
  function place() { root.style.left = Math.round(fx) + 'px'; root.style.top = Math.round(fy) + 'px'; }
  function makeFree() {
    if (free) return;
    const r = root.getBoundingClientRect();
    free = true;
    root.classList.add('free');
    root.style.setProperty('--pet-x', '0px');
    posX = 0;
    document.body.append(root);
    fx = r.left; fy = r.top;
    place();
  }
  function goHome() {
    stopFall(); stopStroll();
    free = false; support = null;
    root.classList.remove('free');
    root.style.left = root.style.top = '';
    posX = 0; root.style.setProperty('--pet-x', '0px');
    if (home) home.prepend(root);
    if (onMove) onMove(null);
    if (!REDUCE.matches) { flash('hop', 650); play(['jump_3', 'sit_1'], 220, back); } else back();
  }

  // На что можно встать: карточки, плитки, кнопки, колонка «Сейчас» и т. п.
  const SURFACES = '.card, .tile, .stage-ready, .live-col, .set-card, .pill, .feedback, .btn, .seg, .lanes-legend > *, .stage-controls label, .route-pop';
  // Только то, что видно сверху: опора под панелью или окном не считается.
  function surfaceAt(x, y) {
    const e = document.elementsFromPoint(x, y).find(n => !root.contains(n));
    const s = e && e.closest(SURFACES);
    return s && !root.contains(s) ? s : null;
  }
  // Опора между y0 и y1 под точкой x (по трём точкам под лапами): верхний край первого, на который можно встать.
  function landing(y0, y1) {
    const w = root.offsetWidth, xs = [fx + w * 0.5, fx + w * 0.3, fx + w * 0.7];
    for (let y = Math.max(0, y0); y <= y1; y += 3) {
      for (const x of xs) {
        if (x < 0 || x > innerWidth || y >= innerHeight) continue;
        const s = surfaceAt(x, y);
        if (!s) continue;
        const r = s.getBoundingClientRect();
        if (r.top >= y0 - 2 && r.top <= y1 && r.width >= 40) return { y: r.top, el: s };
      }
    }
    return y1 >= innerHeight - 1 ? { y: innerHeight - 1, el: null } : null;
  }
  // Стоит ли ещё на опоре (её могли убрать — сменился экран, свернулась карточка).
  function supported() {
    const f = feet();
    if (f >= innerHeight - 3) return true;
    if (!support || !support.isConnected) return false;
    const r = support.getBoundingClientRect(), cx = Math.max(r.left + 2, Math.min(r.right - 2, fx + root.offsetWidth / 2));
    if (!(Math.abs(r.top - f) < 4 && fx + root.offsetWidth / 2 > r.left - 10 && fx + root.offsetWidth / 2 < r.right + 10 && r.width > 0)) return false;
    return surfaceAt(cx, r.top + 2) === support; // не закрыта ли опора панелью
  }

  // ---------- полёт ----------
  let falling = false, fallRaf = 0, vx = 0, vy = 0;
  function stopFall() { cancelAnimationFrame(fallRaf); falling = false; }
  function fall() {
    if (!free || held) return;
    stopStroll(); stopAnim();
    falling = true;
    show('jump_2');
    let t0 = performance.now();
    const step = now => {
      if (!root || !falling) return;
      const dt = Math.min(0.04, (now - t0) / 1000); t0 = now;
      const w = root.offsetWidth;
      vy = Math.min(1600, vy + 2200 * dt);
      vx *= Math.pow(0.4, dt);
      const f0 = feet();
      fx += vx * dt; fy += vy * dt;
      // Края окна: брошенный кот отлетает от стенок.
      if (fx < 0) { fx = 0; vx = Math.abs(vx) * 0.5; }
      if (fx > innerWidth - w) { fx = innerWidth - w; vx = -Math.abs(vx) * 0.5; }
      if (fy < 0) { fy = 0; vy = Math.abs(vy) * 0.3; }
      const hit = vy > 0 ? landing(f0, feet()) : null;
      if (hit) {
        fy = hit.y - root.offsetHeight + FOOT();
        support = hit.el;
        place();
        land(vy);
        return;
      }
      place();
      fallRaf = requestAnimationFrame(step);
    };
    fallRaf = requestAnimationFrame(step);
  }
  function land(speed) {
    falling = false; vx = vy = 0;
    if (onMove) onMove([+(fx / Math.max(1, innerWidth)).toFixed(4), +(fy / Math.max(1, innerHeight)).toFixed(4)]);
    if (REDUCE.matches) { back(); return; }
    // С высоты — приземляется с прыжка и немного пугается, с небольшой — просто садится.
    if (speed > 900) { flash('hop', 650); play(['jump_3', ['scared_1', 300], 'sit_1'], 160, back); }
    else play(['jump_3'], 160, back);
  }

  // ---------- руки: перенос мышью ----------
  let held = null, press = null, dragged = false;
  function down(e) {
    if (e.button !== 0) return;
    press = { x: e.clientX, y: e.clientY, id: e.pointerId };
  }
  function moveHeld(e) {
    if (!press) return false;
    if (!held) {
      if (Math.hypot(e.clientX - press.x, e.clientY - press.y) < 6) return false;
      // Схватили: кот выходит из меню и висит под курсором.
      makeFree();
      stopFall(); stopStroll(); stopAnim();
      root.setPointerCapture(press.id);
      held = { dx: press.x - fx, dy: press.y - fy, lx: e.clientX, ly: e.clientY, t: performance.now(), vx: 0, vy: 0 };
      root.classList.add('held');
      show('jump_2');
      if (bubble) bubble.hidden = true;
    }
    const now = performance.now(), dt = Math.max(1, now - held.t) / 1000;
    held.vx = held.vx * 0.5 + (e.clientX - held.lx) / dt * 0.5;
    held.vy = held.vy * 0.5 + (e.clientY - held.ly) / dt * 0.5;
    held.lx = e.clientX; held.ly = e.clientY; held.t = now;
    fx = Math.max(0, Math.min(innerWidth - root.offsetWidth, e.clientX - held.dx));
    fy = Math.max(0, Math.min(innerHeight - root.offsetHeight, e.clientY - held.dy));
    place();
    // Раскачивается вслед за рукой.
    root.style.setProperty('--pet-tilt', Math.max(-20, Math.min(20, -held.vx / 60)).toFixed(1) + 'deg');
    show(Math.abs(held.vx) > 600 ? 'scared_1' : 'jump_2');
    return true;
  }
  function up(e) {
    if (!held) { press = null; return; }
    const h = held;
    held = null; press = null; dragged = true;
    root.classList.remove('held');
    root.style.setProperty('--pet-tilt', '0deg');
    try { root.releasePointerCapture(e.pointerId); } catch (_) { /* уже отпущен */ }
    // Отпустили над меню — домой.
    const hr = home && home.getBoundingClientRect();
    if (hr && e.clientX >= hr.left && e.clientX <= hr.right && e.clientY >= hr.top - 40 && e.clientY <= hr.bottom) { goHome(); return; }
    vx = Math.max(-1500, Math.min(1500, h.vx)); vy = Math.max(-900, Math.min(900, h.vy));
    support = null;
    fall();
  }

  // Прогулка: шагом в случайное место — дома по ширине меню, на выезде по ширине опоры.
  let strolling = false, strollRaf = 0;
  function setFlip(v) { if (body) body.setAttribute('transform', v ? 'translate(64 0) scale(-1 1)' : ''); }
  function stopStroll() { cancelAnimationFrame(strollRaf); strolling = false; }
  function stroll() {
    const w = root.offsetWidth;
    let lo, hi, cur;
    if (free) {
      if (!support || !support.isConnected) return;
      const r = support.getBoundingClientRect();
      lo = Math.max(0, r.left - w * 0.2); hi = Math.min(innerWidth - w, r.right - w * 0.8); cur = fx;
    } else {
      const host = root.parentElement;
      lo = 0; hi = host ? host.clientWidth - w - 8 : 0; cur = posX;
    }
    if (hi - lo < 30) return;
    let to = lo + Math.random() * (hi - lo);
    if (Math.abs(to - cur) < 25) to = cur - lo < (hi - lo) / 2 ? hi : lo;
    setFlip(to < cur);
    strolling = true;
    loop(() => ['walk_1', 'walk_2', 'walk_3', 'walk_4'], 170);
    busy = true;
    const dir = Math.sign(to - cur);
    let t0 = performance.now();
    const step = now => {
      if (!root || !strolling) return;
      cur += dir * 28 * Math.min(0.05, (now - t0) / 1000); // 28 px/с
      t0 = now;
      const done = (dir > 0 && cur >= to) || (dir < 0 && cur <= to);
      if (done) cur = to;
      if (free) { fx = cur; place(); } else { posX = cur; root.style.setProperty('--pet-x', posX + 'px'); }
      if (done) {
        strolling = false; busy = false;
        if (free && onMove) onMove([+(fx / innerWidth).toFixed(4), +(fy / innerHeight).toFixed(4)]);
        back();
        return;
      }
      strollRaf = requestAnimationFrame(step);
    };
    strollRaf = requestAnimationFrame(step);
  }

  // Унесённый кот проверяет, не пропала ли опора, и не вылез ли за окно после смены размера.
  let gravTimer = 0;
  function gravity() {
    if (!root || !free || inAir() || strolling) return;
    const w = root.offsetWidth, h = root.offsetHeight;
    if (fx > innerWidth - w || fy > innerHeight - h) {
      fx = Math.max(0, Math.min(innerWidth - w, fx)); fy = Math.max(0, Math.min(innerHeight - h, fy)); place();
    }
    if (!supported()) { vx = vy = 0; fall(); }
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

  // opts: say(state) — реплика по щелчку, onPurr — погладили, spot — сохранённое место ([доля x, доля y]
  // или null — дома), onMove(spot) — кота перенесли.
  function mount(container, opts = {}) {
    if (root) return;
    say = opts.say || say;
    onPurr = opts.onPurr || onPurr;
    onMove = opts.onMove || onMove;
    home = container;
    root = document.createElement('div');
    root.className = 'pet';
    root.setAttribute('role', 'img');
    root.setAttribute('aria-label', 'Талисман Naivka');
    root.title = 'Можно погладить, ткнуть и перенести мышью; двойной щелчок — домой';
    svg = el('svg', { viewBox: '0 0 64 84', 'aria-hidden': 'true' }, el('ellipse', { class: 'pet-shadow', cx: 32, cy: 80, rx: 20, ry: 2.6 }),
      el('g', { class: 'pet-jump' }, ...makeBody()), extras());
    bubble = document.createElement('div');
    bubble.className = 'pet-bubble';
    bubble.hidden = true;
    root.append(svg, bubble);
    container.prepend(root);
    posX = 0;
    root.addEventListener('click', poke);
    root.addEventListener('dblclick', () => { if (free) goHome(); });
    root.addEventListener('pointerdown', down);
    root.addEventListener('pointermove', e => { if (!moveHeld(e)) pet(e); });
    root.addEventListener('pointerup', up);
    root.addEventListener('pointercancel', up);
    show('sit_1');
    // Сохранённое место: ставим туда и даём упасть на ближайшую опору.
    const sp = opts.spot;
    if (Array.isArray(sp) && sp.length === 2) {
      makeFree();
      fx = sp[0] * innerWidth; fy = sp[1] * innerHeight;
      fx = Math.max(0, Math.min(innerWidth - root.offsetWidth, fx)); fy = Math.max(0, Math.min(innerHeight - root.offsetHeight, fy));
      place();
      requestAnimationFrame(() => { if (root && !supported()) { const hit = landing(feet() - 4, feet() + 4); if (hit) support = hit.el; else fall(); } });
    }
    gravTimer = setInterval(gravity, 700);
    addEventListener('resize', gravity);
    idleLoop();
  }

  function unmount() {
    if (!root) return;
    stopAnim(); stopStroll(); stopFall(); clearTimeout(idleTimer); clearInterval(gravTimer);
    removeEventListener('resize', gravity);
    root.remove();
    root = svg = bubble = body = sprite = home = support = null; state = prev = ''; free = false; held = press = null;
  }

  // Погладить: несколько проходов мышью туда-обратно (не держа кнопку).
  function pet(e) {
    if (press) return;
    if (lastX != null && Math.abs(e.clientX - lastX) > 3) {
      const now = performance.now();
      if (now - strokeAt > 900) strokes = 0;
      strokes++; strokeAt = now;
      if (strokes >= 6) {
        strokes = 0; flash('purr', 2000); talk('Мррр…'); if (onPurr) onPurr();
        if (state !== 'run' && state !== 'err' && !inAir()) {
          stopStroll();
          play(state === 'off' ? ['sleep_3', 'sleep_1', 'sleep_3', 'sleep_1'] : ['purr_1', 'purr_2', 'purr_1', 'purr_2', 'purr_1'], 400, back);
        }
      }
    }
    lastX = e.clientX;
  }

  function poke() {
    if (dragged) { dragged = false; return; } // это был перенос, а не щелчок
    if (inAir()) return;
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
    // У правого края окна или в правой части меню облачко — слева от кота.
    const r = root.getBoundingClientRect();
    root.classList.toggle('bubble-left', free ? r.right + 170 > innerWidth : posX > 40);
    root.classList.toggle('bubble-down', free && r.top < 70);
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
    if (!REDUCE.matches && state !== 'off' && !inAir()) {
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

  return { mount, unmount, update, talk, home: () => { if (root && free) goHome(); }, cheer: (t) => { flash('hop', 650); joy(); talk(t, 3600); } };
})();
