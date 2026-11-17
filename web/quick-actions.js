'use strict';
// UI actions reuse the existing selection, settings and routing transactions.
window.QuickActions=(()=>{
 function serverButton(sv){return h('button',{class:'btn small',type:'button','data-server-action':'menu','aria-label':'Действия: '+sv.name,'aria-haspopup':'menu',title:'Действия с сервером',onclick:e=>serverMenu(sv.id,e.currentTarget)},'⋯');}
 function bindServer(card,id){
  card.addEventListener('contextmenu',e=>{e.preventDefault();e.stopPropagation();serverMenu(id,card,{x:e.clientX,y:e.clientY});});
  card.addEventListener('keydown',e=>{if(e.key==='ContextMenu'||(e.shiftKey&&e.key==='F10')){e.preventDefault();e.stopPropagation();serverMenu(id,card);}});
 }
 function serverMenu(id,anchor,point){
  const sv=S.servers.find(x=>x.id===id);if(!sv)return;closeRoutePop();
  const action=(label,run,disabled=false)=>h('button',{role:'menuitem',disabled,onclick:async()=>{closeRoutePop();const current=S.servers.find(x=>x.id===id);if(!current){toast('Сервер уже удалён',true);return;}try{await run(current);}catch(e){toast(e.message,true);}}},label);
  const pop=h('div',{class:'route-pop server-action-menu',role:'menu','aria-label':'Действия с сервером'},h('b',null,sv.name||sv.host),
   action(ST?.running?'Переключиться на сервер':'Подключить',async()=>{if(busy)return;if(zapretOnly()){toast('Сначала включите режим «Сервер + zapret» на главной.');return;}if(!await saveNow())return;if(await selectServer(id)){if(!ST?.running&&!ST?.starting&&!ST?.reviving&&!ST?.blocked)await toggleConnect();}},busy||ST?.starting||ST?.reviving||ST?.blocked),
   action('Проверить пинг',()=>testServers(id),!!delayFor(id)?.pending),
   action('Переименовать',rename,!!sv.subscriptionId),
   action(sv.fav?'Убрать из избранного':'В избранное',async current=>{const before=current.fav;current.fav=!before;save(0);if(!await saveNow())current.fav=before;renderServers(true);}),
   action(ServerListUI.state.compare.has(id)?'Убрать из сравнения':'Добавить в сравнение',current=>ServerListUI.comparisonButton(current).click()),
   action('Скопировать ссылку',async()=>{if(!await saveNow())return;const r=await api('/api/link?id='+encodeURIComponent(id));await navigator.clipboard.writeText(r.link);toast('Ссылка скопирована. Она содержит ключ подключения.');}),
   action('Подробнее',current=>WorkspaceUI.server(current)));
  if(busy||ST?.starting||ST?.reviving||ST?.blocked)pop.append(h('small',null,ST?.blocked?'Переключение недоступно, пока действует защита от утечек.':'Дождитесь завершения подключения, чтобы выбрать другой сервер.'));
  if(delayFor(id)?.pending)pop.append(h('small',null,'Проверка пинга уже выполняется.'));
  if(sv.subscriptionId)pop.append(h('small',null,'Название задаётся подпиской.'));
  document.body.append(pop);routePop=pop;routeAnchor=anchor;
  const r=anchor.getBoundingClientRect(),x=point?.x??r.left,y=point?.y??r.bottom+6;
  pop.style.left=Math.max(8,Math.min(x,innerWidth-pop.offsetWidth-8))+'px';pop.style.top=Math.max(8,Math.min(y,innerHeight-pop.offsetHeight-8))+'px';
  const buttons=[...pop.querySelectorAll('button:not(:disabled)')];buttons[0]?.focus();
  pop.addEventListener('keydown',e=>{if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){e.preventDefault();const i=buttons.indexOf(document.activeElement),n=e.key==='Home'?0:e.key==='End'?buttons.length-1:(i+(e.key==='ArrowDown'?1:-1)+buttons.length)%buttons.length;buttons[n]?.focus();}else if(e.key==='Tab')closeRoutePop();});
 }
 function rename(sv){
  const field=h('input',{value:sv.name||'',maxlength:160,'aria-label':'Название сервера'}),error=h('p',{class:'err-text',role:'status'});
  const apply=h('button',{class:'btn primary',onclick:async()=>{const name=field.value.trim(),current=S.servers.find(x=>x.id===sv.id);if(!name){error.textContent='Введите название';field.focus();return;}if(!current||current.subscriptionId){error.textContent='Сервер удалён или управляется подпиской';return;}apply.disabled=true;const old=current.name;current.name=name;save(0);if(await saveNow()){renderServers(true);dlg.close();}else{current.name=old;error.textContent='Не удалось сохранить название';apply.disabled=false;}}},'Сохранить');
  const dlg=openDialog(h('h2',null,'Переименовать сервер'),h('label',{class:'field'},h('span',null,'Название'),field),error,h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>dlg.close()},'Отмена'),apply));field.focus();field.select();
 }
 addEventListener('resize',()=>{if(routePop?.classList.contains('server-action-menu'))closeRoutePop();});
 addEventListener('blur',()=>{if(routePop?.classList.contains('server-action-menu'))closeRoutePop();});
 const baseExe=value=>(value||'').split(/[\\/]/).pop().toLowerCase();
 function matches(item,c){
  if(item.kind==='process')return baseExe(c.metadata?.processPath)===baseExe(item.exe);
  const host=(c.metadata?.host||'').toLowerCase(),domains=SERVICES.find(s=>s.name===item.key)?.domains||branchLeaves[item.key]?.domains||[item.key];
  return domains.some(d=>host===d.toLowerCase()||host.endsWith('.'+d.toLowerCase()));
 }
 function branch(item,anchor){
  closeRoutePop();const process=item.kind==='process';let loading=false,working=false;
  const facts=h('div',{class:'quick-route-facts'}),connections=h('div',{class:'quick-connections'}),error=h('p',{class:'err-text',role:'status'}),buttons=[];
  const updateFacts=()=>{
   const group=process?procList().find(x=>baseExe(x.exe)===baseExe(item.exe)):siteGroups().find(x=>x.key===item.key);
   const routes=Object.keys(group?.routes||{}).map(x=>ROUTE_SAY[x]||x);
   facts.replaceChildren(h('p',null,'Скорость: '+(ST?.running?fmtSpeed(group?.speed||0):'нет подключения')),h('p',null,'За сеанс: '+fmtBytes(group?.total||0)),h('p',null,'Текущий путь: '+(ST?.running&&routes.length?routes.join(', '):'нет активного трафика')));
  };
  const load=async()=>{
   if(loading)return;loading=true;refresh.disabled=true;updateFacts();connections.replaceChildren(h('p',{class:'hint'},'Загружаю соединения…'));
   try{
    if(!ST?.running){connections.replaceChildren(h('p',{class:'hint'},'Подключитесь, чтобы увидеть соединения.'));return;}
    const result=await api('/api/conns');if(!dlg.isConnected)return;
    const rows=(result.connections||[]).filter(c=>matches(item,c));
    connections.replaceChildren(h('p',{class:'hint'},'Соединений в снимке: '+rows.length+(rows.length>40?' · показаны первые 40':'')),...rows.slice(0,40).map(c=>h('div',{class:'quick-connection'},h('b',null,connHost(c)),h('small',null,[connProc(c),routeOf(c).text,'↓ '+fmtBytes(c.download||0),'↑ '+fmtBytes(c.upload||0)].filter(Boolean).join(' · ')))));
   }catch(_){connections.replaceChildren(h('p',{class:'err-text'},'Не удалось получить соединения. Попробуйте обновить.'));}finally{loading=false;refresh.disabled=false;}
  };
  const refresh=h('button',{class:'btn small',onclick:load},'Обновить данные');
  const rule=process?procRule(item.exe):siteRule(branchLeaves[item.key]||{key:item.key,domains:[item.key]});
  for(const [key,label]of ROUTE_OPTS.filter(([k])=>k!=='zapret'||zapretOn())){
   const button=h('button',{class:'btn small','aria-pressed':String((rule?.action||'auto')===key),onclick:async()=>{
    if(working)return;working=true;buttons.forEach(b=>b.disabled=true);error.textContent='';
    try{const ok=process?await setProcRoute(item.exe,key):key==='auto'?await siteClear(item.key):await branchDrop(item.key,key);
     if(ok){if(key==='auto')diagramClearLeaf(item.key);else diagramMoveLeaf(item.key,key);dlg.close();}else error.textContent='Не удалось изменить маршрут. Прежнее правило сохранено.';
    }catch(e){error.textContent=e.message;}finally{working=false;buttons.forEach(b=>b.disabled=false);}
   }},label);buttons.push(button);
  }
  const dlg=openDialog(h('h2',null,item.label||item.exe||item.key),h('p',{class:'hint'},process?'Правило действует на всю программу.':'Правило действует на все адреса этого сервиса.'),facts,process?window.InterfacePolish?.pinButton(item.exe):null,h('h3',null,'Куда направлять'),h('div',{class:'row'},...buttons),h('p',{class:'hint'},'Кнопка с отметкой показывает сохранённое правило. Список соединений — снимок на момент обновления; существующие соединения могут сохранять прежний путь.'),error,h('details',null,h('summary',null,'Соединения'),connections),h('div',{class:'row end mt'},refresh,h('button',{class:'btn',onclick:()=>dlg.close()},'Закрыть')));
  dlg.classList.add('quick-route-dialog');dlg.addEventListener('close',()=>{if(anchor?.isConnected)anchor.focus({preventScroll:true});},{once:true});load();
 }
 let eventFilter='all',eventQuery='';
 function journal(technical){
  const list=h('div',{id:'eventJournal',class:'event-journal'});
  const tools=h('div',{class:'row'},h('input',{type:'search','aria-label':'Поиск событий',placeholder:'Найти событие',value:eventQuery,oninput:e=>{eventQuery=e.target.value;refreshEvents();}}),h('select',{'aria-label':'Тип событий',onchange:e=>{eventFilter=e.target.value;refreshEvents();}},...[['all','Все события'],['problems','Предупреждения и ошибки'],['ok','Успешные действия'],['info','Информация']].map(([value,label])=>h('option',{value,selected:value===eventFilter},label))));
  const root=h('div',null,h('h2',null,'События'),h('p',{class:'hint'},'Последние 60 событий за время работы Naivka. После перезапуска история очищается.'),tools,list,h('details',{class:'technical-journal mt'},h('summary',null,'Технический журнал и отчёт'),technical));
  requestAnimationFrame(()=>refreshEvents());return root;
 }
 function refreshEvents(){
  const list=document.querySelector('#eventJournal');if(!list)return;
  const events=(STATS?.events||[]).slice().reverse().filter(e=>(eventFilter==='all'||eventFilter==='problems'&&['warn','error'].includes(e.kind)||e.kind===eventFilter)&&[e.text,e.detail].join(' ').toLowerCase().includes(eventQuery.trim().toLowerCase()));
  const sig=JSON.stringify(events);if(list.dataset.signature===sig)return;list.dataset.signature=sig;
  const expanded=new Set([...list.querySelectorAll('article')].filter(a=>a.querySelector('details[open]')).map(a=>a.dataset.event));
  list.replaceChildren(...(events.length?events.map(e=>{
   const labels={ok:'Успешно',info:'Информация',warn:'Предупреждение',error:'Ошибка'},text=String(e.text||''),title=text.split('\n')[0],short=title.length>180?title.slice(0,180)+'…':title,detail=e.detail||(short!==text?text:'');
   return h('article',{class:'journal-entry','data-event':JSON.stringify(e)},h('div',{class:'row'},h('time',{datetime:new Date(e.t*1000).toISOString()},new Date(e.t*1000).toLocaleString(LOCALE)),h('span',{class:'event-kind '+(['ok','info','warn','error'].includes(e.kind)?e.kind:'info')},labels[e.kind]||'Информация')),h('p',null,short),detail?h('details',{open:expanded.has(JSON.stringify(e))},h('summary',null,'Подробности'),h('pre',null,detail)):null);
  }):[h('p',{class:'empty'},'Событий по этому фильтру нет.')]));
 }
 return {serverButton,bindServer,serverMenu,branch,matches,journal,refreshEvents};
})();
