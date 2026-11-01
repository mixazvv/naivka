'use strict';
const HomeLayout = (() => {
  const registry = [
    ['status','Статус и сервер','.stage-head',3,true],['notices','Уведомления','#notices',3,true],
    ['routes','Схема подключения','#lanesCard',3,true],['controls','Режим и путь','.stage-controls',3,true],
    ['speed','Скорость','#tileSpeed',1],['ip','Адрес и страна','#tileIp',1],['session','Сеанс','#tileSession',1],
    ['ready','Подсказка подключения','#stageReady',3],['activity','Сейчас','#liveCol',1]
  ];
  const narrow=matchMedia('(max-width: 720px)');
  const defaults=()=>registry.map(([id,, ,span])=>({id,span,hidden:false}));
  function apply() {
    const root=document.querySelector('.stage-inner');if(!root||!S)return;
    const layout=S.layout;root.classList.toggle('home-custom',layout?.version===1);
    for(const [, ,selector] of registry){const el=root.querySelector(selector);if(el){el.style.removeProperty('order');el.style.removeProperty('grid-column');el.removeAttribute('data-layout-hidden');}}
    if(layout?.version!==1)return;
    const list=layout[narrow.matches?'narrow':'wide']||defaults();
    list.forEach((w,i)=>{
      const spec=registry.find(r=>r[0]===w.id);if(!spec)return;
      const el=root.querySelector(spec[2]);if(!el)return;
      el.style.order=String(i);el.style.gridColumn='span '+(narrow.matches?1:Math.max(1,Math.min(3,w.span||1)));
      if(w.hidden&&!spec[4])el.dataset.layoutHidden='';
    });
  }
  narrow.addEventListener('change',apply);
  function edit() {
    const area=narrow.matches?'narrow':'wide';
    const rows=h('div'),note=h('p',{class:'hint'},area==='narrow'?'Раскладка узкого окна':'Раскладка широкого окна');
    let dragID='', modified=false;
    const before=JSON.parse(JSON.stringify(S.layout||{}));
    const ensure=()=>{if(S.layout?.version!==1)S.layout={version:1,wide:defaults(),narrow:defaults()};return S.layout[area]||=(defaults());};
    const changed=()=>{modified=true;const focus=document.activeElement?.getAttribute('aria-label');save();apply();paint();if(focus)[...rows.querySelectorAll('[aria-label]')].find(e=>e.getAttribute('aria-label')===focus)?.focus();};
    const move=(id,delta)=>{const list=ensure(),i=list.findIndex(w=>w.id===id),j=i+delta;if(j<0||j>=list.length)return;[list[i],list[j]]=[list[j],list[i]];changed();};
    const paint=()=>{
      rows.replaceChildren(...ensure().map((w,i)=>{
        const spec=registry.find(r=>r[0]===w.id);if(!spec)return null;
        const row=h('div',{class:'layout-editor-row',draggable:true,'data-widget':w.id},
          h('b',null,spec[1]),
          h('button',{class:'btn small',disabled:i===0,'aria-label':spec[1]+': выше',onclick:()=>move(w.id,-1)},'↑'),
          h('button',{class:'btn small',disabled:i===ensure().length-1,'aria-label':spec[1]+': ниже',onclick:()=>move(w.id,1)},'↓'),
          h('select',{'aria-label':spec[1]+': ширина',disabled:area==='narrow',onchange:e=>{w.span=Number(e.target.value);changed();}},[1,2,3].map(n=>h('option',{value:n,selected:w.span===n},String(n)))),
          h('label',{class:'check'},h('input',{type:'checkbox',checked:!w.hidden,disabled:!!spec[4],'aria-label':spec[1]+': показывать',onchange:e=>{w.hidden=!e.target.checked;changed();}}),'Показывать'));
        row.addEventListener('dragstart',e=>{dragID=w.id;e.dataTransfer.setData('text/plain',w.id);e.dataTransfer.effectAllowed='move';});
        row.addEventListener('dragover',e=>{if(dragID)e.preventDefault();});
        row.addEventListener('drop',e=>{e.preventDefault();const list=ensure(),from=list.findIndex(x=>x.id===dragID),to=list.findIndex(x=>x.id===w.id);dragID='';if(from<0||to<0||from===to)return;list.splice(to,0,list.splice(from,1)[0]);changed();});
        row.addEventListener('dragend',()=>{dragID='';});return row;
      }));
    };
    const preset=(kind)=>{
      if(kind==='standard'){modified=true;S.layout={};save();apply();dialog.close();return;}
      const list=defaults();
      if(kind==='minimal')for(const w of list)if(['ip','session','activity','ready'].includes(w.id))w.hidden=true;
      if(kind==='monitor') {const priority=['status','notices','speed','ip','session','routes','controls','ready','activity'];list.sort((a,b)=>priority.indexOf(a.id)-priority.indexOf(b.id));}
      S.layout={version:1,wide:list,narrow:defaults()};changed();
    };
    const dialog=openDialog(h('h2',null,'Настроить главный экран'),note,
      h('div',{class:'row'},h('button',{class:'btn',onclick:()=>preset('standard')},'Стандартный'),h('button',{class:'btn',onclick:()=>preset('minimal')},'Минимализм'),h('button',{class:'btn',onclick:()=>preset('monitor')},'Мониторинг')),
      rows,h('div',{class:'row end mt'},h('button',{class:'btn',onclick:()=>dialog.close()},'Готово')));
    dialog.classList.add('wide');dialog.addEventListener('close',()=>{if(!modified){S.layout=before;apply();}});paint();
  }
  return {apply,edit,registry};
})();
