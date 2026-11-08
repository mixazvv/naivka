'use strict';
const VPS_SERVICE_NAMES={'sing-box':'sing-box · VPN','xray':'Xray · VPN','naivka-caddy':'Caddy · NaiveProxy','x-ui':'3X-UI','s-ui':'s-ui'};
function vpsManagerDialog(host){
 const body=h('div',{class:'vps-manager-body'}),message=h('p',{class:'hint',role:'status'});
 let report=null,busy=false,tab='overview',logService='sing-box',logResult=null;
 const refresh=h('button',{class:'btn primary',type:'button',onclick:()=>load()},'Получить сведения');
 const copy=h('button',{class:'btn small',onclick:async()=>{try{await navigator.clipboard.writeText(host.host);toast('Адрес скопирован');}catch(_){toast('Не удалось скопировать',true);}}},'Скопировать адрес');
 const exportReport=h('button',{class:'btn small',disabled:true,onclick:()=>download('naivka-vps-info.json',JSON.stringify({host:host.host,sshPort:host.sshPort,report},null,2),'application/json')},'Сохранить отчёт');
 const tabs=h('div',{class:'vps-tabs',role:'group','aria-label':'Управление VPS'},[['overview','Обзор'],['services','Службы'],['ports','Порты'],['logs','Журнал'],['audit','Аудит']].map(([key,label])=>h('button',{class:'btn small','data-tab':key,'aria-pressed':String(tab===key),onclick:()=>{tab=key;paint();}},label)));
 const dialog=openDialog(h('h2',null,'Управление VPS: '+(host.name||host.host)),h('div',{class:'row'},h('code',null,host.host),h('small',{class:'hint'},'SSH '+(host.sshUser||'root')+' · порт '+(host.sshPort||22)),copy),
  h('p',{class:'hint'},'Вход по SSH с проверкой ключа сервера. Пароль не сохраняется. Получение сведений не меняет настройки VPS; действия со службами подтверждаются отдельно.'),tabs,body,message,
  h('div',{class:'row end mt'},h('button',{class:'btn',onclick:()=>dialog.close()},'Закрыть'),exportReport,refresh));
 dialog.classList.add('wide','vps-manager');
 const cached=VPS_HISTORY.get(host.id);if(cached?.length)report=cached.at(-1);
 function controls(){refresh.disabled=busy;exportReport.disabled=!report||busy;body.querySelectorAll('[data-vps-op]').forEach(b=>b.disabled=busy);}
 function status(service){return report?.checks?.find(c=>c.id==='service_'+service)?.value||'unknown';}
 function paint(){
  for(const b of tabs.children)b.setAttribute('aria-pressed',String(b.dataset.tab===tab));
  if(tab==='logs'){
   const select=h('select',{'aria-label':'Служба для журнала',onchange:e=>{logService=e.target.value;logResult=null;paint();}},Object.entries(VPS_SERVICE_NAMES).map(([key,label])=>h('option',{value:key,selected:key===logService},label)));
   body.replaceChildren(h('div',{class:'row'},select,h('button',{class:'btn','data-vps-op':'logs',onclick:()=>action(logService,'logs')},'Загрузить журнал')),
    h('p',{class:'hint'},'Последние 100 строк systemd journal, до 16 КиБ. Известные ссылки и ключи маскируются; перед отправкой другим людям проверьте текст. Журнал автоматически не обновляется.'),
    logResult?h('div',null,h('p',{class:'hint'},VPS_SERVICE_NAMES[logResult.service]+' · '+new Date(logResult.checkedAt*1000).toLocaleString()),h('pre',{class:'log vps-log',tabindex:0},logResult.log||'Журнал пуст или недоступен для чтения.'),logResult.truncated?h('p',{class:'hint'},'Вывод ограничен 16 КиБ.'):null):h('p',{class:'hint'},'Выберите службу и загрузите журнал.'));
  }else if(!report){body.replaceChildren(h('p',{class:'hint'},'Нажмите «Получить сведения» — Naivka запросит состояние сервера по SSH.'));
  }else if(tab==='overview'){
   const labels={os:'Операционная система',kernel:'Ядро Linux',arch:'Архитектура',hostname:'Имя системы',singbox:'Версия sing-box',xray:'Версия Xray',caddy:'Версия Naive Caddy'};
   body.replaceChildren(h('div',{class:'panel'},h('h3',null,'Система и VPN'),h('table',{class:'table'},h('tbody',null,Object.entries(labels).map(([key,label])=>h('tr',null,h('td',null,label),h('td',null,report.info?.[key]||'Не определено')))))),vpsResults(report,VPS_HISTORY.get(host.id)||[],'monitor'));
  }else if(tab==='services'){
   const cards=Object.entries(VPS_SERVICE_NAMES).map(([service,label])=>{
    const buttons=['start','restart','stop'].map(op=>h('button',{class:'btn small','data-vps-op':op,disabled:busy,onclick:()=>action(service,op)},({start:'Запустить',restart:'Перезапустить',stop:'Остановить'})[op]));
    return h('section',{class:'panel vps-service'},h('div',{class:'row'},h('b',null,label),h('span',{class:'hint'},VPS_VALUES[status(service)]||'Не определено')),h('div',{class:'row'},buttons));
   });
   body.replaceChildren(h('p',{class:'hint'},'Запуск, остановка и перезапуск требуют root или sudo без пароля. Остановка VPN-службы прервёт её подключения.'),...cards);
  }else if(tab==='ports'){body.replaceChildren(vpsListenersDetails(report)||h('p',{class:'hint'},'Сведения о портах не получены.'),h('p',{class:'hint'},'Локальный слушающий порт не подтверждает доступность через firewall хостинга или из мобильной сети.'));
  }else body.replaceChildren(vpsResults(report,VPS_HISTORY.get(host.id)||[],'audit'));
  controls();
 }
 async function request(path,payload,creds,done){
  if(busy||!dialog.isConnected)return;
  busy=true;message.className='hint';message.textContent='Выполняю…';controls();
  try{
   const result=await api(path,{host:host.id,...payload,...creds});if(!dialog.isConnected)return;
   if(result.needAuth){busy=false;controls();message.textContent='Нужен вход по SSH';return sshAuthDialog(host,c=>request(path,payload,c,done));}
   done(result);message.textContent='';paint();
  }catch(e){if(dialog.isConnected){message.className='err-text';message.textContent=e.message;}if(creds)throw e;}
  finally{busy=false;if(dialog.isConnected)controls();}
 }
 function load(){return request('/api/vps/report',{},null,r=>{report=r;vpsRemember(host.id,r);refresh.textContent='Обновить сведения';});}
 async function action(service,operation){
  if(busy)return;
  if(operation!=='logs'&&!await ask(({start:'Запустить',restart:'Перезапустить',stop:'Остановить'})[operation]+' службу '+VPS_SERVICE_NAMES[service]+' на '+(host.name||host.host)+'? Текущие подключения этой службы могут прерваться.'))return;
  if(!dialog.isConnected)return;
  await request('/api/vps/service',{service,action:operation,confirm:operation!=='logs'},null,r=>{
   if(operation==='logs'){if(logService===service)logResult=r;}
   else{
    if(report){report=JSON.parse(JSON.stringify(report));const check=report.checks.find(c=>c.id==='service_'+service);if(check){check.value=r.state;check.level=r.state==='failed'?'warning':'info';}}
    toast(VPS_SERVICE_NAMES[service]+': '+(VPS_VALUES[r.state]||r.state));
    setTimeout(()=>{if(dialog.isConnected)load();},0);
   }
  });
 }
 dialog.addEventListener('close',()=>{logResult=null;report=null;});paint();
}
