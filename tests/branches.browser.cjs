// Browser regression checks for the tree, without a VPN connection or credentials.
// Requires Playwright and Chromium. Run: node tests/branches.browser.cjs
// Optional: CHROMIUM_PATH=/path/to/chromium TREE_SCREENSHOTS=/directory
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '..');
(async () => {
  const executablePath = process.env.CHROMIUM_PATH || (fs.existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined);
  const browser = await chromium.launch({ headless: true, executablePath, args: ['--no-sandbox'] });
  try {
    const page = await browser.newPage({ viewport: { width: 1180, height: 760 } });
    const errors = []; page.on('pageerror', e => errors.push(e.stack));
    await page.setContent('<div id="tree" class="lanes-box"><button class="power" aria-label="Disconnect"></button></div>');
    for (const file of ['web/app.css', 'web/flow.css']) await page.addStyleTag({ path: path.join(root, file) });
    await page.addStyleTag({ content: '#tree{position:relative;width:1000px;height:540px;margin:50px auto}.power{background:var(--raised)}' });
    await page.evaluate(() => {
      window.REDUCE = matchMedia('(prefers-reduced-motion: reduce)'); window.ICONS = { server: 'M5 5h14v14H5z', globe: 'M12 3v18', shield: 'M5 5h14v14H5z', tg: 'M3 12h18' };
      window.particleFrames = new Map(); window.drawFrame = 0;
      const raf = requestAnimationFrame; window.requestAnimationFrame = cb => raf(t => { window.drawFrame++; cb(t); });
      window.branchPolygons = [];
      const proto = CanvasRenderingContext2D.prototype;
      const begin = proto.beginPath, move = proto.moveTo, line = proto.lineTo, fill = proto.fill;
      proto.beginPath = function (...args) { this.testPoints = []; return begin.apply(this, args); };
      proto.moveTo = function (x, y) { this.testPoints?.push([x,y]); return move.call(this,x,y); };
      proto.lineTo = function (x, y) { this.testPoints?.push([x,y]); return line.call(this,x,y); };
      proto.fill = function (...args) {
        if (this.testPoints?.length > 100) { window.branchPolygons.push({ points:this.testPoints.slice(), color:this.fillStyle }); if(window.branchPolygons.length>8)window.branchPolygons.shift(); }
        return fill.apply(this,args);
      };
      const ellipse = CanvasRenderingContext2D.prototype.ellipse;
      CanvasRenderingContext2D.prototype.ellipse = function (...args) {
        if (args[2] === 4) { const a = window.particleFrames.get(window.drawFrame) || []; a.push(this.getTransform().e); window.particleFrames.set(window.drawFrame, a); }
        return ellipse.apply(this, args);
      };
      Math.random = () => .5; // distinguish the two streams from consecutive frames
    });
    await page.addScriptTag({ path: path.join(root, 'web/branches.js') });
    await page.evaluate(() => {
      window.dropCalls = 0; window.acceptDrop = true;
      window.treeData = {
        phase: 'off', offline: false, controlLabel: 'Подключить', compSub: 'Компьютер', endSub: 'Интернет',
        lanes: ['proxy', 'direct', 'zapret', 'tg'].map((key, i) => ({ key, active: true, speed: key === 'proxy' ? 900000 : 0,
          down: key === 'proxy' ? 700000 : 0, up: key === 'proxy' ? 200000 : 0,
          color: ['var(--tunnel)', 'var(--direct)', 'var(--warn)', 'var(--tg)'][i],
          node: key === 'direct' ? null : { icon: key === 'proxy' ? 'server' : key === 'tg' ? 'tg' : 'shield', title: key === 'proxy' ? 'Netherlands' : key, sub: key === 'proxy' ? '125 мс' : '' } })),
        top: ['Twitch', 'ChatGPT', 'Spotify', 'proc:chrome.exe'].map((key, i) => ({ key, label: key, kind: i === 3 ? 'process' : 'site', exe: i === 3 ? 'chrome.exe' : undefined, route: i === 2 ? 'direct' : 'proxy', speed: 50000 })),
        onMore: () => { window.moreOpened = true; }, why: () => 'Тестовый маршрут', onNode: () => {}, onPick: () => { window.picked = true; },
        onDrop: async (key, branch, item) => { window.lastDropKind = item.kind; window.lastDropExe = item.exe; window.dropCalls++; await new Promise(r => setTimeout(r, 200)); return window.acceptDrop; }
      };
      window.renderTree = () => Branches.update(document.getElementById('tree'), treeData);
      renderTree(); treeData.phase = 'on'; renderTree(); window.treeTimer = setInterval(renderTree, 1000);
    });
    await page.waitForTimeout(1800);
    const leaf = page.locator('.br-leaf[data-host="Twitch"]'); await leaf.waitFor({ state: 'visible' });
    const frames = await page.evaluate(() => [...particleFrames.values()].filter(a => a.length > 2).slice(-2));
    assert.equal(frames.length, 2); assert(frames[1][0] < frames[0][0], 'download must move toward computer');
    assert(frames[1].at(-1) > frames[0].at(-1), 'upload must move toward internet');
    assert.equal(await page.locator('.br-node[data-key="net"]').count(), 1, 'routes must share one Internet node');
    assert.equal(await page.locator('.route-node').count(), 4);
    const joins = await page.evaluate(() => {
      const box = document.querySelector('#tree').getBoundingClientRect();
      const end = document.querySelector('.br-node[data-key="net"] .br-disc').getBoundingClientRect();
      const net = [end.x - box.x + end.width/2, end.y - box.y + end.height/2];
      return treeData.lanes.map(l => {
        const node = document.querySelector(`.br-node[data-key="${l.key}"] .br-disc`).getBoundingClientRect();
        const center = [node.x-box.x+node.width/2,node.y-box.y+node.height/2];
        const ctx = document.querySelector('canvas').getContext('2d');
        ctx.save(); ctx.fillStyle=getComputedStyle(document.querySelector('#tree')).getPropertyValue(l.color.slice(4,-1)).trim(); const color=ctx.fillStyle; ctx.restore();
        const polygon = branchPolygons.filter(p=>p.color===color).at(-1);
        return { key:l.key, node:Math.min(...polygon.points.map(p=>Math.hypot(p[0]-center[0],p[1]-center[1]))), end:Math.min(...polygon.points.map(p=>Math.hypot(p[0]-net[0],p[1]-net[1]))) };
      });
    });
    for(const j of joins) { assert(j.node<6,`${j.key} misses its circle`);assert(j.end<6,`${j.key} misses Internet`); }
    const ordered = await page.evaluate(() => {
      const names=['tg','proxy','direct','zapret'];
      const ctx=document.querySelector('canvas').getContext('2d'),styles=getComputedStyle(document.querySelector('#tree'));
      const curves=names.map(key=>{
        const lane=treeData.lanes.find(l=>l.key===key);ctx.save();ctx.fillStyle=styles.getPropertyValue(lane.color.slice(4,-1)).trim();const color=ctx.fillStyle;ctx.restore();
        return branchPolygons.filter(p=>p.color===color).at(-1).points;
      });
      const yAt=(points,x)=>{const ys=[];for(let i=1;i<points.length;i++){const a=points[i-1],b=points[i];if((a[0]<=x&&b[0]>x)||(b[0]<=x&&a[0]>x))ys.push(a[1]+(b[1]-a[1])*(x-a[0])/(b[0]-a[0]));}return (Math.min(...ys)+Math.max(...ys))/2;};
      return [200,300,400,500,600,700,800].every(x=>{const y=curves.map(p=>yAt(p,x));return y.every((v,i)=>Number.isFinite(v)&&(!i||v>y[i-1]));});
    });
    assert(ordered,'Telegram, proxy, direct and zapret must not cross');
    const sizes=await page.locator('.br-leaf[data-hidden="false"]').evaluateAll(els=>els.map(el=>({w:el.offsetWidth,h:el.offsetHeight})));
    assert(sizes.every(s=>s.w<=113&&s.h<=29),'markers must stay compact');
    console.log('Directed streams, circle joins, ordered branches and compact markers: OK');
    const drag = async (cancel = false, target = leaf) => {
      const a = await target.boundingBox(), box = await page.locator('#tree').boundingBox();
      await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2); await page.mouse.down();
      await page.mouse.move(box.x + box.width * .62, box.y + box.height * .51, { steps: 12 });
      if (cancel) await target.dispatchEvent('pointercancel', { pointerId: 1 });
      await page.mouse.up(); await page.waitForTimeout(900);
    };
    await page.evaluate(() => { window.acceptDrop = false; }); await drag();
    assert.equal(await leaf.getAttribute('data-branch'), 'proxy', 'rejected save must roll back');
    const n = await page.evaluate(() => dropCalls); assert.equal(n, 1);
    await drag(true); assert.equal(await page.evaluate(() => dropCalls), n, 'cancel must not save');
    await page.evaluate(() => { window.acceptDrop = true; }); await drag();
    assert.equal(await leaf.getAttribute('data-branch'), 'direct');
    const app = page.locator('.br-leaf[data-host="proc:chrome.exe"]');
    await app.waitFor({state:'visible'}); await drag(false, app);
    assert.equal(await page.evaluate(() => lastDropKind), 'process');
    assert.equal(await page.evaluate(() => lastDropExe), 'chrome.exe');
    assert.equal(await app.getAttribute('data-branch'), 'direct');
    console.log('Site/app drag, rejected save rollback, pointer cancellation: OK');
    if (process.env.TREE_SCREENSHOTS) { fs.mkdirSync(process.env.TREE_SCREENSHOTS, { recursive: true }); await page.screenshot({ path: path.join(process.env.TREE_SCREENSHOTS, 'tree-active.png') }); }
    await page.evaluate(() => { treeData.phase = 'revive'; renderTree(); }); await page.waitForTimeout(400);
    assert.equal(await leaf.getAttribute('data-dead'), 'true');
    assert.equal(await leaf.locator('.br-leaf-body').evaluate(el => getComputedStyle(el).animationName), 'none', 'a disconnected leaf must stop swaying');
    await page.evaluate(() => { treeData.phase = 'on'; renderTree(); }); await page.waitForTimeout(400);
    assert.equal(await leaf.getAttribute('data-dead'), 'false');
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.evaluate(() => particleFrames.clear()); await page.waitForTimeout(250);
    assert.equal(await page.evaluate(() => particleFrames.size), 0, 'reduced motion must stop particles');
    assert.equal(await leaf.locator('.br-leaf-body').evaluate(el => getComputedStyle(el).animationName), 'none');
    console.log('Recovery and reduced motion: OK');
    await page.setViewportSize({ width: 600, height: 700 });
    await page.addStyleTag({ content: '#tree{width:540px;height:420px;margin:25px auto}' });
    await page.waitForTimeout(300); await page.evaluate(() => renderTree());
    const leafBoxes = await page.locator('.br-leaf[data-hidden="false"]').evaluateAll(els => els.map(el => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; }));
    for (const a of leafBoxes) { assert(a.x >= 0 && a.x + a.w <= 600); for (const b of leafBoxes) if (a !== b) assert(!(a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h), 'leaves overlap'); }
    if (await page.locator('.br-leaf[data-hidden="true"]').count()) {
      const more = page.locator('.br-more').first(); await more.click();
      assert(await page.evaluate(() => moreOpened), 'overflow must open connections');
    }
    await page.addStyleTag({content:'#tree{height:320px}'});await page.waitForTimeout(250);await page.evaluate(()=>renderTree());
    const nodes=await page.locator('.route-node').evaluateAll(els=>els.map(el=>{const r=el.getBoundingClientRect();return{x:r.x,y:r.y,w:r.width,h:r.height}}));
    for(const a of nodes)for(const b of nodes)if(a!==b)assert(!(a.x<b.x+b.w&&b.x<a.x+a.w&&a.y<b.y+b.h&&b.y<a.y+a.h),'route labels overlap in a short diagram');
    await page.evaluate(() => { Branches.unmount(); renderTree(); }); await page.waitForTimeout(200);
    assert.equal(await page.locator('.br-canvas').count(), 1); assert.equal(await page.locator('.br-node[data-key="comp"]').count(), 1);
    console.log('Narrow layout and remount: OK'); assert.deepEqual(errors, []);
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
