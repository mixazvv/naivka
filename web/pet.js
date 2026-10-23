// Талисман — серый котёнок. Кадры — в атласе web/cat-atlas.webp, их места и точки опоры — в pet-atlas.js.
// Живёт сам по себе: моргает, умывается, чешет ухо, потягивается, прислушивается, играет с клубком
// и хвостом, охотится на муху, ходит и бегает. Уходит из меню гулять по окну: перепрыгивает с карточки
// на карточку, спрыгивает с краёв, бегает по низу окна и карабкается наверх; иногда бежит туда, где щёлкнули. Следит за курсором, ловит его лапой и охотится; если курсор задержать на нём —
// ластится; гладят по голове, чешут подбородок, трогают живот — каждое по-своему. Взяли мышью — висит
// по-разному: за холку, хвост, лапу или туловище; трясут — кружится голова; швырнули — обижается.
// Живёт состоянием подключения: отключено — ложится спать; подключается — насторожен; много трафика —
// носится по окну; сбой — пугается; заблокировано защитой — грустит; связь вернулась — радуется.
// Выключается в «Настройках» → «Внешний вид». Место, куда его унесли, хранится в настройках.
const Pet = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const NS = 'http://www.w3.org/2000/svg';
  const FR = PET_ATLAS.frames;
  const K = 64 / 120; // единиц SVG на пиксель атласа: сидящий кот ростом 113 px — около 60 единиц
  let root = null, svg = null, bubble = null, body = null, home = null, sprite = null, clipRect = null;
  let state = '', prev = '', say = () => '', onPurr = null, onMove = null;

  const el = (tag, attrs = {}, ...kids) => {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
    e.append(...kids);
    return e;
  };
  const rnd = (a, b) => a + Math.random() * (b - a);
  const pick = a => a[Math.floor(Math.random() * a.length)];
  const chance = p => Math.random() < p;
  const seq = (base, n, from = 1) => Array.from({ length: n }, (_, i) => base + '_' + (i + from));
  const night = () => (h => h >= 23 || h < 6)(new Date().getHours());

  // ---------- кадр ----------
  // Кадр ставится точкой опоры в (32; 80) — лапы на «полу», или в (32; 4) — висит под рукой.
  let cur = '', box = { x: 0, y: 0, w: 0, h: 0 }, flip = false;
  function show(name) {
    const f = FR[name];
    if (!f || !sprite) return;
    cur = name;
    const [x, y, w, h, ax, top] = f;
    const left = 32 - ax * K, tp = top ? 4 : 80 - h * K;
    box = { x: left, y: tp, w: w * K, h: h * K };
    sprite.setAttribute('x', left - x * K);
    sprite.setAttribute('y', tp - y * K);
    clipRect.setAttribute('x', left); clipRect.setAttribute('y', tp);
    clipRect.setAttribute('width', w * K); clipRect.setAttribute('height', h * K);
  }
  function setFlip(v) { flip = !!v; if (body) body.setAttribute('transform', v ? 'translate(64 0) scale(-1 1)' : ''); }
  // Где на экране сейчас нарисован кадр — для «за какую часть тела взяли».
  function frameRect() {
    const r = svg.getBoundingClientRect(), s = r.width / 64;
    const x = flip ? 64 - box.x - box.w : box.x;
    return { left: r.left + x * s, top: r.top + box.y * s, width: box.w * s, height: box.h * s };
  }
  // Часть тела под точкой: голова (холка), хвост, передние лапы, задние лапы, туловище.
  function zoneAt(cx, cy) {
    const r = frameRect();
    let u = (cx - r.left) / r.width; const v = (cy - r.top) / r.height;
    if (flip) u = 1 - u;
    if (v < 0.38) return 'head';
    if (u < 0.28 && v > 0.45) return 'tail';
    if (v > 0.8 && u >= 0.3 && u <= 0.72) return 'paw';
    if (v > 0.72) return 'leg';
    return v < 0.55 && u > 0.35 && u < 0.75 ? 'chin' : 'body';
  }
  function makeBody() {
    sprite = el('image', { class: 'pet-sprite', href: 'cat-atlas.webp', width: PET_ATLAS.w * K, height: PET_ATLAS.h * K, preserveAspectRatio: 'none' });
    clipRect = el('rect', { x: 0, y: 0, width: 1, height: 1 });
    body = el('g', { class: 'pet-body' }, el('g', { 'clip-path': 'url(#petClip)' }, sprite));
    return [el('clipPath', { id: 'petClip' }, clipRect), body];
  }

  // ---------- проигрыватель ----------
  // Шаг — имя кадра или [имя, мс]. Новая последовательность прерывает прежнюю.
  let animTimer = 0, busy = false;
  function stopAnim() { clearTimeout(animTimer); busy = false; }
  function play(steps, ms, done) {
    stopAnim();
    busy = true;
    let i = 0;
    const step = () => {
      if (!root) return;
      if (i >= steps.length) { busy = false; if (done) done(); return; }
      const s = steps[i++];
      const [name, t] = Array.isArray(s) ? s : [s, ms];
      show(name);
      animTimer = setTimeout(step, t);
    };
    step();
  }
  // Фон состояния: бесконечно, next() — следующая последовательность. Делам в простое не мешает.
  function loop(next, ms) {
    const run = () => play(next(), typeof ms === 'function' ? ms() : ms, run);
    run();
    busy = false;
  }

  function flash(cls, ms) {
    if (!root || REDUCE.matches) return;
    root.classList.remove(cls);
    void root.offsetWidth;
    root.classList.add(cls);
    setTimeout(() => root && root.classList.remove(cls), ms);
  }
  let bubbleTimer = 0;
  function talk(text, ms = 2600) {
    if (!bubble || !text) return;
    bubble.textContent = text;
    const r = root.getBoundingClientRect();
    root.classList.toggle('bubble-left', free ? r.right + 170 > innerWidth : posX > 40);
    root.classList.toggle('bubble-down', free && r.top < 70);
    bubble.hidden = false;
    clearTimeout(bubbleTimer);
    bubbleTimer = setTimeout(() => { if (bubble) bubble.hidden = true; }, ms);
  }
  // Говорит — открывает рот.
  function meow(text) {
    talk(text, 1800);
    if (canAct()) play([['meow_1', 180], ['meow_2', 450], ['meow_1', 180]], 0, back);
  }

  // ---------- состояния ----------
  const inAir = () => !!(held || falling || hanging || leaping);
  const sitting = () => !busy && !strolling && !inAir() && !watching && !napping;
  const canAct = () => root && !REDUCE.matches && !inAir() && !strolling;
  let side = 0, grumpyUntil = 0, napping = false;
  const grumpy = () => performance.now() < grumpyUntil;

  const sleepSeq = () => {
    const r = Math.random();
    if (r < 0.05) { side = 1 - side; return ['sleep_3', side ? 'sleep_4' : 'sleep_1']; }
    if (r < 0.08) return [['lieyawn_1', 900]];
    if (night() && r < 0.12) return [['sleepback_1', 4000]];
    if (side) return ['sleep_4', 'sleep_5'];
    return r < 0.2 ? ['sleep_1', 'sleep_2', ['sleep_3', 500], 'sleep_1'] : r < 0.35 ? [['sleepb_4', 2600]] : ['sleep_1', 'sleep_2'];
  };
  let boxSleep = false;
  const runMs = () => Math.max(60, Math.min(160, parseFloat(getComputedStyle(root).getPropertyValue('--pet-step')) * 1000 / 3 || 140));

  // enter включает состояние s: из сна — встаёт, зевает и потягивается; в сон — ложится и засыпает.
  function enter(s, from) {
    halt();
    if (inAir()) return;
    if (REDUCE.matches) {
      show({ off: 'sleep_1', wait: 'alert_1', run: 'run_2', err: 'scared_1', sad: 'sad_1' }[s] || 'idle_1');
      return;
    }
    if (from === 'off' && s !== 'off') {
      boxSleep = false;
      play([['lieyawn_1', 700], 'liedown_2', 'liedown_1', ['stretchb_1', 700], ['stretchb_3', 300], ['yawnsit_2', 500], 'yawnsit_3'], 260, () => enterNow(s));
      return;
    }
    if (s === 'off' && from && from !== 'off') {
      boxSleep = chance(0.2);
      play(boxSleep ? [['box_1', 500], ['box_2', 1200]] : seq('sleepb', 4), 450, () => enterNow(s));
      return;
    }
    enterNow(s);
  }
  function enterNow(s) {
    if (!root || s !== state || inAir()) return;
    setFlip(false);
    root.classList.remove('nap');
    if (napping && (s === 'on' || s === 'sad')) {
      root.classList.add('nap');
      loop(() => boxSleep ? [['box_3', 3000], ['box_2', 400]] : sleepSeq(), 1400);
      return;
    }
    switch (s) {
      case 'off': loop(() => boxSleep ? [['box_3', 3000], ['box_2', 500], ['box_3', 4000]] : sleepSeq(), 1400); break;
      case 'wait': loop(() => pick([['alert_1', 'alert_2', 'alert_3'], seq('listen', 4), ['scan_1', 'scan_2', 'scan_3', 'scan_4'], ['earmove_1', 'earmove_2', 'earmove_1']]), 600); break;
      case 'run': runAbout(); break;
      case 'err': loop(() => chance(0.15) ? [['hiss_1', 300], ['hiss_2', 400]] : ['scared_1', 'scared_2'], 170); break;
      case 'sad': stopAnim(); show(chance(0.5) ? 'sad_1' : 'sadlie_1'); break;
      default: stopAnim(); show(grumpy() ? 'angry_4' : 'idle_1');
    }
  }
  const back = () => enterNow(state);

  // Идёт трафик — носится по-настоящему: бегом по меню или по своей опоре, с опоры на опору по окну,
  // иногда выскакивает из меню; время от времени останавливается перевести дух. Места нет — бежит на месте.
  let rested = false;
  function runAbout() {
    if (REDUCE.matches) { loop(() => seq('run', 4, 5), runMs); return; }
    if (!rested && chance(0.3)) {
      rested = true;
      play([['brake_1', 110], ['brake_2', 160], ['brake_3', 220], ...pick([
        [['pant_1', 900], ['pant_2', 1200], ['pant_1', 900], ['pant_2', 1000]],
        [['pant_3', rnd(2500, 4500)]],
        [...seq('observe', 4).map(n => [n, 800])],
        [['tosit_5', 300], ['scan_2', rnd(2500, 4500)]]])], 0, back);
      return;
    }
    rested = false;
    if (free ? chance(0.55) && roam(true) : chance(0.3) && explore(null, true)) return;
    if (stroll(null, { fast: true })) return;
    play([...seq('run', 4, 5), ...seq('run', 4, 5), ...seq('run', 4, 5)], runMs(), back);
  }

  // ---------- живой простой: позы, моргание, дела ----------
  const IDLE = ['idle_1', 'idle_2', 'idle_3', 'idle_4', 'idle_5', 'idle_6', 'idle_7', 'idle_8', 'sit_1', 'sit_2', 'sit_3', 'sit_4', 'look_1', 'look_2'];
  let tickTimer = 0;
  function tick() {
    clearTimeout(tickTimer);
    tickTimer = setTimeout(() => {
      if (!root) return;
      if (state === 'on' && sitting() && !REDUCE.matches && !grumpy()) {
        if (chance(0.35)) {
          const base = cur;
          play([['blink_2', 70], ['blink_3', 110], ['blink_2', 70], [base, 10]], 0, () => { busy = false; show(base); });
        } else show(pick(IDLE));
      } else if (state === 'sad' && sitting() && chance(0.3)) show(cur === 'sad_1' ? 'sadlie_1' : 'sad_1');
      tick();
    }, rnd(2500, 6000));
  }

  // У края опоры: -1 — у левого, 1 — у правого, 0 — нет.
  const nearEdge = () => {
    if (!free || !support || !support.isConnected) return 0;
    const r = support.getBoundingClientRect(), cx = fx + root.offsetWidth / 2;
    return cx - r.left < 50 ? -1 : r.right - cx < 50 ? 1 : 0;
  };
  // Охота на невидимую муху: следит взглядом, крадётся, подпрыгивает — поймал или промахнулся.
  const bugHunt = () => {
    flash('hop', 650);
    play([['lookup_3', 700], ['lookup_4', 600], ['lookup_3', 500], ['stalk_1', 200], ['stalk_2', 200], ['stalk_1', 200],
      ['pounceup_1', 180], ['pounceup_2', 320], ['touch_3', 220], chance(0.4) ? ['happy_1', 900] : ['tiltb_2', 900]], 0, back);
  };
  // Дела в простое: [вес, условие, действие]. Чем больше вес, тем чаще. «Ничего» — просто сидит.
  const ACTIONS = [
    [10, null, () => {}],
    [6, null, () => play([...seq('groom', 4), 'groom_3', 'groom_4', 'groom_2'], 320, back)],
    [4, null, () => play(['ear_1', 'ear_2', 'ear_1', 'ear_2', 'ear_3'], 260, back)],
    [3, null, () => play(['wash_1', 'wash_2', 'wash_3', 'wash_2', 'wash_3', 'wash_1'], 330, back)],
    [3, null, () => play([['stretchb_1', 1100], ['stretchb_3', 600], ['stretchb_4', 500]], 0, back)],
    [2, null, () => play([['stretch_1', 900], ['stretch_2', 1300], ['stretch_1', 500]], 0, back)],
    [3, null, () => play([['yawnsit_1', 250], ['yawnsit_2', 900], ['yawnsit_3', 300]], 0, back)],
    [2, null, () => play([['yawn_1', 300], ['yawn_2', 1000], ['yawn_1', 300]], 0, back)],
    [1, null, () => play(['shake_1', 'shake_2', 'shake_3', 'shake_2', 'shake_1'], 120, back)],
    [1, null, () => play(seq('shakefull', 4).concat(['shakefull_2', 'shakefull_3']), 110, back)],
    [3, null, () => play([...seq('scan', 4), 'scan_1'], 650, back)],
    [3, null, () => play(seq('scanb', 5), 600, back)],
    [3, null, () => play([...seq('lookhead', 5), 'lookhead_3', 'lookhead_1'], 600, back)],
    [3, null, () => play([...seq('lookaround', 4), 'lookaround_2', 'lookaround_1'], 700, back)],
    [3, null, () => play([...seq('observe', 4), 'observe_2', 'observe_3'], 900, back)],
    [2, null, () => play([...seq('inspect', 4), 'inspect_3', 'inspect_4'], 500, back)],
    // Послышалось что-то — вздрогнул, обернулся, успокоился.
    [1, null, () => play([['alertb_1', 300], ['alertb_2', 400], ['alertb_3', 500], ['alertb_4', 900]], 0, back)],
    // Охотится на невидимую добычу: припал, подкрался, прыгнул, прижал лапами.
    [2, null, () => play([['hunt_1', 600], ['hunt_2', 500], ['hunt_3', 220], ['hunt_4', 260], ['hunt_5', 400], ['hunt_6', 900]], 0, back)],
    // Поворачивается к нам мордочкой и обратно.
    [2, null, () => play([['turnin_1', 150], ['turnin_2', 150], ['turnin_3', 1500], ['turnin_4', 900], ['turnin_3', 300], ['turnin_2', 150], ['turnin_1', 150]], 0, back)],
    [2, null, () => play([...seq('lookup', 4), 'lookup_2'], 550, back)],
    [3, null, () => play([...seq('listen', 4), 'listen_2'], 550, back)],
    [3, null, () => play(['earmove_1', 'earmove_2', 'earmove_3', 'earmove_2', 'earmove_4', 'earmove_1'], 350, back)],
    [3, null, () => play(seq('sniff', 4).concat(['sniff_2', 'sniff_3']), 300, back)],
    [2, null, () => play([pick(['tiltb_1', 'tiltb_2', 'tiltb_3', 'tiltb_4', 'tilt_1']), 'idle_1'], 900, back)],
    [3, null, () => play([['ball_1', 600], ['ball_2', 250], ['ball_3', 400], ['ball_2', 250], ['ball_3', 400], ['ball_4', 700], ['ball_1', 400]], 0, back)],
    [2, null, () => play([...seq('tailplay', 4), ...seq('tailplay', 4), ['tailplay_4', 400]], 150, back)],
    [1, null, () => play(['tail_1', 'tail_2', 'tail_3', 'tail_4', 'tail_1', 'tail_2', 'tail_3', 'tail_4'], 140, back)],
    [2, null, () => play(['scratch_1', 'scratch_2', 'scratch_3', 'scratch_2', 'scratch_3', 'scratch_1'], 210, back)],
    [3, null, () => play([['loaf_1', 3500], ['loaf_2', 2500], ['loaf_1', 1500]], 0, back)],
    // Разворачивается спиной, смотрит в окно, поворачивается обратно.
    [2, null, () => play([['bodyturn_5', 250], ['bodyturn_4', 300], ['back_1', 1200], ['back_2', 900], ['bodyturn_1', 300], ['bodyturn_2', 300], ['bodyturn_3', 350], ['bodyturn_6', 300]], 0, back)],
    [1, null, () => play([['sneeze_1', 600], ['sneeze_2', 250], ['tiltb_2', 700]], 0, back)],
    [2, null, () => play([...seq('play', 5), 'play_4', 'play_5', 'play_2'], 260, back)],
    [2, null, () => play([...seq('backroll', 4), 'backroll_3', 'backroll_4', 'backroll_1'], 320, back)],
    [1, null, () => play([...seq('playbow', 4), 'playbow_3', 'playbow_4'], 260, back)],
    [2, null, bugHunt],
    [1, null, () => play([['eat_1', 500], ['eat_2', 500], ['eat_1', 500], ['eat_2', 500], ['eat_1', 500], ['happy_1', 800]], 0, back)],
    [1, null, () => play([['box_1', 500], ['box_2', 3000], ['box_3', 2500], ['box_2', 800], ['box_1', 400]], 0, back)],
    [1, null, () => play([['mouse_1', 1600], ['mouse_2', 1200], ['happy_1', 700]], 0, back)],
    [2, () => nearEdge() !== 0, () => { setFlip(nearEdge() < 0); play([['ledge_1', 1600], ['ledge_2', 1600], ['ledge_1', 1600], ['ledge_2', 1600]], 0, back); }],
    [1, () => nearEdge() !== 0, () => { setFlip(nearEdge() < 0); play([['knock_1', 900], ['knock_2', 700], ['knock_3', 500], ['wink_1', 900]], 0, () => { talk('Упс…', 1400); back(); }); }],
    [1, () => free, () => play([['peek_1', 1400], ['peek_2', 1200], ['peek_1', 1000]], 0, back)],
    // Перемещения: дома — прогулка по меню и вылазки в окно; в окне — ходит, перепрыгивает, путешествует.
    [8, null, () => stroll()],
    [6, () => free, () => hop()],
    [6, () => free, () => roam()],
    [2, () => free, () => roam(true)],
    [8, () => !free && !exploreUntil, () => explore()],
    [2, () => !free && !exploreUntil, () => explore(null, true)],
  ];
  let idleTimer = 0, lastMeow = performance.now();
  function idleLoop() {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      if (!root) return;
      if (!REDUCE.matches && !busy && !strolling && !inAir() && !watching && !hovering) {
        const idleFor = performance.now() - lastInput;
        if ((state === 'on' || state === 'sad') && !napping && idleFor > (night() ? 60e3 : 180e3)) {
          napping = true; boxSleep = chance(0.25);
          play(boxSleep ? [['box_1', 500], ['box_2', 1500]] : [['loaf_1', 2500], ['loaf_2', 3000], ...seq('sleepb', 4).map(n => [n, 450])], 0, back);
        } else if (napping) {
          // спит дальше
        } else if (exploreUntil && performance.now() > exploreUntil && state !== 'run') {
          comeHome();
        } else if (state === 'on' && !grumpy()) {
          if (performance.now() - lastMeow > 480e3 && chance(0.2) && bubble && bubble.hidden) {
            lastMeow = performance.now();
            meow(pick(['Мяу', 'Мрр?', 'Мяу!', say(state) || 'Мяу']));
          } else {
            const ok = ACTIONS.filter(a => !a[1] || a[1]());
            let r = ok.reduce((s, a) => s + a[0], 0) * Math.random();
            for (const a of ok) { if ((r -= a[0]) < 0) { a[2](); break; } }
          }
        } else if (state === 'sad' && chance(0.3)) {
          play([['lookup_2', 900], ['sad_1', 600]], 0, back);
        }
      }
      idleLoop();
    }, rnd(9000, 20000));
  }

  // ---------- где стоит ----------
  // Дома (внизу меню) кот сдвигается по ширине меню (--pet-x). Вне дома — висит над окном
  // (position: fixed) в точке fx, fy (левый верхний угол) и стоит на «опоре»: карточке, кнопке или низе окна.
  let free = false, fx = 0, fy = 0, posX = 0, support = null, exploreUntil = 0;
  const FOOT = () => root.offsetHeight * 4 / 84; // от низа рамки до лап
  const feet = () => fy + root.offsetHeight - FOOT();
  const save = () => { if (onMove && !exploreUntil) onMove([+(fx / Math.max(1, innerWidth)).toFixed(4), +(fy / Math.max(1, innerHeight)).toFixed(4)]); };
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
  // Домой: сразу (двойной щелчок, брошен на меню) — прыжком; после прогулки — уходит вглубь и выходит из меню.
  function goHome(quiet) {
    stopFall(); halt();
    free = false; support = null; exploreUntil = 0;
    root.classList.remove('free', 'leaving');
    root.style.left = root.style.top = '';
    posX = 0; root.style.setProperty('--pet-x', '0px');
    if (home) home.prepend(root);
    if (onMove && !quiet) onMove(null);
    if (REDUCE.matches) { back(); return; }
    if (quiet) {
      root.classList.add('arriving');
      play([['front_1', 200], ['front_2', 200], ['front_3', 200], ['front_4', 200], ['tosit_4', 160], ['tosit_5', 160]], 0, () => { root && root.classList.remove('arriving'); back(); });
    } else { flash('hop', 650); play([['leap_3', 220], ['touch_3', 160], ['touch_5', 160]], 0, back); }
  }
  function comeHome() {
    if (!free) { exploreUntil = 0; return; }
    halt();
    root.classList.add('leaving');
    play([['stand_4b', 160], ['away_1', 220], ['away_2', 220], ['away_3', 220], ['away_4', 220], ['away_1', 220]], 0, () => goHome(true));
  }
  // Вылазка из меню в окно: спрыгивает и добирается до карточки (t или случайной) по другим опорам;
  // через 1,5–4 минуты — домой. fast — носится бегом.
  function explore(t, fast) {
    if (free || !root) return false;
    const sideR = home && home.getBoundingClientRect();
    const minX = sideR ? sideR.right + 20 : 0;
    const c = t ? [t] : targets().filter(t => t.r.left > minX - 40);
    if (!c.length) return false;
    makeFree();
    exploreUntil = performance.now() + rnd(90e3, 240e3);
    const to = pick(c);
    jumpOff(to.r.left + to.r.width / 2 > fx ? 1 : -1, () => travel(to, { fast }));
    return true;
  }

  // На что можно встать: карточки, плитки, кнопки, колонка «Сейчас» и т. п.
  const SURFACES = '.card, .tile, .stage-ready, .live-col, .set-card, .pill, .feedback, .btn, .seg, .lanes-legend > *, .stage-controls label, .route-pop';
  // Только то, что видно сверху: опора под панелью или окном не считается.
  function surfaceAt(x, y) {
    const e = document.elementsFromPoint(x, y).find(n => !root.contains(n));
    const s = e && e.closest(SURFACES);
    return s && !root.contains(s) ? s : null;
  }
  // Опора под серединой кота (как и в supported): иначе он «приземлялся» бы на край лапой и тут же падал.
  function landing(y0, y1) {
    const w = root.offsetWidth, xs = [fx + w * 0.5, fx + w * 0.42, fx + w * 0.58];
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
  function supported() {
    const f = feet();
    if (f >= innerHeight - 3) return true;
    if (onSupport(f)) return true;
    // Приложение перерисовало опору (тот же элемент, но новый) или её сдвинули — ищем под лапами новую
    // на той же высоте.
    const mid = fx + root.offsetWidth / 2, s = surfaceAt(mid, f + 2);
    if (s && Math.abs(s.getBoundingClientRect().top - f) < 4) { support = s; return true; }
    return false;
  }
  function onSupport(f) {
    if (!support || !support.isConnected) return false;
    const r = support.getBoundingClientRect(), mid = fx + root.offsetWidth / 2;
    if (!(Math.abs(r.top - f) < 4 && mid > r.left - 10 && mid < r.right + 10 && r.width > 0)) return false;
    // Сверху видна сама опора или что-то внутри неё (кнопка в карточке) — стоит; закрыта панелью — нет.
    const top = surfaceAt(Math.max(r.left + 2, Math.min(r.right - 2, mid)), r.top + 2);
    return !!top && (top === support || support.contains(top) || top.contains(support));
  }
  // Видимые опоры, на которые можно перепрыгнуть.
  function targets() {
    const w = root.offsetWidth, h = root.offsetHeight;
    // Опора и то, что в неё вложено (кнопки в переключателе), — одно и то же место: туда не прыгает.
    return [...document.querySelectorAll(SURFACES)].filter(e => e !== support && !root.contains(e) && !(support && (support.contains(e) || e.contains(support)))).map(e => ({ e, r: e.getBoundingClientRect() }))
      .filter(({ r }) => r.width >= 70 && r.top > h && r.top < innerHeight - 4 && r.left >= 0 && r.right <= innerWidth + 1)
      .filter(({ e, r }) => surfaceAt(Math.min(r.right - 4, Math.max(r.left + 4, (r.left + r.right) / 2)), r.top + 2) === e && w > 0);
  }

  // ---------- полёт ----------
  let falling = false, fallRaf = 0, vx = 0, vy = 0, hanging = false, leaping = false, afterLand = null;
  function stopFall() { cancelAnimationFrame(fallRaf); falling = false; leaping = false; hanging = false; }
  // airFrame(vy) — кадр в полёте: вверх — прыжок, вниз — лапами вперёд.
  function fall(airFrame) {
    if (!free || held) return;
    stopStroll(); stopAnim();
    falling = true;
    const startFeet = vy >= 0 ? feet() : -1e9;
    let t0 = performance.now();
    const step = now => {
      if (!root || !falling) return;
      const dt = Math.min(0.04, (now - t0) / 1000); t0 = now;
      const w = root.offsetWidth;
      vy = Math.min(1600, vy + 2200 * dt);
      vx *= Math.pow(0.4, dt);
      show(airFrame ? airFrame(vy) : vy < 0 ? 'leap_3' : 'hopdown_2');
      const f0 = feet();
      fx += vx * dt; fy += vy * dt;
      if (fx < 0) { fx = 0; vx = Math.abs(vx) * 0.5; }
      if (fx > innerWidth - w) { fx = innerWidth - w; vx = -Math.abs(vx) * 0.5; }
      if (fy < 0) { fy = 0; vy = Math.abs(vy) * 0.3; }
      // Опору ищем ниже, чем начали падать: с той, откуда упал, не «приземляется» обратно.
      const hit = vy > 0 && feet() > startFeet + 2 ? landing(Math.max(f0, startFeet + 3), feet()) : null;
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
    falling = false; leaping = false; vx = vy = 0;
    setFlip(false);
    flash('squash', 300);
    save();
    if (REDUCE.matches) { back(); return; }
    const then = afterLand; afterLand = null;
    if (then) { then(speed); return; }
    if (speed > 900) { flash('hop', 650); play([['touch_1', 160], ['touch_3', 220], ['alert_2', 400]], 0, back); }
    else play([['touch_3', 140], ['touch_5', 180]], 0, back);
  }
  // Прыжок на опору: выше — присел, прыгнул, подтянулся за край; сильно выше — вскарабкался;
  // ниже — заглянул вниз и спрыгнул (иногда передумал).
  // done — что дальше после приземления (в путешествии); без него — садится. quick — на бегу, без примерки.
  function leapTo(t, done, quick) {
    if (!free || !root) return;
    const w = root.offsetWidth, f = feet();
    const tx = Math.max(t.r.left - w * 0.2, Math.min(t.r.right - w * 0.8, (t.r.left + t.r.right) / 2 - w / 2 + rnd(-30, 30)));
    const dy = t.r.top - f, far = Math.abs(tx - fx) > 260;
    setFlip(tx < fx);
    // Вскарабкаться можно только по боку высокого элемента, который доходит донизу, до лап.
    if (dy < -180 && Math.abs(tx - fx) < 260 && t.r.bottom >= f - 40) { climbTo(t, done); return; }
    // Цель — то же место, где стоит (вложенный элемент): просто остаётся.
    if (Math.abs(tx - fx) < 12 && Math.abs(dy) < 6) { support = t.e; (done || back)(); return; }
    leaping = true; // пока готовится к прыжку, проверка опоры его не роняет
    const go = (pre) => play(pre, 0, () => {
      if (!root || held) { leaping = false; return; }
      const T = Math.max(0.45, Math.min(0.9, Math.hypot(tx - fx, dy) / 700)), g = 2200;
      vx = (tx - fx) / T; vy = (dy - 0.5 * g * T * T) / T;
      support = null; leaping = true;
      afterLand = sp => play(dy < -40 ? [['highjump_6', 200], ['touch_3', 140], ['touch_5', 180]]
        : sp > 900 ? [['touch_1', 120], ['touch_3', 160], ['touch_5', 200]] : [['touch_3', 130], ['touch_5', 170]], 0, done || back);
      // Вверх — тянется лапами; далеко — длинный прыжок; вниз — спрыгивает; рядом — обычный прыжок вбок.
      fall(v => dy < -60 ? (v < -300 ? 'highjump_2' : v < 0 ? 'highjump_3' : 'leap_5')
        : far ? (v < -250 ? 'longjump_3' : v < 0 ? 'longjump_4' : v < 300 ? 'longjump_5' : 'longjump_6')
        : dy > 40 ? (v < 0 ? 'leap_3' : 'hopdown_2') : (v < -150 ? 'leap_2' : v < 150 ? 'leap_3' : 'leap_5'));
    });
    if (!done && dy > 140 && chance(0.25)) { // передумал прыгать вниз
      play([['edgethink_1', 500], ['edgethink_2', 600], ['edgethink_3', 700], ['edgethink_4', 500]], 0, () => { leaping = false; back(); });
      return;
    }
    if (quick) { go(dy < -60 ? [['highjump_1', 140]] : [['leap_1', 120]]); return; }
    go(dy > 40 ? [['lookdown_1', 400], ['lookdown_2', 500], ['lookdown_3', 400], ['leap_1', 220]]
      : dy < -60 ? [['highjump_1', 300]] : far ? [['longjump_1', 200], ['longjump_2', 150]] : [['leap_1', 250]]);
  }
  // Карабкается по боковому краю карточки вверх.
  function climbTo(t, done) {
    const w = root.offsetWidth, h = root.offsetHeight;
    const leftSide = fx + w / 2 < (t.r.left + t.r.right) / 2;
    const x = leftSide ? t.r.left - w * 0.6 : t.r.right - w * 0.4;
    setFlip(!leftSide);
    leaping = true; stopAnim();
    const y0 = fy, y1 = t.r.top - h + FOOT(), x0 = fx;
    const frames = seq('climb', 4);
    const t0 = performance.now(), dur = Math.max(1200, (y0 - y1) * 9);
    const step = now => {
      if (!root || !leaping) return;
      const k = Math.min(1, (now - t0) / dur);
      fx = x0 + (x - x0) * Math.min(1, k * 4); fy = y0 + (y1 - y0) * k;
      show(frames[Math.floor(now / 160) % 4]);
      place();
      if (k < 1) { requestAnimationFrame(step); return; }
      leaping = false; support = t.e;
      fx = Math.max(t.r.left - w * 0.2, Math.min(t.r.right - w * 0.8, fx + (leftSide ? w * 0.6 : -w * 0.6))); place();
      save();
      play([['highjump_5', 200], ['highjump_6', 220], ['touch_3', 160], ['touch_5', 200]], 0, done || (() => { setFlip(false); back(); }));
    };
    requestAnimationFrame(step);
  }
  function hop() {
    if (!free) return;
    const f = feet(), cx = fx + root.offsetWidth / 2;
    const c = targets().filter(({ r }) => Math.abs(r.top - f) < 320 && Math.abs((r.left + r.right) / 2 - cx) < 420 && Math.abs((r.left + r.right) / 2 - cx) > 50);
    if (c.length) leapTo(pick(c));
  }
  // Спрыгивает с края опоры (или из меню) в сторону dir и падает на то, что внизу.
  function jumpOff(dir, then) {
    if (!free || !root) return;
    setFlip(dir < 0);
    leaping = true;
    play([['leap_1', 200]], 0, () => {
      if (!root || held) { leaping = false; return; }
      vx = dir * 320; vy = -380; support = null;
      afterLand = () => play([['hopdown_3', 220]], 0, then);
      fall(v => v < 0 ? 'leap_3' : 'hopdown_2');
    });
  }

  // ---------- путешествия ----------
  // К опоре t через другие: перепрыгивает на ту, что ближе к цели; некуда — бежит к краю и спрыгивает;
  // внизу окна — бежит под цель и запрыгивает или вскарабкивается. opt.fast — носится бегом.
  let trip = null;
  function halt() { trip = null; stopStroll(); }
  function travel(t, opt = {}) {
    if (!root || !free || held) return;
    const me = trip = { hops: 0, fast: !!opt.fast, seen: [] };
    const stop = () => { if (trip === me) trip = null; setFlip(false); back(); };
    const finish = () => {
      if (trip !== me) return;
      trip = null;
      play(me.fast ? [['brake_1', 110], ['brake_2', 160], ['brake_3', 220], ['pant_1', 700], ['pant_2', 900], ['pant_1', 500]]
        : [['stop_3', 140], ['stop_4', 150], ['stop_5', 220]], 0, () => { setFlip(false); save(); back(); });
    };
    const next = () => {
      if (trip !== me || !root || !free || held) {
        // Путешествие прервали (новое состояние, руки) — если никто не занял кота, возвращается к своему.
        if (trip !== me && root && !held && !busy && !strolling && !inAir()) back();
        return;
      }
      if (!t.e.isConnected) { stop(); return; }
      t.r = t.e.getBoundingClientRect();
      if (support === t.e || ++me.hops > 14) { finish(); return; }
      if (support) { if (me.seen.includes(support)) me.hops += 2; me.seen = [support, ...me.seen].slice(0, 4); }
      if (!supported()) { vx = vy = 0; support = null; afterLand = () => play([['touch_3', 160], ['touch_5', 160]], 0, next); fall(); return; }
      const w = root.offsetWidth, f = feet(), cx = fx + w / 2;
      const mid = r => (r.left + r.right) / 2;
      const gap = r => Math.max(0, r.left - cx, cx - r.right);
      const ok = r => r.width > 0 && gap(r) <= 360 && (r.top - f >= -260 || (gap(r) < 200 && r.bottom >= f - 40));
      if (ok(t.r)) { leapTo(t, next, me.fast); return; }
      const far = c => Math.hypot(mid(c.r) - mid(t.r), c.r.top - t.r.top);
      const mine = Math.hypot(cx - mid(t.r), f - t.r.top);
      // Промежуточная опора: ближе к цели, не из недавних и в стороне (чтобы не скакать на месте по соседним).
      const c = targets().filter(c => c.e !== t.e && ok(c.r) && far(c) < mine - 60 && !me.seen.includes(c.e)
        && (Math.abs(Math.max(c.r.left, Math.min(c.r.right - w, cx - w / 2)) - fx) > 60 || Math.abs(c.r.top - f) > 24))
        .sort((a, b) => far(a) - far(b));
      if (c.length) { leapTo(c.length > 1 && chance(0.3) ? c[1] : c[0], next, me.fast); return; }
      const dir = mid(t.r) > cx ? 1 : -1;
      if (support) {
        const r = support.getBoundingClientRect();
        if (!stroll(dir > 0 ? r.right - w * 0.8 : r.left - w * 0.2, { fast: me.fast, done: () => jumpOff(dir, next) })) jumpOff(dir, next);
      } else if (!stroll(mid(t.r) - w / 2, { fast: me.fast, done: () => { me.hops += 3; next(); } })) stop();
    };
    next();
  }
  // Отправляется к случайной далёкой опоре; fast — «носится» бегом.
  function roam(fast) {
    const f = feet(), cx = fx + root.offsetWidth / 2;
    const c = targets().filter(({ r }) => Math.hypot((r.left + r.right) / 2 - cx, r.top - f) > 300);
    if (!c.length) return false;
    travel(pick(c), { fast });
    return true;
  }

  // ---------- ходьба ----------
  // Встал — развернулся, если нужно, — шагом или бегом — остановился — сел. Иногда по пути натыкается
  // на коробочку и обходит её.
  let strolling = false, strollRaf = 0;
  function stopStroll() { cancelAnimationFrame(strollRaf); if (strolling) busy = false; strolling = false; }
  // opt.fast — бегом (по умолчанию — как выйдет), opt.done — что делать на месте вместо «сесть».
  // false — идти некуда (опоры нет или она слишком узкая).
  function stroll(target, opt = {}) {
    const w = root.offsetWidth, done0 = opt.done;
    let lo, hi, cur0;
    if (free) {
      if (support && support.isConnected) {
        const r = support.getBoundingClientRect();
        lo = Math.max(0, r.left - w * 0.2); hi = Math.min(innerWidth - w, r.right - w * 0.8);
      } else if (!support && feet() >= innerHeight - 3) { lo = 0; hi = innerWidth - w; } // по низу окна
      else return false;
      cur0 = fx;
    } else {
      const host = root.parentElement;
      lo = 0; hi = host ? host.clientWidth - w - 8 : 0; cur0 = posX;
    }
    if (hi - lo < 30) return false;
    let to = target != null ? Math.max(lo, Math.min(hi, target)) : lo + Math.random() * (hi - lo);
    if (Math.abs(to - cur0) < 25) {
      if (done0) { busy = true; done0(); return true; }
      to = cur0 - lo < (hi - lo) / 2 ? hi : lo;
    }
    const dir = Math.sign(to - cur0), left = dir < 0, dist = Math.abs(to - cur0);
    // Аллюр: галоп (с разгоном), рысь, шаг или шаг вполоборота.
    const fast = opt.fast != null ? opt.fast : dist > 220 && chance(0.4);
    const trot = !fast && dist > 150 && chance(0.35);
    const diag = !fast && !trot && !done0 && chance(0.15);
    const gait = fast ? { cycle: seq('gallop', 6), ms: 75, speed: 140 } : trot ? { cycle: seq('trot', 6), ms: 95, speed: 70 }
      : diag ? { cycle: seq(left ? 'diagl' : 'diagr', 4), ms: 190, speed: 30 } : { cycle: seq('stride', 8), ms: 115, speed: 34 };
    const turn = flip !== left && !diag;
    strolling = true; busy = true;
    const begin = () => {
      setFlip(diag ? false : left);
      const t00 = performance.now();
      let t0 = t00, x = cur0, pausedUntil = 0;
      // По пути (шагом, далеко): иногда коробочка на дороге, иногда спотыкается.
      let gag = !fast && !done0 && dist > 120 && chance(0.2) ? pick(['box', 'trip']) : null;
      const gagAt = (cur0 + to) / 2;
      const step = now => {
        if (!root || !strolling) return;
        if (now < pausedUntil) { strollRaf = requestAnimationFrame(step); t0 = now; return; }
        const accel = fast && now - t00 < 360; // разгон: первые шаги медленнее
        x += dir * (accel ? 80 : gait.speed) * Math.min(0.05, (now - t0) / 1000); t0 = now;
        if (gag && (dir > 0 ? x >= gagAt : x <= gagAt)) {
          const pseq = gag === 'box' ? [...seq('obstop', 4), ...seq('obgo', 4)].map(n => [n, 260])
            : [['trip_1', 130], ['trip_2', 150], ['trip_3', 200], ['trip_4', 450], ['getup_3', 180], ['getup_4', 180]];
          gag = null;
          let t = 0;
          for (const [n, ms] of pseq) { setTimeout(() => strolling && show(n), t); t += ms; }
          pausedUntil = now + t;
        } else if (now >= pausedUntil) {
          show(accel ? seq('accel', 4, 5)[Math.min(3, Math.floor((now - t00) / 90))] : gait.cycle[Math.floor(now / gait.ms) % gait.cycle.length]);
        }
        const done = dir > 0 ? x >= to : x <= to;
        if (done) x = to;
        if (free) { fx = x; place(); } else { posX = x; root.style.setProperty('--pet-x', posX + 'px'); }
        if (done) {
          strolling = false;
          save();
          if (done0) { busy = true; done0(); return; }
          const sit = () => { setFlip(false); back(); };
          // Галоп — тормозит с заносом и иногда переводит дух; рысь и шаг — плавно останавливается.
          if (fast) play([['brake_1', 110], ['brake_2', 160], ['brake_3', 220], ...(chance(0.4) ? [['pant_1', 700], ['pant_2', 900]] : [])], 0, sit);
          else if (trot) play(seq('stop', 5).map(n => [n, 120]), 0, sit);
          else if (chance(0.4)) play([['sitdown_2', 160], ['sitdown_3', 180], ['sitdown_4', 220]], 0, sit);
          else play([['stop_3', 140], ['stop_4', 150], ['stop_5', 200]], 0, sit);
          return;
        }
        strollRaf = requestAnimationFrame(step);
      };
      strollRaf = requestAnimationFrame(step);
    };
    // Встаёт (в пути — сразу трогается) и разворачивается на месте через «лицом к нам».
    const pre = done0 ? [['start_6', 90]] : [['start_3', 110], ['start_4', 110], ['start_5', 110]];
    if (turn) pre.push(['turnin_1', 110], ['turnin_2', 110], ['turnin_3', 130], [() => setFlip(left), 0], ['turnin_2', 110], ['start_6', 100]);
    stopAnim(); busy = true;
    let i = 0;
    const steps = () => {
      if (!strolling) return;
      if (i >= pre.length) { begin(); return; }
      const [f, ms] = pre[i++];
      if (typeof f === 'function') { f(); steps(); return; }
      show(f); animTimer = setTimeout(steps, ms);
    };
    steps();
    return true;
  }

  // ---------- курсор ----------
  let lastInput = performance.now(), watching = false, watchTimer = 0, lastPounce = 0, lastPaw = 0, ptr = null;
  let hovering = false, hoverTimer = 0, lastPetAt = 0;
  function wake() {
    if (!napping) return;
    napping = false;
    if (root) root.classList.remove('nap');
    if (!root || inAir() || (state !== 'on' && state !== 'sad')) return;
    halt();
    play(boxSleep ? [['box_2', 400], ['box_1', 400], ['shakefull_1', 120], ['shakefull_2', 120]]
      : [['lieyawn_1', 700], ...seq('wakestretch', 4).map(n => [n, 450]), ['yawnsit_2', 600]], 260, back);
  }
  function onKey() { lastInput = performance.now(); wake(); }
  // Куда смотреть: голова и глаза поворачиваются к курсору.
  function lookFrame(dx, dy, dist) {
    const ax = dx / dist, ay = dy / dist;
    if (ay < -0.6) return ax < -0.3 ? 'headturn_5' : ax > 0.3 ? 'lookeye_4' : 'lookeye_5';
    if (ax < -0.6) return 'headturn_3';
    if (ax < -0.2) return 'headturn_2';
    if (ax > 0.6) return 'lookeye_3';
    if (ax > 0.2) return 'lookeye_2';
    return 'lookeye_1';
  }
  function onPointer(e) {
    const now = performance.now();
    lastInput = now;
    if (napping) { wake(); return; }
    const prevPtr = ptr;
    ptr = { x: e.clientX, y: e.clientY, t: now };
    if (!root || REDUCE.matches || held || press || inAir() || strolling || busy || hovering) return;
    if (state !== 'on' && state !== 'sad') return;
    if (grumpy()) return; // обиделся — не смотрит
    const r = root.getBoundingClientRect();
    if (!r.width) return;
    const cx = r.left + r.width / 2, cy = r.top + r.height * 0.45;
    const dx = e.clientX - cx, dy = e.clientY - cy, dist = Math.hypot(dx, dy);
    if (dist > 650 || dist < 20) return;
    const speed = prevPtr ? Math.hypot(e.clientX - prevPtr.x, e.clientY - prevPtr.y) / Math.max(1, now - prevPtr.t) : 0;
    if (state === 'on') {
      // Совсем рядом и двигается — бьёт лапой.
      if (dist < 90 && speed > 0.4 && now - lastPaw > 4000) {
        lastPaw = now;
        play(dy < -40 ? [['pounceup_1', 160], ['pounceup_2', 260], ['reachb_4', 200]] : seq('paw', 6), 90, back);
        return;
      }
      // Резко махнули рядом — бросается ловить (иногда сначала зовёт поиграть).
      if (speed > 1.6 && dist < 260 && dist > 60 && Math.abs(dy) < 170 && now - lastPounce > 15000 && chance(0.35)) {
        lastPounce = now;
        pounce(e.clientX);
        return;
      }
    }
    // Курсор над головой — тянется лапой; иначе поворачивает голову и глаза.
    watching = true;
    if (dy < -50 && Math.abs(dx) < 70 && dist < 200) show(dist < 120 ? pick(['reach_2', 'reachb_3', 'reachb_4']) : pick(['reach_1', 'reachb_1', 'reachb_2']));
    else if (dy < -120 && Math.abs(dx) < 120) show(pick(['lookup_3', 'lookup_4']));
    else show(lookFrame(dx, dy, dist));
    clearTimeout(watchTimer);
    watchTimer = setTimeout(() => { watching = false; if (root && !busy && !strolling && !inAir()) back(); }, 1500);
  }
  // Прыжок за курсором по своей опоре.
  function pounce(targetX) {
    const w = root.offsetWidth;
    let lo, hi, cur0;
    if (free) {
      if (!support || !support.isConnected) return;
      const r = support.getBoundingClientRect();
      lo = Math.max(0, r.left - w * 0.2); hi = Math.min(innerWidth - w, r.right - w * 0.8); cur0 = fx;
    } else {
      const host = root.parentElement, hr = host && host.getBoundingClientRect();
      if (!hr) return;
      lo = 0; hi = host.clientWidth - w - 8; cur0 = posX;
      targetX -= hr.left;
    }
    const to = Math.max(lo, Math.min(hi, targetX - w / 2));
    watching = false; clearTimeout(watchTimer);
    setFlip(to < cur0);
    const bow = chance(0.35) ? [['playbow_1', 200], ['playbow_2', 200], ['playbow_3', 200]] : [];
    const pre = bow.length * 200 + 720;
    setTimeout(() => flash('pounce', 700), pre);
    play([...bow, ['sneak_1', 180], ['sneak_2', 180], ['sneak_3', 180], ['sneak_4', 180], ['leap_2', 150], ['leap_3', 150], ['leap_5', 150], ['touch_3', 300]], 0, () => {
      setFlip(false);
      // Поймал — гордо сидит с «добычей».
      if (ptr && Math.abs(ptr.x - (root.getBoundingClientRect().left + w / 2)) < 50 && chance(0.4)) play([['mouse_1', 1300], ['happy_1', 600]], 0, back);
      else back();
    });
    const from = cur0, t0 = performance.now() + pre;
    const step = now => {
      if (!root) return;
      const k = Math.max(0, Math.min(1, (now - t0) / 450));
      const x = from + (to - from) * k;
      if (free) { fx = x; place(); } else { posX = x; root.style.setProperty('--pet-x', posX + 'px'); }
      if (k < 1) requestAnimationFrame(step); else if (free) save();
    };
    requestAnimationFrame(step);
  }
  // Щёлкнули по карточке или кнопке — иногда бежит посмотреть, что там.
  let lastFollow = 0;
  function onAppClick(e) {
    if (!root || REDUCE.matches || root.contains(e.target) || state !== 'on' || napping || grumpy()) return;
    if (held || inAir() || strolling || busy || trip || hovering) return;
    const s = e.target.closest && e.target.closest(SURFACES), now = performance.now();
    if (!s || s === support || now - lastFollow < 60e3 || !chance(0.25)) return;
    lastFollow = now;
    setTimeout(() => {
      if (!root || !s.isConnected || held || inAir() || strolling || busy || trip) return;
      const t = targets().find(t => t.e === s);
      if (t) { if (free) travel(t); else explore(t); }
    }, 700);
  }
  // Курсор задержался на коте — ластится, а если давно не гладили — просит погладить.
  function hoverStart() {
    hovering = true;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => {
      if (!root || !hovering || held || press || inAir() || state !== 'on' || napping) return;
      halt(); watching = false;
      const begging = performance.now() - lastPetAt > 600e3 && chance(0.5);
      loop(() => begging ? [...seq('beg', 4), 'beg_3', 'beg_4'] : pick([['rub_1', 'rub_2', 'rub_1', 'rub_5'], ['bonk_1', 'bonk_2', 'bonk_1'], ['rub_5', 'rub_2', 'rub_4']]), 380);
      busy = true;
    }, 1200);
  }
  function hoverEnd() {
    hovering = false; clearTimeout(hoverTimer);
    if (root && !inAir() && !strolling && /^(rub|bonk|beg)_/.test(cur)) back();
  }

  // ---------- ласка и тычки ----------
  let strokes = 0, strokeAt = 0, lastX = null, lastPurr = 0, petCount = 0;
  function pet(e) {
    if (press) return;
    if (lastX != null && Math.abs(e.clientX - lastX) > 3) {
      const now = performance.now();
      if (now - strokeAt > 900) strokes = 0;
      strokes++; strokeAt = now;
      if (strokes >= 6) {
        strokes = 0; lastPetAt = now;
        if (onPurr) onPurr();
        if (state === 'off') { flash('purr', 2000); talk('Мррр…'); play(['sleep_3', 'sleep_1', 'sleep_3', 'sleep_1'], 400, back); lastX = e.clientX; return; }
        if (state === 'run' || state === 'err' || inAir()) { lastX = e.clientX; return; }
        halt(); hovering = false;
        // Обиделся — гладят — мирится.
        if (grumpy()) {
          grumpyUntil = 0; flash('purr', 2000); talk('Ну ладно…');
          play([...seq('forgive', 4).map(n => [n, 450]), ['happy_1', 900]], 0, back);
          lastX = e.clientX; return;
        }
        flash('purr', 2000); talk('Мррр…');
        const zone = zoneAt(e.clientX, e.clientY), again = now - lastPurr < 12000;
        lastPurr = now; petCount++;
        if (/^(backroll|belly|kick)_/.test(cur)) play([...seq('kick', 3), 'kick_2', 'kick_3', 'kick_1'], 220, () => play([['happy_1', 700]], 0, back));
        else if (again && chance(0.6)) play([...seq('backroll', 4), ['backroll_3', 500], ['belly_1', 600], ['belly_2', 700], ['belly_1', 500]], 350, back);
        else if (zone === 'head') play(['headpat_1', 'headpat_2', 'headpat_1', 'headpat_2', ['happy_1', 700]], 450, back);
        else if (zone === 'chin') play(['chin_1', 'chin_2', 'chin_1', 'chin_2', ['happy_1', 700]], 500, back);
        else play([...seq('purrb', 4), 'purrb_2', 'purrb_3', 'purr_1', 'purr_2'], 380, back);
      }
    }
    lastX = e.clientX;
  }
  let dragged = false;
  function poke(e) {
    if (dragged) { dragged = false; return; }
    if (inAir()) return;
    if (napping) { wake(); talk('Мр… я не сплю!'); return; }
    if (state === 'run' || state === 'err') { talk(say(state) || '…'); return; }
    halt();
    if (state === 'off') { play([['sleep_3', 600]], 0, back); talk(say(state) || '…'); return; }
    const zone = zoneAt(e.clientX, e.clientY);
    if (zone === 'tail') { play([['angry_1', 600], ['angry_2', 500]], 0, back); talk('Мяу! Не трогай хвост!'); return; }
    if (zone === 'paw' || zone === 'leg') { play(seq('paw', 6), 90, back); talk(say(state) || '…'); return; }
    if (zone === 'head' || zone === 'chin') { flash('hop', 650); play([[pick(['tiltb_1', 'tiltb_3', 'tilt_1', 'poke_1', 'wink_1']), 900]], 0, back); }
    else play([['alert_2', 500], ['tiltb_2', 600]], 0, back);
    talk(say(state) || '…');
  }

  // ---------- руки: перенос мышью ----------
  // За что взяли, так и висит: холка — смирно, хвост — вниз головой и возмущается, лапа — болтается,
  // туловище — «колбаской». Трясут — кружится голова. Висящий кадр держится верхом под курсором.
  let held = null, press = null;
  const HOLD = { head: ['scruff_1', 'scruff_2', 'scruff_3'], tail: ['bytail_1', 'bytail_2', 'bytail_3'], paw: ['bypaw_1', 'bypaw_2'],
    leg: ['byleg_1', 'byleg_2'], body: ['bybody_1', 'bybody_2', 'bybody_3'], chin: ['scruff_1', 'scruff_2', 'scruff_3'] };
  function down(e) {
    if (e.button !== 0) return;
    dragged = false;
    press = { x: e.clientX, y: e.clientY, id: e.pointerId, zone: zoneAt(e.clientX, e.clientY) };
    try { root.setPointerCapture(e.pointerId); } catch (_) { /* старый WebView */ }
  }
  function moveHeld(e) {
    if (!press) return false;
    const now = performance.now();
    if (!held) {
      if (Math.hypot(e.clientX - press.x, e.clientY - press.y) < 6) return false;
      makeFree();
      stopFall(); halt(); stopAnim();
      exploreUntil = 0; napping = false; hovering = false;
      root.classList.remove('nap', 'leaving', 'arriving');
      try { root.setPointerCapture(press.id); } catch (_) { /* уже пойман */ }
      setFlip(false);
      const s = root.offsetWidth / 64;
      held = { zone: press.zone, dx: 32 * s, dy: 4 * s, lx: e.clientX, ly: e.clientY, t: now, since: now, vx: 0, vy: 0, flips: [], lastSign: 0, dizzy: false };
      root.classList.add('held');
      if (bubble) bubble.hidden = true;
    }
    const dt = Math.max(1, now - held.t) / 1000;
    held.vx = held.vx * 0.5 + (e.clientX - held.lx) / dt * 0.5;
    held.vy = held.vy * 0.5 + (e.clientY - held.ly) / dt * 0.5;
    held.lx = e.clientX; held.ly = e.clientY; held.t = now;
    // Тряска: частая смена направления на скорости.
    const sg = Math.abs(held.vx) > 700 ? Math.sign(held.vx) : 0;
    if (sg && sg !== held.lastSign) { held.flips.push(now); held.lastSign = sg; }
    held.flips = held.flips.filter(t => now - t < 1200);
    if (held.flips.length >= 4) held.dizzy = true;
    fx = Math.max(0, Math.min(innerWidth - root.offsetWidth, e.clientX - held.dx));
    fy = Math.max(0, Math.min(innerHeight - root.offsetHeight, e.clientY - held.dy));
    place();
    root.style.setProperty('--pet-tilt', Math.max(-20, Math.min(20, -held.vx / 60)).toFixed(1) + 'deg');
    const frames = held.dizzy ? ['dizzy_1', 'dizzy_2', 'dizzy_3'] : HOLD[held.zone] || HOLD.head;
    // Держат долго — вырывается быстрее; за хвост — всё время извивается.
    const ms = held.zone === 'tail' || now - held.since > 2500 ? 140 : 380;
    show(frames[Math.floor(now / ms) % frames.length]);
    return true;
  }
  function up(e) {
    if (!held) { press = null; try { root.releasePointerCapture(e.pointerId); } catch (_) { /* не пойман */ } return; }
    const h = held, now = performance.now();
    held = null; press = null; dragged = true;
    root.classList.remove('held');
    root.style.setProperty('--pet-tilt', '0deg');
    try { root.releasePointerCapture(e.pointerId); } catch (_) { /* уже отпущен */ }
    const hr = home && home.getBoundingClientRect();
    if (hr && e.clientX >= hr.left && e.clientX <= hr.right && e.clientY >= hr.top - 40 && e.clientY <= hr.bottom) { goHome(); return; }
    vx = Math.max(-1500, Math.min(1500, h.vx)); vy = Math.max(-900, Math.min(900, h.vy));
    const thrown = Math.abs(vx) > 900 || Math.abs(vy) > 700;
    const byTail = h.zone === 'tail' && now - h.since > 1200;
    // Что будет после приземления: головокружение, обида, испуг — или просто отряхнётся.
    afterLand = () => {
      if (h.dizzy) { play([...seq('dizzy', 3), ...seq('dizzy', 3), ...seq('shakefull', 4)].map(n => [n, 180]), 0, back); talk('Всё кружится…', 1800); return; }
      if (byTail || (thrown && now - h.since > 300)) {
        grumpyUntil = performance.now() + 45000;
        play([['touch_1', 250], ['hiss_1', 350], ['hiss_2', 500], ...seq('angry', 4).map(n => [n, 600]), ['sulk_1', 2500]], 0, back);
        setTimeout(() => talk(byTail ? 'Шшш! За хвост нельзя!' : 'Хмф!', 1800), 300);
        return;
      }
      if (thrown) { play([...seq('tumble', 3, 5).map(n => [n, 160]), ...seq('getup', 4).map(n => [n, 170]), ['alert_1', 400]], 0, back); return; }
      play(chance(0.5) ? [['touch_1', 200], ['touch_3', 150], ...seq('shakefull', 4).map(n => [n, 120])] : [['touch_3', 160], ['touch_5', 200]], 0, back);
    };
    support = null;
    // Швырнули — кувыркается в воздухе; просто отпустили — летит лапами вниз.
    const t0 = performance.now();
    fall(thrown ? () => seq('flip', 3)[Math.floor((performance.now() - t0) / 90) % 3] : v => v < 0 ? 'spook_2' : 'fallhigh_2');
  }

  // ---------- гравитация ----------
  let gravTimer = 0;
  function gravity() {
    if (!root || !free || inAir() || strolling) return;
    const w = root.offsetWidth, h = root.offsetHeight;
    if (fx > innerWidth - w || fy > innerHeight - h) {
      fx = Math.max(0, Math.min(innerWidth - w, fx)); fy = Math.max(0, Math.min(innerHeight - h, fy)); place();
    }
    // Опора пропала — секунду висит, цепляясь, потом падает.
    if (!supported()) {
      vx = vy = 0;
      if (REDUCE.matches || feet() > innerHeight - 40) { fall(); return; }
      halt(); hanging = true;
      play(['hang_1', 'hang_2', 'hang_1', 'hang_2'], 200, () => {
        hanging = false;
        afterLand = () => play([['fallhigh_3', 300], ['fallhigh_4', 500]], 0, back);
        fall(v => v < 500 ? 'fallhigh_1' : 'fallhigh_2');
      });
    }
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
    root.title = 'Можно погладить, ткнуть и унести мышью; двойной щелчок — домой';
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
    root.addEventListener('pointerenter', hoverStart);
    root.addEventListener('pointerleave', hoverEnd);
    show('idle_1');
    const sp = opts.spot;
    if (Array.isArray(sp) && sp.length === 2) {
      makeFree();
      fx = Math.max(0, Math.min(innerWidth - root.offsetWidth, sp[0] * innerWidth));
      fy = Math.max(0, Math.min(innerHeight - root.offsetHeight, sp[1] * innerHeight));
      place();
      requestAnimationFrame(() => { if (root && !supported()) { const hit = landing(feet() - 4, feet() + 4); if (hit) support = hit.el; else fall(); } });
    }
    gravTimer = setInterval(gravity, 700);
    addEventListener('resize', gravity);
    addEventListener('pointermove', onPointer, { passive: true });
    addEventListener('keydown', onKey);
    addEventListener('click', onAppClick, true);
    lastInput = performance.now(); napping = false;
    const hr = new Date().getHours();
    if (!REDUCE.matches && hr >= 6 && hr < 11 && !sp) setTimeout(() => { if (root && !busy && !inAir()) play([['stretchb_1', 1000], ['stretchb_3', 500], ['yawnsit_2', 700]], 0, back); }, 1500);
    tick();
    idleLoop();
  }

  function unmount() {
    if (!root) return;
    stopAnim(); halt(); stopFall(); clearTimeout(idleTimer); clearTimeout(tickTimer); clearInterval(gravTimer);
    clearTimeout(watchTimer); clearTimeout(hoverTimer);
    removeEventListener('resize', gravity);
    removeEventListener('pointermove', onPointer);
    removeEventListener('keydown', onKey);
    removeEventListener('click', onAppClick, true);
    root.remove();
    root = svg = bubble = body = sprite = clipRect = home = support = null;
    state = prev = ''; free = false; held = press = null; exploreUntil = 0; napping = false;
  }

  // Радость: прыжок и искры.
  let joyTimer = 0;
  function joy() {
    if (!root) return;
    clearTimeout(joyTimer);
    root.classList.add('joy');
    joyTimer = setTimeout(() => root && root.classList.remove('joy'), 2400);
    if (!REDUCE.matches && state !== 'off' && !inAir()) {
      halt();
      play([['happy_1', 400], ['highjump_1', 170], ['highjump_3', 170], ['highjump_4', 200], ['highjump_5', 170], ['touch_3', 200], ['happy_1', 700]], 0, back);
    }
  }
  // Сообщение об ошибке: подпрыгивает от испуга.
  function startle() {
    if (!root || REDUCE.matches || inAir() || state === 'off' || state === 'run') return;
    if (napping) { wake(); return; }
    halt();
    play([['spook_1', 200], ['spook_2', 350], ['spook_3', 400], ['alert_2', 600], ['alert_3', 400]], 0, back);
  }
  // Нажали «Подключить» — нажимает лапой вместе с вами.
  function pressBtn() {
    if (!root || REDUCE.matches || inAir() || state === 'off' || state === 'run') return;
    if (napping) { wake(); return; }
    halt();
    play([['press_1', 250], ['press_2', 450], ['press_1', 200]], 0, back);
  }

  // update — фаза подключения (off | wait | on | revive | err | blocked) и скорость в байтах/с.
  function update(phase, bps) {
    if (!root) return;
    const s = phase === 'on' ? (bps > 150 * 1024 ? 'run' : 'on') : phase === 'wait' || phase === 'revive' ? 'wait'
      : phase === 'err' ? 'err' : phase === 'blocked' ? 'sad' : 'off';
    const kb = Math.max(0, bps) / 1024;
    root.style.setProperty('--pet-step', (0.7 - Math.min(0.48, Math.log10(1 + kb) * 0.13)).toFixed(2) + 's');
    if (s === state) return;
    const revived = (state === 'err' || state === 'wait' || state === 'sad') && (s === 'on' || s === 'run');
    if (s !== 'on' && s !== 'sad') napping = false;
    prev = state; state = s; root.dataset.mood = s;
    enter(s, prev);
    if (revived && prev !== 'wait') { joy(); talk('Ура, связь вернулась!'); }
  }

  return {
    mount, unmount, update, talk, startle, press: pressBtn,
    home: () => { if (root && free) goHome(); },
    cheer: (t) => { flash('hop', 650); joy(); talk(t, 3600); },
  };
})();
