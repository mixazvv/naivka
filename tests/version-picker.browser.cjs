const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright'),root=path.resolve(__dirname,'../web');let installed=[],pages=[],failed=false;
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://localhost');
 if(u.pathname.startsWith('/api/')){
  let raw='';for await(const c of req)raw+=c;
  let value={};
  if(u.pathname==='/api/app/versions'){pages.push(u.searchParams.get('page'));value=u.searchParams.get('page')==='2'?{versions:[{tag:'v2.8.0',available:true,current:false}],hasMore:false}:{versions:[{tag:'v2.27.0',available:true,current:true},{tag:'v2.26.0',available:true,current:false},{tag:'v2.0.0',available:false,current:false}],hasMore:true};}
  if(u.pathname==='/api/app/versions/install'){installed.push(JSON.parse(raw));failed=true;}
  if(u.pathname==='/api/app/update')value={busy:failed?'':'download',error:failed?'Архив не прошёл проверку подписи':''};
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value));return;
 }
 const file=path.resolve(root,'.'+(u.pathname==='/'?'/index.html':u.pathname));if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404).end();return;}
 let content=fs.readFileSync(file);if(file.endsWith('/app.js'))content=content.toString().replace(/\nboot\(\);\s*$/,'\n');res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream');res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage();page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.evaluate(()=>{SELF={version:'2.27.0'};APPUP={};window.saved=0;saveNow=async()=>{saved++};main.replaceChildren(selfBox());});
  await page.getByRole('button',{name:'Выбрать версию / откат',exact:true}).click();const select=page.getByLabel('Версия Naivka');await select.waitFor();
  await page.getByRole('option',{name:'v2.26.0',exact:true}).waitFor({state:'attached'});assert.equal(installed.length,0);
  assert(await page.getByRole('option',{name:'v2.27.0 · установлена',exact:true}).isDisabled());assert(await page.getByRole('option',{name:/v2.0.0 · нет/}).isDisabled());
  await page.getByRole('button',{name:'Ещё версии',exact:true}).click();await page.getByRole('option',{name:'v2.8.0',exact:true}).waitFor({state:'attached'});await select.selectOption('v2.8.0');
  const install=page.getByRole('button',{name:'Установить выбранную версию',exact:true});await install.click();await page.getByText(/Скачиваю и проверяю подпись/).waitFor();assert.deepEqual(installed,[{tag:'v2.8.0'}]);assert.equal(await page.evaluate(()=>saved),1);assert.deepEqual(pages,['1','2']);
  await page.getByText('Архив не прошёл проверку подписи',{exact:true}).waitFor();assert(await install.isEnabled());assert(await select.isEnabled());
  await page.getByRole('button',{name:'Закрыть',exact:true}).click();await page.locator('dialog').waitFor({state:'detached'});assert.equal(await page.locator('dialog').count(),0);assert.deepEqual(errors,[]);
  console.log('Version chooser: stable signed platform options, pagination, explicit target, save-before-install, verification error and close: OK');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
