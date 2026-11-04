'use strict';
// Presentation only. Connection actions keep using the existing lifecycle/API.
const WorkspaceUI=(()=>{
  const prefs=()=>{S.experience ||= {};return S.experience;};
  const compact=()=>![1,2].includes(S.layout?.version)&&prefs().home!=='full';
  function apply(root=document.querySelector('.stage-inner')){
    if(!root||!S)return;
    const was=root.classList.contains('home-compact'),small=compact();root.classList.toggle('home-compact',small);root.classList.toggle('diagram-expanded',!!prefs().expanded);
    const params=root.querySelector('.home-parameters');if(params){if(!small)params.open=true;else if(!was)params.open=false;}
    const power=root.querySelector('#connectBtn');if(power){power.tabIndex=0;power.removeAttribute('aria-hidden');}
    const expand=root.querySelector('#diagramExpand');if(expand){expand.textContent=prefs().expanded?'Свернуть схему':'Развернуть схему';expand.setAttribute('aria-expanded',String(!!prefs().expanded));}
  }
  function home(root){
    const head=root.querySelector('.stage-head'),controls=root.querySelector('.stage-controls');
    const button=h('button',{id:'homeConnect',class:'btn primary home-connect',type:'button',onclick:toggleConnect},'Подключить');
    const sys=root.querySelector('#sysLine');head.append(h('div',{class:'home-actions'},button,sys));
    const parameters=h('details',{class:'home-parameters'},h('summary',null,'Параметры подключения'),h('div',{class:'row'},[...controls.children].filter(e=>!e.classList.contains('stage-help'))));
    const help=controls.querySelector('.stage-help');if(help)help.textContent='Проверить соединение';
    controls.replaceChildren(parameters,help);
    const summary=h('div',{id:'homePulse',class:'home-pulse'},
      h('button',{id:'homeMetrics',class:'btn',onclick:()=>openDiag('now'),'aria-label':'Скорость и сеанс: подробности'},'Скорость и сеанс'),
      h('button',{id:'homeAddress',class:'btn',onclick:address},'Как меня видят сайты'),
      h('button',{class:'btn',id:'diagramExpand','aria-controls':'lanesCard',onclick:()=>{prefs().expanded=!prefs().expanded;save();apply();renderLanes();}},'Развернуть схему'),
      h('button',{class:'btn',onclick:()=>{prefs().home='full';save();apply();renderLanes();}},'Полный вид'));
    root.insertBefore(summary,root.querySelector('#lanesCard'));
    // A way back remains available in the full view and in a custom layout.
    controls.append(h('button',{class:'btn small home-compact-choice',onclick:()=>{
      if([1,2].includes(S.layout?.version)){prefs().home='compact';save();toast('Сохранённая сетка остаётся активной. Выберите «Стандартный» в настройке главного экрана для компактного вида.');return;}
      prefs().home='compact';save();apply();renderLanes();
    }},'Компактный вид'));
    apply(root);
  }
  function status(p){
    apply();const summary=document.querySelector('.home-parameters>summary');if(summary)summary.textContent='Параметры подключения: '+(zapretOnly()?'только zapret':(S.mode==='tun'?'TUN':'Прокси')+' · '+(ROUTING[S.routing]||''));const b=document.querySelector('#homeConnect');if(b){b.textContent=powerLabel(p);b.disabled=busy||p==='wait';b.title=powerLabel(p)+' (Ctrl+Enter)';}
  }
  function pulse(on,sp,t){
    const metrics=document.querySelector('#homeMetrics'),a=document.querySelector('#homeAddress');
    if(metrics)metrics.textContent=on?`↓ ${fmtSpeed(sp.pd+sp.dd+sp.zd+sp.td)} · ↑ ${fmtSpeed(sp.pu+sp.du+sp.zu+sp.tu)} · ${fmtDur(Date.now()/1000-sessionStart())} · ${zapretOnly()?'zapret '+fmtBytes((t[4]||0)+(t[5]||0)):'через сервер '+fmtBytes((t[0]||0)+(t[1]||0))}`:'Скорость и сеанс · не подключено';
    const data=ip.data,g=data?.running?data.proxy?.info:data?.direct?.info;
    if(a)a.textContent=!S.showIp?'Проверка IP выключена':g?'IP последней проверки: '+(g.country||g.countryCode||'адрес определён'):'Как меня видят сайты';
  }
  function address(){
    const out=h('div');const d=openDialog(h('h2',null,'Как меня видят сайты'),out,h('div',{class:'row end'},h('button',{class:'btn',onclick:()=>d.close()},'Закрыть')));
    if(!S.showIp){out.append(h('p',null,'Проверка IP выключена в настройках.'));return;}
    out.append(h('p',{class:'hint'},'Последний полученный результат. Обновить проверку и открыть карту можно в «Сводке».'));
    const data=ip.data,results=data?.running?[['Через сервер',data.proxy],['Напрямую',data.direct]]:[['Сейчас',data?.direct]];
    for(const [label,result]of results){const g=result?.info;out.append(h('section',{class:'panel'},h('h3',null,label),g?h('code',{class:'full-address'},g.ip):h('p',null,'Не определено'),g?h('p',null,[g.country,g.city,g.isp].filter(Boolean).join(' · ')):null,g?h('button',{class:'btn small',onclick:async()=>{try{await navigator.clipboard.writeText(g.ip);toast('IP скопирован');}catch(_){toast('Не удалось скопировать IP',true);}}},'Скопировать IP'):null));}
  }
  const activity=()=>prefs().activity==='programs'?'programs':'services';
  function activityHeader(){
    return h('div',{class:'live-head'},h('b',null,'Сейчас'),h('div',{class:'seg',role:'group','aria-label':'Группировка активности'},[['services','Сервисы'],['programs','Программы']].map(([key,title])=>h('button',{'data-activity':key,'aria-pressed':String(activity()===key),onclick:()=>{prefs().activity=key;save();renderLive();document.querySelector(`[data-activity="${key}"]`)?.focus();}},title))));
  }
  let routingSeq=0;
  async function routing(flush=true){
    const el=document.querySelector('#routingOutcome');if(!el)return;const seq=++routingSeq;
    if(zapretOnly()){el.textContent='В режиме «Только zapret» маршрут UDP определяет система. Настройки UDP ядра применяются в режиме с сервером; фильтр DPI zapret настраивается в «Обходе». Это не VPN и не шифрование трафика.';return;}
    el.textContent='Получаю итог UDP из конфигурации…';
    try{if(flush&&await saveNow()===false)throw new Error('Изменения не сохранены. Повторите сохранение перед просмотром итога.');const r=await api('/api/routing/summary');if(!el.isConnected||seq!==routingSeq)return;
      if(typeof r.quic!=='boolean'||typeof r.server!=='boolean'||typeof r.direct!=='boolean'||!['proxy','tun'].includes(r.mode)||!['direct','proxy','other'].includes(r.final))throw new Error('Неподдерживаемый формат отчёта');
      const route={direct:'напрямую',proxy:'по маршрутам через сервер',other:'по своему outbound'};
      el.replaceChildren(h('b',null,'UDP по конфигурации настроек'),
        h('p',null,r.quic?'UDP/443: есть правило отклонения QUIC.':'QUIC отдельно не отклоняется стандартным правилом.'),
        h('p',null,r.server?'UDP для маршрутов через сервер имеет настроенный UDP-выход.':r.direct?'Остальной UDP направляется напрямую.':'Отдельный UDP-выход не найден; итоговый выход — '+(route[r.final]||'не определён')+'.'),
        h('p',{class:'hint'},'Исключения по сайтам и программам применяются в порядке правил. '+(r.custom?'Есть ручные правила или переопределения: они могут менять приоритет. ':'')+(r.mode==='proxy'?'В системном прокси UDP приложений не перехватывается целиком. ':'TUN перехватывает поддерживаемый трафик приложений. ')+'Это проект конфигурации, не проверка текущих пакетов. Изменения, требующие переподключения, отмечены в уведомлениях.'));
    }catch(e){if(el.isConnected&&seq===routingSeq)el.textContent='Итог UDP не определён: '+e.message;}
  }
  function ruleGroups(){
    const group=(title,options)=>{
      const count=h('small');const mark=()=>count.textContent=' · включено '+options.filter(x=>S[x[0]]).length+' из '+options.length;
      const d=h('details',{class:'card rule-options'},h('summary',null,title,count),h('div',{class:'toggle-grid'},options.map(([key,title,desc])=>check(key,title,desc,()=>{mark();routing(true);})))) ;mark();return d;
    };
    const out=h('section',{class:'rule-groups'},
      group('Совместимость и UDP',[
        ['ruInsideDirect','Сайты только из России — напрямую','Госуслуги, банки и другие сервисы, не пускающие иностранные IP.'],
        ['blockQuic','Блокировать QUIC','Отклонять UDP/443; браузер сможет перейти на HTTPS по TCP.'],
        ['udpDirect','Остальной UDP напрямую','Итог зависит от наличия совместимого UDP-выхода и правил ниже.'],
        ['udpProxy','UDP через сервер','Для совместимых протоколов. Для перехвата UDP приложений нужен TUN.']]),
      group('Приватность',[['adBlock','Блокировать рекламу и трекеры','Баннеры, счётчики и рекламные сети. Рекламу в видео YouTube так не убрать.']]),
      h('div',{class:'panel routing-outcome',id:'routingOutcome',role:'status'},'Итог UDP ещё не получен'),
      h('button',{class:'btn small',onclick:()=>routing()},'Обновить итог UDP'));
    requestAnimationFrame(()=>{if(out.isConnected)routing();});return out;
  }
  function server(sv){
    const own=(S.ownHosts||[]).find(o=>String(o.host).toLowerCase()===String(sv.host).toLowerCase());
    const d=openDialog(h('h2',null,sv.name||sv.host),h('dl',{class:'server-facts'},h('dt',null,'Адрес'),h('dd',null,sv.host+':'+sv.port),h('dt',null,'Протокол'),h('dd',null,protoName(sv)),h('dt',null,'Сохранённый выбор'),h('dd',null,S.selected===sv.id?'Выбран': 'Не выбран')),
      h('p',{class:'hint'},sv.subscriptionId?'Сервер управляется подпиской. Обновить или удалить её можно в разделе «Серверы».':'Ключи и пароли доступны в редакторе. Выбор сервера выполняется отдельной кнопкой.'),
      h('div',{class:'row'},h('button',{class:'btn primary',onclick:async()=>{if(await selectServer(sv.id))d.close();}},'Выбрать сервер'),h('button',{class:'btn',onclick:async e=>{const b=e.currentTarget;b.disabled=true;b.textContent='Проверяю…';await testServers(sv.id);if(d.isConnected){const v=delayFor(sv.id);b.disabled=false;b.textContent=v?.ms?'Задержка: '+v.ms+' мс':'Проверить задержку';}}},'Проверить задержку'),h('button',{class:'btn',disabled:!!sv.subscriptionId,onclick:()=>{d.close();editServer(sv);}},'Изменить'),h('button',{class:'btn',onclick:()=>{d.close();qrDialog(sv);}},'QR-код'),own&&ST?.install?h('button',{class:'btn',onclick:()=>{d.close();vpsDialog(own);}},'Мониторинг и аудит VPS'):null),h('div',{class:'row end mt'},h('button',{class:'btn',onclick:()=>d.close()},'Закрыть')));
  }
  return {home,apply,status,pulse,activity,activityHeader,ruleGroups,routing,server};
})();
