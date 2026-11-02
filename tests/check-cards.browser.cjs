// Real shared check() and shipped theme styles. No VPN, host settings or boot.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright'),root=path.resolve(__dirname,'../web');
const server=http.createServer((req,res)=>{
 const u=new URL(req.url,'http://localhost'),file=path.resolve(root,'.'+(u.pathname==='/'?'/index.html':u.pathname));
 if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404).end();return;}
 let content=fs.readFileSync(file);if(file.endsWith('/app.js'))content=content.toString().replace(/\nboot\(\);\s*$/,'\n');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.woff2':'font/woff2'})[path.extname(file)]||'application/octet-stream');res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:1050}});page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.evaluate(()=>{
   S={ruInsideDirect:true,blockQuic:true,udpDirect:true,udpProxy:true,adBlock:false,regular:true,disabled:false,partial:false};window.saves=0;save=()=>{saves++};
   const options=[['ruInsideDirect','Сайты, которые работают только из России, всегда напрямую','Госуслуги, банки и другие сервисы, которые не пускают иностранные IP.'],['blockQuic','Блокировать QUIC','Нужно для серверов Naive.'],['udpDirect','Остальной UDP напрямую','Игры и звонки — мимо сервера.'],['udpProxy','UDP через сервер — голос Discord, игры','Для серверов VLESS, Hysteria2, TUIC, Trojan и Shadowsocks. Работает в режиме TUN.'],['adBlock','Блокировать рекламу и трекеры','Баннеры, счётчики и рекламные сети. Рекламу в видео YouTube так не убрать.']];
   const grid=h('div',{class:'toggle-grid',id:'cards'},options.map(x=>check(...x)));[...grid.children].forEach((e,i)=>e.id=options[i][0]);
   const regular=check('regular','Очень длинное имя параметра '+ 'hostname'.repeat(18),'Пояснение '.repeat(35));regular.id='regular';
   const disabled=check('disabled','Недоступный параметр','Действие сейчас недоступно.');disabled.id='disabled';disabled.querySelector('input').disabled=true;
   const partial=check('partial','Частичный выбор');partial.id='partial';partial.querySelector('input').indeterminate=true;
   document.body.replaceChildren(h('div',{id:'fixture'},h('section',{class:'card'},h('h2',null,'Особые случаи'),grid),h('section',{class:'card mt'},h('h2',null,'Другие переключатели'),h('div',{class:'settings-item-body'},h('div',{class:'settings-control',id:'setting'},check('regular','Параметр в настройках','Пояснение'))),regular,disabled,partial)));
  });
  await page.addStyleTag({content:'body{height:auto;overflow:auto;padding:20px}#fixture{max-width:1320px;margin:auto}.mt{margin-top:20px}'});
  await page.evaluate(()=>document.fonts.ready);
  const screenshotDir=process.env.CONTROL_SCREENSHOTS;if(screenshotDir)fs.mkdirSync(screenshotDir,{recursive:true});
  for(const theme of ['flow','gold','blocks','custom']){
   await page.evaluate(theme=>{
    const r=document.documentElement;r.removeAttribute('data-skin');r.removeAttribute('data-custom');r.removeAttribute('style');
    if(theme==='gold'||theme==='blocks')r.dataset.skin=theme;
    if(theme==='custom'){r.dataset.custom='1';r.style.setProperty('--tunnel','#80c8ff');r.style.setProperty('--on-accent','#101820');for(const [k,v] of Object.entries({'--bg':'#f5f6f7','--surface':'#ffffff','--raised':'#edf1f4','--ink':'#17212a','--muted':'#52616b','--line':'#c5cdd3'}))r.style.setProperty(k,v);}
   },theme);
   for(const width of [1440,900,480,320]){
    await page.setViewportSize({width,height:1050});
    const layout=await page.locator('#cards > .check').evaluateAll(labels=>labels.map(e=>{
     const r=e.getBoundingClientRect(),i=e.querySelector('input').getBoundingClientRect(),s=e.querySelector('span').getBoundingClientRect();
     return {padding:i.left-r.left,gap:s.left-i.right,right:r.right-s.right,inside:i.bottom<=r.bottom&&s.bottom<=r.bottom+1,width:i.width,textOverflow:e.querySelector('span').scrollWidth>e.querySelector('span').clientWidth+1};
    }));
    for(const row of layout){assert(row.padding>=15,`${theme}/${width}: checkbox touches card edge`);assert(row.gap>=10,`${theme}/${width}: text overlaps checkbox`);assert(row.right>=14&&row.inside&&!row.textOverflow,`${theme}/${width}: clipped card content`);assert.equal(row.width,20);}
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`${theme}/${width}: horizontal page overflow`);
    const setting=await page.locator('#setting').evaluate(e=>{const i=e.querySelector('input').getBoundingClientRect(),s=e.querySelector('label > span').getBoundingClientRect();return s.left-i.right});assert(setting>=10,`${theme}: settings grid overlaps indicator`);
    if(screenshotDir&&[1440,320].includes(width))await page.locator('#fixture').screenshot({path:path.join(screenshotDir,`checks-${theme}-${width}.png`)});
   }
  }
  await page.setViewportSize({width:900,height:1050});
  const ad=page.locator('#adBlock input');await page.locator('#adBlock b').click();assert(await ad.isChecked());assert.equal(await page.evaluate(()=>S.adBlock),true);
  await ad.focus();await page.keyboard.press('Space');assert.equal(await ad.isChecked(),false);assert.equal(await page.evaluate(()=>S.adBlock),false);assert.equal(await page.evaluate(()=>saves),2);
  assert(await ad.evaluate(e=>e.matches(':focus-visible')));assert.equal(await page.locator('#adBlock').evaluate(e=>getComputedStyle(e).outlineStyle),'solid');
  await page.locator('#disabled b').click();assert.equal(await page.evaluate(()=>S.disabled),false);assert.equal(await page.evaluate(()=>saves),2);
  assert(await page.locator('#partial input').evaluate(e=>e.indeterminate));assert.equal(await page.locator('#partial input').evaluate(e=>getComputedStyle(e,'::before').visibility),'visible');
  await page.setViewportSize({width:320,height:1050});await page.locator('#regular .more-link').waitFor();const before=await page.evaluate(()=>S.regular);await page.locator('#regular .more-link').click();assert.equal(await page.evaluate(()=>S.regular),before,'details button toggled the setting');
  await page.emulateMedia({forcedColors:'active'});assert.equal(await ad.evaluate(e=>getComputedStyle(e).appearance),'auto');assert.equal(await ad.evaluate(e=>getComputedStyle(e,'::before').display),'none');
  await page.emulateMedia({forcedColors:'none',reducedMotion:'reduce'});await ad.focus();await page.keyboard.press('Space');assert(await ad.isChecked());assert.equal(await page.evaluate(()=>S.adBlock),true);
  assert.deepEqual(errors,[]);console.log('Checkbox cards: shipped themes, 320–1440 px, real label/Space changes, disabled/mixed/focus/forced-colors, long text and details: OK');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
