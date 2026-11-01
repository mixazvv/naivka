'use strict';
// No timers, network, DOM mutations or offline penalties. The existing mascot
// controller retains ownership of movement, animation and action priorities.
window.PetMind = (() => {
  const surfaces = new Set(['lanesCard','tileSpeed','tileIp','tileSession','stageReady','liveCol','homeServer','chartBox']);
  const events = ['night-star','stable-session','toy','anniversary'];
  function create({memory = {}, save = () => {}, clock = () => Date.now(), monotonic = () => performance.now(), random = Math.random} = {}) {
    memory=memory&&typeof memory==='object'?memory:{};
    const now = Math.floor(clock()/1000);
    const data = {version:1,created:now,lastEvent:0,visits:{},events:{}};
    if (memory.version === 1) {
      if (Number.isFinite(memory.created) && memory.created > 0 && memory.created <= now) data.created = Math.floor(memory.created);
      if (Number.isFinite(memory.lastEvent) && memory.lastEvent > 0 && memory.lastEvent <= now) data.lastEvent = Math.floor(memory.lastEvent);
      for (const [id,n] of Object.entries(memory.visits || {})) if (surfaces.has(id) && Number.isFinite(n) && n > 0) data.visits[id]=Math.min(1000,Math.floor(n));
      for (const id of events) if (Number.isFinite(memory.events?.[id]) && memory.events[id] > 0 && memory.events[id] <= now) data.events[id]=Math.floor(memory.events[id]);
    }
    const sameMap=(a,b)=>Object.keys(a||{}).length===Object.keys(b).length&&Object.entries(a||{}).every(([k,v])=>b[k]===v);
    let dirty=memory.version!==1||memory.created!==data.created||(memory.lastEvent||0)!==data.lastEvent||!sameMap(memory.visits,data.visits)||!sameMap(memory.events,data.events), lastSave=monotonic(), energy=.7, comfort=.6, curiosity=.5, playfulness=.5, goal='rest', paused=false, stableSince=null, lastStep=monotonic(), mountedAt=monotonic();
    const clamp=n=>Math.max(0,Math.min(1,n));
    const snapshot=()=>JSON.parse(JSON.stringify(data));
    function flush(force=false){
      if (!dirty || (!force && monotonic()-lastSave<60000)) return;
      save(snapshot());dirty=false;lastSave=monotonic();
    }
    function pause(value=true){paused=value;lastStep=monotonic();}
    function step(){
      if(paused){lastStep=monotonic();return;}
      const dt=Math.max(0,Math.min(30,(monotonic()-lastStep)/1000));lastStep=monotonic();
      energy=clamp(energy+dt*(goal==='move'||goal==='play'?-.0015:.001));
      curiosity=clamp(curiosity+dt*.0002);flush();
    }
    function acted(next){step();goal=next;if(next==='move'){energy=clamp(energy-.06);curiosity=clamp(curiosity-.12);}if(next==='play'){energy=clamp(energy-.04);playfulness=clamp(playfulness-.05);}}
    function stroked(){comfort=clamp(comfort+.04);playfulness=clamp(playfulness+.02);}
    function visit(id){if(!surfaces.has(id))return;const n=Math.min(1000,(data.visits[id]||0)+1);if(n===data.visits[id])return;data.visits[id]=n;dirty=true;flush();}
    function affinity(id){return 1+Math.min(.75,(data.visits[id]||0)/30);}
    function observe(phase){step();const on=phase==='on'||phase==='run';if(!on)stableSince=null;else if(stableSince===null)stableSince=monotonic();}
    function nextEvent(){
      step();const seconds=Math.floor(clock()/1000);
      if(monotonic()-mountedAt<10*60000 || seconds-data.lastEvent<3600 || random()>.12)return null;
      const date=new Date(clock()),hour=date.getHours();
      const choices=[];
      if(hour>=22||hour<5)choices.push('night-star');
      if(stableSince!==null&&monotonic()-stableSince>=30*60000)choices.push('stable-session');
      if(playfulness>.4)choices.push('toy');
      const created=new Date(data.created*1000);
      if(seconds-data.created>300*86400&&date.getMonth()===created.getMonth()&&date.getDate()===created.getDate())choices.push('anniversary');
      const eligible=choices.filter(id=>seconds-(data.events[id]||0)>86400);
      if(!eligible.length)return null;
      const id=eligible[Math.min(eligible.length-1,Math.floor(random()*eligible.length))];
      data.lastEvent=seconds;data.events[id]=seconds;dirty=true;flush();return id;
    }
    return {acted,stroked,visit,affinity,observe,nextEvent,flush,snapshot,pause,
      weight:tag=>tag==='move'? .35+energy*curiosity*2 : .7+comfort*.6,
      state:()=>({energy,comfort,curiosity,playfulness,goal})};
  }
  return {create};
})();
