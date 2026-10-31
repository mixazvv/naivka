// Settings navigation and persistence with a local API fixture; no VPN or downloads.
// Run: node tests/settings.browser.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '..', 'web');
let settings = {
  theme: 'dark', accent: '#B6E35A', lang: 'ru', nav: 'list', mode: 'proxy',
  servers: [], rules: [], ruleLists: [], selected: '', achievements: false, mascot: false,
  mixedPort: 2080, dnsRemote: '1.1.1.1', dnsDirect: 'https://common.dot.dns.yandex.net/dns-query',
  testUrl: 'https://example.com', testInterval: '3m', logLevel: 'info',
  extraOutbounds: '', extraRules: '', override: '', ownServer: true, wall: {},
  share: { enabled: false, auth: true, port: 2090, password: 'fixture-password' },
};
const state = { running: false, starting: false, admin: false, os: 'windows', core: { present: true, version: '1.12', naive: true, cronet: true }, download: {}, dataDir: 'C:\\Naivka' };
const sums = { core: {}, zapret: {}, telegram: {}, xray: {} };
const uiErrors = [];
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (url.pathname.startsWith('/api/')) {
    let raw = ''; for await (const chunk of req) raw += chunk;
    const body = raw ? JSON.parse(raw) : {};
    if (url.pathname === '/api/settings' && req.method === 'POST') settings = body;
    if (url.pathname === '/api/uilog') uiErrors.push(body.msg);
    const result = {
      '/api/settings': settings, '/api/state': state,
      '/api/share': { ips: ['192.168.1.2'], port: 2090, username: 'naivka', password: 'fixture-password' },
      '/api/config': { config: '{"route":{"final":"direct"}}' },
    }[url.pathname] || {};
    res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(result)); return;
  }
  const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
  if (!file.startsWith(root + path.sep) || !fs.existsSync(file)) { res.writeHead(404); res.end(); return; }
  let content = fs.readFileSync(file);
  // Mount the real settings screen, skipping the unrelated connection polling/onboarding.
  if (file.endsWith('/app.js')) content = content.toString().replace(/\nboot\(\);\s*$/, '\n');
  res.setHeader('Content-Type', ({ '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.woff2': 'font/woff2' })[path.extname(file)] || 'application/octet-stream');
  res.end(content);
});
(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const executablePath = process.env.CHROMIUM_PATH || (fs.existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined);
  const browser = await chromium.launch({ headless: true, executablePath, args: ['--no-sandbox'] });
  try {
    const p = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    p.setDefaultTimeout(10000);
    const errors = []; p.on('pageerror', e => errors.push(e.stack));
    await p.addInitScript(() => { window.NAIVKA_HOST = { frame: true, mica: true }; });
    await p.goto(`http://127.0.0.1:${server.address().port}`);
    const mountFixture = ({ settings, state, sums }) => {
      S = settings; ST = state; SELF = { version: 'v2.22.2' }; AD = { updates: sums }; view = 'settings';
      setupChrome();
      window.mountSettings = () => {
        drawerBody.replaceChildren(viewSettings());
        document.querySelector('#drawer').hidden = false; document.querySelector('#drawer').classList.add('open');
        applyTheme();
        patch('coreBox', JSON.stringify(ST.core), buildCoreBox); patch('adminBox', S.mode, buildAdminBox);
        patch('selfBox', SELF.version, selfBox); shareBox();
      };
      mountSettings();
    };
    await p.evaluate(mountFixture, { settings, state, sums });
    await p.waitForTimeout(200);
    const settle = () => p.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
    async function category(title) {
      await p.locator('.settings-category-select').evaluate((e, v) => { e.value = v; e.dispatchEvent(new Event('change')); }, title); await settle();
    }
    async function tab(index) {
      await p.locator('.settings-tab-select').evaluate((e, v) => { e.value = v; e.dispatchEvent(new Event('change')); }, String(index)); await settle();
    }
    async function fits(label) {
      const problems = await p.evaluate(() => {
        const elements = [document.querySelector('#drawerBody'), ...document.querySelectorAll('.settings-view,.settings-workspace,.settings-body,.settings-categories,.settings-tabs')];
        const bad = elements.filter(e => e.clientWidth && e.clientHeight && (e.scrollWidth > e.clientWidth + 2 || e.scrollHeight > e.clientHeight + 2)).map(e => e.className);
        const box = document.querySelector('.settings-body').getBoundingClientRect();
        for (const e of document.querySelectorAll('.settings-item:not([hidden])')) {
          const r = e.getBoundingClientRect();
          if (r.height && (r.bottom > box.bottom + 2 || r.top < box.top - 2)) bad.push('clipped item');
        }
        return bad;
      });
      assert.deepEqual(problems, [], label);
    }
    async function controlGeometry() {
      const result = await p.evaluate(() => {
        const box = h('div', { style: 'position:fixed;left:-10000px;top:0;display:flex;align-items:center;gap:12px' },
          h('button', { class: 'btn' }, 'Обычная'), h('button', { class: 'btn primary' }, 'Основная'),
          h('button', { class: 'btn', disabled: true }, 'Недоступна'),
          h('input', { type: 'text', value: 'Поле' }), h('select', null, h('option', null, 'Список')),
          h('button', { class: 'btn small' }, 'Малая'), h('button', { class: 'btn link' }, 'Ссылка'),
          h('button', { class: 'icon-btn' }, '×'));
        document.body.append(box);
        const heights = [...box.children].map(e => e.getBoundingClientRect().height);
        box.remove(); return heights;
      });
      assert.deepEqual(result, [36, 36, 36, 36, 36, 32, 32, 32], 'shared controls have consistent heights in each theme');
    }
    async function alignedTraffic() {
      await category('Перехват трафика'); await tab(0);
      const rows = await p.locator('.choices .settings-control').evaluateAll(es => es.map(e => {
        const input = e.querySelector('input'), label = e.querySelector('label'), info = e.querySelector('.settings-info');
        const r = e.getBoundingClientRect(), i = input.getBoundingClientRect(), b = info.getBoundingClientRect();
        return { height: r.height, inputX: i.x, infoX: b.x, inputCenter: i.y + i.height / 2, infoCenter: b.y + b.height / 2, center: r.y + r.height / 2,
          shadow: getComputedStyle(label).boxShadow, image: getComputedStyle(label).borderImageSource, border: getComputedStyle(label).borderLeftWidth, skin: document.documentElement.dataset.skin };
      }));
      assert.equal(rows.length, 2);
      assert(Math.abs(rows[0].height - rows[1].height) < .25, 'selected and unselected rows have equal heights');
      assert(Math.abs(rows[0].inputX - rows[1].inputX) < .25, JSON.stringify(rows));
      assert(Math.abs(rows[0].infoX - rows[1].infoX) < .25);
      for (const row of rows) {
        assert(Math.abs(row.inputCenter - row.center) < 1 && Math.abs(row.infoCenter - row.center) < 1, 'radio and help button must be centred in the row');
        assert.equal(row.shadow, 'none', 'old selected-radio inset must not leave a white arc');
        assert.equal(row.image, 'none');
      }
      const radio = p.getByRole('radio', { name: 'Весь трафик (TUN)', exact: true });
      await radio.check(); await settle(); assert(await radio.isChecked());
      await p.getByRole('radio', { name: 'Системный прокси', exact: true }).check(); await settle();
      await fits('aligned traffic rows');
      await category('Запуск'); await tab(0);
      const checkColumns = await p.locator('.settings-body .settings-control input[type=checkbox]').evaluateAll(es => es.filter(e => e.getBoundingClientRect().width).map(e => e.getBoundingClientRect().x));
      assert.equal(checkColumns.length, 4, 'settings without descriptions use the same row layout');
      assert(checkColumns.every(x => Math.abs(x - checkColumns[0]) < .25));
      await category('Перехват трафика'); await tab(0);
    }
    await controlGeometry(); await alignedTraffic();
    assert.match(await p.locator('#drawer').evaluate(e => getComputedStyle(e).backgroundColor), /^rgb\([\d,\s]+\)$/, 'Mica drawer must be opaque without a custom wallpaper');
    async function scan(label) {
      const visited = new Set();
      const expected = await p.locator('.settings-item').evaluateAll(es => es.flatMap((e, i) => {
        const content = e.querySelector('.settings-item-body')?.firstElementChild;
        return content && getComputedStyle(content).display !== 'none' && content.querySelector('input:not([type=file]),select,button,textarea') ? [i] : [];
      }));
      for (const c of await p.locator('.settings-category-select option').allTextContents()) {
        await category(c);
        for (let i = 0, n = await p.locator('.settings-tab-select option').count(); i < n; i++) {
          await tab(i);
          const reached = new Set();
          for (let page = 0; page < 30; page++) {
            await fits(`${label} ${c} tab ${i}`);
            for (const id of await p.locator('.settings-item:not([hidden])').evaluateAll(es => es.map(e => [...e.parentElement.children].indexOf(e)))) { reached.add(id); visited.add(id); }
            const next = p.locator('.settings-pager button').last();
            if (await next.isDisabled()) break;
            await next.click(); await settle(); assert(page < 29, 'pagination must terminate');
          }
          assert(reached.size > 0, c + ' must have reachable settings');
        }
      }
      assert(expected.every(i => visited.has(i)), label + ': every control must be reachable through pages or its editor');
    }
    for (const [w, h] of [[1280, 900], [1000, 640], [760, 640], [600, 480]]) {
      await p.setViewportSize({ width: w, height: h }); await settle();
      await scan(`${w}×${h}`);
      console.log(`${w}×${h}: all categories, tabs and pages fit`);
    }
    await category('Надёжность'); await tab(0);
    const leak = p.getByRole('checkbox', { name: 'Защита от утечек', exact: true });
    const before = await leak.isChecked();
    await p.getByRole('button', { name: 'Подробнее: Защита от утечек', exact: true }).click();
    assert.equal(await leak.isChecked(), before, 'help must not toggle a checkbox');
    await p.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click();
    await leak.check(); await p.evaluate(() => saveNow()); assert.equal(settings.killSwitch, true);
    await p.evaluate(() => mountSettings()); await settle();
    assert.equal(await p.getByRole('checkbox', { name: 'Защита от утечек', exact: true }).isChecked(), true, 'selection survives rebuilding');
    await p.locator('#setSearch').fill('dns'); await settle();
    assert(await p.locator('.settings-item:not([hidden])').count() > 0);
    await fits('search');
    await p.locator('#setSearch').fill('неттакойнастройки'); await settle(); assert(await p.locator('.set-empty').isVisible());
    await p.locator('#setSearch').fill(''); await settle(); assert.equal(await p.locator('.settings-workspace h2').textContent(), 'Надёжность');
    await category('Для опытных'); await tab(1);
    // Reveal an additional field after initial layout; it must stay reachable.
    const directDNS = p.locator('.field').filter({ hasText: 'DNS напрямую' }).locator('select');
    for (let n = 0; !await directDNS.isVisible(); n++) {
      assert(n < 10, 'direct DNS must be reachable');
      const open = p.locator('.settings-item').filter({ has: directDNS }).locator('.settings-open');
      if (await open.isVisible()) await open.click();
      else { assert(!await p.locator('.settings-pager button').last().isDisabled()); await p.locator('.settings-pager button').last().click(); }
      await settle();
    }
    await directDNS.selectOption('custom'); await settle(); await fits('custom DNS field');
    const dns = p.getByPlaceholder('https://адрес/dns-query или tls://адрес');
    if (!await dns.isVisible()) {
      await p.locator('.settings-open:visible').click();
    }
    await dns.fill('https://dns.example/dns-query'); await dns.press('Tab'); await p.evaluate(() => saveNow());
    assert.equal(settings.dnsDirect, 'https://dns.example/dns-query');
    if (await p.getByRole('dialog').count()) await p.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click();
    await category('Раздача на другие устройства');
    await p.locator('#shareBox .switch').click();
    await p.evaluate(async () => { await saveNow(); await shareBox(); }); await settle();
    await fits('enabled sharing');
    await p.locator('.settings-item').filter({ has: p.locator('#shareBox') }).locator('.settings-open').click();
    await p.locator('dialog summary').filter({ hasText: 'Настройки раздачи' }).click();
    const sharePort = p.getByRole('dialog').getByRole('spinbutton', { name: 'Порт', exact: true });
    await sharePort.fill('2091'); await sharePort.press('Tab'); await p.evaluate(() => saveNow());
    assert.equal(settings.share.port, 2091, 'sharing editor still saves after moving into a dialog');
    await p.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click(); await settle();
    await p.setViewportSize({ width: 1280, height: 900 }); await category('Внешний вид'); await tab(0);
    await category('Для опытных'); await tab(1);
    const remoteDNS = p.locator('.field').filter({ hasText: 'DNS через сервер (DoH)' }).locator('input');
    await remoteDNS.focus(); await p.keyboard.type('abc', { delay: 50 }); await settle();
    assert(await remoteDNS.evaluate(e => e === document.activeElement), 'layout must preserve keyboard focus while typing');
    await dns.focus(); await p.keyboard.type('xyz', { delay: 50 }); await settle();
    assert(await dns.evaluate(e => e === document.activeElement), 'reflow must also preserve focus in a later field');
    await category('Внешний вид'); await tab(0);
    await p.locator('.settings-action').getByRole('button', { name: 'Открыть', exact: true }).click();
    assert(await p.locator('dialog #customEditor').isVisible());
    await p.getByRole('textbox', { name: 'Фон (HEX)', exact: true }).fill('#17212B');
    await p.getByRole('textbox', { name: 'Фон (HEX)', exact: true }).press('Tab'); await p.evaluate(() => saveNow());
    assert.equal(settings.custom.bg, '#17212B');
    await p.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click();
    for (const theme of ['gold', 'blocks', 'custom']) {
      await p.evaluate(theme => { S.theme = theme; applyTheme(); mountSettings(); }, theme); await p.waitForTimeout(400);
      await controlGeometry(); await alignedTraffic();
      await scan(theme);
      await p.setViewportSize({ width: 600, height: 480 }); await settle(); await scan(theme + ' small');
      await p.setViewportSize({ width: 1280, height: 900 }); await settle();
    }
    // Dynamic oversized content opens separately instead of being clipped or lost.
    await category('Ядро sing-box');
    await p.evaluate(() => { ST.download.error = 'Очень длинная ошибка загрузки. '.repeat(300); patch('coreBox', 'error', buildCoreBox); }); await settle();
    assert(await p.locator('.settings-open:visible').count()); await fits('download error');
    await p.locator('.settings-open:visible').click(); assert(await p.locator('dialog #coreBox').isVisible());
    await p.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click(); await settle();
    await category('Внешний вид'); await tab(0);
    await p.locator('.theme-tile[data-theme="dark"]').click(); await settle();
    if (process.env.SETTINGS_SCREENSHOTS) {
      fs.mkdirSync(process.env.SETTINGS_SCREENSHOTS, { recursive: true });
      await p.screenshot({ path: path.join(process.env.SETTINGS_SCREENSHOTS, 'settings-desktop.png') });
      await alignedTraffic(); await p.waitForTimeout(180); await p.screenshot({ path: path.join(process.env.SETTINGS_SCREENSHOTS, 'settings-traffic.png') });
    }
    await p.evaluate(() => { goNow('overview'); });
    assert(await p.evaluate(() => settingsDispose === null), 'observers are disconnected on exit');
    await p.evaluate(() => localStorage.setItem('naivka-lang', 'en')); await p.reload();
    await p.evaluate(mountFixture, { settings: { ...settings, lang: 'en', theme: 'dark' }, state, sums }); await settle();
    await p.locator('#setSearch').fill('leaks'); await settle();
    assert.equal(await p.locator('.settings-workspace h2').textContent(), 'Search results');
    assert(await p.locator('.settings-item:not([hidden])').count(), 'English search must find translated settings');
    await fits('English search');
    await p.setViewportSize({ width: 600, height: 480 }); await settle(); await fits('English small window');
    // Shared button/field geometry also applies outside the settings screen.
    await p.setViewportSize({ width: 1280, height: 900 });
    for (const screen of ['servers', 'rules']) {
      await p.evaluate(screen => {
        if (settingsDispose) settingsDispose();
        S.routing = 'ru-direct'; view = screen;
        drawerBody.replaceChildren(screen === 'servers' ? viewServers() : viewRules());
      }, screen);
      const heights = await p.locator('#drawerBody .btn:not(.small):not(.link)').evaluateAll(es => es.map(e => e.getBoundingClientRect().height));
      assert(heights.length && heights.every(h => Math.abs(h - 36) < .25), screen + ': action buttons share the same height');
    }
    await p.evaluate(() => editServer(null));
    const formHeights = await p.locator('dialog .field :is(input,select)').evaluateAll(es => es.filter(e => e.getBoundingClientRect().height).map(e => e.getBoundingClientRect().height));
    assert(formHeights.length && formHeights.every(h => Math.abs(h - 36) < .25), 'server dialog fields align');
    await p.evaluate(() => document.querySelector('dialog').close());
    const scaled = await browser.newPage({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2 });
    try {
      scaled.on('pageerror', e => errors.push(e.stack));
      await scaled.addInitScript(() => { window.NAIVKA_HOST = { frame: true, mica: true }; });
      await scaled.goto(`http://127.0.0.1:${server.address().port}`);
      await scaled.evaluate(mountFixture, { settings: { ...settings, lang: 'ru', theme: 'dark', accent: '#FFFFFF' }, state, sums });
      await scaled.locator('.settings-category-select').evaluate(e => { e.value = 'Перехват трафика'; e.dispatchEvent(new Event('change')); });
      await scaled.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
      await scaled.evaluate(async () => { await document.fonts.ready; });
      const points = await scaled.locator('.choices .settings-control').evaluateAll(es => es.map(e => {
        const r = e.getBoundingClientRect(), radio = e.querySelector('input').getBoundingClientRect(), info = e.querySelector('.settings-info').getBoundingClientRect();
        return [r.height, radio.x, info.x];
      }));
      assert.deepEqual(points[0], points[1], '200% DPI preserves matching control dimensions and columns');
      if (process.env.SETTINGS_SCREENSHOTS) await scaled.screenshot({ path: path.join(process.env.SETTINGS_SCREENSHOTS, 'settings-traffic-200.png') });
    } finally { await scaled.close(); }
    assert.deepEqual(errors, []); assert.deepEqual(uiErrors, []);
    console.log('Search (RU/EN), help, saving, themes, aligned controls, server/rule forms and 200% DPI: OK');
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; }).finally(() => server.close());
