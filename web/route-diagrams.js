'use strict';
// One presentation model and the same rule callbacks for all route diagrams.
const RouteDiagrams = (() => {
  let box=null,surface=null,data=null,mode='',dragged=null,epoch=0,hover=null,resize=null,resizeFrame=0,motion=null;
  const routes=new Map(),leaves=new Map(),servers=new Map(),pending=new Map();
  const ns='http://www.w3.org/2000/svg';
  const titles={proxy:'Через сервер',direct:'Напрямую',zapret:'zapret',tg:'Telegram'};
  const routeOf=r=>r==='telegram'?'tg':Object.hasOwn(titles,r)?r:'proxy';
  const normalizeKey=k=>k.startsWith('proc:')?k.toLowerCase():k;
  const eligible=key=>data?.phase==='on'&&!data.offline&&key!=='tg'&&data.lanes.some(l=>l.key===key&&l.active&&!l.wilt);
  function motionChanged(){surface?.classList.toggle('route-animating',!motion?.matches&&data?.phase==='on'&&!data?.offline);surface?.classList.toggle('route-still',!!motion?.matches);}
  function unmount(){
    epoch++;resize?.disconnect();resize=null;cancelAnimationFrame(resizeFrame);motion?.removeEventListener('change',motionChanged);motion=null;
    surface?.remove();box?.classList.remove('routes-alt');
    if(box){delete box.dataset.diagram;box.style.removeProperty('--diagram-min');box.style.removeProperty('--pcx');box.style.removeProperty('--pcy');}
    box=surface=data=null;mode='';dragged=hover=null;routes.clear();leaves.clear();servers.clear();pending.clear();
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
  // Size the scene from its rendered labels, including translated text and zoom.
  function layoutRoutes(lanes,tree,svg,hasServers){
    const width=box.clientWidth;
    if(!width)return;
    const margin=12, gap=14, shelf=hasServers?50:0;
    const paired=!tree&&lanes.length===2;
    const cardWidth=paired?Math.min((width-100)/2,210):tree?Math.min(width*.65,480):Math.min((width-3*margin)/2,210);
    for(const {group} of routes.values())group.style.width=cardWidth+'px';
    const heights=lanes.map(l=>routes.get(l.key).group.offsetHeight);
    const rows=tree?heights:heights.filter((_,i)=>i%2===0).map((h,i)=>Math.max(h,heights[i*2+1]||0));
    const middle=tree?gap:90;
    const minHeight=rows.reduce((a,b)=>a+b,0)+Math.max(0,rows.length-1)*middle+2*margin+shelf+(tree||paired||rows.length===2?0:90);
    box.style.setProperty('--diagram-min',Math.ceil(minHeight)+'px');
    const height=surface.clientHeight,area=height-shelf;
    const rootX=tree?Math.max(32,(width-cardWidth)*.32):width/2;
    const firstY=margin;
    const rootY=tree||paired?area/2:rows.length===2?(area+rows[0]-rows[1])/2:area-45;
    box.style.setProperty('--pcx',rootX+'px');box.style.setProperty('--pcy',rootY+'px');
    svg.setAttribute('viewBox','0 0 '+width+' '+height);
    const spacing=rows.length>1?(area-2*margin-rows.reduce((a,b)=>a+b,0))/(rows.length-1):0;
    let top=firstY;
    lanes.forEach((lane,i)=>{
      const r=routes.get(lane.key),row=tree?i:Math.floor(i/2);
      if(i&&(tree||i%2===0))top+=rows[row-1]+spacing;
      const x=tree?width-margin-cardWidth/2:paired?(i===0?margin+cardWidth/2:width-margin-cardWidth/2):lanes.length===1?width/2:(i%2===0?width*.25:width*.75);
      const y=paired||tree&&rows.length===1?(area-heights[i])/2:top;
      r.group.style.left=x+'px';r.group.style.top=y+'px';
      const endX=tree?x-cardWidth/2:paired?x+(i===0?1:-1)*cardWidth/2:x,endY=tree||paired?y+heights[i]/2:y+(y<rootY?heights[i]:0);
      const trunk=(rootX+endX)/2;
      const curve=tree?'H'+trunk+' V'+endY+' H'+endX:'Q'+endX+' '+rootY+' '+endX+' '+endY;
      const path='M'+rootX+' '+rootY+' '+curve;
      for(const line of r.paths)line.setAttribute('d',path);
    });
  }
  function update(container,d,selected='branches'){
    if(selected==='branches'){unmount();return Branches.update(container,d);}
    Branches.unmount();
    if(box!==container||mode!==selected){
      unmount();box=container;mode=selected;box.classList.add('routes-alt');box.dataset.diagram=mode;
      surface=document.createElement('div');surface.className='route-scene';box.prepend(surface);
      const svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 1000 600');svg.setAttribute('preserveAspectRatio','none');svg.setAttribute('aria-hidden','true');svg.classList.add('route-lines');surface.append(svg);
      const shelf=document.createElement('div');shelf.className='diagram-servers';shelf.setAttribute('role','group');shelf.setAttribute('aria-label','Выбор сервера');surface.append(shelf);
      motion=window.NaivkaExperience?.motion||matchMedia('(prefers-reduced-motion: reduce)');motion.addEventListener('change',motionChanged);
      resize=new ResizeObserver(()=>{cancelAnimationFrame(resizeFrame);const owner=epoch;resizeFrame=requestAnimationFrame(()=>{if(box&&owner===epoch)update(box,data,mode);});});resize.observe(box);
    }
    data=d;motionChanged();
    const compact=box.clientWidth<620,tree=mode==='tree',svg=surface.querySelector('.route-lines');
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
      r.group.style.setProperty('--route-color',lane.color);
      r.group.classList.toggle('inactive',!lane.active||d.phase!=='on');
      const label=[lane.node?.title||titles[lane.key],lane.node?.sub||'',!lane.active?'неактивен':''].filter(Boolean).join(' · ');
      const title=titles[lane.key];
      if(r.button.textContent!==title)r.button.textContent=title;
      r.button.title=label;r.button.setAttribute('aria-label',title+' · '+label);
      r.paths.forEach((line,n)=>{
        line.dataset.route=lane.key;line.setAttribute('stroke',lane.color);
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
        button.addEventListener('pointerenter',()=>{surface.dataset.highlight=button.dataset.route;});button.addEventListener('pointerleave',()=>{delete surface.dataset.highlight;});button.addEventListener('focus',()=>{surface.dataset.highlight=button.dataset.route;});button.addEventListener('blur',()=>{delete surface.dataset.highlight;});
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
    const allServers=d.lanes.some(l=>l.key==='proxy')?(d.servers||[]):[],activeServer=allServers.find(s=>s.active);
    const nodes=(activeServer?[activeServer,...allServers.filter(s=>s!==activeServer)]:allServers).slice(0,compact?3:6),ids=new Set(nodes.map(s=>s.id));
    for(const [id,b]of servers)if(!ids.has(id)){b.remove();servers.delete(id);}
    nodes.forEach((server,i)=>{
      let b=servers.get(server.id);if(!b){b=document.createElement('button');b.type='button';b.className='route-server';b.dataset.server=server.id;servers.set(server.id,b);surface.querySelector('.diagram-servers').append(b);
        b.addEventListener('click',async()=>{if(b.disabled)return;b.disabled=true;try{await data.onServer?.(server.id);}finally{if(b.isConnected)b.disabled=false;}});}
      b.textContent=server.name||server.id;b.title=(server.name||server.id)+(server.active?' · текущий сервер':' · выбрать для трафика через сервер');b.setAttribute('aria-pressed',String(!!server.active));
      const shelf=surface.querySelector('.diagram-servers');
      if(shelf.children[i]!==b)shelf.insertBefore(b,shelf.children[i]||null);
    });
    layoutRoutes(lanes,tree,svg,nodes.length>0);
    return over;
  }
  return {update,unmount,hoverAt,endHover,moveLeaf,clearLeaf};
})();
