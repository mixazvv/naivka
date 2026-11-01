// Mascot regression checks; no app backend, VPN or credentials.
// node tests/pet.browser.cjs; optional PET_SCREENSHOTS=/directory.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '..');
const petSource = fs.readFileSync(path.join(root, 'web/pet.js'), 'utf8');
const marker = '    mount, unmount, update, talk, startle, press: pressBtn,';
assert(petSource.includes(marker));
// Internal probes exist only in the test fixture, not in the shipped API.
const fixtureSource = petSource.replace(marker,
  `    inspect: { show, play, frameRect, setFlip, sleepSeq, stroll,
      stop: () => { halt(); stopFall(); stopAnim(); },
      timers: () => timers.size, isStrolling: () => strolling },\n${marker}`);

(async () => {
  const server = http.createServer((req, res) => {
    if (req.url === '/') { res.end('<!doctype html><div id="home"></div>'); return; }
    const name = path.basename(req.url);
    const file = path.join(root, 'web', name);
    if (!fs.existsSync(file)) { res.writeHead(404).end(); return; }
    res.setHeader('Content-Type', name.endsWith('.webp') ? 'image/webp' : name.endsWith('.css') ? 'text/css' : 'text/javascript');
    res.end(fs.readFileSync(file));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox'] });
  const errors = [];
  try {
    const page = await browser.newPage({ viewport: { width: 1000, height: 720 } });
    page.on('pageerror', e => errors.push(e.stack));
    await page.clock.install({ time: new Date('2026-11-01T12:00:00Z') });
    await page.goto(`http://127.0.0.1:${server.address().port}/`);
    await page.addStyleTag({ path: path.join(root, 'web/app.css') });
    await page.addStyleTag({ content: '#home{position:absolute;left:30px;bottom:20px;width:250px;height:110px}.pet{margin:0}' });
    await page.addScriptTag({ path: path.join(root, 'web/pet-atlas.js') });
    await page.addScriptTag({ content: fixtureSource });
    await page.evaluate(() => { Math.random = () => .5; Pet.mount(document.querySelector('#home')); Pet.update('on', 0); });

    const palette = await page.evaluate(() => {
      const image = document.querySelector('.pet-sprite');
      Pet.inspect.show('idle_1'); const colour = [...document.querySelectorAll('feFuncR,feFuncG,feFuncB')].map(e => Number(e.getAttribute('slope')));
      return { alpha: document.querySelector('feFuncA').getAttribute('tableValues'), colour, cssFilter: getComputedStyle(image).filter };
    });
    assert.equal(palette.alpha, '0 0 1');
    assert(palette.colour.every(c => c >= .88 && c <= 1.12));
    assert.notEqual(palette.cssFilter, 'none', 'the alpha/tone filter must actually apply');

    // Check actual rendered pixels, rather than merely the filter attributes.
    const matte = await page.evaluate(async () => {
      const atlas = new Image(); atlas.src = 'cat-atlas.webp'; await atlas.decode();
      const src = document.createElement('canvas'); src.width = atlas.width; src.height = atlas.height;
      const ctx = src.getContext('2d'); ctx.drawImage(atlas, 0, 0);
      const pixels = ctx.getImageData(0, 0, src.width, src.height).data;
      let sample;
      for (const [name, [x, y, w, h, ax]] of Object.entries(PET_ATLAS.frames)) {
        for (let v = 1; v < h - 1 && !sample; v++) for (let u = 1; u < w - 1 && !sample; u++) {
          const at = ((y + v) * src.width + x + u) * 4;
          let solidMatte = pixels[at + 3] === 150 && Math.max(...pixels.slice(at, at + 3)) < 195;
          for (let dy = -1; dy <= 1; dy++) for (let dx = -1; dx <= 1; dx++)
            solidMatte &&= pixels[((y + v + dy) * src.width + x + u + dx) * 4 + 3] !== 255;
          if (solidMatte && !PET_ATLAS.fallbacks[name]) sample = { name, u, v, w, h, ax };
        }
        if (sample) break;
      }
      if (!sample) throw new Error('No source matte patch found');
      Pet.inspect.show(sample.name);
      const clip = document.querySelector('#petClip rect');
      const k = Number(clip.getAttribute('width')) / sample.w;
      const x = (Number(clip.getAttribute('x')) + (sample.u + .5) * k) * 4;
      const y = (Number(clip.getAttribute('y')) + (sample.v + .5) * k) * 4;
      const svg = document.querySelector('.pet svg').cloneNode(true);
      svg.querySelector('.pet-sprite').setAttribute('href', src.toDataURL());
      svg.querySelector('.pet-shadow').remove(); svg.querySelector('.pet-fx').remove();
      svg.setAttribute('width', '256'); svg.setAttribute('height', '336');
      const blob = URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(svg)], { type: 'image/svg+xml' }));
      const clean = new Image(); clean.src = blob; await clean.decode();
      const out = document.createElement('canvas'); out.width = 256; out.height = 336;
      const render = out.getContext('2d'); render.drawImage(clean, 0, 0); URL.revokeObjectURL(blob);
      return { alpha: render.getImageData(Math.floor(x), Math.floor(y), 1, 1).data[3], visible: render.getImageData(0, 0, 256, 336).data.filter((v, i) => i % 4 === 3 && v > 200).length };
    });
    assert.equal(matte.alpha, 0, 'source background fragments must be transparent in the rendered kitten');
    assert(matte.visible > 1000, 'matte cleanup must preserve the kitten itself');

    const grounded = await page.evaluate(() => {
      const names = ['sit_1', 'sit_2', 'sit_3', 'sit_4', 'stride_1', 'stride_4', 'run_5', 'sleepb_1', 'touch_3'];
      return names.map(name => {
        Pet.inspect.show(name);
        const clip = document.querySelector('#petClip rect');
        return Number(clip.getAttribute('y')) + Number(clip.getAttribute('height'));
      });
    });
    assert(grounded.every(y => Math.abs(y - 80) < .0001), 'scale correction must not move the floor');
    const replacements = await page.evaluate(() => Object.entries(PET_ATLAS.fallbacks).map(([name, target]) => {
      Pet.inspect.show(name); return [target, document.querySelector('.pet').dataset.frame];
    }));
    for (const [expected, actual] of replacements) assert.equal(actual, expected);
    const flip = await page.evaluate(() => {
      Pet.inspect.show('stride_1'); const a = Pet.inspect.frameRect();
      Pet.inspect.setFlip(true); const b = Pet.inspect.frameRect(); Pet.inspect.setFlip(false);
      return { a, b, root: document.querySelector('.pet').getBoundingClientRect().toJSON() };
    });
    assert(Math.abs(flip.a.width - flip.b.width) < .001);
    assert(Math.abs(flip.b.left - (flip.root.x + flip.root.width - (flip.a.left - flip.root.x) - flip.a.width)) < .001);
    console.log('Frame calibration, floor anchors, cropped-pose replacement and mirroring: OK');

    // Tick-triggered blinking stays in the same seated drawing family.
    await page.evaluate(() => { Pet.inspect.stop(); Pet.inspect.show('sit_1'); window.seenFrames = []; window.obs = new MutationObserver(() => seenFrames.push(document.querySelector('.pet')?.dataset.frame)); obs.observe(document.querySelector('.pet'), { attributes: true, attributeFilter: ['data-frame'] }); });
    await page.clock.runFor(5000);
    const blink = await page.evaluate(() => { obs.disconnect(); return seenFrames; });
    assert(blink.length >= 3 && blink.every(n => /^sit_/.test(n)), 'blinking must not replace the kitten with the unrelated blink family');
    await page.evaluate(() => { Pet.update('off', 0); });
    await page.clock.runFor(3500);
    const asleep = await page.evaluate(() => ({ frame: document.querySelector('.pet').dataset.frame, animation: getComputedStyle(document.querySelector('.pet-jump')).animationName, filter: getComputedStyle(document.querySelector('.pet-sprite')).filter }));
    assert.equal(asleep.frame, 'sleepb_1');
    assert.equal(asleep.animation, 'petBreathe');
    assert(!asleep.filter.includes('brightness'), 'sleep must preserve the fur palette');
    console.log('Seated blinking and breathing without pose/palette jumps: OK');

    await page.evaluate(() => { Pet.update('on', 0); });
    await page.clock.runFor(4000);
    const looking = await page.evaluate(() => {
      Pet.inspect.stop();
      const r = document.querySelector('.pet').getBoundingClientRect();
      const frames = [];
      for (let i = 0; i < 12; i++) {
        dispatchEvent(new PointerEvent('pointermove', { clientX: r.x + r.width / 2 + i % 2, clientY: r.y - 75 }));
        frames.push(document.querySelector('.pet').dataset.frame);
      }
      return frames;
    });
    assert(looking.every(n => n === looking[0]), 'small cursor movements must not randomly change the reaching pose');

    // A live accessibility preference change cancels JS motion as well as CSS.
    await page.evaluate(() => { Pet.update('on', 0); });
    await page.clock.runFor(4000);
    await page.evaluate(() => { Pet.inspect.stop(); Pet.inspect.stroll(140, { fast: true }); });
    await page.clock.runFor(1400);
    assert(await page.evaluate(() => Pet.inspect.isStrolling()));
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.clock.runFor(50);
    await page.waitForFunction(() => !Pet.inspect.isStrolling());
    assert.equal(await page.evaluate(() => Pet.inspect.isStrolling()), false);
    const still = await page.locator('.pet').getAttribute('data-frame');
    await page.clock.runFor(2000);
    assert.equal(await page.locator('.pet').getAttribute('data-frame'), still);
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await page.clock.runFor(50);

    // Turning the mascot off clears every pending gesture; enabling starts clean.
    await page.evaluate(() => { Pet.cheer('old'); Pet.unmount(); });
    assert.equal(await page.evaluate(() => Pet.inspect.timers()), 0);
    await page.evaluate(() => { Pet.mount(document.querySelector('#home')); Pet.update('on', 0); });
    assert.equal(await page.locator('.pet').getAttribute('data-frame'), 'sit_1', 'remount must paint even if the prior frame had the same name');
    await page.clock.runFor(2500);
    assert.equal(await page.locator('.pet-bubble').isVisible(), false);
    assert.equal(await page.locator('.pet.joy').count(), 0);
    console.log('Live reduced motion, timer cleanup and clean remount: OK');

    const pickUp = await page.evaluate(() => { Pet.inspect.stop(); return Pet.inspect.frameRect(); });
    const hand = { x: pickUp.left + pickUp.width / 2, y: pickUp.top + pickUp.height * .25 };
    await page.mouse.move(hand.x, hand.y); await page.mouse.down();
    await page.mouse.move(hand.x + 250, hand.y - 180, { steps: 8 });
    assert.equal(await page.locator('.pet.held').count(), 1);
    assert.equal(await page.locator('#petClip rect').getAttribute('y'), '4', 'held poses must share the hand anchor');
    await page.mouse.up();
    await page.clock.runFor(4000);
    assert.equal(await page.locator('.pet.held').count(), 0);
    const landed = await page.evaluate(() => { const r = document.querySelector('.pet').getBoundingClientRect(); return r.y + r.height * 80 / 84; });
    assert(Math.abs(landed - 719) < 2, 'release must land with the feet on the window floor');
    console.log('Mouse pickup, hand anchor and release onto the floor: OK');

    if (process.env.PET_SCREENSHOTS) {
      fs.mkdirSync(process.env.PET_SCREENSHOTS, { recursive: true });
      await page.evaluate(() => {
        Pet.inspect.stop();
        const names = ['sit_1', 'sit_3', 'stride_1', 'stride_4', 'run_5', 'gallop_2', 'sleepb_1', 'scruff_1', 'jumpup_4', 'jumpdown_1', 'lookdown_1', 'climb_1'];
        const gallery = document.createElement('main'); gallery.id = 'gallery';
        names.forEach((name, i) => {
          Pet.inspect.show(name);
          const box = document.createElement('section');
          const image = document.querySelector('.pet svg').outerHTML.replaceAll('petClip', 'petClip' + i).replaceAll('petClean', 'petClean' + i);
          box.innerHTML = image + `<b>${name}</b>`;
          gallery.append(box);
        });
        document.body.append(gallery);
      });
      await page.addStyleTag({ content: '#home{display:none}#gallery{display:grid;grid-template-columns:repeat(6,150px);gap:12px;padding:24px;background:#24272d;color:#ddd}#gallery section{display:grid;justify-items:center;border:1px solid #657080;background:linear-gradient(transparent calc(100% - 30px),#657080 calc(100% - 29px),transparent calc(100% - 28px));padding:12px 0 8px}#gallery svg{width:112px;height:147px;overflow:visible}#gallery b{font:12px monospace;padding-top:10px}' });
      await page.clock.runFor(100);
      await page.locator('#gallery').screenshot({ path: path.join(process.env.PET_SCREENSHOTS, 'pet-frames-dark.png') });
      await page.addStyleTag({ content: '#gallery{background:#f1f0eb;color:#303136}#gallery section{border-color:#bcc2c6}' });
      await page.locator('#gallery').screenshot({ path: path.join(process.env.PET_SCREENSHOTS, 'pet-frames-light.png') });
    }
    assert.deepEqual(errors, []);
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(e => { console.error(e); process.exitCode = 1; });
