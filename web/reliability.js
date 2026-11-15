'use strict';
window.NaivkaReliability=(()=>{
 function explain(raw){
  const s=String(raw||'').toLowerCase();
  const rules=[
   [/certificate|сертификат|x509/,'Не удалось проверить сертификат','Проверьте дату Windows и адрес сервера. Обновите подписку; если ошибка остаётся — уточните сертификат у владельца сервера.','servers'],
   [/unknown utls|fingerprint|invalid.*config|конфиг.*провер|unknown field|unsupported/,'Параметры подключения не поддерживаются','Обновите компоненты и подписку. Если профиль всё ещё отклоняется, сохраните отчёт об ошибке.','components'],
   [/unauthorized|authentication|invalid password|неверн.*парол|permission denied.*(publickey|password)|http.*407/,'Сервер отклонил данные входа','Обновите подписку или проверьте пароль и ключи сервера. Повторные попытки с теми же данными обычно не помогают.','servers'],
   [/no such host|lookup |dns|resolve/,'Не удалось определить адрес','Проверьте обычный интернет и настройки DNS. Если проблема только с одним сервером, уточните его адрес.','check'],
   [/permission|access.*denied|administrator|администратор|доступ.*запрещ/,'Недостаточно прав','Режим TUN и zapret требуют прав администратора. Для файлов также проверьте права на папку программы.','settings'],
   [/not installed|не установлен|сначала установите|не найден.*(sing-box|mieru|naive)|executable.*not found/,'Не хватает компонента','Откройте компоненты программы и установите компонент выбранного протокола.','components'],
   [/address already in use|bind:|порт.*занят/,'Локальный порт занят','Другая программа уже использует этот порт. Закройте вторую копию VPN или выберите свободный порт в настройках.','settings'],
   [/timeout|deadline exceeded|timed out|время ожидания|не ответил.*вовремя/,'Сервер не ответил вовремя','Проверьте доступность интернета, затем другой сервер. Другой VPN или фильтр сети может мешать подключению.','check'],
   [/refused|unreachable|reset by peer|network is down/,'Соединение с сервером недоступно','Проверьте адрес и порт. На своём VPS проверьте состояние службы и файрвол хостинга.','servers']
  ];
  const r=rules.find(([re])=>re.test(s));return r?{title:r[1],hint:r[2],action:r[3]}:{title:'Не удалось подключиться',hint:'Откройте проверку соединения. Если причина не определилась, сохраните отчёт из журнала.',action:'check'};
 }
 function action(kind){
  if(kind==='check'){openDiag('check');return;}
  if(kind==='servers'){go('servers');return;}
  if(kind==='components'){settingsCategory='О программе';settingsPositions.set(settingsCategory,{tab:1,page:0});}go('settings');
 }
 function errorNotice(raw){
  const e=explain(raw),label={servers:'Открыть серверы',components:'Открыть компоненты',settings:'Открыть настройки',check:'Проверить соединение'}[e.action];
  return h('div',{class:'notice error'},h('div',{class:'grow'},h('b',null,e.title),h('p',null,e.hint),h('details',null,h('summary',null,'Технические сведения'),h('pre',null,raw))),h('div',{class:'row'},h('button',{class:'btn small',onclick:()=>action(e.action)},label),h('button',{class:'btn small',onclick:()=>{diagTab='logs';go('diag');}},'Журнал и отчёт')));
 }
 let measuring=false,lastReport=null;
 function metrics(intervals,elapsed){
  const sorted=[...intervals].sort((a,b)=>a-b),q=p=>sorted.length?sorted[Math.min(sorted.length-1,Math.floor(sorted.length*p))]:null;
  return {frames:intervals.length,fps:elapsed>0?intervals.length*1000/elapsed:null,medianMs:q(.5),p95Ms:q(.95),maxMs:q(1),framesOver50ms:intervals.filter(x=>x>50).length};
 }
 function showReport(report){
  lastReport=report;const f=report.frames,round=n=>n==null?'нет данных':Number(n).toFixed(1),mib=n=>n==null?'нет данных':(n/1048576).toFixed(1)+' МБ';
  const d=openDialog(h('h2',null,'Результат замера'),h('p',null,report.cancelled?'Замер прерван: '+report.cancelled:'Длительность: '+round(report.durationMs/1000)+' с'),
   h('p',null,'Кадров/с: '+round(f.fps)+' · 95% кадров быстрее '+round(f.p95Ms)+' мс · максимум '+round(f.maxMs)+' мс · кадров дольше 50 мс: '+f.framesOver50ms),
   h('p',null,'CPU управляющего процесса: '+round(report.cpuPercent)+'% одного ядра. Память Go: '+mib(report.backend?.heapBytes)+'. Память JavaScript: '+mib(report.jsHeapBytes)),
   h('p',{class:'hint'},'CPU и память Go относятся к управляющему процессу Naivka, без sing-box, WebView и внешних компонентов. Результаты зависят от монитора и нагрузки компьютера.'),
   h('p',{class:'hint'},report.cancelled?'Для оценки плавности выполните полный замер.':f.framesOver50ms?'Есть длинные кадры. Сравните с минимальными эффектами и с меньшей нагрузкой других программ.':'В этом коротком замере длинные кадры не обнаружены.'),
   h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>download('naivka-performance.json',JSON.stringify(report,null,2),'application/json')},'Сохранить замер'),h('button',{class:'btn',onclick:()=>{d.close();go('settings');toast('Внешний вид → Эффекты и звуки → Минимум');}},'Настройки эффектов'),h('button',{class:'btn primary',onclick:()=>d.close()},'Закрыть')));
 }
 function measure(){
  if(measuring){toast('Замер уже выполняется');return;}
  const d=openDialog(h('h2',null,'Проверка плавности'),h('p',null,'15 секунд на главном экране. Можно двигать окно и пользоваться ветвями. Открытие диалога или сворачивание прервёт замер, чтобы результат не вводил в заблуждение.'),h('p',{class:'hint'},'Измерение не меняет подключение. После него можно сохранить результат без адресов серверов и ключей.'),h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>d.close()},'Отмена'),h('button',{class:'btn primary',onclick:()=>{d.close();go('overview');sample();}},'Начать замер')));
 }
 async function sample(){
  if(measuring)return;measuring=true;let before=null;try{before=await api('/api/performance');}catch(_){}
  if(document.hidden){measuring=false;toast('Окно скрыто. Повторите замер в открытом окне.',true);return;}
  let finished=false,raf=0,timer=0,previous=0,intervals=[],longTasks=0,longTaskMs=0,observer=null;const start=performance.now();
  const status=h('span',null,'Замер плавности: 15 с'),cancel=h('button',{class:'btn small',onclick:()=>finish('отменён пользователем')},'Остановить');
  const panel=h('aside',{class:'notice',role:'status',style:'position:fixed;bottom:16px;right:16px;z-index:40;max-width:calc(100vw - 32px)'},status,cancel);document.body.append(panel);
  const visibility=()=>{if(document.hidden)finish('окно скрыто');};document.addEventListener('visibilitychange',visibility);
  try{observer=new PerformanceObserver(list=>{for(const e of list.getEntries()){longTasks++;longTaskMs+=e.duration;}});observer.observe({type:'longtask',buffered:false});}catch(_){observer=null;}
  const step=now=>{
   if(finished)return;if(view!=='overview'){finish('открыт другой раздел');return;}if(document.querySelector('dialog[open]')){finish('открыт диалог');return;}
   if(previous&&intervals.length<12000)intervals.push(now-previous);previous=now;const label='Замер плавности: '+Math.max(0,Math.ceil((15000-(now-start))/1000))+' с';if(status.textContent!==label)status.textContent=label;
   if(now-start>=15000){finish('');return;}raf=requestAnimationFrame(step);
  };
  async function finish(cancelled){
   if(finished)return;finished=true;cancelAnimationFrame(raf);clearTimeout(timer);observer?.disconnect();document.removeEventListener('visibilitychange',visibility);panel.remove();const elapsed=performance.now()-start;
   let backend=null;try{backend=await api('/api/performance');}catch(_){}
   const dt=backend&&before?backend.at-before.at:0;const cpu=dt>0&&backend.cpuAvailable&&before.cpuAvailable?Math.max(0,(backend.cpuSeconds-before.cpuSeconds)*100000/dt):null;
   const report={version:SELF?.version||'unknown',createdAt:new Date().toISOString(),durationMs:elapsed,cancelled,frames:metrics(intervals,elapsed),cpuPercent:cpu,backend,jsHeapBytes:performance.memory?.usedJSHeapSize??null,longTasks:observer?longTasks:null,longTaskMs:observer?longTaskMs:null,effects:S.experience?.effects||'balanced',reducedMotion:matchMedia('(prefers-reduced-motion: reduce)').matches};measuring=false;
   if(document.hidden){lastReport=report;toast('Замер прерван: окно скрыто. Результат доступен в «Последний замер».');return;}showReport(report);
  }
  raf=requestAnimationFrame(step);timer=setTimeout(()=>finish('истёк предельный срок ожидания кадров'),20000);
 }
 async function recovery(){
  const status=h('p',{role:'status'},'Проверяю операции…');let revision='';
  const confirm=h('button',{class:'btn primary',disabled:true,onclick:async()=>{confirm.disabled=true;try{await api('/api/app/recovery',{revision,confirm:true});status.textContent='Операция завершена. Текущая версия сохранена, резервные файлы перенесены в version-backups. Повторите обновление.';APPUP=await api('/api/app/update');refreshAppUpdate();}catch(e){status.textContent=e.message;}}},'Завершить прерванную операцию');
  const d=openDialog(h('h2',null,'Восстановление обновления'),status,h('p',{class:'hint'},'Действие завершает оставшуюся операцию для работающей сейчас версии. Прежний EXE и файлы операции сохраняются в архиве. Выбрать другую версию можно кнопкой «Выбрать версию / откат».'),h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>d.close()},'Закрыть'),confirm));
  try{const r=await api('/api/app/recovery');if(!d.isConnected)return;revision=r.revision;status.textContent=!r.pending?'Незавершённых операций нет.':r.ready?'Найдено прерванных операций: '+r.pending+'. Их процессы завершились. Можно сохранить текущую версию и убрать блокировку обновления.':r.reason;confirm.disabled=!r.ready;}catch(e){status.textContent=e.message;}
 }
 return{explain,errorNotice,measure,metrics,recovery,last:()=>lastReport?showReport(lastReport):toast('Сначала выполните замер')};
})();
