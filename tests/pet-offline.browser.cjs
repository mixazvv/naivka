// Offline regression fixture: no listening sockets or external requests.
const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');
const {chromium}=require('playwright');const root=path.resolve(__dirname,'../web');
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_PATH||(fs.existsSync('/usr/bin/chromium')?'/usr/bin/chromium':undefined),args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1000,height:720}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.clock.install({time:new Date('2026-11-01T12:00:00Z')});await page.setContent('<!doctype html><div id="home"></div><input id="typing"><div class="card" id="tileSpeed">Карточка</div>');
  await page.addStyleTag({path:path.join(root,'app.css')});await page.addStyleTag({content:'#home{position:absolute;left:30px;bottom:20px;width:250px;height:110px}.pet{margin:0}#typing{position:absolute;top:30px;left:30px}.card{position:absolute;top:200px;left:400px;width:200px;height:100px}'});
  for(const name of ['pet-atlas.js','experience.js','pet-mind.js','pet.js'])await page.addScriptTag({path:path.join(root,name)});
  await page.evaluate(()=>{window.saves=[];NaivkaExperience.configure({effects:'balanced',sound:'off',atmosphere:'off'});Math.random=()=>.5;Pet.mount(document.querySelector('#home'),{onMemory:v=>saves.push(v)});Pet.update('on',0);});
  assert(await page.evaluate(()=>Pet.game('yarn')));assert.equal(await page.locator('.pet-yarn').count(),1);
  assert.equal(await page.locator('.pet-yarn').evaluate(e=>getComputedStyle(e).pointerEvents),'none');
  await page.keyboard.press('Escape');assert.equal(await page.locator('.pet-yarn').count(),0);
  for(const kind of ['dot','hide']){
    assert(await page.evaluate(k=>Pet.game(k),kind));
    if(kind==='dot'){assert.equal(await page.locator('.pet-dot').count(),1);assert.equal(await page.locator('.pet-dot').evaluate(e=>getComputedStyle(e).pointerEvents),'none');}
    else {assert.equal(await page.locator('.pet-peeking').count(),1);}
    await page.clock.runFor(1400);assert.equal(await page.locator('.pet-playing').count(),1);
    await page.keyboard.press('Escape');assert.equal(await page.locator('.pet-playing,.pet-peeking,.pet-dot').count(),0);
  }
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
  await page.evaluate(()=>{
    Pet.update('on',0,'ok');const p=document.querySelector('.pet'),r=p.getBoundingClientRect();
    p.dispatchEvent(new PointerEvent('pointerdown',{button:0,isPrimary:true,pointerId:41,clientX:r.x+30,clientY:r.y+30}));
    p.dispatchEvent(new PointerEvent('pointermove',{pointerId:41,clientX:r.x+100,clientY:r.y+40}));
  });
  assert(await page.locator('.pet').evaluate(e=>e.classList.contains('held')));
  await page.locator('.pet').dispatchEvent('lostpointercapture',{pointerId:41});
  assert.equal(await page.locator('.pet.held').count(),0,'lost capture leaves kitten held forever');
  await page.evaluate(()=>window.dispatchEvent(new Event('blur')));
  await page.evaluate(()=>window.dispatchEvent(new Event('focus')));
  await page.evaluate(()=>{
    const drawer=document.createElement('div');drawer.className='drawer open';drawer.innerHTML='<div class="set-card" style="position:absolute;left:400px;top:200px;width:200px;height:120px">Настройки</div>';document.body.append(drawer);
  });
  assert(await page.evaluate(()=>Pet.game('yarn')),'Settings drawer incorrectly disables kitten');
  await page.keyboard.press('Escape');
  for(let i=0;i<3;i++){
    await page.evaluate(()=>{const p=document.querySelector('.pet'),r=p.getBoundingClientRect();p.dispatchEvent(new PointerEvent('pointerdown',{button:0,isPrimary:true,pointerId:77,clientX:r.x+30,clientY:r.y+30}));p.dispatchEvent(new PointerEvent('pointermove',{pointerId:77,clientX:450,clientY:170}));p.dispatchEvent(new PointerEvent('pointerup',{pointerId:77,clientX:450,clientY:170}));document.querySelector('.drawer').replaceChildren();});
    await page.clock.runFor(5000);
    assert.equal(await page.locator('.pet.held').count(),0,'kitten stuck after view replacement');
    const r=await page.locator('.pet').boundingBox();assert(r.x>=-1&&r.y<720&&r.x<1000,'kitten escaped viewport');
  }
  await page.evaluate(()=>{Pet.unmount();Pet.unmount();});assert.equal(await page.locator('.pet').count(),0);assert.equal(await page.evaluate(()=>saves.length),1,'memory not flushed once on teardown');
  await page.evaluate(()=>{Pet.mount(document.querySelector('#home'),{memory:saves[0],onMemory:v=>saves.push(v)});Pet.update('on',0);Pet.unmount();});assert.equal(await page.evaluate(()=>saves.length),1,'unchanged remount writes memory');
  assert.deepEqual(errors,[]);console.log('Explicit games, form interaction, modal/network priority, timeout, reduced motion, memory and teardown: OK');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
