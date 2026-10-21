// Свой фон окна: фото или видео под интерфейсом, с затемнением, размытием и подсветкой по состоянию.
// Файл лежит в папке данных Naivka; окно берёт его по локальному адресу с токеном и показывает через blob:.
// В теме «Пиксели» (или по галочке) фон рисуется крупными клетками с урезанной палитрой.
const Wall = (() => {
  'use strict';
  const REDUCE = matchMedia('(prefers-reduced-motion: reduce)');
  const PX = 6;           // размер «пикселя» на экране, CSS px
  const LEVELS = 6;       // уровней на канал при урезании палитры
  const VIDEO_FPS = 12;   // пиксельное видео: кадров в секунду

  let layer = null, holder = null, back = null;
  let url = '', kind = '', ver = null, failed = false;
  let src = null;         // <img> или <video> с файлом
  let canvas = null, ctx = null, timer = 0;
  let pixel = false, loading = null;
  let fit = 'cover', scale = 1, animated = false; // animated — видео или GIF: кадры надо перерисовывать

  function ensureLayer() {
    if (layer) return;
    layer = document.createElement('div');
    layer.className = 'wall';
    layer.setAttribute('aria-hidden', 'true');
    // back — размытая копия под картинкой «целиком» или уменьшенной: поля не чёрные, а в тон фону.
    back = document.createElement('div');
    back.className = 'wall-back';
    holder = document.createElement('div');
    holder.className = 'wall-media';
    const tint = document.createElement('div');
    tint.className = 'wall-tint';
    layer.append(back, holder, tint);
    // Фон — только в основной области (справа от меню, под заголовком окна), а не под всем окном.
    const host = document.querySelector('.stage-wrap') || document.body;
    host.prepend(layer);
    if (window.ResizeObserver) new ResizeObserver(() => { if (pixel && src && !animated) drawPixel(); }).observe(layer);
  }

  function clear() {
    stopVideoLoop();
    if (src && src.tagName === 'VIDEO') src.pause();
    if (url) URL.revokeObjectURL(url);
    url = ''; kind = ''; src = null; canvas = null; ctx = null; ver = null;
    setGif(null);
    if (layer) { layer.remove(); layer = null; holder = null; back = null; }
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
    return { el, next, blob };
  }

  // Пиксельная отрисовка: кадр вписывается по выбранному размеру в маленький холст, цвета сводятся
  // к LEVELS уровням. Если кадр не закрывает весь холст, под ним — затемнённая копия «с обрезкой».
  function drawPixel(frame) {
    const img0 = frame || src;
    if (!canvas || !img0) return;
    const lw = (layer && layer.clientWidth) || innerWidth, lh = (layer && layer.clientHeight) || innerHeight;
    const w = Math.max(16, Math.ceil(lw / PX)), h = Math.max(16, Math.ceil(lh / PX));
    if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
    const sw = img0.displayWidth || img0.videoWidth || img0.naturalWidth, sh = img0.displayHeight || img0.videoHeight || img0.naturalHeight;
    if (!sw || !sh) return;
    ctx.imageSmoothingEnabled = true;
    const cover = Math.max(w / sw, h / sh);
    if (fit !== 'cover' || scale < 1) {
      ctx.drawImage(img0, (w - sw * cover) / 2, (h - sh * cover) / 2, sw * cover, sh * cover);
      ctx.fillStyle = 'rgba(0,0,0,.55)';
      ctx.fillRect(0, 0, w, h);
    }
    let dw, dh;
    if (fit === 'fill') { dw = w; dh = h; }
    else { const k = fit === 'contain' ? Math.min(w / sw, h / sh) : cover; dw = sw * k; dh = sh * k; }
    dw *= scale; dh *= scale;
    ctx.drawImage(img0, (w - dw) / 2, (h - dh) / 2, dw, dh);
    const img = ctx.getImageData(0, 0, w, h), d = img.data, step = 255 / (LEVELS - 1);
    for (let i = 0; i < d.length; i += 4) {
      d[i] = Math.round(d[i] / step) * step;
      d[i + 1] = Math.round(d[i + 1] / step) * step;
      d[i + 2] = Math.round(d[i + 2] / step) * step;
    }
    ctx.putImageData(img, 0, 0);
  }

  function stopVideoLoop() { clearTimeout(timer); timer = 0; gifRun++; }
  function videoLoop() {
    stopVideoLoop();
    if (!pixel || !src || !animated || document.hidden || REDUCE.matches) return;
    if (gif) { gifLoop(); return; }
    drawPixel();
    timer = setTimeout(videoLoop, 1000 / VIDEO_FPS);
  }

  // GIF на холсте: браузер рисует только первый кадр анимированной картинки (так по стандарту),
  // поэтому кадры раскладывает ImageDecoder — с той скоростью, что записана в самом GIF.
  let gif = null, gifFrame = 0, gifRun = 0;
  function setGif(blob) {
    if (gif) { try { gif.close(); } catch (_) { /* уже закрыт */ } }
    gif = null; gifFrame = 0;
    if (!blob || typeof ImageDecoder === 'undefined') return;
    try { gif = new ImageDecoder({ data: blob.stream(), type: 'image/gif' }); } catch (_) { gif = null; }
  }
  async function gifLoop() {
    const run = ++gifRun;
    try {
      await gif.tracks.ready;
      const count = gif.tracks.selectedTrack ? gif.tracks.selectedTrack.frameCount : 1;
      const r = await gif.decode({ frameIndex: gifFrame % Math.max(1, count) });
      if (run !== gifRun || !pixel) { r.image.close(); return; }
      drawPixel(r.image);
      const ms = (r.image.duration || 100000) / 1000;
      r.image.close();
      gifFrame = (gifFrame + 1) % Math.max(1, count);
      if (count > 1 && !document.hidden) timer = setTimeout(gifLoop, Math.max(20, ms));
    } catch (_) { drawPixel(); }
  }

  // Копия файла для размытой подложки: для видео — второй ролик с тем же файлом.
  function backCopy() {
    if (src.tagName === 'VIDEO') {
      const v = document.createElement('video');
      Object.assign(v, { muted: true, loop: true, playsInline: true, src: url });
      return v;
    }
    const i = new Image();
    i.src = url;
    i.alt = '';
    return i;
  }

  // Показ: в пиксельном режиме — холст, иначе сам файл (картинка или видео) на весь фон.
  function mount() {
    stopVideoLoop();
    holder.replaceChildren();
    back.replaceChildren();
    if (pixel) {
      canvas = document.createElement('canvas');
      canvas.className = 'wall-px';
      ctx = canvas.getContext('2d', { willReadFrequently: true });
      holder.append(canvas);
      drawPixel();
      if (src.tagName === 'VIDEO') playVideo();
      videoLoop();
    } else {
      canvas = null; ctx = null;
      holder.append(src);
      if (needsBack()) back.append(backCopy());
      if (src.tagName === 'VIDEO') playVideo();
    }
    holder.classList.toggle('drift', kind === 'image' && !pixel && fit === 'cover' && !REDUCE.matches);
    requestAnimationFrame(() => layer && layer.classList.add('shown'));
  }
  const needsBack = () => fit !== 'fill' && (fit === 'contain' || scale < 1);

  function playVideo() {
    if (!src || src.tagName !== 'VIDEO') return;
    if (document.hidden || REDUCE.matches) { src.pause(); return; }
    src.play().catch(() => { /* автозапуск запрещён — покажется первый кадр */ });
    const b = back && back.querySelector('video');
    if (b) { b.currentTime = src.currentTime; b.play().catch(() => {}); }
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
    const wantFit = w.fit === 'contain' || w.fit === 'fill' ? w.fit : 'cover', wantScale = (w.scale || 100) / 100;
    layer.dataset.fit = wantFit;
    layer.style.setProperty('--wall-scale', String(wantScale));
    if (ver !== w.ver || kind !== w.kind) {
      if (loading) return loading.then(() => apply(S, token));
      loading = fetchFile(token, w.kind).then(({ el, next, blob }) => {
        if (src && src.tagName === 'VIDEO') src.pause();
        if (url) URL.revokeObjectURL(url);
        url = next; src = el; kind = w.kind; ver = w.ver; failed = false;
        pixel = wantPixel; fit = wantFit; scale = wantScale;
        animated = w.kind === 'video' || w.ext === '.gif';
        setGif(w.ext === '.gif' ? blob : null);
        if (layer) { layer.classList.remove('shown'); mount(); }
      }).catch(() => { failed = true; ver = w.ver; delete root.dataset.wall; if (layer) layer.remove(); layer = null; })
        .finally(() => { loading = null; });
      return loading;
    }
    const backBefore = needsBack();
    const changed = wantPixel !== pixel || wantFit !== fit;
    pixel = wantPixel; fit = wantFit; scale = wantScale;
    // Ползунок масштаба двигается часто: перестраиваем, только когда меняется, нужна ли подложка.
    if (changed || backBefore !== needsBack()) mount();
    else if (pixel && !animated) drawPixel();
  }

  document.addEventListener('visibilitychange', () => {
    if (!src || !animated) return;
    if (src.tagName !== 'VIDEO') { if (!document.hidden) videoLoop(); else stopVideoLoop(); return; }
    const b = back && back.querySelector('video');
    if (document.hidden) { src.pause(); if (b) b.pause(); stopVideoLoop(); } else { playVideo(); videoLoop(); }
  });

  return { apply, clear, url: () => url, kind: () => kind };
})();
