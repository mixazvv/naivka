'use strict';
// One presentation model and the same rule callbacks for all route diagrams.
const RouteDiagrams = (() => {
  let box=null,surface=null,data=null,mode='',dragged=null,epoch=0,hover=null,resize=null,resizeFrame=0,motion=null;
  const routes=new Map(),leaves=new Map(),servers=new Map(),serverLinks=new Map(),pending=new Map();
  const ns='http://www.w3.org/2000/svg';
  const titles={proxy:'Через сервер',direct:'Напрямую',zapret:'zapret',tg:'Telegram'};
  const routeOf=r=>r==='telegram'?'tg':Object.hasOwn(titles,r)?r:'proxy';
  const normalizeKey=k=>k.startsWith('proc:')?k.toLowerCase():k;
  const eligible=key=>data?.phase==='on'&&!data.offline&&key!=='tg'&&data.lanes.some(l=>l.key===key&&l.active&&!l.wilt);
  function motionChanged(){surface?.classList.toggle('route-animating',!motion?.matches&&data?.phase==='on'&&!data?.offline);surface?.classList.toggle('route-still',!!motion?.matches);}
  function unmount(){
    epoch++;resize?.disconnect();resize=null;cancelAnimationFrame(resizeFrame);motion?.removeEventListener('change',motionChanged);motion=null;
    surface?.remove();box?.classList.remove('routes-alt');
    if(box){delete box.dataset.diagram;box.style.removeProperty('--pcx');box.style.removeProperty('--pcy');}
    box=surface=data=null;mode='';dragged=hover=null;routes.clear();leaves.clear();servers.clear();serverLinks.clear();pending.clear();
  }
  function hoverAt(x,y){
    hover=null;
    for(const [key,r] of routes){const b=r.group.getBoundingClientRect();const hit=eligible(key)&&x>=b.left-12&&x<=b.right+12&&y>=b.top-12&&y<=b.bottom+12;r.group.classList.toggle('drop-target',hit);if(hit)hover=key;}
    return hover;
  }
  function endHover(){const result=hover;hover=null;for(const r of routes.values())r.group.classList.remove('drop-target');return result;}
  function moveLeaf(key,route){if(!box||!Object.hasOwn(titles,route))return;pending.set(normalizeKey(key),{route,at:Date.now()});update(box,data,mode);}
  function clearLeaf(key){pending.delete(normalizeKey(key));if(box)update(box,data,mode);}
  function makePath(svg,color,kind){const p=document.createElementNS(ns,'path');p.setAttribute('fill','none');p.setAttribute('stroke',color);p.dataset.direction=kind;svg.append(p);return p;}
  function update(container,d,selected='branches'){
    if(selected==='branches'){unmount();return Branches.update(container,d);}
    Branches.unmount();
    if(box!==container||mode!==selected){
      unmount();box=container;mode=selected;box.classList.add('routes-alt');box.dataset.diagram=mode;
      surface=document.createElement('div');surface.className='route-scene';box.prepend(surface);
      const svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 1000 600');svg.setAttribute('preserveAspectRatio','none');svg.setAttribute('aria-hidden','true');svg.classList.add('route-lines');surface.append(svg);
      motion=window.NaivkaExperience?.motion||matchMedia('(prefers-reduced-motion: reduce)');motion.addEventListener('change',motionChanged);
      resize=new ResizeObserver(()=>{cancelAnimationFrame(resizeFrame);const owner=epoch;resizeFrame=requestAnimationFrame(()=>{if(box&&owner===epoch)update(box,data,mode);});});resize.observe(box);
    }
    data=d;motionChanged();
    const compact=box.clientWidth<620,tree=mode==='tree',svg=surface.querySelector('.route-lines');
    box.style.setProperty('--pcx','50%');box.style.setProperty('--pcy',tree?'82%':'43%');
    const lanes=d.lanes.filter(l=>Object.hasOwn(titles,l.key)),keys=new Set(lanes.map(l=>l.key));
    for(const [key,r]of routes)if(!keys.has(key)){r.group.remove();r.paths.forEach(p=>p.remove());routes.delete(key);}
    lanes.forEach((lane,i)=>{
      let r=routes.get(lane.key);
      if(!r){
        const group=document.createElement('section');group.className='route-group';group.dataset.route=lane.key;
        const button=document.createElement('button');button.type='button';button.className='route-title';
        const list=document.createElement('div');list.className='route-services';
        button.addEventListener('click',()=>data.onNode(lane.key));group.append(button,list);surface.append(group);
        r={group,button,list,paths:['base','down','up'].map(k=>makePath(svg,lane.color,k))};routes.set(lane.key,r);
        group.addEventListener('dragover',e=>{if(dragged&&eligible(lane.key)){e.preventDefault();e.dataTransfer.dropEffect='move';group.classList.add('drop-target');}});
        group.addEventListener('dragleave',()=>group.classList.remove('drop-target'));
        group.addEventListener('drop',async e=>{
          e.preventDefault();group.classList.remove('drop-target');if(!dragged||!eligible(lane.key))return;
          const item=dragged,run=epoch,callback=data.onDrop;dragged=null;const node=leaves.get(normalizeKey(item.key));if(node)node.disabled=true;
          try{if(await callback(item.key,lane.key,item)){if(run===epoch)moveLeaf(item.key,lane.key);}}
          catch(_){if(run===epoch)data.toast?.('Не удалось сохранить правило',true);}
          finally{if(run===epoch&&node)node.disabled=false;}
        });
      }
      const x=tree?(i+1)*100/(lanes.length+1):i%2===0?20:80;
      const y=tree?10:lanes.length>2?(i<2?6:48):20;
      r.group.style.left=x+'%';r.group.style.top=y+'%';r.group.style.setProperty('--route-color',lane.color);
      r.group.classList.toggle('inactive',!lane.active||d.phase!=='on');
      const label=[lane.node?.title||titles[lane.key],lane.node?.sub||'',!lane.active?'неактивен':''].filter(Boolean).join(' · ');
      if(r.button.textContent!==label)r.button.textContent=label;r.button.title=label;
      r.paths.forEach((line,n)=>{
        const rx=x*10+(n===2?4:0),ry=y*6;
        const path=tree?`M500 492 C500 350 ${rx} 300 ${rx} ${ry+40}`:`M500 258 Q${rx} 258 ${rx} ${ry+40}`;
        line.setAttribute('d',path);line.style.d=`path("${path}")`;line.setAttribute('stroke',lane.color);
        line.setAttribute('stroke-width',String(n?2:lane.active?2+Math.min(5,Math.log2(1+(lane.speed||0)/1024)):1));
        line.style.opacity=n?((lane.active&&d.phase==='on'&&(n===1?lane.down:lane.up)>0)?'.8':'0'):(lane.active&&d.phase==='on'?'.35':'.15');
        if(n)line.style.animationDuration=Math.max(.6,4-Math.log2(1+(n===1?lane.down:lane.up)/1024)*.25)+'s';
      });
    });
    const seen=new Set(),counts={},over={};
    if(['on','revive','blocked'].includes(d.phase))for(const item of d.top.slice(0,48)){
      if(!item.key)continue;const key=normalizeKey(item.key);if(seen.has(key))continue;
      let route=routeOf(item.route),p=pending.get(key);if(p&&(p.route===route||Date.now()-p.at>600000)){pending.delete(key);p=null;}if(p)route=p.route;
      const r=routes.get(route);if(!r)continue;if((counts[route]||0)>=(compact?1:3)){over[route]=(over[route]||0)+1;continue;}counts[route]=(counts[route]||0)+1;seen.add(key);
      let button=leaves.get(key);
      if(!button){button=document.createElement('button');button.type='button';button.className='route-service';button.draggable=true;leaves.set(key,button);
        button.addEventListener('click',()=>data.onPick?.({...button._item,current:button.dataset.route},button));
        button.addEventListener('dragstart',e=>{dragged=button._item;e.dataTransfer.effectAllowed='move';e.dataTransfer.setData('text/plain',button._item.key);});
        button.addEventListener('dragend',()=>{dragged=null;endHover();});}
      button._item=item;button.dataset.route=route;button.dataset.key=key;
      const label=item.label||item.key;if(button.textContent!==label)button.textContent=label;
      button.title=[...([data.why?.(item.key,route,item)||''].flat()),'Перетащите на маршрут или нажмите для выбора.'].join('\n');button.setAttribute('aria-label',label+': '+titles[route]);
      if(button.parentNode!==r.list)r.list.append(button);
    }
    for(const [key,b]of leaves)if(!seen.has(key)){b.remove();leaves.delete(key);}
    for(const [key,r]of routes){r.more?.remove();if(over[key]){r.more=document.createElement('button');r.more.type='button';r.more.className='route-more';r.more.textContent='Ещё '+over[key];r.more.onclick=()=>data.onMore();r.list.append(r.more);}}
    const allServers=d.servers||[],activeServer=allServers.find(s=>s.active);
    const nodes=(activeServer?[activeServer,...allServers.filter(s=>s!==activeServer)]:allServers).slice(0,compact?3:6),ids=new Set(nodes.map(s=>s.id));
    for(const [id,b]of servers)if(!ids.has(id)){b.remove();servers.delete(id);serverLinks.get(id)?.remove();serverLinks.delete(id);}
    nodes.forEach((server,i)=>{
      let b=servers.get(server.id);if(!b){b=document.createElement('button');b.type='button';b.className='route-server';b.dataset.server=server.id;servers.set(server.id,b);surface.append(b);
        b.addEventListener('click',async()=>{if(b.disabled)return;b.disabled=true;try{await data.onServer?.(server.id);}finally{if(b.isConnected)b.disabled=false;}});}
      b.textContent=server.name||server.id;b.title=(server.name||server.id)+(server.active?' · текущий сервер':' · выбрать для трафика через сервер');b.setAttribute('aria-pressed',String(!!server.active));
      const sx=(i+1)*100/(nodes.length+1),sy=tree?60:91;
      b.style.left=sx+'%';b.style.top=sy+'%';
      let link=serverLinks.get(server.id);if(!link){link=makePath(svg,'var(--tunnel)','base');serverLinks.set(server.id,link);}
      const proxy=routes.get('proxy');if(proxy){const rx=parseFloat(proxy.group.style.left)*10,ry=parseFloat(proxy.group.style.top)*6+35;const path=`M${rx} ${ry} Q${sx*10} ${ry} ${sx*10} ${sy*6}`;link.setAttribute('d',path);link.style.d=`path("${path}")`;}
      link.style.opacity=server.active&&d.phase==='on'?'.55':'.12';link.setAttribute('stroke-width',server.active?'2':'1');
    });
    return over;
  }
  return {update,unmount,hoverAt,endHover,moveLeaf,clearLeaf};
})();
