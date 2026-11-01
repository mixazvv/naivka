// No browser/network: verify reported confidence, unknown speed and explicit refresh.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
class El {
  constructor(tag, children) { this.tag=tag;this.children=children;this.isConnected=true;this.events={}; }
  replaceChildren(...items) { this.children=items; }
  addEventListener(name,fn) { this.events[name]=fn; }
  close() { this.isConnected=false; }
}
const text = el => el instanceof El ? el.children.flat(Infinity).map(text).join(' ') : String(el ?? '');
let modal, requests=0, finish;
const context = vm.createContext({Date,console,
 h:(tag,attrs,...children)=>new El(tag,children),
 serverByTag: tag=>tag==='s-a' ? {name:'Свой сервер'} : null,
 openDialog:(...children)=>(modal=new El('dialog',children)),
 api:async path=>{assert.equal(path,'/api/server-scores');requests++;return new Promise(resolve=>{finish=resolve;});}
});
vm.runInContext(fs.readFileSync(require.resolve('../web/server-scores.js'),'utf8'),context);
context.data={enabled:true,rows:[{tag:'s-a',samples:10,successes:8,score:78.5,median:100,p95:500,quarantineUntil:1800000000,eligible:false},{tag:'s-b',samples:2,successes:0,score:0,median:0,p95:0,eligible:false}]};
const view=vm.runInContext('serverScoresResults(data)',context), rendered=text(view);
assert(rendered.includes('Свой сервер') && rendered.includes('8 / 10') && rendered.includes('100 / 500 мс'));
assert(rendered.includes('Карантин до') && rendered.includes('Не определено'));
assert(rendered.includes('не ICMP-потери') && rendered.includes('Скорость загрузки неизвестна'));
assert.equal(requests,0,'rendering scores must not initiate network probes');
(async()=>{
 const pending=vm.runInContext('serverScoresDialog()',context);
 assert.equal(requests,1);
 modal.close();finish(context.data);await pending;
 assert.equal(text(modal).includes('Свой сервер'),false,'closed dialog accepted late result');
 assert.equal(requests,1,'unexpected background polling');
 console.log('Server scores: confidence labels, unknown speed, explicit API read and closed dialog: OK');
})().catch(e=>{console.error(e);process.exitCode=1;});
