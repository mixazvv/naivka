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
    await page.evaluate(() => {
      window.received=[];
      for (const n of ['connection.connected','connection.disconnected','connection.failed','server.changed']) NaivkaExperience.on(n,()=>received.push(n));
      NaivkaExperience.observe({running:false}, '');
      NaivkaExperience.observe({running:true}, 'a');
      NaivkaExperience.observe({running:true}, 'a');
      NaivkaExperience.observe({running:true}, 'b');
      NaivkaExperience.observe({running:false,error:'private error'}, '');
    });
    assert.deepEqual(await page.evaluate(()=>received),['connection.connected','server.changed','connection.disconnected','connection.failed']);
    await page.evaluate(()=>NaivkaExperience.configure({effects:'off',sound:'off',atmosphere:'night'}));
    assert(await page.evaluate(()=>NaivkaExperience.motion.matches));
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.effects),'off');
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.atmosphere),'night');
    await page.evaluate(()=>NaivkaExperience.configure({effects:'full',sound:'off',atmosphere:'auto'}));
    await page.emulateMedia({reducedMotion:'reduce'});
    assert(await page.evaluate(()=>NaivkaExperience.motion.matches));
    await page.emulateMedia({reducedMotion:'no-preference'});
    assert.equal(await page.evaluate(()=>NaivkaExperience.scale),1);
    await page.evaluate(()=>NaivkaExperience.atmosphere(new Date(2026,0,1,3)));
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.atmosphere),'night');
    assert.equal(await page.evaluate(()=>document.documentElement.dataset.season),'winter');
    await page.evaluate(()=>{ S={experience:{effects:'low',sound:'quiet',volume:10,atmosphere:'morning'}};main.replaceChildren(experienceSettings()); });
    assert.equal(await page.getByLabel('Интенсивность эффектов').inputValue(),'low');
    await page.getByLabel('Интенсивность эффектов').selectOption('off');
    assert.equal(await page.evaluate(()=>S.experience.effects),'off');
    await page.evaluate(() => {
      S.experience.diagram='orbit';
      const box=document.createElement('div');box.id='routeFixture';box.style.cssText='width:600px;height:360px;position:relative';main.replaceChildren(box);
      window.actions=[];
      window.model={phase:'on',lanes:[{key:'proxy',active:true,color:'#80ff80',speed:1000,node:{title:'Сервер'}},{key:'direct',active:true,color:'#80aaff'}],top:[{key:'example',label:'Сервис',route:'proxy',speed:1000}],onNode:k=>actions.push(k),why:()=>['Правило'],onPick:i=>actions.push(i.current),onDrop:async()=>false};
      RouteDiagrams.update(box,model,'orbit');
    });
    await page.getByRole('button',{name:'Сервер',exact:true}).click();
    await page.getByRole('button',{name:'Сервис: Через сервер',exact:true}).press('Enter');
    assert.deepEqual(await page.evaluate(()=>actions),['proxy','proxy']);
    assert.equal(await page.locator('.route-group').count(),2);
    const drop = async () => page.evaluate(() => {
      const dt=new DataTransfer(), leaf=document.querySelector('.route-service'), target=document.querySelector('[data-route="direct"]');
      leaf.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:dt}));
      target.dispatchEvent(new DragEvent('drop',{bubbles:true,dataTransfer:dt}));
    });
    await drop(); await page.waitForTimeout(50);
    assert.equal(await page.locator('.route-service').getAttribute('data-route'),'proxy','rejected save must retain route');
    await page.evaluate(()=>model.onDrop=async()=>true);
    await drop(); await page.waitForTimeout(50);
    assert.equal(await page.locator('.route-service').getAttribute('data-route'),'direct','successful save applies temporary presentation choice');

    await page.evaluate(()=>{const b=document.querySelector('#routeFixture');RouteDiagrams.update(b,model,'tree');RouteDiagrams.update(b,model,'tree');});
    assert.equal(await page.locator('.route-scene').count(),1);
    assert.equal(await page.locator('[data-diagram="tree"]').count(),1);
    await page.evaluate(()=>RouteDiagrams.unmount());
    assert.equal(await page.locator('.route-scene').count(),0);
    assert.deepEqual(errors,[]);console.log('Presentation policy, event deduplication, reduced motion, local atmosphere and controls: OK');
  } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
