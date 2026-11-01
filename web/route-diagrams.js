'use strict';
// All renderers consume the existing branches presentation model and callbacks.
const RouteDiagrams = (() => {
  let box = null, surface = null, data = null, mode = '', dragged = null, epoch = 0;
  const routes = new Map(), leaves = new Map(), pending = new Map();
  const ns = 'http://www.w3.org/2000/svg';
  const routeOf = r => r === 'telegram' ? 'tg' : ['proxy','direct','zapret','tg'].includes(r) ? r : 'proxy';
  const titles = { proxy:'Через сервер', direct:'Напрямую', zapret:'zapret', tg:'Telegram' };
  function unmount() {
    epoch++; surface?.remove(); box?.classList.remove('routes-alt');
    if (box) { delete box.dataset.diagram; box.style.removeProperty('--pcx'); box.style.removeProperty('--pcy'); }
    box=surface=data=null;mode='';dragged=null;routes.clear();leaves.clear();pending.clear();
  }
  function update(container, d, selected = 'branches') {
    if (selected === 'branches') { unmount(); return Branches.update(container,d); }
    Branches.unmount();
    if (box !== container || mode !== selected) {
      unmount(); box=container;mode=selected;box.classList.add('routes-alt');box.dataset.diagram=mode;
      surface=document.createElement('div');surface.className='route-scene';box.prepend(surface);
    }
    data=d;
    box.style.setProperty('--pcx','50%');box.style.setProperty('--pcy',mode==='tree'?'82%':'48%');
    const lanes=d.lanes.filter(l=>l.key in titles), keys=new Set(lanes.map(l=>l.key));
    for(const [key,r] of routes)if(!keys.has(key)){r.group.remove();routes.delete(key);}
    const svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 1000 600');svg.setAttribute('preserveAspectRatio','none');svg.setAttribute('aria-hidden','true');svg.classList.add('route-lines');
    const old=surface.querySelector('.route-lines');old?.remove();surface.prepend(svg);
    lanes.forEach((lane,i)=>{
      let r=routes.get(lane.key);
      if(!r){
        const group=document.createElement('section');group.className='route-group';group.dataset.route=lane.key;
        const button=document.createElement('button');button.type='button';button.className='route-title';
        const list=document.createElement('div');list.className='route-services';
        button.addEventListener('click',()=>data.onNode(lane.key));
        group.append(button,list);surface.append(group);r={group,button,list};routes.set(lane.key,r);
        group.addEventListener('dragover',e=>{if(canDrop(lane.key)){e.preventDefault();e.dataTransfer.dropEffect='move';group.classList.add('drop-target');}});
        group.addEventListener('dragleave',()=>group.classList.remove('drop-target'));
        group.addEventListener('drop',async e=>{
          e.preventDefault();group.classList.remove('drop-target');
          if(!canDrop(lane.key))return;
          const item=dragged, run=epoch, callback=data.onDrop;dragged=null;
          const node=leaves.get(item.key);if(node)node.disabled=true;
          try {
            const ok=await callback(item.key,lane.key,item);
            if(run!==epoch)return;
            if(ok){pending.set(item.key,{route:lane.key,at:Date.now()});update(box,data,mode);}
          }catch(_){if(run===epoch)data.toast?.('Не удалось сохранить правило',true);}
          finally{if(run===epoch&&node)node.disabled=false;}
        });
      }
      const tree=mode==='tree';
      const x=tree?(i+1)*100/(lanes.length+1):i%2===0?20:80;
      const y=tree?12:lanes.length>2?(i<2?8:62):26;
      r.group.style.left=x+'%';r.group.style.top=y+'%';r.group.style.setProperty('--route-color',lane.color);
      r.group.classList.toggle('inactive',!lane.active||d.phase!=='on');
      const label=[lane.node?.title||titles[lane.key],lane.node?.sub||'',!lane.active?'неактивен':''].filter(Boolean).join(' · ');
      if(r.button.textContent!==label)r.button.textContent=label;
      r.button.title=label;
      const line=document.createElementNS(ns,'path');
      const rx=x*10,ry=y*6;
      line.setAttribute('d',tree?`M500 492 C500 350 ${rx} 300 ${rx} ${ry+40}`:`M500 288 Q${rx} 288 ${rx} ${ry+40}`);
      line.setAttribute('stroke',lane.color);line.setAttribute('fill','none');line.setAttribute('stroke-width',String(lane.active?2+Math.min(5,Math.log2(1+(lane.speed||0)/1024)):1));
      line.setAttribute('opacity',lane.active&&d.phase==='on'?'.7':'.2');svg.append(line);
    });
    const seen=new Set(),over={};
    if(['on','revive','blocked'].includes(d.phase))for(const item of d.top.slice(0,48)){
      if(!item.key||seen.has(item.key))continue;
      let route=routeOf(item.route), p=pending.get(item.key);
      if(p&&(p.route===route||Date.now()-p.at>600000)){pending.delete(item.key);p=null;}
      if(p)route=p.route;
      const r=routes.get(route);if(!r)continue;
      const count=[...seen].filter(k=>leaves.get(k)?.dataset.route===route).length;
      if(count>=3){over[route]=(over[route]||0)+1;continue;}
      seen.add(item.key);
      let button=leaves.get(item.key);
      if(!button){button=document.createElement('button');button.type='button';button.className='route-service';button.draggable=true;leaves.set(item.key,button);
        button.addEventListener('click',()=>{const t=button._item;data.onPick?.({...t,current:button.dataset.route},button);});
        button.addEventListener('dragstart',e=>{dragged=button._item;e.dataTransfer.effectAllowed='move';e.dataTransfer.setData('text/plain',button._item.key);});
        button.addEventListener('dragend',()=>{dragged=null;for(const r of routes.values())r.group.classList.remove('drop-target');});
      }
      button._item=item;button.dataset.route=route;
      const label=item.label||item.key;if(button.textContent!==label)button.textContent=label;
      button.title=[...([data.why?.(item.key,route,item)||''].flat()),'Перетащите на маршрут или нажмите для выбора.'].join('\n');
      button.setAttribute('aria-label',label+': '+titles[route]);
      if(button.parentNode!==r.list)r.list.append(button);
    }
    for(const [key,b]of leaves)if(!seen.has(key)){b.remove();leaves.delete(key);}
    for(const [key,r]of routes){
      r.more?.remove();
      if(over[key]){r.more=document.createElement('button');r.more.type='button';r.more.className='route-more';r.more.textContent='Ещё '+over[key];r.more.onclick=()=>data.onMore();r.list.append(r.more);}
    }
    return over;
  }
  function canDrop(key){return dragged&&data?.phase==='on'&&!data.offline&&key!=='tg'&&data.lanes.some(l=>l.key===key&&l.active&&!l.wilt);}
  return {update,unmount};
})();
