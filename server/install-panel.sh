#!/usr/bin/env bash
# Ставит на свой VPS веб-панель для управления протоколами:
#   * 3X-UI (MHSanaei/3x-ui) — самая популярная панель на Xray: VLESS Reality/xHTTP, Trojan,
#     Shadowsocks, пользователи, лимиты трафика, подписки;
#   * s-ui (alireza0/s-ui) — панель на sing-box: Hysteria2, TUIC, AnyTLS, Naive, Reality и другие.
#
# Панели — сторонние программы. Скрипт запускает их ОФИЦИАЛЬНЫЕ установщики, взятые из последнего
# релиза на GitHub (сами установщики сверяют контрольные суммы архивов), и настраивает их безопасно:
#   * случайные порт, путь, логин и длинный пароль;
#   * по умолчанию панель слушает только 127.0.0.1 — из интернета её не видно, а Naivka открывает
#     её через SSH-туннель;
#   * 3X-UI можно открыть в интернет по HTTPS (сертификат Let's Encrypt на IP сервера, нужен
#     свободный порт 80). s-ui в интернет не открывается: её веб-интерфейс без HTTPS.
#
# Параметры (переменные окружения):
#   PANEL        3x-ui или s-ui
#   ACCESS       tunnel (по умолчанию) или public (только 3x-ui)
#   SERVER_ADDR  адрес сервера (для ссылки на панель в режиме public)
#
# Повторный запуск не переустанавливает панель, а выдаёт новые логин, пароль, порт и путь.
# Удаление: x-ui uninstall или s-ui uninstall (меню самих панелей).
#
# Последняя строка вывода «NAIVKA-PANEL <base64 JSON>» — данные для входа, их сохраняет Naivka.
set -Eeuo pipefail

log()  { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
trap 'die "Ошибка на строке $LINENO. Подробности выше."' ERR

[[ "${EUID}" -eq 0 ]] || die "Нужны права root."
PANEL="${PANEL:-}"
ACCESS="${ACCESS:-tunnel}"
SERVER_ADDR="${SERVER_ADDR:-}"
[[ "$PANEL" == 3x-ui || "$PANEL" == s-ui ]] || die "PANEL — 3x-ui или s-ui"
[[ "$ACCESS" == tunnel || "$ACCESS" == public ]] || die "ACCESS — tunnel или public"
[[ "$PANEL" == s-ui && "$ACCESS" == public ]] && { warn "s-ui без HTTPS — открываю только через туннель."; ACCESS=tunnel; }

# shellcheck disable=SC1091
. /etc/os-release
[[ "${ID:-}" == debian || "${ID:-}" == ubuntu || " ${ID_LIKE:-} " == *" debian "* ]] \
  || die "Скрипт рассчитан на Debian или Ubuntu (у вас ${PRETTY_NAME:-неизвестно})."
command -v systemctl >/dev/null || die "Нужен systemd."

export DEBIAN_FRONTEND=noninteractive
log "Ставлю нужное (curl, jq, sqlite3)…"
apt-get update -y >/dev/null
apt-get install -y curl ca-certificates openssl jq sqlite3 iproute2 >/dev/null

rand_hex() { openssl rand -hex "$1"; }
tcp_busy() { ss -Htln "( sport = :$1 )" 2>/dev/null | grep -q .; }
free_port() {
  local p
  for _ in $(seq 1 50); do
    p=$(( 20000 + $(od -An -N2 -tu2 /dev/urandom | tr -d ' ') % 40000 ))
    tcp_busy "$p" || { echo "$p"; return; }
  done
  die "Не нашёл свободный порт для панели."
}
latest_tag() { # latest_tag владелец/репозиторий
  local tag url
  tag="$(curl -fsSL --max-time 30 "https://api.github.com/repos/$1/releases/latest" 2>/dev/null | jq -r '.tag_name // empty')" || true
  if [[ -z "$tag" ]]; then
    url="$(curl -sSLI -o /dev/null -w '%{url_effective}' --max-time 30 "https://github.com/$1/releases/latest" 2>/dev/null)" || true
    tag="${url##*/tag/}"
    [[ "$tag" == "$url" ]] && tag=""
  fi
  [[ "$tag" =~ ^v?[0-9][0-9A-Za-z._-]*$ ]] || die "Не удалось узнать последнюю версию $1."
  echo "$tag"
}
listening_on() { # listening_on порт → адрес, на котором слушает
  ss -Htln "( sport = :$1 )" 2>/dev/null | awk '{print $4}' | head -n1
}

USER_NAME="admin$(rand_hex 3)"
PASS="$(rand_hex 16)"
PORT="$(free_port)"
WEBPATH="$(rand_hex 9)"
URL=""
VERSION=""

if [[ "$PANEL" == 3x-ui ]]; then
  XUI=/usr/local/x-ui/x-ui
  SSL_MODE=none
  IPV4=""
  [[ "$SERVER_ADDR" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && IPV4="$SERVER_ADDR"
  if [[ "$ACCESS" == public ]]; then
    if tcp_busy 80; then
      warn "Порт 80 занят (например, Caddy для NaiveProxy) — сертификат Let's Encrypt на IP не получить. Панель будет доступна только через туннель."
      ACCESS=tunnel
    else
      SSL_MODE=ip
    fi
  fi
  if [[ ! -x "$XUI" ]]; then
    VERSION="$(latest_tag MHSanaei/3x-ui)"
    log "Устанавливаю 3X-UI ${VERSION} официальным установщиком…"
    tmp="$(mktemp -d)"
    curl -fsSL --max-time 60 -o "$tmp/install.sh" "https://raw.githubusercontent.com/MHSanaei/3x-ui/${VERSION}/install.sh"
    XUI_NONINTERACTIVE=1 XUI_SSL_MODE="$SSL_MODE" XUI_USERNAME="$USER_NAME" XUI_PASSWORD="$PASS" \
      XUI_PANEL_PORT="$PORT" XUI_WEB_BASE_PATH="$WEBPATH" XUI_SERVER_IP="$IPV4" \
      bash "$tmp/install.sh" "$VERSION" </dev/null 2>&1 | sed -e 's/\x1b\[[0-9;]*m//g' | grep -viE 'password|username' || true
    rm -rf "$tmp"
    [[ -x "$XUI" ]] || die "Установщик 3X-UI не справился (журнал выше)."
  else
    log "3X-UI уже установлена — выдаю новые логин, пароль, порт и путь."
  fi
  "$XUI" setting -username "$USER_NAME" -password "$PASS" -port "$PORT" -webBasePath "$WEBPATH" >/dev/null
  if [[ "$ACCESS" == tunnel ]]; then
    "$XUI" setting -listenIP 127.0.0.1 >/dev/null
  fi
  systemctl restart x-ui
  sleep 3
  addr="$(listening_on "$PORT")"
  [[ -n "$addr" ]] || { journalctl -u x-ui -n 20 --no-pager >&2 || true; die "Панель не запустилась на порту $PORT."; }
  if [[ "$ACCESS" == tunnel && "$addr" != 127.0.0.1:* ]]; then
    systemctl stop x-ui
    die "Не удалось закрыть панель от интернета (слушает $addr) — на всякий случай остановил её."
  fi
  if [[ "$ACCESS" == public ]]; then
    # shellcheck disable=SC1091
    [[ -r /etc/x-ui/install-result.env ]] && . /etc/x-ui/install-result.env
    URL="${XUI_ACCESS_URL:-https://${SERVER_ADDR}:${PORT}/${WEBPATH}}"
    [[ "$URL" == */ ]] || URL="$URL/"
    if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then ufw allow "${PORT}/tcp" >/dev/null; fi
  fi
  [[ -n "$VERSION" ]] || VERSION="$("$XUI" -v 2>/dev/null | head -n1 || true)"
else
  SUI=/usr/local/s-ui/sui
  DB=/usr/local/s-ui/db/s-ui.db
  SUBPORT="$(free_port)"
  SUBPATH="$(rand_hex 8)"
  if [[ ! -x "$SUI" ]]; then
    VERSION="$(latest_tag alireza0/s-ui)"
    log "Устанавливаю s-ui ${VERSION} официальным установщиком…"
    tmp="$(mktemp -d)"
    curl -fsSL --max-time 60 -o "$tmp/install.sh" "https://raw.githubusercontent.com/alireza0/s-ui/${VERSION}/install.sh"
    # Вопросы установщика остаются без ответа (stdin пустой) — он ставит случайный логин, мы его сразу меняем.
    SUI_LANG=en bash "$tmp/install.sh" "$VERSION" </dev/null 2>&1 | sed -e 's/\x1b\[[0-9;]*m//g' | grep -viE 'password|username' || true
    rm -rf "$tmp"
    [[ -x "$SUI" ]] || die "Установщик s-ui не справился (журнал выше)."
  else
    log "s-ui уже установлена — выдаю новые логин, пароль, порт и путь."
  fi
  systemctl stop s-ui
  "$SUI" setting -port "$PORT" -path "/${WEBPATH}/" -subPort "$SUBPORT" -subPath "/${SUBPATH}/" >/dev/null
  "$SUI" admin -username "$USER_NAME" -password "$PASS" >/dev/null
  # Адреса прослушивания CLI не меняет — пишем в базу настроек (таблица settings: key, value).
  for k in webListen subListen; do
    sqlite3 "$DB" "DELETE FROM settings WHERE key='$k'; INSERT INTO settings(key, value) VALUES('$k', '127.0.0.1');"
  done
  systemctl start s-ui
  sleep 3
  addr="$(listening_on "$PORT")"
  [[ -n "$addr" ]] || { journalctl -u s-ui -n 20 --no-pager >&2 || true; die "s-ui не запустилась на порту $PORT."; }
  if [[ "$addr" != 127.0.0.1:* ]]; then
    systemctl stop s-ui
    die "Не удалось закрыть s-ui от интернета (слушает $addr) — на всякий случай остановил её."
  fi
  WEBPATH="${WEBPATH}/"
  [[ -n "$VERSION" ]] || VERSION="$("$SUI" -v 2>/dev/null | head -n1 || true)"
fi

if [[ "$ACCESS" == tunnel ]]; then
  log "Панель слушает только 127.0.0.1:${PORT} — из интернета её не видно. Открывайте через Naivka («Серверы» → «Панели»)."
else
  log "Панель доступна по HTTPS: ${URL}"
fi
log "Логин и пароль сохранены в Naivka. В панели не забудьте: порты протоколов не должны совпадать с уже занятыми."

json="$(jq -cn --arg type "$PANEL" --arg version "$VERSION" --arg access "$ACCESS" --argjson port "$PORT" \
  --arg path "/${WEBPATH%/}/" --arg user "$USER_NAME" --arg pass "$PASS" --arg url "$URL" \
  '{type:$type, version:$version, access:$access, port:$port, path:$path, user:$user, pass:$pass, url:$url}')"
echo "NAIVKA-PANEL $(printf '%s' "$json" | base64 -w0)"
