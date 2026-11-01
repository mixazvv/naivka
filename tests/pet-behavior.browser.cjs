const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');const http=require('node:http');
const {chromium}=require('playwright');const root=path.resolve(__dirname,'../web');
const server=http.createServer((req,res)=>{const name=path.basename(req.url);if(req.url==='/'){res.end('<!doctype html><div id="home"></div><input id="typing"><div class="card" id="tileSpeed">Карточка</div>');return;}
 const file=path.join(root,name);if(!fs.existsSync(file)){res.writeHead(404).end();return;}res.setHeader('Content-Type',name.endsWith('.css')?'text/css':name.endsWith('.webp')?'image/webp':'text/javascript');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_PATH||'/usr/bin/chromium',args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1000,height:720}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.clock.install({time:new Date('2026-11-01T12:00:00Z')});await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.addStyleTag({path:path.join(root,'app.css')});await page.addStyleTag({content:'#home{position:absolute;left:30px;bottom:20px;width:250px;height:110px}.pet{margin:0}#typing{position:absolute;top:30px;left:30px}.card{position:absolute;top:200px;left:400px;width:200px;height:100px}'});
  for(const name of ['pet-atlas.js','experience.js','pet-mind.js','pet.js'])await page.addScriptTag({path:path.join(root,name)});
  await page.evaluate(()=>{window.saves=[];NaivkaExperience.configure({effects:'balanced',sound:'off',atmosphere:'off'});Math.random=()=>.5;Pet.mount(document.querySelector('#home'),{onMemory:v=>saves.push(v)});Pet.update('on',0);});
  assert(await page.evaluate(()=>Pet.game('yarn')));assert.equal(await page.locator('.pet-yarn').count(),1);
  assert.equal(await page.locator('.pet-yarn').evaluate(e=>getComputedStyle(e).pointerEvents),'none');
  await page.keyboard.press('Escape');assert.equal(await page.locator('.pet-yarn').count(),0);
  assert(await page.evaluate(()=>Pet.game('cursor')));
  await page.locator('#typing').fill('формы работают');assert.equal(await page.locator('#typing').inputValue(),'формы работают');
  assert.equal(await page.locator('.pet-playing').count(),0);
  assert(await page.evaluate(()=>Pet.game('yarn')));
  await page.evaluate(()=>{const d=document.createElement('dialog');d.id='modal';d.textContent='Диалог';document.body.append(d);d.showModal();});
  assert.equal(await page.locator('.pet-yarn').count(),0);assert.equal(await page.evaluate(()=>Pet.game('cursor')),false);
  await page.evaluate(()=>{document.querySelector('#modal').close();document.querySelector('#modal').remove();});
  assert(await page.evaluate(()=>Pet.game('yarn')));await page.clock.fastForward(22000);assert.equal(await page.locator('.pet-yarn').count(),0,'game outlives timeout');
  assert(await page.evaluate(()=>Pet.game('yarn')));await page.emulateMedia({reducedMotion:'reduce'});await page.clock.runFor(50);await page.waitForFunction(()=>!document.querySelector('.pet-yarn'));assert.equal(await page.locator('.pet-yarn').count(),0);assert.equal(await page.evaluate(()=>Pet.game('cursor')),false);
  await page.emulateMedia({reducedMotion:'no-preference'});await page.clock.runFor(50);await page.waitForTimeout(30);assert(await page.evaluate(()=>Pet.game('yarn')));
  await page.evaluate(()=>NaivkaExperience.configure({effects:'off'}));assert.equal(await page.locator('.pet-yarn').count(),0);assert.equal(await page.evaluate(()=>Pet.game('yarn')),false);
  await page.evaluate(()=>NaivkaExperience.configure({effects:'balanced'}));assert(await page.evaluate(()=>Pet.game('yarn')));
  await page.evaluate(()=>Pet.update('on',1024*1024));await page.clock.runFor(3500);await page.evaluate(()=>Pet.update('on',1024*1024));assert.equal(await page.locator('.pet-playing').count(),1,'traffic intensity interrupts an explicit game');
  await page.evaluate(()=>Pet.update('err',0));assert.equal(await page.locator('.pet-yarn').count(),0,'game overrides network failure');
  await page.evaluate(()=>{Pet.unmount();Pet.unmount();});assert.equal(await page.locator('.pet').count(),0);assert.equal(await page.evaluate(()=>saves.length),1,'memory not flushed once on teardown');
  await page.evaluate(()=>{Pet.mount(document.querySelector('#home'),{memory:saves[0],onMemory:v=>saves.push(v)});Pet.update('on',0);Pet.unmount();});assert.equal(await page.evaluate(()=>saves.length),1,'unchanged remount writes memory');
  assert.deepEqual(errors,[]);console.log('Explicit games, form interaction, modal/network priority, timeout, reduced motion, memory and teardown: OK');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
