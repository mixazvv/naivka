// Opt-in visible-window monitoring. No saved credentials or persistent history.
const VPS_HISTORY = new Map();
const VPS_CHECKS = {
  root_login: ['Вход root по SSH', 'Базовый контекст sshd -T. Результат Match для текущего SSH-сеанса показан отдельно.'],
  password_auth: ['Парольный вход SSH', 'Эффективные значения sshd -T по умолчанию.'],
  keyboard_auth:['Интерактивный вход SSH','Может использовать PAM и пароль даже при PasswordAuthentication no.'],
  pam:['PAM','Проверяется включение PAM; содержимое цепочек PAM не анализируется.'],
  publickey_auth:['Вход по ключам','Эффективное значение sshd -T.'],
  empty_passwords:['Пустые пароли SSH','Эффективное значение sshd -T.'],
  auth_tries:['Попытки аутентификации','Предупреждение при MaxAuthTries больше 6.'],
  x11_forwarding:['X11 forwarding','Проверьте необходимость перенаправления X11.'],
  match_root:['SSH Match: root','sshd -T -C для пользователя и IP текущего SSH-сеанса; другие пользователи, IP, host/rdomain и локальные политики могут отличаться.'],
  match_password:['SSH Match: пароль','Для текущего SSH-сеанса; не полный перебор правил Match.'],
  match_keyboard:['SSH Match: интерактивный вход','Для текущего SSH-сеанса.'],
  firewalld:['firewalld','Состояние службы не подтверждает достаточность правил.'],
  nft_input:['nftables','Наблюдение input hook и drop/reject в ruleset; не анализ достижимости или всех цепочек и namespaces.'],
  iptables_input:['iptables INPUT','Только политика цепочки INPUT IPv4; исключения и IPv6 отдельно не подтверждены.'],
  firewall: ['UFW', 'Активность UFW не подтверждает достаточность правил. Другие firewall здесь не проверяются.'],
  config_mode: ['Права /etc/sing-box/config.json', 'Unix-биты доступа; владелец и именованные ACL показаны отдельно.'],
  certificate: ['Сертификат Hysteria2', 'Только /etc/sing-box/naivka-hy2.crt. Проверяется срок окончания, без проверки доверия и имени. Сертификаты панели и Caddy по фиксированным путям показаны отдельно.'],
  os_updates: ['Обновления ОС по локальному кэшу', 'Debian/Ubuntu: только существующие индексы APT. Полнота источников и security-обновления отдельно не проверены. Скачивание и установка не выполняются.'],
  local_listeners: ['Локальные TCP/UDP-порты', 'Привязки в сетевом окружении SSH. Firewall, NAT и другие сетевые пространства контейнеров могут менять доступность из интернета.'],
  external_ports: ['Порты, доступные из интернета', 'Внешнее сканирование не выполнялось. Наличие нестандартного порта само по себе не означает уязвимость.']
};
const VPS_VALUES = {
  unknown: 'Не определено', unsupported: 'Не поддерживается для этой ОС',
  none_in_cache: 'В локальном кэше обновлений не найдено', available_in_cache: 'В локальном кэше есть обновления',
  observed: 'Список получен', truncated: 'Частичный список (до 128 записей)',
  no: 'Запрещён', yes: 'Разрешён',
  'prohibit-password': 'Разрешён по ключу', 'without-password': 'Разрешён по ключу',
  'forced-commands-only': 'Только заданные команды', bounded:'Не более 6',high:'Больше 6',root:'root',other:'Другой владелец',basic:'Именованных записей нет',extended:'Есть именованные записи ACL',drop_observed:'Обнаружены hook input и политика drop/reject',hook_observed:'Обнаружен hook input',no_hook:'Hook input не найден',accept:'ACCEPT',drop:'DROP',active: 'Активна', inactive: 'Неактивна', failed: 'Ошибка',
  enabled: 'Включён', disabled: 'Отключён', static: 'Статическая служба', masked: 'Заблокирован',
  restricted: 'Нет доступа группы и остальных', shared_access: 'Есть доступ группы или остальных',
  valid_30d: 'Не истекает в ближайшие 30 дней', expires_30d: 'Истекает в течение 30 дней', expired: 'Истёк'
};
function vpsRemember(id, report) {
  const items = VPS_HISTORY.get(id) || [];
  items.push(report); if (items.length > 60) items.shift();
  VPS_HISTORY.delete(id); VPS_HISTORY.set(id, items);
  if (VPS_HISTORY.size > 20) VPS_HISTORY.delete(VPS_HISTORY.keys().next().value);
  return items;
}
function vpsResults(report, history, tab='all') {
  const m = report.metrics || {};
  const rate=vpsNetworkRate(report,history);
  const num = k => Number.isFinite(m[k]) ? m[k] : null;
  const bytes = k => num(k) === null ? 'Не определено' : (m[k] / 1073741824).toLocaleString('ru-RU', { maximumFractionDigits: 2 }) + ' ГиБ';
  const pair = (used, total) => bytes(used) + ' / ' + bytes(total);
  const rows = [
    ['Использование CPU (замер 1 с)', num('cpu_usage') === null ? 'Не определено' : m.cpu_usage + ' %'],
    ['Нагрузка CPU за 1 мин / логических CPU', (num('load') ?? 'Не определено') + ' / ' + (num('cpus') ?? 'Не определено')],
    ['RAM: занято / всего', pair('memory_used', 'memory_total')],
    ['Корневой диск: занято / всего', pair('disk_used', 'disk_total')],
    ['Нагрузка за 5 / 15 мин', (num('load5') ?? '?')+' / '+(num('load15') ?? '?')],
    ['Swap: занято / всего',pair('swap_used','swap_total')],
    ['Inode: занято / всего',(num('inodes_used') ?? '?')+' / '+(num('inodes_total') ?? '?')],
    ['Процессы',num('processes') ?? 'Не определено'],
    ['PSI avg10: CPU / память / I/O',(num('pressure_cpu') ?? '?')+' / '+(num('pressure_memory') ?? '?')+' / '+(num('pressure_io') ?? '?')+' %'],
    ['Скорость: приём / передача',rate?rate.map(n=>(n/1048576).toLocaleString('ru-RU',{maximumFractionDigits:2})+' МиБ/с').join(' / '):'Нужны два совместимых замера'],
    ['Время работы ОС', num('uptime') === null ? 'Не определено' : (m.uptime / 3600).toLocaleString('ru-RU', { maximumFractionDigits: 1 }) + ' ч'],
    ['Счётчики сети: принято / отправлено', bytes('network_rx') + ' / ' + bytes('network_tx')]
  ];
  const audit=h('div',{class:'vps-audit'},
    h('h3', null, 'Службы и аудит безопасности'),
    h('p', { class: 'hint' }, 'Это частичная проверка, а не заключение о безопасности. Неизвестный результат обычно означает отсутствие службы, утилиты или прав.'),
    ...(report.checks || []).slice().sort((a,b)=>({warning:0,unknown:1,info:2,ok:3}[a.level]??1)-({warning:0,unknown:1,info:2,ok:3}[b.level]??1)).map(c => {
      let spec = VPS_CHECKS[c.id];
      if (!spec && /^(service|enabled)_(sing-box|xray|naivka-caddy|x-ui|marzban|nginx|caddy|fail2ban|ssh|sshd)$/.test(c.id)) {
        const i = c.id.indexOf('_'); spec = [(c.id.startsWith('service_') ? 'Состояние: ' : 'Автозапуск: ') + c.id.slice(i + 1), 'По данным systemd; другие панели и службы не проверены.'];
      }
      if(!spec&&/^(owner|access|acl)_(singbox|xray|panel|ssh)$/.test(c.id)){
        const [kind,label]=c.id.split('_');
        spec=[({owner:'Владелец',access:'Права',acl:'ACL'})[kind]+': '+label,'Фиксированный путь конфигурации: sing-box, /usr/local/etc/xray/config.json, /etc/x-ui/x-ui.db или /etc/ssh/sshd_config. Именованные ACL требуют проверки эффективной маски и доступа вручную; сами записи не передаются.'];
      }
      if(!spec&&/^cert_(caddy|panel)$/.test(c.id))spec=['Сертификат '+c.id.slice(5),'Только /etc/caddy/cert.pem или /etc/x-ui/cert.pem; срок окончания, без доверия, имени и сертификатов из других путей.'];
      if (!spec) return null;
      const value = VPS_VALUES[c.value] || 'Не определено';
      return h('div', { class: 'panel' }, h('div', { class: c.level === 'warning' ? 'err-text' : '' }, spec[0] + ': ' + value), h('p', { class: 'hint' }, spec[1]),
        c.level === 'warning' ? h('p', { class: 'hint' }, c.id === 'os_updates' ? 'Проверьте доступные обновления в рамках обслуживания сервера.' : 'Проверьте настройку вручную. Перед изменением SSH или firewall сохраните альтернативный доступ и план восстановления.') : null);
    }),
    vpsUpdatesDetails(report),
    vpsListenersDetails(report));
  const primary=[rows[0],rows[2],rows[3],rows[9],rows[10]];
  const extras=rows.filter(r=>!primary.includes(r));
  const table=items=>h('table',{class:'table'},h('tbody',null,items.map(([label,value])=>h('tr',null,h('td',null,label),h('td',null,value)))));
  return h('div',null,
    h('p',{class:'hint'},'SSH доступен · '+new Date(report.checkedAt*1000).toLocaleString('ru-RU')),
    tab!=='audit'?h('div',null,table(primary),
      h('p',{class:'hint'},'Аудит: предупреждений '+(report.checks||[]).filter(c=>c.level==='warning').length+' · неизвестных проверок '+(report.checks||[]).filter(c=>c.level==='unknown').length),
      vpsHistoryChart(history),h('details',{class:'vps-extra'},h('summary',null,'Дополнительные метрики'),table(extras)),
      h('p',{class:'hint'},'Скорость — разница суммарных счётчиков интерфейсов кроме lo; туннели могут учитывать трафик повторно. После перезагрузки, смены интерфейсов, снижения суммарных счётчиков или перерыва больше 5 минут скорость не вычисляется. PSI — доля ожидания ресурсов; нагрузка CPU — очередь задач.')):null,
    tab!=='monitor'?audit:null);
}
function vpsUpdatesDetails(report) {
  const u = report.updates;
  if (!u || u.provider !== 'apt_cached') return null;
  const date = t => new Date(t * 1000).toLocaleString('ru-RU');
  const old = report.checkedAt - u.oldestIndex > 7 * 86400;
  return h('div', { class: 'panel' }, h('h3', null, 'Кэш APT'),
    h('p', null, 'Пакетов с доступными обновлениями: ' + u.count),
    h('p', { class: 'hint' }, 'Изменение индексов: от ' + date(u.oldestIndex) + ' до ' + date(u.newestIndex) + ' (время сервера).'),
    h('p', { class: 'hint' }, old ? 'Часть индексов старше 7 дней. Свежесть и полнота данных не подтверждены; нулевой результат не означает, что сервер полностью обновлён.' : 'Даты файлов не подтверждают успешное обновление всех источников. Нулевой результат не означает, что сервер полностью обновлён.'));
}
function vpsListenersDetails(report) {
  const state = (report.checks || []).find(c => c.id === 'local_listeners');
  if (!state || !['observed', 'truncated'].includes(state.value)) return null;
  const bindings = { loopback: 'Loopback (на этом сервере)', wildcard: 'Все интерфейсы', specific: 'Отдельный адрес' };
  const items = (report.listeners || []).filter(p => ['tcp', 'udp'].includes(p.protocol) && ['loopback', 'wildcard', 'specific'].includes(p.binding) && Number.isInteger(p.port) && p.port > 0 && p.port <= 65535).slice(0, 128);
  return h('div', { class: 'panel' }, h('h3', null, 'Локальные привязки портов'),
    items.length ? h('table', { class: 'table' },
      h('thead', null, h('tr', null, h('th', null, 'Протокол'), h('th', null, 'Порт'), h('th', null, 'Привязка'))),
      h('tbody', null, items.map(p => h('tr', null, h('td', null, p.protocol.toUpperCase()), h('td', null, p.port), h('td', null, bindings[p.binding]))))) : h('p', null, 'Локальных слушающих TCP/UDP-сокетов в этом окружении не найдено.'),
    state.value === 'truncated' ? h('p', { class: 'hint' }, 'Показаны первые 128 различных привязок; список неполный.') : null,
    h('p', { class: 'hint' }, 'Одинаковые записи объединены. Эти привязки не подтверждают доступность портов из интернета; нестандартный порт сам по себе не означает уязвимость.'));
}
// Require the same boot and interface set, positive server clock/uptime deltas
// and monotonic counters. Missing identity means unknown, never guessed speed.
function vpsNetworkRate(report,history){
  const prev=history.filter(r=>r!==report).at(-1), a=prev?.metrics,b=report.metrics;
  if(!a||!b||!report.bootId||report.bootId!==prev.bootId||!Number.isFinite(b.network_set)||b.network_set!==a.network_set)return null;
  const dt=b.server_time-a.server_time,du=b.uptime-a.uptime;
  if(!(dt>0&&dt<=300&&du>0&&Math.abs(dt-du)<=3))return null;
  if(!['network_rx','network_tx'].every(k=>Number.isSafeInteger(a[k])&&Number.isSafeInteger(b[k])))return null;
  const diff=['network_rx','network_tx'].map(k=>b[k]-a[k]);
  return diff.every(n=>Number.isFinite(n)&&n>=0)?diff.map(n=>n/dt):null;
}
function vpsHistoryChart(history){
  if(history.length<2)return null;
  return h('div',{class:'panel'},h('h3',null,'CPU: последние '+history.length+' замеров'),
    h('div',{class:'vps-chart',role:'img','aria-label':'История CPU, проценты: '+history.map(r=>r.metrics.cpu_usage??'?').join(', ')},history.map(r=>{
      const n=r.metrics.cpu_usage;
      return h('span',{class:'vps-bar',style:'height:'+Math.max(2,Math.min(100,Number.isFinite(n)?n:0))+'%',title:new Date(r.checkedAt*1000).toLocaleTimeString('ru-RU')+' · '+(n??'?')+' %'});
    })),h('p',{class:'hint'},'До 60 замеров на сервер и 20 серверов; только в памяти окна. Пропуск данных не считается нулевой нагрузкой.'));
}
function vpsDialog(host) {
  const result = h('div');
  const err = h('div', { class: 'err-text', role: 'status' });
  const go = h('button', { class: 'btn primary' }, 'Проверить сейчас');
  let busy = false, timer=0, stopped=false, tab='monitor';
  let latest=null;
  const paint=()=>{if(latest)result.replaceChildren(vpsResults(latest,VPS_HISTORY.get(host.id)||[],tab));};
  const tabs=h('div',{class:'vps-tabs',role:'group','aria-label':'Раздел отчёта VPS'},[['monitor','Мониторинг'],['audit','Аудит безопасности']].map(([key,title])=>h('button',{class:'btn small','aria-pressed':String(tab===key),onclick:()=>{tab=key;for(const b of tabs.children)b.setAttribute('aria-pressed',String(b.textContent===title));paint();}},title)));
  const auto=h('input',{type:'checkbox','aria-label':'Обновлять каждые 30 секунд'});
  const stop=()=>{clearTimeout(timer);timer=0;auto.checked=false;};
  const schedule=()=>{clearTimeout(timer);if(!stopped&&auto.checked&&d.isConnected&&!document.hidden)timer=setTimeout(()=>run(null,true),30000);};
  const close=()=>{stopped=true;stop();document.removeEventListener('visibilitychange',visibility);};
  const visibility=()=>{if(document.hidden)stop();};
  const d = openDialog(h('h2', null, 'Состояние VPS: ' + (host.name || host.host)),
    h('p', { class: 'hint' }, 'Отчёт по SSH для своего Linux-сервера. Команды только читают состояние; ожидание отчёта — до 30 секунд после входа. Отчёт и история хранятся только в памяти окна.'),
    h('label',{class:'field'},auto,'Обновлять каждые 30 секунд, пока окно открыто'),tabs,result, err, h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Закрыть'), go));
  const old = VPS_HISTORY.get(host.id);
  if (old && old.length) {latest=old[old.length-1];paint();}
  async function run(creds, automatic=false) {
    if(automatic&&(!auto.checked||document.hidden)){stop();return;}
    if (busy || !d.isConnected) return;
    busy = true; go.disabled = true; err.textContent = 'Проверяю…';
    try {
      const r = await api('/api/vps/report', { host: host.id, ...(creds || {}) });
      if (!d.isConnected) return;
      if (r.needAuth) {
        stop();
        if(automatic){err.textContent='SSH-сеанс истёк. Нажмите «Проверить сейчас» для входа.';return;}
        busy = false; go.disabled = false; err.textContent = '';
        return sshAuthDialog(host, c => run(c));
      }
      const history = vpsRemember(host.id, r);
      latest=r;paint(); err.textContent = '';
    } catch (e) {
      stop();if (d.isConnected) err.textContent = e.message;
      if (creds) throw e;
    } finally { busy = false; go.disabled = false;schedule(); }
  }
  go.addEventListener('click', () => run());
  auto.addEventListener('change',()=>{if(auto.checked)return run();stop();});
  d.addEventListener('close',close);d.addEventListener('cancel',close);
  document.addEventListener('visibilitychange',visibility);
}
