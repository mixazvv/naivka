// Production wizard, local DOM and API fixture; never connects to a VPS.
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'../web');
const server=http.createServer((req,res)=>{
 const file=path.resolve(root,'.'+new URL(req.url,'http://localhost').pathname.replace(/^\/$/,'/index.html'));
 if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){res.writeHead(404).end();return;}
 let content=fs.readFileSync(file);if(file.endsWith('/app.js'))content=content.toString().replace(/\nboot\(\);\s*$/,'\n');
 res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css'})[path.extname(file)]||'application/octet-stream');res.end(content);
});
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{}),args:['--no-sandbox']});
 try{
  const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.evaluate(()=>{
   S={ownHosts:[{id:'vps',host:'203.0.113.10'}]};saveNow=async()=>true;renderOwn=()=>{};
   window.calls=[];window.rejectPreview=false;
   api=async(path,body)=>{
    calls.push({path,body});if(rejectPreview)throw Error('Ключ SSH отличается');
    const plan={revision:'revision',old:'203.0.113.10',address:body.address,fingerprint:'SHA256:fixture',servers:[{id:'manual',name:'Мой VPN'}],panels:[],backup:'address-backup-fixture'};
    if(body.apply){await new Promise(r=>setTimeout(r,100));return {plan,settings:{ownHosts:[{id:'vps',host:body.address}]}};}
    return {plan};
   };
   vpsAddressDialog(S.ownHosts[0]);
  });
  assert.equal(await page.evaluate(()=>calls.length),0);
  await page.getByPlaceholder('Новый IPv4 или IPv6').fill('203.0.113.20');
  await page.getByRole('button',{name:'Проверить новый адрес',exact:true}).click();
  await page.getByText('Мой VPN',{exact:true}).waitFor();
  await page.getByRole('checkbox').uncheck();
  await page.getByRole('button',{name:'Сохранить новый адрес',exact:true}).click();
  await page.keyboard.press('Escape');
  await page.getByText('Готово. Теперь можно подключиться и проверить интернет.',{exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>S.ownHosts[0].host),'203.0.113.20');
  assert.deepEqual(await page.evaluate(()=>calls[1].body.servers),[]);
  assert.equal(await page.evaluate(()=>calls[1].body.revision),'revision');
  await page.getByRole('button',{name:'Готово',exact:true}).click();
  await page.getByPlaceholder('Новый IPv4 или IPv6').waitFor({state:'detached'});
  await page.evaluate(()=>{rejectPreview=true;vpsAddressDialog({id:'vps',host:'203.0.113.20'});});
  await page.getByPlaceholder('Новый IPv4 или IPv6').fill('203.0.113.30');
  await page.getByRole('button',{name:'Проверить новый адрес',exact:true}).click();
  await page.getByText('Ключ SSH отличается',{exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Сохранить новый адрес',exact:true}).isVisible(),false);
  assert.equal(await page.evaluate(()=>S.ownHosts[0].host),'203.0.113.20');
  assert.deepEqual(errors,[]);
  console.log('VPS address wizard: explicit preview, selection, commit, Escape guard and SSH failure passed');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(()=>server.close());
