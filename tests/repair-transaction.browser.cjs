const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright'),root=path.resolve(__dirname,'../web');
let operation={phase:'IDLE',checks:[]},polls=0,confirmations=0,mode='success',round=0;
function plan(){return {operationId:'fixture-'+round,phase:'PLAN_READY',diagnosis:'PRIMARY_UNREACHABLE',result:'NO_ACTION',expectedRevision:7,proposal:'TEMPORARY_FAILOVER',expiresAt:Math.floor(Date.now()/1000)+120,candidates:[{id:'b',checkedAt:Math.floor(Date.now()/1000)}],checks:[{name:'DNS_PROXY',status:'TIMEOUT'},{name:'BACKUP_1',status:'OK'}]};}
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://localhost');
 if(u.pathname.startsWith('/api/')){
  let raw='';for await(const c of req)raw+=c;const body=raw?JSON.parse(raw):{};
  if(u.pathname.endsWith('/start')){round++;polls=0;operation={operationId:'fixture-'+round,phase:'CAPTURING',checks:[]};}
  if(u.pathname.endsWith('/confirm')){
   assert.equal(body.operationId,operation.operationId);assert.equal(body.expectedRevision,7);assert.equal(body.target,'b');assert.equal(body.action,'temporary_failover');confirmations++;
   if(mode==='stale'){res.writeHead(400,{'Content-Type':'application/json'});res.end(JSON.stringify({error:'STALE_PLAN'}));return;}
   operation={...operation,phase:'VERIFYING',proposal:'',diagnosis:''};polls=0;
  }
  if(u.pathname.endsWith('/cancel')){assert.equal(body.operationId,operation.operationId);operation={...operation,phase:'CANCELLING'};polls=0;}
  if(u.pathname.endsWith('/status')){
   polls++;
   if(operation.phase==='CAPTURING'&&polls>=2)operation=plan();
   else if(operation.phase==='CANCELLING'&&polls>=2)operation={...operation,phase:'ROLLED_BACK',result:'ROLLED_BACK',diagnosis:'ORIGINAL_CONFIGURATION_RESTORED'};
   else if(operation.phase==='VERIFYING'&&polls>=2&&mode!=='cancel')operation={...operation,phase:mode==='rollback'?'ROLLED_BACK':'COMMITTED',result:mode==='rollback'?'ROLLED_BACK':'SWITCHED',diagnosis:mode==='rollback'?'ORIGINAL_CONFIGURATION_RESTORED':'CONTROL_PATH_CONFIRMED'};
  }
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify(operation));return;
 }
 const file=path.resolve(root,'.'+(u.pathname==='/'?'/index.html':u.pathname));if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404).end();return;}
 let content=fs.readFileSync(file);if(file.endsWith('/app.js'))content=content.toString().replace(/\nboot\(\);\s*$/,'\n');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream');res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_PATH||(fs.existsSync('/usr/bin/chromium')?'/usr/bin/chromium':undefined),args:['--no-sandbox']});
 try{
  const page=await browser.newPage();page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.evaluate(()=>{S={servers:[{id:'b',name:'Проверенный сервер'}]};main.replaceChildren(viewRepair());});
  const start=page.getByRole('button',{name:'Проверить соединение',exact:true});const confirm=page.getByRole('button',{name:'Подтвердить временное переключение',exact:true});
  await start.click();await confirm.waitFor();assert.equal(confirmations,0,'diagnosis mutated the route without confirmation');
  assert.equal(await page.getByLabel('Проверенный резерв').inputValue(),'b');await confirm.click();await page.getByText(/Переключение выполнено: контрольные HTTPS/).waitFor();assert(await start.isEnabled());
  mode='rollback';await start.click();await confirm.waitFor();await confirm.click();await page.getByText('Исходный маршрут восстановлен. Доступность интернета не подтверждена.',{exact:true}).waitFor();
  mode='cancel';await start.click();await confirm.waitFor();await confirm.click();await page.getByRole('button',{name:'Отмена',exact:true}).click();await page.getByText('Исходный маршрут восстановлен. Доступность интернета не подтверждена.',{exact:true}).waitFor();
  mode='stale';await start.click();await confirm.waitFor();await confirm.click();await page.getByText('Предложение устарело. Повторите диагностику.',{exact:true}).waitFor();
  await page.evaluate(()=>main.replaceChildren());const before=polls;await page.waitForTimeout(900);assert.equal(polls,before,'polling survived panel teardown');assert.deepEqual(errors,[]);
  console.log('Repair: read-only diagnosis, explicit revision-bound confirmation, verified success, rollback, cancel, stale plan and teardown: OK');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
