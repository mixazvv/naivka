'use strict';
window.InterfacePolish=(()=>{
 const prefs=()=>{S.experience ||= {};return S.experience;};
 const pins=()=>prefs().pinnedPrograms||[];
 function configure(){if(S)document.documentElement.dataset.density=prefs().density==='compact'?'compact':'comfortable';}
 function help(title,text){return h('button',{class:'btn small help-inline',type:'button','aria-label':'Подробнее: '+title,onclick:()=>{const d=openDialog(h('h2',null,title),h('p',null,text),h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>d.close()},'Понятно')));}},'?');}
 function settings(){return h('div',null,h('label',{class:'field'},h('span',null,'Плотность интерфейса'),h('select',{'aria-label':'Плотность интерфейса',onchange:e=>{prefs().density=e.target.value;configure();save();}},h('option',{value:'comfortable',selected:prefs().density!=='compact'},'Просторный'),h('option',{value:'compact',selected:prefs().density==='compact'},'Компактный'))),h('p',{class:'hint'},'Меняет отступы и высоту строк. Размер текста и схема маршрутов сохраняются.'),h('div',{class:'row'},h('button',{class:'btn',onclick:managePins},'Закреплённые программы'),help('Эффекты и доступность','Переходы короткие и не мешают нажатию. Если в Windows включено уменьшенное движение или эффекты Naivka выключены, декоративные переходы отключаются.')));}
 let pinBusy=false;
 async function togglePin(exe){
  exe=(exe||'').trim().toLowerCase();if(!/^[^\\/:\x00-\x1f]{1,124}\.exe$/i.test(exe)){toast('Укажите имя процесса, например firefox.exe, без пути.',true);return false;}
  if(pinBusy)return false;const before=[...pins()],has=before.includes(exe);if(!has&&before.length>=12){toast('Можно закрепить до 12 программ.');return false;}
  pinBusy=true;prefs().pinnedPrograms=has?before.filter(p=>p!==exe):[...before,exe];save(0);
  try{if(!await saveNow()){prefs().pinnedPrograms=before;return false;}renderPins();return true;}finally{pinBusy=false;}
 }
 function pinButton(exe){const b=h('button',{class:'btn small',type:'button',onclick:async()=>{b.disabled=true;try{await togglePin(exe);draw();}finally{b.disabled=false;}}});const draw=()=>{const has=pins().includes(exe.toLowerCase());b.textContent=has?'Открепить с главной':'Закрепить на главной';b.setAttribute('aria-pressed',String(has));};draw();return b;}
 function managePins(){
  const list=h('div',{class:'pinned-manager'}),input=h('input',{'aria-label':'Имя процесса',placeholder:'Например, firefox.exe',maxlength:128});
  const draw=()=>{const available=[...new Set([...pins(),...procList().map(p=>p.exe.toLowerCase()),'chrome.exe','firefox.exe','browser.exe','discord.exe','steam.exe'])];list.replaceChildren(...available.map(exe=>h('div',{class:'row'},h('span',null,procName(exe)||exe),pinButton(exe))));};
  const add=h('button',{class:'btn',onclick:async()=>{add.disabled=true;try{const name=input.value.trim().toLowerCase();if(pins().includes(name)){toast('Программа уже закреплена');return;}if(await togglePin(name)){input.value='';draw();}}finally{add.disabled=false;}}},'Закрепить');
  const d=openDialog(h('h2',null,'Закреплённые программы'),h('p',{class:'hint'},'До 12 программ. Закрепление не меняет маршруты и не запускает программу. Для другой программы введите имя её процесса, без пути.'),list,h('div',{class:'row mt'},input,add),h('div',{class:'row end mt'},h('button',{class:'btn',onclick:()=>d.close()},'Готово')));draw();
 }
 function mount(root){
  const idle=h('section',{class:'home-idle-card',id:'homeIdle'},h('div',{class:'idle-computer','aria-hidden':'true'},svgIcon('pc')),h('div',null,h('h3',null,'Готово к подключению'),h('p',{id:'idleServer',class:'hint'})),h('button',{class:'btn primary',id:'idleConnect'},'Подключить'));
  root.insertBefore(idle,root.querySelector('#lanesCard'));
  root.append(h('section',{class:'home-pins',id:'homePins'},h('div',{class:'row'},h('b',null,'Закреплённые программы'),h('button',{class:'btn small',onclick:managePins},'Настроить'),help('Маршрут программы','Закреплённая программа доступна даже без трафика. Нажмите на неё, чтобы выбрать маршрут. В режиме системного прокси видны только соединения, попавшие в Naivka; для перехвата остального трафика используется TUN.')),h('div',{class:'row',id:'pinnedPrograms'})));
  renderPins();status(phase());
 }
 function renderPins(){const el=document.querySelector('#pinnedPrograms');if(!el||!S)return;el.closest('.home-pins').hidden=!pins().length;const sig=JSON.stringify([pins(),S.rules.filter(r=>r.kind==='process')]);if(el.dataset.sig===sig)return;el.dataset.sig=sig;
  el.replaceChildren(...pins().map(exe=>{const rule=procRule(exe),name=procName(exe)||exe;return h('button',{class:'btn pin-program',onclick:e=>QuickActions.branch({kind:'process',exe,key:'proc:'+exe,label:name},e.currentTarget)},h('b',null,name),h('small',null,rule?ROUTE_SAY[rule.action]||'блокируется':'Как решит Naivka'));}),!pins().length?h('span',{class:'hint'},'Закрепите браузер, игру или мессенджер для быстрого выбора маршрута.'):null);
 }
 function status(p){
  const root=document.querySelector('.stage-inner');if(!root)return;const idle=p==='off'&&!zapretOn()&&!tgOn();root.classList.toggle('home-idle',idle);
  const label=document.querySelector('#idleServer');if(label&&S)label.textContent=S.servers.length||S.wl.enabled?'Выбран: '+currentServer().name:'Добавьте сервер в разделе «Серверы».';
  const b=document.querySelector('#idleConnect');if(b){const missing=!S.servers.length&&!S.wl.enabled&&!zapretOnly();b.textContent=missing?'Добавить сервер':'Подключить';b.onclick=missing?()=>go('servers'):toggleConnect;b.disabled=busy;}
  renderPins();
 }
 let undoTimer=0;
 function routeChanged(before,after,match,label,key){
  const old=before.filter(match).map(r=>({...r})),applied=after.filter(match).map(r=>({...r}));if(JSON.stringify(old)===JSON.stringify(applied))return;
  clearTimeout(undoTimer);document.querySelector('#routeUndo')?.remove();
  const close=()=>{clearTimeout(undoTimer);box.remove();};
  const button=h('button',{class:'btn small',onclick:async()=>{
   if(branchSaving)return;
   if(JSON.stringify(S.rules.filter(match))!==JSON.stringify(applied)){toast('Правило уже изменилось. Отмена не затронула новые настройки.',true);close();return;}
   button.disabled=true;branchSaving=true;const previous=S.rules,next=[...previous.filter(r=>!match(r)),...old];S.rules=next;save(0);
   let ok=false;try{ok=await saveNow();}finally{branchSaving=false;}
   if(!ok){if(S.rules===next)S.rules=previous;button.disabled=false;toast('Не удалось отменить. Можно повторить.',true);return;}
   diagramClearLeaf(key);renderPins();toast('Прежний маршрут восстановлен');close();
  }},'Отменить');
  const box=h('div',{id:'routeUndo',class:'route-undo',role:'status'},h('span',null,label+': маршрут изменён'),button,h('button',{class:'btn small','aria-label':'Закрыть подтверждение',onclick:close},'×'));document.body.append(box);
  undoTimer=setTimeout(close,15000);box.addEventListener('focusin',()=>clearTimeout(undoTimer));box.addEventListener('focusout',()=>{undoTimer=setTimeout(close,15000);});renderPins();
 }
 document.addEventListener('toggle',e=>{if(!(e.target instanceof HTMLDetailsElement)||!e.target.open||document.hidden||window.NaivkaExperience?.motion.matches)return;for(const child of e.target.children)if(child.tagName!=='SUMMARY')child.animate?.([{opacity:0,transform:'translateY(-3px)'},{opacity:1,transform:'translateY(0)'}],{duration:140,easing:'ease-out'});},true);
 return {configure,settings,help,mount,status,pinButton,togglePin,renderPins,routeChanged};
})();
