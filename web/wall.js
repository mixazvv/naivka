// Свой фон окна: фото или видео под интерфейсом, с затемнением, размытием и подсветкой по состоянию.
// Файл лежит в папке данных Naivka; окно берёт его по локальному адресу с токеном и показывает через blob:.
// В теме «Пиксели» (или по галочке) фон рисуется крупными клетками с урезанной палитрой.
const Wall = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const PX = 6;           // размер «пикселя» на экране, CSS px
  const LEVELS = 6;       // уровней на канал при урезании палитры
  const VIDEO_FPS = 12;   // пиксельное видео: кадров в секунду

  let layer = null, holder = null;
  let url = '', kind = '', ver = null, failed = false;
  let src = null;         // <img> или <video> с файлом
  let canvas = null, ctx = null, timer = 0;
  let pixel = false, loading = null;

  function ensureLayer() {
    if (layer) return;
    layer = document.createElement('div');
    layer.className = 'wall';
    layer.setAttribute('aria-hidden', 'true');
    holder = document.createElement('div');
    holder.className = 'wall-media';
    const tint = document.createElement('div');
    tint.className = 'wall-tint';
    layer.append(holder, tint);
    document.body.prepend(layer);
  }

  function clear() {
    stopVideoLoop();
    if (src && src.tagName === 'VIDEO') src.pause();
    if (url) URL.revokeObjectURL(url);
    url = ''; kind = ''; src = null; canvas = null; ctx = null; ver = null;
    if (layer) { layer.remove(); layer = null; holder = null; }
    delete document.documentElement.dataset.wall;
  }

  async function fetchFile(token, k) {
    const res = await fetch('/api/wallpaper', { headers: { 'X-Naivka-Token': token } });
    if (!res.ok) throw new Error('нет файла фона');
    const blob = await res.blob();
    const el = k === 'video' ? document.createElement('video') : new Image();
    const next = URL.createObjectURL(blob);
    if (k === 'video') {
      Object.assign(el, { muted: true, loop: true, playsInline: true, autoplay: !REDUCE.matches, preload: 'auto' });
      el.setAttribute('muted', '');
      await new Promise((ok, bad) => { el.onloadeddata = ok; el.onerror = () => bad(new Error('видео не открылось')); el.src = next; });
    } else {
      await new Promise((ok, bad) => { el.onload = ok; el.onerror = () => bad(new Error('картинка не открылась')); el.src = next; });
    }
    return { el, next };
  }

  // Пиксельная отрисовка: кадр вписывается «с обрезкой» в маленький холст, цвета сводятся к LEVELS уровням.
  function drawPixel() {
    if (!canvas || !src) return;
    const w = Math.max(16, Math.ceil(innerWidth / PX)), h = Math.max(16, Math.ceil(innerHeight / PX));
    if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
    const sw = src.videoWidth || src.naturalWidth, sh = src.videoHeight || src.naturalHeight;
    if (!sw || !sh) return;
    const k = Math.max(w / sw, h / sh), dw = sw * k, dh = sh * k;
    ctx.imageSmoothingEnabled = true;
    ctx.drawImage(src, (w - dw) / 2, (h - dh) / 2, dw, dh);
    const img = ctx.getImageData(0, 0, w, h), d = img.data, step = 255 / (LEVELS - 1);
    for (let i = 0; i < d.length; i += 4) {
      d[i] = Math.round(d[i] / step) * step;
      d[i + 1] = Math.round(d[i + 1] / step) * step;
      d[i + 2] = Math.round(d[i + 2] / step) * step;
    }
    ctx.putImageData(img, 0, 0);
  }

  function stopVideoLoop() { clearTimeout(timer); timer = 0; }
  function videoLoop() {
    stopVideoLoop();
    if (!pixel || !src || src.tagName !== 'VIDEO' || document.hidden) return;
    drawPixel();
    timer = setTimeout(videoLoop, 1000 / VIDEO_FPS);
  }

  // Показ: в пиксельном режиме — холст, иначе сам файл (картинка или видео) на весь фон.
  function mount() {
    stopVideoLoop();
    holder.replaceChildren();
    if (pixel) {
      canvas = document.createElement('canvas');
      canvas.className = 'wall-px';
      ctx = canvas.getContext('2d', { willReadFrequently: true });
      holder.append(canvas);
      drawPixel();
      if (src.tagName === 'VIDEO') { playVideo(); videoLoop(); }
    } else {
      canvas = null; ctx = null;
      holder.append(src);
      if (src.tagName === 'VIDEO') playVideo();
    }
    holder.classList.toggle('drift', kind === 'image' && !pixel && !REDUCE.matches);
    requestAnimationFrame(() => layer && layer.classList.add('shown'));
  }

  function playVideo() {
    if (!src || src.tagName !== 'VIDEO') return;
    if (document.hidden || REDUCE.matches) { src.pause(); return; }
    src.play().catch(() => { /* автозапуск запрещён — покажется первый кадр */ });
  }

  // apply — по настройкам (вызывается при запуске, смене темы и движении ползунков).
  async function apply(S, token) {
    const w = S && S.wall;
    if (!w || !w.kind) { clear(); return; }
    if (failed && ver === w.ver) return; // этот файл уже не открылся — не пробуем на каждом движении ползунка
    const root = document.documentElement;
    ensureLayer();
    root.dataset.wall = w.kind;
    layer.style.setProperty('--wall-dim', String((w.dim || 0) / 100));
    layer.style.setProperty('--wall-blur', (w.blur || 0) + 'px');
    layer.classList.toggle('react', !!w.react);
    layer.classList.toggle('blurred', (w.blur || 0) > 0);
    const wantPixel = !!w.pixel || S.theme === 'blocks';
    if (ver !== w.ver || kind !== w.kind) {
      if (loading) return loading.then(() => apply(S, token));
      loading = fetchFile(token, w.kind).then(({ el, next }) => {
        if (src && src.tagName === 'VIDEO') src.pause();
        if (url) URL.revokeObjectURL(url);
        url = next; src = el; kind = w.kind; ver = w.ver; failed = false;
        pixel = wantPixel;
        if (layer) { layer.classList.remove('shown'); mount(); }
      }).catch(() => { failed = true; ver = w.ver; delete root.dataset.wall; if (layer) layer.remove(); layer = null; })
        .finally(() => { loading = null; });
      return loading;
    }
    if (wantPixel !== pixel) { pixel = wantPixel; mount(); }
  }

  document.addEventListener('visibilitychange', () => {
    if (!src || src.tagName !== 'VIDEO') return;
    if (document.hidden) { src.pause(); stopVideoLoop(); } else { playVideo(); videoLoop(); }
  });
  let rz = 0;
  addEventListener('resize', () => { clearTimeout(rz); rz = setTimeout(() => { if (pixel && src && src.tagName === 'IMG') drawPixel(); }, 150); });

  return { apply, clear, url: () => url, kind: () => kind };
})();
