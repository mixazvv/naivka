const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');const web=path.resolve(__dirname,'../web');
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_PATH||(fs.existsSync('/usr/bin/chromium')?'/usr/bin/chromium':undefined),args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1180,height:900}});page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.setContent('<!doctype html><div class="stage-inner"><div class="stage-head">Статус</div><div id="notices"></div><div id="lanesCard"><div id="lanesBox"><button class="power">Питание</button></div></div><div class="stage-controls">Управление</div><div class="stage-tiles"><div id="tileSpeed" class="tile">Скорость</div><div id="tileIp" class="tile">IP</div><div id="tileSession" class="tile">Сеанс</div></div><div id="stageReady"></div><div id="liveCol"></div></div>');
  for(const file of ['home-layout.css','route-diagrams.css'])await page.addStyleTag({path:path.join(web,file)});
  await page.addStyleTag({content:':root{--line:#555;--raised:#222;--text:#fff;--tunnel:#b6e35a;--accent:#b6e35a}.stage-inner{width:1000px}#lanesBox{position:relative;height:480px;width:100%}.power{width:60px;height:60px}.stage-head,.stage-controls,.tile{background:#222;color:white}dialog{max-height:85vh;overflow:auto;width:850px}.row{display:flex}'});
  await page.evaluate(()=>{
   window.S={layout:{version:1,wide:[{id:'speed',span:2}],narrow:[]}};window.saves=0;window.save=()=>saves++;
   window.h=(tag,attrs,...children)=>{const e=document.createElement(tag);for(const[k,v]of Object.entries(attrs||{})){if(k.startsWith('on'))e.addEventListener(k.slice(2),v);else if(k==='class')e.className=v;else if(typeof v==='boolean'){e[k]=v;}else e.setAttribute(k,v);}e.append(...children.flat(Infinity).filter(x=>x!==null&&x!==undefined));return e;};
   window.openDialog=(...children)=>{const d=h('dialog',null,...children);d.addEventListener('close',()=>d.remove());document.body.append(d);d.showModal();return d;};
   window.Branches={unmount(){},update(){return{};}};window.dropCalls=[];window.serverCalls=[];window.acceptDrop=true;
  });
  await page.addScriptTag({path:path.join(web,'experience.js')});
  await page.addScriptTag({path:path.join(web,'home-layout.js')});await page.addScriptTag({path:path.join(web,'route-diagrams.js')});
  await page.evaluate(()=>{S.layout={version:2,wide:HomeLayout.normalize([{id:'routes',span:2,row:2,col:2,height:520},{id:'speed',span:1,row:2,col:1,height:180}]),narrow:HomeLayout.normalize([],1)};HomeLayout.apply();});
  assert.equal(await page.locator('#lanesCard').evaluate(e=>e.style.gridColumn),'2 / span 2');
  assert.equal(await page.locator('#lanesCard').evaluate(e=>e.style.minHeight),'520px');
  assert.equal(await page.evaluate(()=>{const observer=new MutationObserver(()=>{});observer.observe(document.querySelector('.stage-inner'),{subtree:true,attributes:true,attributeFilter:['style','data-layout-hidden']});for(let i=0;i<20;i++)HomeLayout.apply();const count=observer.takeRecords().length;observer.disconnect();return count;}),0,'unchanged status polls rewrite custom grid styles');
  await page.evaluate(()=>HomeLayout.edit());
  await page.getByLabel('Скорость: Мин. высота, px',{exact:true}).fill('260');await page.getByLabel('Скорость: Мин. высота, px',{exact:true}).dispatchEvent('change');
  assert.equal(await page.locator('#tileSpeed').evaluate(e=>e.style.minHeight),'260px','height edit was lost through normalization');
  await page.getByRole('button',{name:'Готово',exact:true}).click();
  assert(await page.evaluate(()=>saves>0));
  await page.evaluate(()=>{S.layout={};HomeLayout.apply();document.querySelector('.stage-inner').style.width='1000px';window.d={phase:'on',offline:false,lanes:[{key:'proxy',active:true,color:'#b6e35a',speed:100000,down:80000,up:20000,node:{title:'VPN'}},{key:'direct',active:true,color:'#55aaff',speed:2000,down:1000,up:1000}],top:[{key:'svc',label:'Сервис',route:'proxy'},{key:'proc:chrome.exe',kind:'process',exe:'chrome.exe',label:'Chrome',route:'proxy'}],servers:[{id:'a',name:'Сервер A',active:true},{id:'b',name:'Сервер B',active:false}],onNode(){},onMore(){},onPick(){},onServer:id=>{serverCalls.push(id);return true;},onDrop:async(key,route,item)=>{dropCalls.push({key,route,kind:item.kind});return acceptDrop;}};RouteDiagrams.update(document.querySelector('#lanesBox'),d,'orbit');});
  await page.locator('.route-server[data-server="b"]').click();assert.deepEqual(await page.evaluate(()=>serverCalls),['b']);
  assert.equal(await page.locator('.route-lines [data-direction="down"]').count(),2);
  assert.equal(await page.locator('.route-service').count(),2);
  const target=await page.locator('.route-group[data-route="direct"]').boundingBox();
  assert.equal(await page.evaluate(({x,y})=>{RouteDiagrams.hoverAt(x,y);return RouteDiagrams.endHover();},{x:target.x+20,y:target.y+10}),'direct','external shelf cannot target alternate diagram');
  const service=page.locator('.route-service[data-key="svc"]');
  await service.dragTo(page.locator('.route-group[data-route="direct"]'));await page.waitForTimeout(100);
  assert.equal(await service.getAttribute('data-route'),'direct');
  await page.evaluate(()=>{acceptDrop=false;});await service.dragTo(page.locator('.route-group[data-route="proxy"]'));await page.waitForTimeout(100);assert.equal(await service.getAttribute('data-route'),'direct','rejected save changed visual route');
  await page.evaluate(()=>RouteDiagrams.update(document.querySelector('#lanesBox'),d,'tree'));
  assert.equal(await page.locator('.route-server').count(),2);assert.equal(await page.locator('.route-scene').count(),1);
  await page.emulateMedia({reducedMotion:'reduce'});await page.waitForTimeout(50);
  assert.equal(await page.locator('.route-lines [data-direction="down"]').first().evaluate(e=>getComputedStyle(e).animationName),'none');
  await page.evaluate(()=>{d.phase='off';RouteDiagrams.update(document.querySelector('#lanesBox'),d,'tree');});assert.equal(await page.locator('.route-service').count(),0);
  await page.evaluate(()=>{RouteDiagrams.unmount();RouteDiagrams.unmount();});assert.equal(await page.locator('.route-scene').count(),0);
  assert.deepEqual(errors,[]);console.log('Home grid positions/heights, graph server nodes, shelf targets, rule persistence, motion and teardown: OK');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
