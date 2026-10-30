// Котёнок на панели задач Windows (pet.html в прозрачном окне поверх панели, см. ui_pet_windows.go).
// Сам котёнок — тот же web/pet.js; здесь — только состояние подключения для него и сообщения окну:
// где сейчас кот (туда окно пропускает мышь, остальное — насквозь) и держат ли его.
// hop=1 — кот живёт в окне Naivka и только наведывается сюда: спрыгнул из окна — появляется здесь
// (arrive), побегал минуту-две — запрыгивает обратно (leave → petback).
const petHost = (() => {
  'use strict';
  const Q = new URLSearchParams(location.search);
  const TOKEN = Q.get('k') || '', HOP = Q.get('hop') === '1';
  const post = m => { if (window.chrome && chrome.webview) chrome.webview.postMessage(JSON.stringify(m)); };
  let zone = null, out = false, backTimer = 0;
  const api = async path => {
    const r = await fetch(path, { headers: { 'X-Naivka-Token': TOKEN } });
    if (!r.ok) throw new Error(r.status);
    return r.json();
  };
  const SAY = { off: 'Naivka отключена — я сплю', wait: 'Подключаюсь…', run: 'Бегу вместе с трафиком!', err: 'Не получилось подключиться', sad: 'Трафик заблокирован…', on: 'Всё работает. Мяу!' };
  let S = null, beta = null;

  async function reload() {
    try { S = await api('/api/settings'); } catch (_) { return; }
    const b = S.mascotBeta === true;
    if (b !== beta) {
      beta = b;
      Pet.mount(document.getElementById('petFloor'), { beta: b, say: m => SAY[m] || 'Мяу', exit: HOP ? EXIT : null });
      if (HOP) { out = false; Pet.hide(); }
    }
  }

  async function poll() {
    try {
      const [st, stats] = await Promise.all([api('/api/state'), api('/api/stats?n=2')]);
      const zo = S && S.zapretOnly;
      const phase = st.starting ? 'wait' : zo ? (st.zapretOn ? 'on' : 'off') : st.blocked ? 'blocked'
        : st.running ? 'on' : st.reviving ? 'revive' : st.error ? 'err' : 'off';
      const smp = (stats && stats.samples) || [];
      const l = smp[smp.length - 1];
      const bps = phase === 'on' && l && Date.now() / 1000 - l.t < 4 ? (l.pd || 0) + (l.pu || 0) : 0;
      Pet.update(phase, bps, stats && stats.health);
    } catch (_) { /* Naivka перезапускается — следующий опрос */ }
  }

  // Переход в окно Naivka: отпустили над ним — кот там; запрыгнул — тоже.
  const inWin = (cx, cy) => !!(zone && zone.win && cx >= zone.win[0] && cx <= zone.win[2] && cy >= zone.win[1] && cy <= zone.win[3]);
  const EXIT = {
    ok: () => false, // уходит сам — по таймеру, см. scheduleBack
    cross: () => false,
    drop: (cx, cy) => out && inWin(cx, cy),
    go: (x, y, vx, vy) => { out = false; clearTimeout(backTimer); post({ cmd: 'petback', x, y, vx, vy }); },
  };
  // Побегал по панели минуту-две — запрыгивает в окно (держат или занят переходом — чуть позже).
  function tryBack() {
    if (!out) return;
    if (Pet.isHeld() || Pet.isGone() || !Pet.leave(zone && zone.win ? zone.win[3] : null)) backTimer = setTimeout(tryBack, 5000);
  }
  function scheduleBack() {
    clearTimeout(backTimer);
    backTimer = setTimeout(tryBack, 40e3 + Math.random() * 100e3);
  }
  function arrive(x, y, vx, vy) {
    out = true;
    Pet.appear(x, y, vx, vy);
    scheduleBack();
  }
  function recall() { out = false; clearTimeout(backTimer); Pet.hide(); }
  function setZone(z) {
    zone = z;
    Pet.bounds(z && z.win ? [z.win[0], z.win[2]] : null);
  }

  // Где кот: окно ловит мышь только там (плюс поля, чтобы было удобно попасть).
  let last = '';
  function report() {
    const el = document.querySelector('.pet');
    if (el && window.chrome && chrome.webview) {
      const r = el.getBoundingClientRect(), pad = 6, here = el.style.visibility !== 'hidden';
      const m = here ? { cmd: 'petrect', x: Math.round(r.left - pad), y: Math.round(r.top - pad), w: Math.round(r.width + 2 * pad), h: Math.round(r.height + 2 * pad), held: el.classList.contains('held') }
        : { cmd: 'petrect', x: 0, y: 0, w: 0, h: 0, held: false };
      const k = JSON.stringify(m);
      if (k !== last) { last = k; chrome.webview.postMessage(k); }
    }
    requestAnimationFrame(report);
  }

  reload().then(poll);
  setInterval(poll, 1000);
  setInterval(reload, 15000);
  requestAnimationFrame(report);
  return { reload, arrive, recall, zone: setZone };
})();
