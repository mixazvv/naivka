// UI controller fixture without a browser/network. Exercises explicit execution,
// credential handoff, duplicate clicks, closed dialogs and bounded memory.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
class Element {
  constructor(tag, attrs, children) { this.tag = tag; this.attrs = attrs || {}; this.children = children; this.events = {}; this.isConnected = true; this.textContent = ''; }
  addEventListener(name, fn) { this.events[name] = fn; }
  replaceChildren(...items) { this.children = items; }
  close() { this.isConnected = false; }
}
const dialogs = [], calls = [];
const report = { checkedAt: 100, metrics: { load: 0, cpu_usage: 0 }, checks: [{id:'root_login',value:'<script>',level:'unknown'}] };
let handler = async () => report;
const context = vm.createContext({
  console, Map, Date, Number,
  h: (tag, attrs, ...children) => new Element(tag, attrs, children),
  openDialog: (...children) => { const d = new Element('dialog', {}, children); dialogs.push(d); return d; },
  api: async (url, body) => { calls.push({url,body}); return handler(body); },
  sshAuthDialog: async (host, run) => run({password:'fixture-secret'})
});
vm.runInContext(fs.readFileSync(require.resolve('../web/vps.js'), 'utf8'), context);
function walk(el) { return el instanceof Element ? [el, ...el.children.flat(Infinity).flatMap(walk)] : []; }
function button(d) { return walk(d).find(e => e.tag === 'button' && e.children[0] === 'Проверить сейчас'); }
function text(el) { return el instanceof Element ? el.children.flat(Infinity).map(text).join(' ') + el.textContent : String(el ?? ''); }
(async () => {
  vm.runInContext("vpsDialog({id:'one',host:'example.test'})", context);
  assert.equal(calls.length, 0, 'opening a panel must not initiate SSH');
  const first = dialogs.at(-1), go = button(first);
  let release;
  handler = () => new Promise(resolve => { release = resolve; });
  const pending = go.events.click();
  await go.events.click(); assert.equal(calls.length, 1, 'duplicate request');
  assert.equal(go.disabled, true);
  release(report); await pending;
  assert.equal(go.disabled, false);
  assert(text(first).includes('Не определено'), 'unrecognized check value must be unknown');
  assert(!text(first).includes('<script>'), 'untrusted raw value shown');
  handler = async body => body.password ? report : {needAuth:true};
  await go.events.click();
  assert.equal(calls.at(-1).body.password, 'fixture-secret');
  assert.equal(calls.at(-1).body.host, 'one');
  assert.equal(calls.at(-1).url, '/api/vps/report');
  assert(!vm.runInContext("JSON.stringify([...VPS_HISTORY])",context).includes('fixture-secret'));
  handler = () => new Promise(resolve => { release = resolve; });
  const closedPending = go.events.click(); first.close(); release({...report, checkedAt:200}); await closedPending;
  assert.equal(vm.runInContext("VPS_HISTORY.get('one').length",context), 2, 'closed panel retained late result');
  vm.runInContext("for(let i=0;i<30;i++) for(let j=0;j<8;j++) vpsRemember(String(i),{metrics:{},checkedAt:j})",context);
  assert.equal(vm.runInContext('VPS_HISTORY.size',context),20);
  assert.equal(vm.runInContext("VPS_HISTORY.get('29').length",context),5);
  console.log('VPS explicit requests, SSH auth, duplicate guard, closed dialogs, unknowns and history: OK');
})().catch(e => { console.error(e); process.exitCode = 1; });
