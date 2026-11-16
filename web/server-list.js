'use strict';
window.ServerListUI=(()=>{
 const state={query:'',favorites:false,replies:false,protocol:'',compare:new Set()};
 const names=new Intl.Collator(undefined,{numeric:true,sensitivity:'base'});
 const measured=d=>!!d&&!d.pending&&!d.error&&Number.isFinite(d.ms)&&d.ms>0;
 const sortMode=()=>['ping','ping-desc','name'].includes(S.serverSort)?S.serverSort:'manual';
 function order(servers,mode,delay){
  const rows=servers.map((server,index)=>({server,index,delay:mode==='ping'||mode==='ping-desc'?delay(server.id):null}));
  return rows.sort((a,b)=>{
   if(mode==='ping'||mode==='ping-desc'){
    const goodA=measured(a.delay),goodB=measured(b.delay);if(goodA!==goodB)return goodA?-1:1;
    if(goodA&&a.delay.ms!==b.delay.ms)return (a.delay.ms-b.delay.ms)*(mode==='ping-desc'?-1:1);
   }else if(mode==='name'){
    const value=names.compare(a.server.name||a.server.host||'',b.server.name||b.server.host||'');if(value)return value;
   }else if(!!a.server.fav!==!!b.server.fav)return a.server.fav?-1:1;
   return a.index-b.index;
  }).map(x=>x.server);
 }
 function accept(sv,query){
  return (!query||[sv.name,sv.host,protoName(sv)].join(' ').toLowerCase().includes(query))&&(!state.favorites||sv.fav)&&(!state.replies||measured(delayFor(sv.id)))&&(!state.protocol||sv.type===state.protocol);
 }
 function filtered(){return state.favorites||state.replies||!!state.protocol;}
 function reset(){state.query='';state.favorites=false;state.replies=false;state.protocol='';const input=document.querySelector('#serverSearch');if(input)input.value='';renderServers(true);input?.focus();}
 function tools(){
  const sort=h('select',{id:'serverSort','aria-label':'Сортировка серверов',onchange:e=>{S.serverSort=e.target.value;save();renderServers(true);}},
   h('option',{value:'manual'},'Мой порядок'),h('option',{value:'ping'},'Пинг: быстрые первыми'),h('option',{value:'ping-desc'},'Пинг: медленные первыми'),h('option',{value:'name'},'По имени'));
  sort.value=sortMode();
  return h('div',null,h('div',{class:'server-tools'},h('label',{class:'field server-sort-field'},h('span',null,'Сортировка'),sort),
   h('button',{id:'serverFavorites',class:'btn small','aria-pressed':String(state.favorites),onclick:()=>{state.favorites=!state.favorites;renderServers(true);}},'Избранное'),
   h('button',{id:'serverReplies',class:'btn small','aria-pressed':String(state.replies),title:'Есть положительный замер времени ответа. TCP и TLS сами по себе не подтверждают работу VPN. Непроверенные серверы скрываются.',onclick:()=>{state.replies=!state.replies;renderServers(true);}},'С ответом'),
   h('label',{class:'field server-sort-field'},h('span',null,'Протокол'),h('select',{id:'serverProtocol','aria-label':'Фильтр по протоколу',onchange:e=>{state.protocol=e.target.value;renderServers(true);}})),
   h('button',{class:'btn small',id:'serverReset',onclick:reset},'Сбросить фильтры')),
   h('div',{class:'row server-list-status'},h('span',{id:'serverCount',class:'hint',role:'status','aria-live':'polite'}),h('span',{class:'spacer'}),
    h('button',{class:'btn small',id:'serverCompare',disabled:state.compare.size<2,onclick:compareDialog},'Сравнить'),h('button',{class:'btn small',id:'serverCompareClear','aria-label':'Очистить сравнение',title:'Очистить сравнение',onclick:()=>{state.compare.clear();renderServers(true);}},'Очистить')),
   h('p',{class:'hint',id:'serverOrderHint'}));
 }
 function refresh(){
  const ids=new Set(S.servers.map(s=>s.id));for(const id of state.compare)if(!ids.has(id))state.compare.delete(id);
  const protocol=document.querySelector('#serverProtocol');if(!protocol)return;
  const types=[...new Set(S.servers.map(s=>s.type))].sort();if(state.protocol&&!types.includes(state.protocol))state.protocol='';
  const signature=JSON.stringify(types);if(protocol.dataset.signature!==signature){protocol.replaceChildren(h('option',{value:''},'Все протоколы'),...types.map(type=>h('option',{value:type},type==='vless'?'VLESS / Reality':type==='naive'?'NaiveProxy':protoName({type}))));protocol.dataset.signature=signature;}protocol.value=state.protocol;
  document.querySelector('#serverSort').value=sortMode();
  for(const [id,value]of [['serverFavorites',state.favorites],['serverReplies',state.replies]])document.querySelector('#'+id).setAttribute('aria-pressed',String(value));
  document.querySelector('#serverReset').disabled=!filtered()&&!state.query;
  document.querySelector('#serverReset').hidden=!filtered()&&!state.query;
  const compare=document.querySelector('#serverCompare');compare.textContent='Сравнить ('+state.compare.size+'/3)';compare.disabled=state.compare.size<2;compare.hidden=!state.compare.size;document.querySelector('#serverCompareClear').disabled=!state.compare.size;document.querySelector('#serverCompareClear').hidden=!state.compare.size;
  const count=S.servers.filter(s=>accept(s,state.query.trim().toLowerCase())).length,answered=S.servers.filter(s=>measured(delayFor(s.id))).length;
  const countText='Показано '+count+' из '+S.servers.length+' · с ответом '+answered;
  const counter=document.querySelector('#serverCount');if(counter.textContent!==countText)counter.textContent=countText;
  const hint=sortMode()==='manual'?'Избранные выше. Перетаскивайте строки; ⇄ — сравнить.':sortMode()==='name'?'Список отсортирован по имени. Ручной порядок сохранён.':'Есть замер — выше, без ответа — в конце. Ручной порядок сохранён.';
  const label=document.querySelector('#serverOrderHint');if(label.textContent!==hint)label.textContent=hint;
 }
 function comparisonButton(sv){
  const selected=state.compare.has(sv.id);
  return h('button',{class:'btn small server-compare-pick',type:'button','data-server-action':'compare','aria-label':(selected?'Убрать из сравнения: ':'Добавить в сравнение: ')+sv.name,'aria-pressed':String(selected),title:selected?'Убрать из сравнения':'Добавить в сравнение (до трёх серверов)',onclick:()=>{if(selected)state.compare.delete(sv.id);else{if(state.compare.size>=3){toast('В сравнении уже три сервера. Уберите один, чтобы добавить другой.');return;}state.compare.add(sv.id);}renderServers(true);}},s('svg',{viewBox:'0 0 24 24','aria-hidden':'true'},s('path',{d:'M4 7h14m-4-4 4 4-4 4M20 17H6m4-4-4 4 4 4'})));
 }
 function compareDialog(){
  const rows=h('tbody'),status=h('p',{class:'hint',role:'status'});let working=false;
  const draw=(message='')=>{
   const servers=S.servers.filter(s=>state.compare.has(s.id));
   rows.replaceChildren(...servers.map(sv=>{
    const d=delayFor(sv.id),kind=d?.kind==='tcp'?'TCP':d?.kind==='tls'?'TLS':'через сервер';
    const geo=srvGeo[sv.id]?.info,place=geo?.country||nameCountry(sv.name)||'не определена';
    const choose=h('button',{class:'btn small',disabled:working||S.selected===sv.id,onclick:async()=>{if(working)return;working=true;draw();if(await selectServer(sv.id))dialog.close();else{working=false;draw('Не удалось выбрать сервер. Прежний выбор сохранён.');}}},S.selected===sv.id?'Выбран':'Выбрать');
    return h('tr',null,h('td',null,sv.name),h('td',null,protoName(sv)),h('td',null,measured(d)?d.ms+' мс · '+kind:d?.pending?'проверяется':d?.error||'не проверен'),h('td',null,place),h('td',null,choose));
   }));status.textContent=message||(servers.length<2?'Часть серверов удалена. Закройте окно и выберите новые для сравнения.':'');
  };
  const dialog=openDialog(h('h2',null,'Сравнение серверов'),h('p',{class:'hint'},'Сравнивайте замеры одинакового вида: TCP, TLS или запрос через сервер. Один пинг не определяет скорость и надёжность VPN. Выбор действует так же, как нажатие на сервер в списке.'),
   h('div',{class:'server-comparison-scroll',tabindex:0,role:'region','aria-label':'Таблица сравнения серверов'},h('table',{class:'table server-comparison'},h('thead',null,h('tr',null,...['Сервер','Протокол','Пинг','Страна',''].map(x=>h('th',null,x)))),rows)),status,
   h('div',{class:'row end'},h('button',{class:'btn',title:'Показать последние уже выполненные замеры. Для новой проверки используйте «Проверить все».',onclick:()=>draw()},'Обновить данные'),h('button',{class:'btn primary',onclick:()=>dialog.close()},'Закрыть')));dialog.classList.add('server-comparison-dialog');draw();
 }
 function captureFocus(list){
  const active=document.activeElement,card=active?.closest?.('.scard');if(!card||!list.contains(card))return ()=>{};
  const id=card.dataset.id,action=active.dataset.serverAction;
  return ()=>{if(active.isConnected)return;const next=[...list.querySelectorAll('.scard')].find(c=>c.dataset.id===id);const target=action?next?.querySelector('[data-server-action="'+action+'"]'):active===card?next:null;target?.focus({preventScroll:true});};
 }
 return {state,sortMode,order,accept,filtered,tools,refresh,comparisonButton,captureFocus,measured};
})();
