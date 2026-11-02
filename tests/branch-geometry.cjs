// Verify cached rendering against independent de Casteljau geometry, including
// resize, Telegram, growth, traffic width and the disconnected droop state.
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
let normalCalls=0;
const math=Object.create(Math);math.hypot=(...v)=>{normalCalls++;return Math.hypot(...v);};
const context={window:{},Math:math};vm.createContext(context);
const source=fs.readFileSync(require('node:path').join(__dirname,'../web/branches.js'),'utf8');
vm.runInContext(source.replace('return { update, unmount, hoverAt, endHover, moveLeaf, clearLeaf };',`globalThis.probe={
 setup(w,h,tg,k,state){W=w;H=h;br.tg={active:tg,target:tg?1:0};br[k]={...state};},
 shape,geom
};return { update, unmount, hoverAt, endHover, moveLeaf, clearLeaf };`),context);
const probe=context.probe;assert(probe,'test hook missing');
function point(points,t){let row=points.map(p=>[...p]);while(row.length>1)row=row.slice(1).map((p,i)=>p.map((v,j)=>row[i][j]*(1-t)+v*t));return row[0];}
function reference(curve,t){const half=t<=.5?curve.slice(0,4):curve.slice(3),u=t<=.5?t*2:(t-.5)*2;
 const p=point(half,u),d=point(half.slice(1).map((p,i)=>p.map((v,j)=>3*(v-half[i][j]))),u),length=Math.hypot(...d)||1;
 return {p,n:[-d[1]/length,d[0]/length]};}
let maxError=0,cases=0;
for(const [width,height] of [[320,260],[800,480],[1200,700]])for(const tg of [false,true])for(const key of ['proxy','direct','zapret','tg'])for(const grow of [.01,.5,1])for(const [speed,droop,thin,active] of [[0,0,false,true],[2e6,.4,false,true],[10000,1,true,true],[0,1,false,false]]){
 const state={grow,speed,droop,thin,active,target:1};probe.setup(width,height,tg,key,state);
 const output=probe.shape(key,0),curve=probe.geom(key),steps=Math.max(2,Math.floor(80*grow));assert.equal(output.L.length,steps+1);
 const w0=thin?1.5:active?2+Math.min(1.6,Math.log2(speed/1024+1)*.2):1.5,w1=Math.max(1,w0*.5);
 for(let i=0;i<=steps;i++){const t=i/80,{p,n}=reference(curve,t),w=(w0+(w1-w0)*t)/2,dy=droop*Math.sin(Math.PI*t*2)**2*14;
  for(const [actual,sign] of [[output.L[i],1],[output.R[i],-1]])for(let j=0;j<2;j++)maxError=Math.max(maxError,Math.abs(actual[j]-(p[j]+sign*n[j]*w+(j?dy:0))));
 }
 cases++;
}
assert(maxError<1e-9,`outline changed: ${maxError}`);
probe.setup(1000,480,true,'proxy',{grow:1,speed:2e6,droop:0,active:true,target:1});const first=probe.shape('proxy',0),left=first.L,vertex=left[40];normalCalls=0;
for(let i=0;i<120;i++){const current=probe.shape('proxy',i);assert.equal(current.L,left);assert.equal(current.L[40],vertex);}
assert.equal(normalCalls,0,'unchanged frames recompute normals');
probe.setup(1100,480,true,'proxy',{grow:1,speed:2e6,droop:0,active:true,target:1});probe.shape('proxy',0);assert.equal(normalCalls,81,'resize must rebuild geometry');
console.log(`${cases} branch geometry cases: max error ${maxError}; 120 warm frames: 0 normal recalculations, outline buffers reused`);
