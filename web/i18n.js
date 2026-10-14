// Английский интерфейс. Тексты в коде — русские; когда выбран английский, всё, что попадает
// на экран (текст, подсказки, подписи для экранного диктора, окна подтверждения), переводится
// по словарю i18n/en.js. Сообщения Naivka (ошибки, события) переводятся так же.
// Порядок: точная фраза → образец с подстановками → замена известных кусков внутри строки.
'use strict';

const LANG = (() => {
  try { return localStorage.getItem('naivka-lang') === 'en' ? 'en' : 'ru'; } catch (_) { return 'ru'; }
})();
const LOCALE = LANG === 'en' ? 'en-GB' : 'ru-RU';

const tr = (() => {
  if (LANG !== 'en' || !window.I18N_EN) return s => s;
  const CYR = /[А-Яа-яЁё]/;
  const exact = new Map(Object.entries(window.I18N_EN.exact));
  const patterns = window.I18N_EN.patterns.map(([re, en]) => [new RegExp(re, 's'), en]);
  const frags = [...exact.entries()].filter(([ru]) => ru.trim().length >= 3 && CYR.test(ru)).sort((a, b) => b[0].length - a[0].length);
  const isLetter = c => !!c && /[А-Яа-яЁёA-Za-z0-9]/.test(c);
  const memo = new Map();

  function core(s) {
    const lead = s.match(/^\s*/)[0], trail = s.match(/\s*$/)[0];
    const t = s.trim();
    if (exact.has(t)) return lead + exact.get(t) + trail;
    for (const [re, en] of patterns) {
      const m = t.match(re);
      if (m) return lead + en.replace(/\{(\d+)\}/g, (_, i) => translate(m[+i] || '')) + trail;
    }
    // Склеенные строки: заменяем известные куски, длинные первыми, только целыми словами.
    let out = s;
    for (const [ru, en] of frags) {
      if (!CYR.test(out)) break;
      let i = out.indexOf(ru);
      while (i >= 0) {
        const before = out[i - 1], after = out[i + ru.length];
        const edgeOk = (!CYR.test(ru[0]) || !isLetter(before)) && (!CYR.test(ru[ru.length - 1]) || !isLetter(after));
        if (edgeOk) { out = out.slice(0, i) + en + out.slice(i + ru.length); i = out.indexOf(ru, i + en.length); }
        else i = out.indexOf(ru, i + 1);
      }
    }
    return out;
  }

  function translate(s) {
    if (!s || !CYR.test(s)) return s;
    let r = memo.get(s);
    if (r === undefined) {
      r = core(s).replace(/«/g, '“').replace(/»/g, '”');
      if (memo.size > 5000) memo.clear();
      memo.set(s, r);
    }
    return r;
  }
  return translate;
})();

// Перевод того, что уже на странице и что появляется потом.
(() => {
  if (LANG !== 'en') return;
  document.documentElement.lang = 'en';
  const ATTRS = ['title', 'aria-label', 'placeholder'];
  const fixText = n => { const v = tr(n.nodeValue); if (v !== n.nodeValue) n.nodeValue = v; };
  const fixAttrs = el => {
    for (const a of ATTRS) {
      const v = el.getAttribute && el.getAttribute(a);
      if (v) { const t = tr(v); if (t !== v) el.setAttribute(a, t); }
    }
  };
  const walk = root => {
    if (root.nodeType === 3) { fixText(root); return; }
    if (root.nodeType !== 1) return;
    if (root.tagName === 'SCRIPT' || root.tagName === 'STYLE' || root.tagName === 'TEXTAREA') return;
    fixAttrs(root);
    const it = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
    for (let n = it.nextNode(); n; n = it.nextNode()) {
      if (n.nodeType === 3) {
        const p = n.parentNode;
        if (p && (p.tagName === 'SCRIPT' || p.tagName === 'STYLE' || p.tagName === 'TEXTAREA')) continue;
        fixText(n);
      } else fixAttrs(n);
    }
  };
  const start = () => {
    walk(document.body);
    document.title = tr(document.title);
    new MutationObserver(list => {
      for (const m of list) {
        if (m.type === 'characterData') fixText(m.target);
        else if (m.type === 'attributes') fixAttrs(m.target);
        else m.addedNodes.forEach(walk);
      }
    }).observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ATTRS });
  };
  if (document.body) start(); else document.addEventListener('DOMContentLoaded', start);
  const nativeConfirm = window.confirm.bind(window);
  window.confirm = msg => nativeConfirm(tr(String(msg)));
})();
