'use strict';
const SubscriptionsUI=(()=>{
 const busy=new Set();let syncing=false;
 function adopt(settings){
  if(!settings||!Array.isArray(settings.servers))return;
  const incoming=new Map(settings.servers.map(x=>[x.id,x])),seen=new Set(),servers=[];
  S.subscriptions=settings.subscriptions||[];
  for(const sv of S.servers){const next=sv.subscriptionId?incoming.get(sv.id):sv;if(next){servers.push(next);seen.add(next.id);}}
  for(const sv of settings.servers)if(!seen.has(sv.id))servers.push(sv);
  S.servers=servers;
  if(!S.servers.some(x=>x.id===S.selected)&&S.selected!=='auto'&&S.selected!=='wl'&&!S.selected.startsWith('w-'))S.selected=settings.selected;
 }
 async function sync(){
  if(syncing||savePending||document.hidden||document.querySelector('dialog[open]'))return;
  syncing=true;try{const settings=await api('/api/settings');if(!savePending&&!document.querySelector('dialog[open]')){adopt(settings);if(view==='servers')renderServers();}}catch(_){}finally{syncing=false;}
 }
 async function action(id,kind,extra={}){
  if(busy.has(id))return;busy.add(id);render();
  try{if(await saveNow()===false)throw new Error('Сначала сохраните изменения настроек');const r=await api('/api/subscriptions/'+kind,{id,...extra});adopt(r.settings);renderServers(true);updateStatus();if(kind==='update')toast('Подписка обновлена');return true;}
  catch(e){toast(e.message,true);await sync();return false;}
  finally{busy.delete(id);render();}
 }
 function clientSelect(value=''){
  const select=h('select',{'aria-label':'Режим подписки'},h('option',{value:''},'Обычный'),h('option',{value:'device'},'С идентификатором устройства'),h('option',{value:'happ'},'Совместимость с Happ'));
  select.value=value;return select;
 }
 function clientHelp(){return h('p',{class:'hint'},'Режимы с идентификатором отправляют сервису постоянный ID этой установки и могут занять место в лимите устройств.');}
 function configure(sub){
  const client=clientSelect(sub.client||'');
  const dialog=openDialog(h('h2',null,'Режим подписки'),client,clientHelp(),h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>dialog.close()},'Отмена'),h('button',{class:'btn primary',onclick:async e=>{e.target.disabled=true;try{if(await action(sub.id,'client',{client:client.value}))dialog.close();}finally{e.target.disabled=false;}}},'Сохранить')));
 }
 function render(){
  const box=document.querySelector('#subscriptionList');if(!box)return;
  const subs=S.subscriptions||[],signature=JSON.stringify([subs,S.servers.map(x=>[x.id,x.subscriptionId]),[...busy]]);if(box.dataset.signature===signature)return;
  const opened=new Set([...box.querySelectorAll('details[open]')].map(e=>e.dataset.subscription));box.dataset.signature=signature;
  box.replaceChildren(...subs.map(sub=>h('details',{class:'panel subscription-row','data-subscription':sub.id,open:opened.has(sub.id)},
   h('summary',null,sub.name,h('span',{class:'hint'},' · '+count(S.servers.filter(x=>x.subscriptionId===sub.id).length,'сервер','сервера','серверов'))),
   h('p',{class:sub.error?'err-text':'hint',role:'status'},sub.error||('Обновлено: '+(sub.updated?new Date(sub.updated*1000).toLocaleString():'ещё не обновлялось'))),
   h('div',{class:'row'},h('button',{class:'btn small',disabled:busy.has(sub.id),onclick:()=>configure(sub)},'Режим подписки'),h('button',{class:'btn small',disabled:busy.has(sub.id),onclick:()=>action(sub.id,'update')},busy.has(sub.id)?'Подождите…':'Обновить'),
    h('label',{class:'check'},h('input',{type:'checkbox',checked:sub.auto,disabled:busy.has(sub.id),onchange:e=>action(sub.id,'auto',{auto:e.target.checked})}),'Автообновление раз в 6 часов'),
    h('button',{class:'btn small',disabled:busy.has(sub.id),onclick:async()=>{if(await ask('Удалить подписку «'+sub.name+'» и её серверы?'))action(sub.id,'remove');}},'Удалить подписку')))));
 }
 function add(){
  let token='',working=false;
  const client=clientSelect();
  const url=h('input',{type:'url',placeholder:'https://…',spellcheck:'false','aria-label':'Ссылка на подписку'}),name=h('input',{type:'text',maxlength:80,placeholder:'Необязательно','aria-label':'Название подписки'}),auto=h('input',{type:'checkbox',checked:true}),error=h('p',{class:'err-text',role:'status'}),preview=h('div',{class:'subscription-preview'});
  const confirm=h('button',{class:'btn primary',disabled:true,onclick:async()=>{
   if(working||!token)return;working=true;confirm.disabled=true;load.disabled=true;
   try{if(await saveNow()===false)throw new Error('Сначала сохраните изменения настроек');const r=await api('/api/subscriptions/add',{token});adopt(r.settings);renderServers(true);updateStatus();dialog.close();toast('Подписка добавлена');}
   catch(e){error.textContent=e.message;}finally{working=false;confirm.disabled=!token;load.disabled=false;}
  }},'Добавить');
  const invalidate=()=>{token='';confirm.disabled=true;preview.replaceChildren();error.textContent='';};[url,name,auto,client].forEach(e=>e.addEventListener('input',invalidate));
  const load=h('button',{class:'btn',onclick:async()=>{
   if(working)return;working=true;invalidate();load.disabled=true;url.disabled=name.disabled=auto.disabled=client.disabled=true;error.textContent='Загружаю…';
   try{const r=await api('/api/subscriptions/preview',{url:url.value,name:name.value,auto:auto.checked,client:client.value});if(!dialog.isConnected)return;token=r.token;preview.replaceChildren(h('div',null,h('b',null,'Найдено серверов: '+r.count),h('ul',null,r.names.slice(0,8).map(n=>h('li',null,n))),r.count>8?h('p',{class:'hint'},'Ещё '+(r.count-8)):null));error.textContent=r.skipped?'Пропущено неподдерживаемых строк: '+r.skipped:'';confirm.disabled=false;}
   catch(e){error.textContent=e.message;}finally{working=false;load.disabled=false;url.disabled=name.disabled=auto.disabled=client.disabled=false;}
  }},'Посмотреть серверы');
  const dialog=openDialog(h('h2',null,'Добавить подписку'),h('label',{class:'field'},h('span',null,'Ссылка на подписку'),url),h('label',{class:'field'},h('span',null,'Название'),name),h('label',{class:'check'},auto,'Автообновление раз в 6 часов'),h('label',{class:'field'},h('span',null,'Режим подписки'),client),clientHelp(),h('p',{class:'hint'},'Список ссылок, Base64 или Xray JSON. Ссылка может содержать личный ключ — не публикуйте её.'),error,preview,h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>dialog.close()},'Отмена'),load,confirm));url.focus();
 }
 return{add,render,adopt,sync};
})();
