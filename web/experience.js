'use strict';
// Presentation only. This module has no network or core controls.
window.NaivkaExperience = (() => {
  const events = new EventTarget(), motion = new EventTarget();
  const system = matchMedia('(prefers-reduced-motion: reduce)');
  let prefs = {}, level = 'balanced', previous = null;
  const on = (name, fn) => { events.addEventListener(name, fn); return () => events.removeEventListener(name, fn); };
  const emit = (name, detail) => events.dispatchEvent(new CustomEvent(name, { detail }));
  Object.defineProperty(motion, 'matches', { get: () => system.matches || document.hidden || level === 'off' });
  const changed = () => {
    document.documentElement.dataset.effects = motion.matches ? 'off' : level;
    motion.dispatchEvent(new Event('change'));
    emit('effects.changed', { level, reduced: motion.matches });
  };
  system.addEventListener('change', changed);
  document.addEventListener('visibilitychange', changed);
  function configure(p = {}) {
    const before = JSON.stringify(prefs);
    prefs = { effects: p.effects || 'balanced', sound: p.sound || 'off', volume: Number.isFinite(p.volume) ? p.volume : 20, atmosphere: p.atmosphere || 'off' };
    level = ['off', 'low', 'balanced', 'full'].includes(prefs.effects) ? prefs.effects : 'balanced';
    if (JSON.stringify(prefs) !== before) { stopSound(); changed(); atmosphere(); }
  }
  // Initial snapshot is silent. Repeated API polling emits no duplicate event.
  function observe(state, server) {
    const next = { running: !!state?.running, error: !!state?.error, server: server || '' };
    if (previous) {
      if (next.running !== previous.running) emit(next.running ? 'connection.connected' : 'connection.disconnected');
      if (next.error && !previous.error) emit('connection.failed');
      if (next.running && previous.running && next.server && previous.server && next.server !== previous.server) emit('server.changed');
    }
    previous = next;
  }
  let audio = null, unlocked = false, sounds = new Set(), cooldown = new Map();
  // WebView/browser autoplay rules are respected; never resume audio on polling.
  document.addEventListener('pointerdown', () => { unlocked = true; }, { capture: true });
  document.addEventListener('keydown', () => { unlocked = true; }, { capture: true });
  function stopSound() {
    for (const o of sounds) { try { o.stop(); } catch (_) {} }
    sounds.clear();
  }
  function sound(name) {
    if (prefs.sound === 'off' || !unlocked || document.hidden || !prefs.volume) return;
    const now = performance.now();
    if (now - (cooldown.get(name) ?? -Infinity) < 3000) return;
    cooldown.set(name, now);
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    try {
      audio ||= new AC();
      // Suspended contexts are resumed only as part of the next user gesture.
      if (audio.state !== 'running') return;
      const o = audio.createOscillator(), gain = audio.createGain();
      o.type = prefs.sound === 'retro' ? 'triangle' : 'sine';
      const base = { 'connection.connected': 660, 'connection.disconnected': 330, 'connection.failed': 220, 'server.changed': 550 }[name] || 440;
      o.frequency.setValueAtTime(base, audio.currentTime);
      o.frequency.exponentialRampToValueAtTime(base * (prefs.sound === 'digital' ? 1.5 : 1.12), audio.currentTime + .12);
      gain.gain.setValueAtTime(0, audio.currentTime);
      gain.gain.linearRampToValueAtTime(Math.max(0, Math.min(100, prefs.volume)) / 100 * .08, audio.currentTime + .015);
      gain.gain.exponentialRampToValueAtTime(.0001, audio.currentTime + .18);
      o.connect(gain); gain.connect(audio.destination); sounds.add(o);
      o.onended = () => { sounds.delete(o); o.disconnect(); gain.disconnect(); };
      o.start(); o.stop(audio.currentTime + .2);
    } catch (_) { /* Audio support is optional. */ }
  }
  const unlock = () => { if (prefs.sound === 'off') return; const AC = window.AudioContext || window.webkitAudioContext; if (!AC) return; try { audio ||= new AC(); if (audio.state === 'suspended') audio.resume().catch(() => {}); } catch (_) {} };
  document.addEventListener('pointerdown', unlock);
  document.addEventListener('keydown', unlock);
  for (const name of ['connection.connected', 'connection.disconnected', 'connection.failed', 'server.changed']) on(name, () => sound(name));
  let seasonalFrame = 0, seasonalLast = 0, seasonalCanvas = null, particles = [];
  function stopSeason() { cancelAnimationFrame(seasonalFrame); seasonalFrame = 0; seasonalLast = 0; seasonalCanvas?.remove(); seasonalCanvas = null; particles = []; }
  function seasonFrame(now) {
    seasonalFrame = 0;
    if (motion.matches || prefs.atmosphere === 'off') { stopSeason(); return; }
    seasonalFrame = requestAnimationFrame(seasonFrame);
    if (now - seasonalLast < 1000 / 24) return;
    const dt = seasonalLast ? Math.min(.1, (now - seasonalLast) / 1000) : 0; seasonalLast = now;
    const host = document.querySelector('.ambient');
    if (!host) { stopSeason(); return; }
    if (!seasonalCanvas) { seasonalCanvas = document.createElement('canvas'); seasonalCanvas.className = 'seasonal-canvas'; seasonalCanvas.setAttribute('aria-hidden','true'); host.append(seasonalCanvas); }
    const w = host.clientWidth, h = host.clientHeight;
    if (!w || !h) return;
    if (seasonalCanvas.width !== w || seasonalCanvas.height !== h) { seasonalCanvas.width = w; seasonalCanvas.height = h; }
    const ctx = seasonalCanvas.getContext('2d'); if (!ctx) { stopSeason(); return; }
    ctx.clearRect(0,0,w,h);
    const count = Math.ceil(18 * ({ low:.3, balanced:.65, full:1 }[level] || 0));
    while (particles.length < count) particles.push({ x:Math.random(), y:Math.random(), speed:.015 + Math.random()*.02, phase:Math.random()*6 });
    particles.length = count;
    const season = document.documentElement.dataset.season;
    for (const p of particles) {
      p.y = (p.y + p.speed * dt) % 1;
      const x = (p.x * w + Math.sin(now / 2500 + p.phase) * 16), y = p.y * h;
      ctx.fillStyle = season === 'winter' ? '#e8f5ff' : season === 'autumn' ? '#dba462' : '#b6e35a';
      ctx.globalAlpha = season === 'summer' ? .12 + .2 * Math.abs(Math.sin(now/1700+p.phase)) : .2;
      ctx.beginPath(); ctx.ellipse(x,y,season==='autumn'?3:1.5,season==='spring'?3:1.5,p.phase,0,Math.PI*2);ctx.fill();
    }
  }
  motion.addEventListener('change', () => { stopSeason(); if (!motion.matches && prefs.atmosphere !== 'off') seasonalFrame=requestAnimationFrame(seasonFrame); });
  function atmosphere(date = new Date()) {
    const root = document.documentElement;
    let value = prefs.atmosphere || 'off';
    if (value === 'auto') {
      const hour = date.getHours();
      value = hour < 6 || hour >= 22 ? 'night' : hour < 11 ? 'morning' : hour < 18 ? 'day' : 'evening';
    }
    root.dataset.atmosphere = value;
    root.dataset.season = ['winter','spring','summer','autumn'][Math.floor(((date.getMonth()+1)%12)/3)];
    stopSeason(); if (!motion.matches && value !== 'off') seasonalFrame=requestAnimationFrame(seasonFrame);
  }
  const atmosphereTimer = setInterval(() => { if (!document.hidden && prefs.atmosphere === 'auto') atmosphere(); }, 60000);
  document.addEventListener('visibilitychange', () => { if (document.hidden) stopSound(); else atmosphere(); });
  window.addEventListener('pagehide', () => { clearInterval(atmosphereTimer); stopSeason(); stopSound(); audio?.close(); }, { once: true });
  return { configure, observe, on, emit, motion, atmosphere, get level() { return level; }, get scale() { return motion.matches ? 0 : ({ low:.3, balanced:.65, full:1 }[level] || 0); } };
})();
