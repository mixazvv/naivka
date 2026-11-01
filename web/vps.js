// Explicit snapshots only; no polling, saved credentials or persistent server history.
const VPS_HISTORY = new Map();
const VPS_CHECKS = {
  root_login: ['Вход root по SSH', 'sshd -T; правила Match для конкретного подключения не проверены.'],
  password_auth: ['Парольный вход SSH', 'sshd -T; keyboard-interactive/PAM и правила Match отдельно не проверены.'],
  firewall: ['UFW', 'Активность UFW не подтверждает достаточность правил. Другие firewall здесь не проверяются.'],
  config_mode: ['Права /etc/sing-box/config.json', 'Проверяются только Unix-биты доступа; ACL и владелец не проверены.'],
  certificate: ['Сертификат Hysteria2', 'Только /etc/sing-box/naivka-hy2.crt. Проверяется срок окончания, без проверки доверия и имени. Сертификаты панелей и Caddy не проверены.'],
  os_updates: ['Обновления ОС', 'Проверка пакетных менеджеров пока не реализована.'],
  external_ports: ['Порты, доступные из интернета', 'Внешнее сканирование не выполнялось. Наличие нестандартного порта само по себе не означает уязвимость.']
};
const VPS_VALUES = {
  unknown: 'Не определено', no: 'Запрещён', yes: 'Разрешён',
  'prohibit-password': 'Разрешён по ключу', 'without-password': 'Разрешён по ключу',
  'forced-commands-only': 'Только заданные команды', active: 'Активна', inactive: 'Неактивна', failed: 'Ошибка',
  enabled: 'Включён', disabled: 'Отключён', static: 'Статическая служба', masked: 'Заблокирован',
  restricted: 'Нет доступа группы и остальных', shared_access: 'Есть доступ группы или остальных',
  valid_30d: 'Не истекает в ближайшие 30 дней', expires_30d: 'Истекает в течение 30 дней', expired: 'Истёк'
};
function vpsRemember(id, report) {
  const items = VPS_HISTORY.get(id) || [];
  items.push(report); if (items.length > 5) items.shift();
  VPS_HISTORY.delete(id); VPS_HISTORY.set(id, items);
  if (VPS_HISTORY.size > 20) VPS_HISTORY.delete(VPS_HISTORY.keys().next().value);
  return items;
}
function vpsResults(report, history) {
  const m = report.metrics || {};
  const num = k => Number.isFinite(m[k]) ? m[k] : null;
  const bytes = k => num(k) === null ? 'Не определено' : (m[k] / 1073741824).toLocaleString('ru-RU', { maximumFractionDigits: 2 }) + ' ГиБ';
  const pair = (used, total) => bytes(used) + ' / ' + bytes(total);
  const rows = [
    ['Использование CPU (замер 1 с)', num('cpu_usage') === null ? 'Не определено' : m.cpu_usage + ' %'],
    ['Нагрузка CPU за 1 мин / логических CPU', (num('load') ?? 'Не определено') + ' / ' + (num('cpus') ?? 'Не определено')],
    ['RAM: занято / всего', pair('memory_used', 'memory_total')],
    ['Корневой диск: занято / всего', pair('disk_used', 'disk_total')],
    ['Время работы ОС', num('uptime') === null ? 'Не определено' : (m.uptime / 3600).toLocaleString('ru-RU', { maximumFractionDigits: 1 }) + ' ч'],
    ['Счётчики сети: принято / отправлено', bytes('network_rx') + ' / ' + bytes('network_tx')]
  ];
  return h('div', null,
    h('p', { class: 'hint' }, 'SSH доступен · ' + new Date(report.checkedAt * 1000).toLocaleString('ru-RU')),
    h('table', { class: 'table' }, h('tbody', null, rows.map(([label, value]) => h('tr', null, h('td', null, label), h('td', null, value))))),
    h('p', { class: 'hint' }, 'Нагрузка CPU — очередь задач, не процент использования. Сетевые счётчики всех интерфейсов кроме lo — не скорость; туннели могут учитывать трафик повторно.'),
    h('h3', null, 'Службы и аудит безопасности'),
    h('p', { class: 'hint' }, 'Это частичная проверка, а не заключение о безопасности. Неизвестный результат обычно означает отсутствие службы, утилиты или прав.'),
    ...(report.checks || []).map(c => {
      let spec = VPS_CHECKS[c.id];
      if (!spec && /^(service|enabled)_(sing-box|xray|naivka-caddy|x-ui)$/.test(c.id)) {
        const i = c.id.indexOf('_'); spec = [(c.id.startsWith('service_') ? 'Состояние: ' : 'Автозапуск: ') + c.id.slice(i + 1), 'По данным systemd; другие панели и службы не проверены.'];
      }
      if (!spec) return null;
      const value = VPS_VALUES[c.value] || 'Не определено';
      return h('div', { class: 'panel' }, h('div', { class: c.level === 'warning' ? 'err-text' : '' }, spec[0] + ': ' + value), h('p', { class: 'hint' }, spec[1]),
        c.level === 'warning' ? h('p', { class: 'hint' }, 'Проверьте настройку вручную. Перед изменением SSH или firewall сохраните альтернативный доступ и план восстановления.') : null);
    }),
    history.length > 1 ? h('p', { class: 'hint' }, 'Последние замеры в этом окне: ' + history.map(r => new Date(r.checkedAt * 1000).toLocaleTimeString('ru-RU') + ' · нагрузка ' + (r.metrics.load ?? '?')).join('; ')) : null);
}
function vpsDialog(host) {
  const result = h('div');
  const err = h('div', { class: 'err-text', role: 'status' });
  const go = h('button', { class: 'btn primary' }, 'Проверить сейчас');
  let busy = false;
  const d = openDialog(h('h2', null, 'Состояние VPS: ' + (host.name || host.host)),
    h('p', { class: 'hint' }, 'Разовый отчёт по SSH для своего Linux-сервера. Команды только читают состояние; ожидание отчёта — до 30 секунд после входа. Отчёт и история хранятся только в памяти окна.'),
    result, err, h('div', { class: 'row end mt' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Закрыть'), go));
  const old = VPS_HISTORY.get(host.id);
  if (old && old.length) result.replaceChildren(vpsResults(old[old.length - 1], old));
  async function run(creds) {
    if (busy || !d.isConnected) return;
    busy = true; go.disabled = true; err.textContent = 'Проверяю…';
    try {
      const r = await api('/api/vps/report', { host: host.id, ...(creds || {}) });
      if (!d.isConnected) return;
      if (r.needAuth) {
        busy = false; go.disabled = false; err.textContent = '';
        return sshAuthDialog(host, c => run(c));
      }
      const history = vpsRemember(host.id, r);
      result.replaceChildren(vpsResults(r, history)); err.textContent = '';
    } catch (e) {
      if (d.isConnected) err.textContent = e.message;
      if (creds) throw e;
    } finally { busy = false; go.disabled = false; }
  }
  go.addEventListener('click', () => run());
}
