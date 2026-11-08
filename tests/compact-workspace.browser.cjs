// Shipped UI with a local static server and an in-page API fixture. No VPN/SSH.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright'),root=path.resolve(__dirname,'../web');
const server=http.createServer((req,res)=>{const u=new URL(req.url,'http://localhost'),file=path.resolve(root,'.'+(u.pathname==='/'?'/index.html':u.pathname));if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404).end();return;}let content=fs.readFileSync(file);if(file.endsWith('/app.js'))content=content.toString().replace(/\nboot\(\);\s*$/,'\n');res.setHeader('Content-Type',({'.html':'text/html','.css':'text/css','.js':'text/javascript','.woff2':'font/woff2'})[path.extname(file)]||'application/octet-stream');res.end(content);});
(async()=>{
 if(process.env.CONTROL_SCREENSHOTS)fs.mkdirSync(process.env.CONTROL_SCREENSHOTS,{recursive:true});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1280,height:720}});page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.evaluate(()=>{
   S={theme:'dark',accent:'#B6E35A',lang:'ru',nav:'list',mode:'proxy',routing:'ru-direct',layout:{},experience:{home:'compact',activity:'services',diagram:'branches',effects:'balanced',sound:'off',atmosphere:'off'},servers:[{id:'a',name:'Нидерланды',type:'trojan',host:'nl.example.invalid',port:443,password:'do-not-display'},{id:'b',name:'Германия',type:'trojan',host:'de.example.invalid',port:443,password:'secret'}],selected:'a',rules:[],ruleLists:[],wl:{enabled:false},zapret:{enabled:false,fallback:true},telegram:{enabled:false},showIp:true,killSwitch:true,watchdog:true,smartDirect:false,achievements:false,mascot:false,mixedPort:2080,dnsRemote:'1.1.1.1',dnsDirect:'https://dns.example/dns-query',testUrl:'https://example.com',testInterval:'3m',extraRules:'',extraOutbounds:'',override:'',ownHosts:[{id:'host',host:'nl.example.invalid',name:'Мой VPS'}]};
   ST={running:true,starting:false,mode:'proxy',os:'windows',admin:false,install:true,startedAt:Date.now()/1000-120,mixedPort:2080,core:{present:true,version:'1.13',naive:true,cronet:true},download:{},conflicts:[]};
   PX={selected:'s-a',autoNow:'s-a',delays:{'s-a':30,'s-b':60}};AD={updates:{},zapret:{installed:false,running:false},telegram:{installed:false,running:false},xray:{}};SELF={version:'2.28.0'};APPUP=null;
   STATS={health:'ok',totals:[10000000,2000000],samples:[{t:Date.now()/1000,pd:800000,pu:12000,dd:10000,du:1000}],top:Array.from({length:8},(_,i)=>({host:`site${i}.example.org`,proc:`program${i}.exe`,speed:100000-i*1000,total:5000000,route:'proxy'}))};
   // Distinct domains are needed to exercise the five-row service limit.
   STATS.top=STATS.top.map((t,i)=>({...t,host:`service${i}.test${i}.org`}));
   ip.data={running:true,proxy:{info:{country:'Нидерланды',countryCode:'NL',ip:'2001:db8:1234:5678:90ab:cdef:1234:5678'}},direct:{info:{country:'Россия',countryCode:'RU',ip:'203.0.113.1'}}};
   window.apiCalls=[];window.saves=0;save=()=>{saves++};saveNow=async()=>true;
   api=async(url,body)=>{apiCalls.push({url,body});if(url==='/api/disconnect')ST.running=false;if(url==='/api/connect')ST.running=true;
    if(url==='/api/state')return {...ST};if(url==='/api/proxies')return PX;if(url==='/api/addons')return AD;
    if(url==='/api/routing/summary')return {quic:true,server:true,direct:false,final:'proxy',mode:S.mode,custom:false};
    if(url==='/api/vps/report')return {checkedAt:1800000000,metrics:{cpu_usage:20,memory_used:20,memory_total:100,disk_used:50,disk_total:100},checks:[{id:'root_login',value:'no',level:'ok'},{id:'password_auth',value:'yes',level:'warning'},{id:'certificate',value:'unknown',level:'unknown'}]};return {};
   };
   view='overview';main.replaceChildren(viewOverview());applyTheme();applySections();updateStatusNow();renderLive();
  });
  for(const theme of ['dark','gold','blocks','custom'])for(const [width,height]of [[1280,720],[1920,1080],[600,900],[320,720]]){
   await page.setViewportSize({width,height});await page.evaluate(theme=>{S.theme=theme;applyTheme();updateStatus();renderLive();document.querySelector('#main').scrollTop=0;},theme);await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
   const geometry=await page.locator('#homeConnect').evaluate(e=>{const r=e.getBoundingClientRect();return {top:r.top,bottom:r.bottom,width:r.width};});assert(geometry.top>=0&&geometry.bottom<=height&&geometry.width>=80,`${theme}/${width}: primary action outside viewport`);
   assert(await page.locator('.stage-inner').evaluate(e=>e.classList.contains('home-compact')));assert.equal(await page.locator('#tileIp').isVisible(),false);
   const overflow=await page.locator('#main').evaluate(e=>{const m=e.getBoundingClientRect();return {bad:e.scrollWidth>e.clientWidth+2,width:e.clientWidth,scroll:e.scrollWidth,nodes:[...e.querySelectorAll('*')].filter(x=>{const r=x.getBoundingClientRect();return r.width&&r.right>m.right+2}).slice(0,12).map(x=>({id:x.id,cls:String(x.className),width:x.getBoundingClientRect().width}))};});
   assert(!overflow.bad,`${theme}/${width}: horizontal overflow `+JSON.stringify(overflow));
   if(width<=1000){const g=await page.locator('#lanesCard').boundingBox(),a=await page.locator('#liveCol').boundingBox();assert(a.y>=g.y+g.height-2,'activity overlaps the diagram');}
   assert((await page.locator('#lanesBox').boundingBox()).height>=240,'diagram collapsed to zero height');
   assert((await page.locator('#liveCol .live-row').count())<=5);assert(await page.locator('#liveCol').isVisible());
   if(width>1000){const fill=await page.evaluate(()=>{const main=document.querySelector('#main').getBoundingClientRect(),card=document.querySelector('#lanesCard').getBoundingClientRect(),box=document.querySelector('#lanesBox').getBoundingClientRect();return {gap:main.bottom-card.bottom,inside:card.bottom-box.bottom};});assert(fill.gap<=65,`${theme}/${width}: unused height below diagram: ${fill.gap}`);assert(fill.inside<=85,`${theme}/${width}: unused height inside diagram card: ${fill.inside}`);}
   if(process.env.CONTROL_SCREENSHOTS&&[1920,1280,320].includes(width))await page.screenshot({path:path.join(process.env.CONTROL_SCREENSHOTS,`home-${theme}-${width}.png`)});
  }
  // Visible site/process labels and the computer button remain functional
  // in compact mode. Hidden labels must still never leave stray canvas stems.
  await page.setViewportSize({width:1920,height:1080});await page.evaluate(()=>{
   S.theme='dark';S.telegram.enabled=true;S.zapret.enabled=true;AD.telegram.running=true;AD.zapret.running=true;applyTheme();updateStatusNow();renderLanes();
   window.stemCalls=0;const original=CanvasRenderingContext2D.prototype.quadraticCurveTo;
   CanvasRenderingContext2D.prototype.quadraticCurveTo=function(...args){if(this.canvas.classList.contains('br-canvas'))stemCalls++;return original.apply(this,args);};
  });
  await page.waitForFunction(()=>stemCalls>0);assert(await page.locator('.br-leaves').isVisible());
  await page.waitForFunction(()=>[...document.querySelectorAll('.br-leaf[data-kind="process"]')].some(e=>e.dataset.hidden==='false'&&e.getBoundingClientRect().width>0));
  assert(await page.locator('.br-leaf[data-kind="site"][data-hidden="false"]').count()>0,'no site labels in compact view');
  await page.evaluate(()=>{document.querySelector('.br-leaves').style.display='none';stemCalls=0;});await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>stemCalls),0,'hidden labels must not leave canvas stems');
  await page.evaluate(()=>{document.querySelector('.br-leaves').style.removeProperty('display');renderLanes();});await page.waitForFunction(()=>stemCalls>0);
  await page.getByRole('button',{name:'Развернуть схему',exact:true}).click();assert(await page.locator('.br-leaves').isVisible());
  await page.getByRole('button',{name:'Свернуть схему',exact:true}).click();assert(await page.locator('.br-leaves').isVisible());
  const computer=page.locator('#connectBtn');assert.equal(await computer.getAttribute('aria-hidden'),null);assert.equal(await computer.evaluate(e=>e.tabIndex),0);
  const disconnects=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/disconnect').length);await computer.click();await page.waitForFunction(()=>!busy);assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/disconnect').length),disconnects+1);assert.equal(await page.evaluate(()=>ST.running),false);
  const connects=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/connect').length);await computer.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>!busy);assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/connect').length),connects+1);assert.equal(await page.evaluate(()=>ST.running),true);
  await page.evaluate(()=>{window.paintCalls=0;const clear=CanvasRenderingContext2D.prototype.clearRect;CanvasRenderingContext2D.prototype.clearRect=function(...args){if(this.canvas.classList.contains('br-canvas'))paintCalls++;return clear.apply(this,args);};main.setAttribute('inert','');renderLanes();});await page.waitForTimeout(200);assert.equal(await page.evaluate(()=>paintCalls),0,'covered main continues painting');
  await page.evaluate(()=>{main.removeAttribute('inert');renderLanes();});await page.waitForFunction(()=>paintCalls>0);
  await page.setViewportSize({width:1280,height:720});await page.evaluate(()=>{S.telegram.enabled=false;S.zapret.enabled=false;AD.telegram.running=false;AD.zapret.running=false;updateStatusNow();});
  await page.evaluate(()=>{S.zapretOnly=true;ST.zapretOn=true;ST.running=false;updateStatusNow();});assert((await page.locator('#sysLine').textContent()).includes('без VPN'));assert((await page.locator('#homeMetrics').textContent()).includes('zapret'));assert(!(await page.locator('#homeMetrics').textContent()).includes('через сервер'));await page.evaluate(()=>{S.zapretOnly=false;ST.zapretOn=false;ST.running=true;updateStatusNow();});
  await page.getByRole('button',{name:'Развернуть схему',exact:true}).click();assert(await page.locator('.stage-inner').evaluate(e=>e.classList.contains('diagram-expanded')));await page.getByRole('button',{name:'Свернуть схему',exact:true}).click();
  await page.getByRole('button',{name:'Программы',exact:true}).click();assert.equal(await page.evaluate(()=>S.experience.activity),'programs');assert.equal(await page.locator('#liveCol .live-row').count(),3);assert((await page.locator('#liveCol .live-row small').first().textContent()).includes('через сервер'));
  await page.locator('#liveCol .live-row').first().focus();await page.evaluate(()=>{window.focusKey=document.activeElement.dataset.key;STATS.top[0].route='direct';renderLive();});assert.equal(await page.evaluate(()=>document.activeElement.dataset.key),await page.evaluate(()=>focusKey));assert((await page.locator('#liveCol .live-row small').first().textContent()).includes('напрямую'));await page.keyboard.press('Enter');await page.locator('.route-pop').waitFor();assert((await page.locator('.route-pop small').textContent()).includes('правила'));await page.keyboard.press('Escape');assert(await page.locator('#liveCol .live-row').first().evaluate(e=>e===document.activeElement));
  await page.locator('#homeAddress').click();assert((await page.locator('dialog .full-address').first().textContent()).includes('2001:db8:'));await page.getByRole('dialog').getByRole('button',{name:'Закрыть',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});
  await page.evaluate(()=>{S.showIp=false;renderTiles();});await page.locator('#homeAddress').click();assert.equal(await page.locator('dialog .full-address').count(),0);await page.getByRole('dialog').getByRole('button',{name:'Закрыть',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});await page.evaluate(()=>{S.showIp=true;renderTiles();});
  await page.getByRole('button',{name:'Полный вид',exact:true}).click();assert.equal(await page.locator('.stage-inner').evaluate(e=>e.classList.contains('home-compact')),false);await page.getByRole('button',{name:'Компактный вид',exact:true}).click();
  await page.evaluate(()=>{S.layout={version:2,wide:[{id:'routes',span:2,row:2,col:1,height:500}],narrow:[]};window.savedLayout=JSON.stringify(S.layout);HomeLayout.apply();});assert.equal(await page.evaluate(()=>JSON.stringify(S.layout)===savedLayout),true);assert(await page.locator('.stage-inner').evaluate(e=>e.classList.contains('home-custom')));assert.equal(await page.locator('.stage-inner').evaluate(e=>e.classList.contains('home-compact')),false);
  await page.evaluate(()=>{S.layout={};HomeLayout.apply();});const before=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/disconnect').length);await page.locator('#homeConnect').click();await page.waitForFunction(()=>!busy);assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/disconnect').length),before+1);
  await page.evaluate(()=>{view='servers';drawerBody.replaceChildren(viewServers());document.querySelector('#drawer').hidden=false;document.querySelector('#drawer').classList.add('open');main.setAttribute('inert','');renderServers();});
  await page.getByLabel('Поиск серверов').fill('Германия');assert.equal(await page.locator('#srvList .scard').count(),1);const selectedBefore=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/select').length);await page.getByRole('button',{name:'Подробнее',exact:true}).click();assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/select').length),selectedBefore);assert.equal(await page.locator('dialog').getByText('secret',{exact:true}).count(),0);assert((await page.locator('dialog').textContent()).includes('de.example.invalid'));await page.getByRole('dialog').getByRole('button',{name:'Закрыть',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});
  // Route button identity survives periodic latency/text refreshes.
  await page.evaluate(()=>{
   const root=document.createElement('div');root.style.cssText='position:fixed;width:800px;height:400px';document.body.append(root);
   const d={phase:'on',lanes:[{key:'proxy',active:true,speed:0,color:'var(--tunnel)',node:{title:'Test',sub:'20 ms',icon:'server'}}],top:[],controlLabel:'Отключить',compSub:'Компьютер',endSub:'',onNode:()=>{}};
   Branches.update(root,d);const node=root.querySelector('[data-key="proxy"]');node.focus();
   Branches.update(root,{...d,lanes:[{...d.lanes[0],node:{...d.lanes[0].node,sub:'30 ms'}}]});
   if(root.querySelector('[data-key="proxy"]')!==node||document.activeElement!==node)throw new Error('Route update replaced focused button');
   Branches.unmount();root.remove();
  });
  // Subscription UI uses in-page provider fixtures, never a real subscription URL.
  await page.evaluate(()=>{
   const previous=api;window.subscriptionFixture={settings:JSON.parse(JSON.stringify(S)),fail:false};
   api=async(url,body)=>{
    if(url==='/api/settings'&&!body)return subscriptionFixture.settings;
    if(url.startsWith('/api/subscriptions/')){
     apiCalls.push({url,body});const f=subscriptionFixture;
     if(url.endsWith('/preview'))return {token:'preview-token',names:['Сервер подписки'],count:1,skipped:0};
     if(url.endsWith('/add')){f.settings.subscriptions=[{id:'sub',name:'Тестовая подписка',auto:true,updated:1800000000}];f.settings.servers.push({id:'subscription-server',subscriptionId:'sub',name:'Сервер подписки',type:'trojan',host:'sub.example.invalid',port:443,password:'initial'});}
     if(url.endsWith('/update')){if(f.fail){f.settings.subscriptions[0].error='Не удалось загрузить подписку; прежние серверы сохранены';throw new Error(f.settings.subscriptions[0].error);}f.settings.subscriptions[0].error='';f.settings.servers.find(x=>x.subscriptionId).password='rotated';}
     if(url.endsWith('/client'))f.settings.subscriptions[0].client=body.client;
     if(url.endsWith('/auto'))f.settings.subscriptions[0].auto=body.auto;
     if(url.endsWith('/remove')){f.settings.subscriptions=[];f.settings.servers=f.settings.servers.filter(x=>!x.subscriptionId);}
     return {settings:JSON.parse(JSON.stringify(f.settings))};
    }
    return previous(url,body);
   };window.confirmAction=ask;window.ask=()=>true;
  });
  await page.getByRole('button',{name:'Добавить',exact:true}).click();
  await page.getByRole('button',{name:/Добавить подписку/}).click();assert(await page.getByRole('dialog').getByRole('button',{name:'Добавить',exact:true}).isDisabled());
  const popupStyles=await page.evaluate(()=>{
   document.documentElement.classList.add('mica');
   const d=document.querySelector('dialog'),select=d.querySelector('select'),option=select.querySelector('option');
   const result=[d,select,option].map(e=>({bg:getComputedStyle(e).backgroundColor,color:getComputedStyle(e).color}));
   document.documentElement.classList.remove('mica');return result;
  });
  for(const style of popupStyles){assert(style.bg.startsWith('rgb('),'Mica popup/control must be opaque');assert.notEqual(style.bg,style.color,'popup text must contrast with its background');}
  await page.getByLabel('Ссылка на подписку',{exact:true}).fill('https://subscription.example.invalid/private-token');await page.getByLabel('Название подписки',{exact:true}).fill('Тестовая подписка');
  await page.getByLabel('Режим подписки',{exact:true}).selectOption('happ');
  const importCount=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/subscriptions/add').length);await page.getByRole('button',{name:'Посмотреть серверы',exact:true}).click();await page.getByText('Найдено серверов: 1',{exact:true}).waitFor();assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/subscriptions/add').length),importCount,'preview committed servers');assert(!(await page.getByRole('dialog').textContent()).includes('private-token'));
  assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/subscriptions/preview').at(-1).body.client),'happ');
  await page.getByRole('dialog').getByRole('button',{name:'Добавить',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});assert.equal(await page.evaluate(()=>S.subscriptions.length),1);
  await page.getByLabel('Поиск серверов').fill('Сервер подписки');await page.locator('#srvList').getByRole('button',{name:'Подробнее',exact:true}).click();assert(await page.getByRole('dialog').getByRole('button',{name:'Изменить',exact:true}).isDisabled());await page.getByRole('dialog').getByRole('button',{name:'Закрыть',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});
  await page.locator('#subscriptionList summary').click();
  await page.locator('#subscriptionList').getByRole('button',{name:'Режим подписки',exact:true}).click();
  await page.getByLabel('Режим подписки',{exact:true}).selectOption('device');
  await page.getByRole('dialog').getByRole('button',{name:'Сохранить',exact:true}).click();
  await page.locator('dialog').waitFor({state:'detached'});assert.equal(await page.evaluate(()=>S.subscriptions[0].client),'device');
  await page.evaluate(()=>{subscriptionFixture.fail=true;});await page.locator('#subscriptionList').getByRole('button',{name:'Обновить',exact:true}).click();await page.locator('#subscriptionList .err-text').waitFor();assert.equal(await page.evaluate(()=>S.servers.filter(x=>x.subscriptionId).length),1,'failed refresh erased servers');
  await page.evaluate(()=>{subscriptionFixture.fail=false;});await page.locator('#subscriptionList').getByRole('button',{name:'Обновить',exact:true}).click();await page.waitForFunction(()=>S.servers.find(x=>x.subscriptionId)?.password==='rotated');
  await page.getByLabel('Автообновление раз в 6 часов',{exact:true}).uncheck();await page.waitForFunction(()=>!S.subscriptions[0].auto);
  await page.locator('#subscriptionList').getByRole('button',{name:'Удалить подписку',exact:true}).click();await page.waitForFunction(()=>S.subscriptions.length===0);assert.equal(await page.evaluate(()=>S.servers.filter(x=>x.subscriptionId).length),0);assert(await page.evaluate(()=>S.servers.some(x=>x.id==='a')),'removal erased manual servers');
  await page.evaluate(()=>{view='rules';drawerBody.replaceChildren(viewRules());renderRules();});await page.getByText('UDP по конфигурации настроек',{exact:true}).waitFor();assert.equal(await page.locator('.rule-options[open]').count(),0);await page.locator('.rule-options summary').first().click();assert(await page.getByText('Блокировать QUIC',{exact:true}).isVisible());
  const routingBefore=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/routing/summary').length);await page.evaluate(()=>{saveNow=async()=>false;});await page.locator('.rule-options input').first().click();await page.getByText(/Изменения не сохранены/).waitFor();assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/routing/summary').length),routingBefore);await page.evaluate(()=>{saveNow=async()=>true;});await page.getByRole('button',{name:'Обновить итог UDP',exact:true}).click();await page.getByText('UDP по конфигурации настроек',{exact:true}).waitFor();
  const serverlessBefore=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/routing/summary').length);await page.evaluate(()=>{S.zapretOnly=true;WorkspaceUI.routing();});await page.getByText(/маршрут UDP определяет система/).waitFor();assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/routing/summary').length),serverlessBefore);await page.evaluate(()=>{S.zapretOnly=false;});
  await page.evaluate(()=>vpsDialog(S.ownHosts[0]));const reportCalls=await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/vps/report').length);await page.getByRole('button',{name:'Проверить сейчас',exact:true}).click();await page.getByText('SSH доступен',{exact:false}).waitFor();
  await page.getByRole('button',{name:'Аудит безопасности',exact:true}).click();assert.equal(await page.evaluate(()=>apiCalls.filter(x=>x.url==='/api/vps/report').length),reportCalls+1);const checks=await page.locator('dialog .vps-audit .panel').allTextContents();assert(checks[0].includes('Парольный вход'));assert(checks[1].includes('Сертификат'));await page.getByRole('button',{name:'Мониторинг',exact:true}).click();assert.equal(await page.locator('.vps-audit').count(),0);
  await page.getByRole('dialog').getByRole('button',{name:'Закрыть',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});assert.deepEqual(errors,[]);
  console.log('Compact/full/custom home, primary action, activity grouping/keyboard, IP disclosure, server search/details, generated UDP and VPS tabs: OK');
  await page.evaluate(()=>{
   S.telegram={enabled:false};S.zapret.enabled=false;ST.running=false;ST.starting=false;AD.telegram={running:false};AD.zapret={running:false};
   if(laneList().length)throw new Error('Disconnected diagram contains routes');
   S.telegram.enabled=true;AD.telegram.running=true;
   if(laneList().map(l=>l.key).join(',')!=='tg')throw new Error('Standalone Telegram must be the only route');
  });
  await page.evaluate(()=>{
   ask=window.confirmAction;S.confirm=true;window.confirm=()=>{throw new Error('Native confirm used');};
   window.beforeDelete=S.servers[0];editServer(beforeDelete);
  });
  await page.getByRole('dialog').getByRole('button',{name:'Удалить',exact:true}).click();
  await page.getByRole('dialog').filter({has:page.getByText('Подтвердите действие',{exact:true})}).getByRole('button',{name:'Отмена',exact:true}).click();
  assert(await page.evaluate(()=>S.servers.some(s=>s.id===beforeDelete.id)),'cancel deleted server');
  await page.evaluate(()=>{saveNow=async()=>false;});
  await page.getByRole('dialog').getByRole('button',{name:'Удалить',exact:true}).click();
  await page.getByRole('button',{name:'Подтвердить',exact:true}).click();
  assert(await page.evaluate(()=>S.servers.some(s=>s.id===beforeDelete.id)),'failed save removed server');
  await page.evaluate(()=>{saveNow=async()=>true;});
  await page.getByRole('dialog').getByRole('button',{name:'Удалить',exact:true}).click();
  await page.getByRole('button',{name:'Подтвердить',exact:true}).click();
  await page.waitForFunction(()=>!S.servers.some(s=>s.id===beforeDelete.id));
  await page.evaluate(()=>{const o={id:'delete-own',host:'vps.example.invalid',sshUser:'root',sshPort:22};S.ownHosts.push(o);const box=h('div',{id:'deleteOwnTest'},hostCard(o));document.body.append(box);});
  await page.locator('#deleteOwnTest').getByRole('button',{name:'Убрать сервер из списка',exact:true}).click();
  await page.keyboard.press('Escape');assert(await page.evaluate(()=>S.ownHosts.some(s=>s.id==='delete-own')));
  await page.locator('#deleteOwnTest').getByRole('button',{name:'Убрать сервер из списка',exact:true}).click();
  await page.getByRole('button',{name:'Подтвердить',exact:true}).click();
  await page.waitForFunction(()=>!S.ownHosts.some(s=>s.id==='delete-own'));await page.locator('#deleteOwnTest').evaluate(e=>e.remove());
  await page.evaluate(()=>installServerDialog());
  await page.getByRole('dialog').getByLabel('Понимаю, что делаю, и это мой сервер').check();
  await page.getByRole('button',{name:'Дальше',exact:true}).click();
  await page.getByRole('button',{name:'Xray через 3X-UI',exact:true}).click();
  assert(!(await page.getByLabel('Поставить протоколы Naivka',{exact:false}).isChecked()));
  assert(await page.getByText(/Будет установлена только 3X-UI с Xray/).isVisible());
  await page.getByRole('button',{name:'Готовый VPN · sing-box',exact:true}).click();
  assert(await page.getByLabel('Поставить протоколы Naivka',{exact:false}).isChecked());
  await page.getByRole('dialog').evaluate(d=>d.close());
  await page.evaluate(()=>{
   window.vpsServiceCalls=[];window.vpsAuthed=false;const previousAPI=api;
   api=async(path,body)=>{
    if(path==='/api/vps/report'&&!vpsAuthed){if(!body.password)return {needAuth:true};vpsAuthed=true;}
    if(path==='/api/vps/report')return {checkedAt:1800000000,info:{os:'Debian 13',kernel:'6.12',arch:'x86_64',singbox:'sing-box version 1.14.4'},metrics:{cpu_usage:10,memory_used:200,memory_total:1000},checks:[{id:'service_sing-box',value:'active',level:'ok'},{id:'local_listeners',value:'observed',level:'info'}],listeners:[{protocol:'tcp',port:443,binding:'wildcard'}]};
    if(path==='/api/vps/service'){vpsServiceCalls.push(body);return {service:body.service,action:body.action,state:'active',checkedAt:1800000000,log:'Example log: connection timeout'};}
    return previousAPI(path,body);
   };
   vpsManagerDialog(S.ownHosts[0]);
  });
  await page.getByRole('button',{name:'Получить сведения',exact:true}).click();
  await page.getByPlaceholder('Пароль SSH',{exact:true}).fill('vps-fixture-password');
  await page.getByRole('dialog').filter({has:page.getByPlaceholder('Пароль SSH',{exact:true})}).getByRole('button',{name:'Подключиться',exact:true}).click();
  await page.getByText('Debian 13',{exact:true}).waitFor();
  if(process.env.CONTROL_SCREENSHOTS)await page.screenshot({path:path.join(process.env.CONTROL_SCREENSHOTS,'vps-manager.png')});
  await page.getByRole('button',{name:'Службы',exact:true}).click();
  await page.getByRole('button',{name:'Перезапустить',exact:true}).first().click();
  await page.getByRole('dialog').filter({has:page.getByText('Подтвердите действие',{exact:true})}).getByRole('button',{name:'Отмена',exact:true}).click();
  assert.equal(await page.evaluate(()=>vpsServiceCalls.length),0,'cancelled service operation executed');
  await page.getByRole('button',{name:'Перезапустить',exact:true}).first().click();await page.getByRole('button',{name:'Подтвердить',exact:true}).click();
  await page.waitForFunction(()=>vpsServiceCalls.length===1);assert.equal(await page.evaluate(()=>vpsServiceCalls[0].service),'sing-box');assert.equal(await page.evaluate(()=>vpsServiceCalls[0].confirm),true);
  await page.getByRole('button',{name:'Журнал',exact:true}).click();await page.getByRole('button',{name:'Загрузить журнал',exact:true}).click();await page.getByText('Example log: connection timeout',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Порты',exact:true}).click();await page.getByText('443',{exact:true}).waitFor();
  await page.getByRole('dialog').getByRole('button',{name:'Закрыть',exact:true}).click();assert.deepEqual(errors,[]);
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
