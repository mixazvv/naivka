'use strict';
function vpsAddressDialog(host) {
  let plan = null, busy = false, finished = false, committing = false;
  const address = h('input', { type: 'text', placeholder: 'Новый IPv4 или IPv6', spellcheck: 'false', autocomplete: 'off', oninput: () => { plan = null; preview.replaceChildren(); controls(); } });
  const status = h('p', { class: 'hint', role: 'status' });
  const preview = h('div');
  const check = h('button', { class: 'btn primary', onclick: () => run(false) }, 'Проверить новый адрес');
  const apply = h('button', { class: 'btn primary', hidden: true, onclick: () => run(true) }, 'Сохранить новый адрес');
  const close = h('button', { class: 'btn', onclick: () => dialog.close() }, 'Отмена');
  const help = h('details', { class: 'more mt' }, h('summary', null, 'Новый адрес недоступен?'),
    h('p', { class: 'hint' }, 'После восстановления VPS из бэкапа Linux может хранить прежний IP. Откройте консоль сервера в личном кабинете Fornex. Возьмите новый IP, префикс сети и шлюз из панели именно этого VPS.'),
    h('ol', null,
      h('li', null, 'Проверьте текущие адреса и маршруты: ', h('code', null, 'ip -br addr'), ', ', h('code', null, 'ip route'), '.'),
      h('li', null, 'Обновите сетевую конфигурацию Linux по инструкции Fornex. Для netplan используйте netplan try с автоматическим возвратом; оставайтесь в консоли хостинга.'),
      h('li', null, 'Проверьте доступность нового IP и порта SSH, затем вернитесь в этот мастер.')),
    h('a', { href: 'https://ru.fornex.com/help/change_ip/', target: '_blank', rel: 'noopener noreferrer' }, 'Инструкция Fornex'),
    h('p', { class: 'hint' }, 'Naivka обновляет свои подключения после восстановления сети. Адреса интерфейсов, шлюз и DNS-записи хостинга задаются в консоли и панели хостинга.'));
  const dialog = openDialog(h('h2', null, 'Смена IP VPS'),
    h('p', { class: 'hint' }, (host.name || host.host) + ' · текущий адрес ' + host.host),
    h('label', { class: 'field' }, h('span', null, 'Новый IP из панели хостинга'), address),
    h('p', { class: 'hint' }, 'Naivka проверит ключ SSH и покажет связанные подключения. Перед сохранением отключите VPN.'),
    help, preview, status, h('div', { class: 'row end mt' }, close, check, apply));
  function controls() {
    address.disabled = busy || finished;
    check.disabled = busy || finished || !address.value.trim();
    check.hidden = finished;
    apply.hidden = !plan || finished;
    apply.disabled = busy;
    close.textContent = finished ? 'Готово' : 'Отмена';
    close.disabled = committing;
    preview.querySelectorAll('input').forEach(x => { x.disabled = busy; });
  }
  async function run(commit) {
    if (busy || finished || (commit && !plan)) return;
    busy = true; committing = commit; controls(); status.className = 'hint'; status.textContent = commit ? 'Сохраняю…' : 'Проверяю SSH по новому адресу…';
    try {
      if (!await saveNow()) throw new Error('Не удалось сохранить текущие настройки. Повторите попытку.');
      const selected = commit ? Array.from(preview.querySelectorAll('input:checked')).map(x => x.value) : [];
      const result = await api('/api/ownhost/address', { host: host.id, address: address.value.trim(), apply: commit, confirm: commit, revision: plan?.revision || '', servers: selected });
      if (commit) { S = result.settings; delete FRIENDS[host.id]; }
      if (!dialog.isConnected) return;
      if (commit) {
        finished = true;
        // Settings returned by commit are already canonical.
                renderOwn();
        preview.replaceChildren(h('p', null, 'Адрес VPS и выбранных подключений обновлён. Старые ссылки с числовым IP и QR-коды нужно выдать заново.'),
          h('p', { class: 'hint' }, 'Резервная копия прежних настроек: ' + result.plan.backup + ' в папке данных Naivka.'),
          h('p', { class: 'hint' }, 'Если используете домен, обновите его A/AAAA-записи в панели DNS. SNI и ключи VPN сохранены.'));
        status.textContent = 'Готово. Теперь можно подключиться и проверить интернет.';
      } else {
        plan = result.plan;
        preview.replaceChildren(h('h3', null, 'Что обновится'),
          h('p', null, plan.old + ' → ' + plan.address),
          h('p', { class: 'hint' }, 'Ключ SSH совпал: ' + plan.fingerprint),
          h('p', null, 'Адрес своего VPS' + (plan.panels.length ? ' и SSH-подключения панелей: ' + plan.panels.map(x => x.name).join(', ') : '') + '.'),
          ...plan.servers.map(x => h('label', { class: 'check' }, h('input', { type: 'checkbox', value: x.id, checked: true }), h('span', null, x.name || x.id))),
          h('p', { class: 'hint' }, 'Выберите подключения с прежним IP. Доменные имена и записи из подписок сохраняются — обновите DNS или подписку у её владельца. Пароли, SNI, сертификаты и ключи VPN сохраняются. Ручные правила и JSON проверьте отдельно.'));
        status.textContent = 'Проверьте список и сохраните новый адрес.';
      }
    } catch (e) {
      if (dialog.isConnected) { status.className = 'err-text'; status.textContent = e.message; }
    } finally { busy = false; committing = false; if (dialog.isConnected) controls(); }
  }
  dialog.addEventListener('cancel', e => { if (committing) e.preventDefault(); });
  controls();
}
