function serverScoresResults(data) {
  const rows = data.rows || [];
  return h('div', null,
    h('p', { class: 'hint' }, data.enabled ? 'Режим включён. Свежие контрольные запросы выполняются раз в минуту при работающем ядре.' : 'Режим выключен. Включите «Умный выбор своих серверов» в настройках надёжности.'),
    h('p', { class: 'hint' }, 'История до 20 проверок за 15 минут хранится только в памяти и сбрасывается после смены выбора, конфигурации или перезапуска ядра. Это успешность HTTP-запросов через сервер, не ICMP-потери. Скорость загрузки неизвестна и в оценке не учитывается.'),
    rows.length ? h('table', { class: 'table' },
      h('thead', null, h('tr', null, ...['Сервер', 'Оценка / 100', 'Успешные запросы', 'Медиана / p95', 'Статус'].map(t => h('th', null, t)))),
      h('tbody', null, rows.map(r => {
        const sv = serverByTag(r.tag);
        const state = r.quarantineUntil ? 'Карантин до ' + new Date(r.quarantineUntil * 1000).toLocaleTimeString('ru-RU') : r.eligible ? 'Свежий ответ' : 'Нет свежего подходящего ответа';
        return h('tr', null, h('td', null, sv ? (sv.name || sv.host) : r.tag),
          h('td', null, r.successes ? r.score.toFixed(1) : 'Не определено'), h('td', null, `${r.successes} / ${r.samples}`),
          h('td', null, r.successes ? `${r.median} / ${r.p95} мс` : 'Не определено'), h('td', null, state));
      }))) : h('p', null, data.enabled ? 'Свежих измерений пока нет. Первый замер появится в течение минуты после подключения.' : 'Измерения не собираются, пока режим выключен.'),
    h('p', { class: 'hint' }, 'Выдержка между переключениями исправных серверов — 3 минуты, порог улучшения — 12 баллов и два подтверждения. Две неудачи текущего сервера позволяют выбрать резерв раньше; после повторных сбоев действует карантин на 2 минуты. Решения записываются в ленту событий.'));
}
async function serverScoresDialog() {
  const body = h('div', { role: 'status' }, 'Загружаю…');
  const refresh = h('button', { class: 'btn small' }, 'Обновить');
  const d = openDialog(h('h2', null, 'Качество серверов'), body,
    h('div', { class: 'row end mt' }, refresh, h('button', { class: 'btn', onclick: () => d.close() }, 'Закрыть')));
  let busy = false;
  async function load() {
    if (busy || !d.isConnected) return;
    busy = true; refresh.disabled = true;
    try { const data = await api('/api/server-scores'); if (d.isConnected) body.replaceChildren(serverScoresResults(data)); }
    catch (e) { if (d.isConnected) body.textContent = e.message; }
    finally { busy = false; refresh.disabled = false; }
  }
  refresh.addEventListener('click', load); await load();
}
