const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '../web');
let operation = { operationId: '', phase: 'IDLE', checks: [] }, polls = 0;
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (url.pathname.startsWith('/api/')) {
    let raw = ''; for await (const c of req) raw += c;
    if (url.pathname.endsWith('/start')) operation = { operationId: 'fixture', phase: 'PROBING_CORE', checks: [{ name: 'DNS', status: 'UNKNOWN' }] };
    if (url.pathname.endsWith('/cancel')) { assert.equal(JSON.parse(raw).operationId, 'fixture'); operation.phase = 'CANCELLED'; }
    if (url.pathname.endsWith('/status')) polls++;
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(operation)); return;
  }
  const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
  if (!file.startsWith(root + path.sep) || !fs.existsSync(file)) {res.writeHead(404);res.end();return;}
  let content = fs.readFileSync(file);
  if (file.endsWith('/app.js')) content = content.toString().replace(/\nboot\(\);\s*$/, '\n');
  res.setHeader('Content-Type', ({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)] || 'application/octet-stream');res.end(content);
});
(async () => {
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const browser = await chromium.launch({headless:true, executablePath:process.env.CHROMIUM_PATH || (fs.existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined), args:['--no-sandbox']});
  try {
    const page = await browser.newPage(); const errors=[];page.on('pageerror',e=>errors.push(e.message));
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.evaluate(() => main.replaceChildren(viewRepair()));
    await page.getByRole('button',{name:'Проверить соединение',exact:true}).click();
    await page.getByText('DNS: не определено',{exact:true}).waitFor();
    assert(await page.getByRole('button',{name:'Проверить соединение',exact:true}).isDisabled());
    await page.getByRole('button',{name:'Отмена',exact:true}).click();
    await page.getByText('Проверка отменена.',{exact:true}).waitFor();
    assert(await page.getByRole('button',{name:'Проверить соединение',exact:true}).isEnabled());
    await page.getByRole('button',{name:'Проверить соединение',exact:true}).click();
    await page.waitForTimeout(100);
    await page.evaluate(() => main.replaceChildren());
    await page.waitForTimeout(650); const before=polls;
    await page.waitForTimeout(650);assert.equal(polls,before,'polling survives detached panel');
    assert.deepEqual(errors,[]);console.log('Repair start/cancel, UNKNOWN labels and polling teardown: OK');
  } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
