'use strict';
const HomeLayout = (() => {
  const registry = [
    ['status','Статус и сервер','.stage-head',3,true],['notices','Уведомления','#notices',3,true],
    ['routes','Схема подключения','#lanesCard',3,true],['controls','Режим и путь','.stage-controls',3,true],
    ['speed','Скорость','#tileSpeed',1],['ip','Адрес и страна','#tileIp',1],['session','Сеанс','#tileSession',1],
    ['ready','Подсказка подключения','#stageReady',3],['activity','Сейчас','#liveCol',1]
  ];
  const narrow=matchMedia('(max-width: 720px)');
  const defaults=()=>registry.map(([id,, ,span])=>({id,span,hidden:false,col:0,row:0,height:0}));
  function normalize(list,cols=3) {
    const occupied=new Set(),seen=new Set(),out=[];
    for(const w of [...(list||[]),...defaults()]) {
      const spec=registry.find(r=>r[0]===w.id);if(!spec||seen.has(w.id))continue;seen.add(w.id);
      const n={...w,span:Math.max(1,Math.min(3,Number(w.span)||1)),height:Math.max(0,Math.min(1200,Math.round(Number(w.height)||0))),hidden:!!w.hidden&&!spec[4]};
      const span=cols===1?1:n.span;
      const fits=(row,col)=>Number.isInteger(row)&&row>=1&&row<=64&&Number.isInteger(col)&&col>=1&&col+span-1<=cols&&Array.from({length:span},(_,i)=>row+':'+(col+i)).every(k=>!occupied.has(k));
      if(!fits(n.row,n.col)){search:for(let y=1;y<=64;y++)for(let x=1;x<=cols;x++)if(fits(y,x)){n.row=y;n.col=x;break search;}}
      for(let x=n.col;x<n.col+span;x++)occupied.add(n.row+':'+x);out.push(n);
    }
    return out;
  }
  function apply() {
    const root=document.querySelector('.stage-inner');if(!root||!S)return;
    const layout=S.layout;root.classList.toggle('home-custom',[1,2].includes(layout?.version));
    for(const [, ,selector] of registry){const el=root.querySelector(selector);if(el){el.style.removeProperty('order');el.style.removeProperty('grid-column');el.style.removeProperty('grid-row');el.style.removeProperty('min-height');el.style.removeProperty('--home-widget-height');el.removeAttribute('data-layout-hidden');}}
    if(![1,2].includes(layout?.version))return;
    const list=normalize(layout[narrow.matches?'narrow':'wide'],narrow.matches?1:3);
    list.forEach((w,i)=>{
      const spec=registry.find(r=>r[0]===w.id);if(!spec)return;
      const el=root.querySelector(spec[2]);if(!el)return;
      el.style.order=String(i);el.style.gridColumn=w.col+' / span '+(narrow.matches?1:w.span);el.style.gridRow=String(w.row);
      if(w.height){el.style.minHeight=w.height+'px';el.style.setProperty('--home-widget-height',w.height+'px');}
      if(w.hidden&&!spec[4])el.dataset.layoutHidden='';
    });
  }
  narrow.addEventListener('change',apply);
  function edit() {
    const area=narrow.matches?'narrow':'wide';
    const rows=h('div'),preview=h('div',{class:'layout-preview','aria-label':'Расположение виджетов'}),note=h('p',{class:'hint'},area==='narrow'?'Раскладка узкого окна':'Раскладка широкого окна');
    let dragID='', modified=false;
    const before=JSON.parse(JSON.stringify(S.layout||{}));
    let prepared=false;
    const ensure=()=>{if(![1,2].includes(S.layout?.version))S.layout={version:2,wide:defaults(),narrow:defaults()};S.layout.version=2;S.layout[area]||=defaults();if(!prepared){S.layout[area]=normalize(S.layout[area],area==='narrow'?1:3);prepared=true;}return S.layout[area];};
    const changed=()=>{modified=true;S.layout[area]=normalize(ensure(),area==='narrow'?1:3);const focus=document.activeElement?.getAttribute('aria-label');save();apply();paint();if(focus)[...rows.querySelectorAll('[aria-label]')].find(e=>e.getAttribute('aria-label')===focus)?.focus();};
    const move=(id,delta)=>{const list=ensure(),i=list.findIndex(w=>w.id===id),j=i+delta;if(j<0||j>=list.length)return;[list[i].row,list[j].row]=[list[j].row,list[i].row];[list[i].col,list[j].col]=[list[j].col,list[i].col];[list[i],list[j]]=[list[j],list[i]];changed();};
    const paint=()=>{
      const list=ensure();preview.replaceChildren(...list.map(w=>h('button',{class:'layout-preview-widget',draggable:true,'data-widget':w.id,style:`grid-column:${w.col}/span ${area==='narrow'?1:w.span};grid-row:${w.row};min-height:${Math.max(34,(w.height||140)/5)}px`,onkeydown:e=>{const delta={ArrowUp:[-1,0],ArrowDown:[1,0],ArrowLeft:[0,-1],ArrowRight:[0,1]}[e.key];if(!delta)return;e.preventDefault();w.row=Math.max(1,Math.min(64,w.row+delta[0]));w.col=Math.max(1,Math.min(area==='narrow'?1:4-w.span,w.col+delta[1]));changed();}},registry.find(r=>r[0]===w.id)[1])));
      preview.querySelectorAll('[data-widget]').forEach(el=>{el.addEventListener('dragstart',e=>{dragID=el.dataset.widget;e.dataTransfer.setData('text/plain',dragID);});el.addEventListener('dragover',e=>{if(dragID)e.preventDefault();});el.addEventListener('drop',e=>{e.preventDefault();const source=ensure().find(w=>w.id===dragID),target=ensure().find(w=>w.id===el.dataset.widget);dragID='';if(!source||source===target)return;[source.row,target.row]=[target.row,source.row];[source.col,target.col]=[target.col,source.col];changed();});el.addEventListener('dragend',()=>{dragID='';});});
      rows.replaceChildren(...list.map((w,i)=>{
        const spec=registry.find(r=>r[0]===w.id);if(!spec)return null;
        const row=h('div',{class:'layout-editor-row',draggable:true,'data-widget':w.id},
          h('b',null,spec[1]),
          h('button',{class:'btn small',disabled:i===0,'aria-label':spec[1]+': выше',onclick:()=>move(w.id,-1)},'↑'),
          h('button',{class:'btn small',disabled:i===ensure().length-1,'aria-label':spec[1]+': ниже',onclick:()=>move(w.id,1)},'↓'),
          h('select',{'aria-label':spec[1]+': ширина',disabled:area==='narrow',onchange:e=>{w.span=Number(e.target.value);changed();}},[1,2,3].map(n=>h('option',{value:n,selected:w.span===n},String(n)))),
          ...[['col','Колонка',1,area==='narrow'?1:4-w.span],['row','Строка',1,64],['height','Мин. высота, px',0,1200]].map(([key,title,min,max])=>h('label',{class:'layout-number'},title,h('input',{type:'number',min,max,step:key==='height'?20:1,value:w[key]||0,'aria-label':spec[1]+': '+title,onchange:e=>{w[key]=Math.max(min,Math.min(max,Math.round(Number(e.target.value)||0)));changed();}}))),
          h('label',{class:'check'},h('input',{type:'checkbox',checked:!w.hidden,disabled:!!spec[4],'aria-label':spec[1]+': показывать',onchange:e=>{w.hidden=!e.target.checked;changed();}}),'Показывать'));
        row.addEventListener('dragstart',e=>{dragID=w.id;e.dataTransfer.setData('text/plain',w.id);e.dataTransfer.effectAllowed='move';});
        row.addEventListener('dragover',e=>{if(dragID)e.preventDefault();});
        row.addEventListener('drop',e=>{e.preventDefault();const list=ensure(),from=list.findIndex(x=>x.id===dragID),to=list.findIndex(x=>x.id===w.id);dragID='';if(from<0||to<0||from===to)return;[list[from].row,list[to].row]=[list[to].row,list[from].row];[list[from].col,list[to].col]=[list[to].col,list[from].col];list.splice(to,0,list.splice(from,1)[0]);changed();});
        row.addEventListener('dragend',()=>{dragID='';});return row;
      }));
    };
    const preset=(kind)=>{
      if(kind==='standard'){modified=true;S.layout={};save();apply();dialog.close();return;}
      const list=defaults();
      if(kind==='minimal')for(const w of list)if(['ip','session','activity','ready'].includes(w.id))w.hidden=true;
      if(kind==='monitor') {const priority=['status','notices','speed','ip','session','routes','controls','ready','activity'];list.sort((a,b)=>priority.indexOf(a.id)-priority.indexOf(b.id));}
      S.layout={version:2,wide:list,narrow:defaults()};changed();
    };
    const dialog=openDialog(h('h2',null,'Настроить главный экран'),note,
      h('div',{class:'row'},h('button',{class:'btn',onclick:()=>preset('standard')},'Стандартный'),h('button',{class:'btn',onclick:()=>preset('minimal')},'Минимализм'),h('button',{class:'btn',onclick:()=>preset('monitor')},'Мониторинг')),
      h('p',{class:'hint'},'Перетаскивайте карточки на схеме или задайте колонку, строку и высоту. Стрелки на карточке двигают её с клавиатуры. Пересечения автоматически устраняются; важные кнопки остаются видимыми.'),preview,rows,h('div',{class:'row end mt'},h('button',{class:'btn',onclick:()=>dialog.close()},'Готово')));
    dialog.classList.add('wide');dialog.addEventListener('close',()=>{if(!modified){S.layout=before;apply();}});paint();
  }
  return {apply,edit,registry,normalize};
})();
