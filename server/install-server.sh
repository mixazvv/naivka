#!/usr/bin/env bash
# Ставит на свой VPS (Debian 11+ / Ubuntu 20.04+):
#   * VLESS + Reality (TCP, sing-box) — основной: маскируется под обычный HTTPS;
#   * Hysteria2 (UDP, sing-box, по желанию) — запасной: быстрый на плохом мобильном интернете;
#   * NaiveProxy (TCP, Caddy с forwardproxy, по желанию) — нужен свой домен.
# Без NaiveProxy домен не нужен. Hysteria2 работает со своим сертификатом, а клиент проверяет его
# по отпечатку ключа (не «без проверки»), поэтому подменить сервер незаметно нельзя.
# С NaiveProxy на 443 работает Caddy с вашим сайтом-заглушкой и сертификатом Let's Encrypt,
# а Reality уходит на 8443 и маскируется под этот же сайт.
#
# Обычно скрипт запускает Naivka по SSH: «Серверы» → «Свой сервер». Можно и вручную, от root:
#   SERVER_ADDR=1.2.3.4 REALITY_SNI=www.example.com bash install-server.sh
#
# Параметры (переменные окружения):
#   SERVER_ADDR   адрес сервера для ссылок (IP или домен) — обязательно
#   REALITY_SNI   чужой сайт для маскировки Reality: TLS 1.3, не заблокирован в России,
#                 лучше всего — у того же хостера, что и ваш сервер
#   REALITY_PORT  порт TCP; auto — 443, если свободен, иначе 8443
#   HY2           1 — добавить Hysteria2, 0 — нет (по умолчанию 1)
#   HY2_PORT      порт UDP; auto — 443, если свободен, иначе 8443
#   BBR           1 — включить BBR (ускоряет TCP на дальних каналах), 0 — не трогать (по умолчанию 1)
#   NAME          подпись серверов в ссылках (по умолчанию Naivka)
#   NAIVE         1 — добавить NaiveProxy (по умолчанию 0)
#   DOMAIN        домен для NaiveProxy: A-запись уже должна указывать на этот сервер
#
# Что делается на сервере:
#   * ставится официальный sing-box из репозитория SagerNet (apt);
#   * пишется /etc/sing-box/config.json (чужой конфиг сохраняется рядом как .bak);
#   * служба sing-box включается в автозапуск, память ограничивается;
#   * в ufw (если он включён) открываются выбранные порты; при BBR=1 — настройка в /etc/sysctl.d;
#   * с NAIVE=1: Caddy с модулем forwardproxy (сборка klzgrad, проверенная по контрольной сумме
#     GitHub, или собранная здесь из исходников) — служба naivka-caddy, конфиг /etc/naivka-caddy,
#     сайт-заглушка /var/www/naivka; порты 80 и 443 TCP.
# Что НЕ делается: не меняются пароли, SSH, пользователи; не трогаются другие программы.
#
# Повторный запуск безопасен: ключи сохраняются в /etc/sing-box/naivka.env, ссылки выводятся заново.
# Удаление: bash install-server.sh --remove
#
# Последние строки вывода «NAIVKA-LINK <ссылка>» Naivka добавляет в список серверов.
set -Eeuo pipefail

CONF_DIR="${CONF_DIR:-/etc/sing-box}"
STATE_FILE="$CONF_DIR/naivka.env"
CONFIG="$CONF_DIR/config.json"
DROPIN_DIR="/etc/systemd/system/sing-box.service.d"
CADDY_BIN="/usr/local/bin/caddy-naive"
CADDY_DIR="/etc/naivka-caddy"
CADDY_UNIT="/etc/systemd/system/naivka-caddy.service"
SITE_DIR="/var/www/naivka"
SYSCTL_FILE="/etc/sysctl.d/90-naivka-bbr.conf"
# NAIVKA_TEST=1 — проверка самой Naivka без VPS: без apt и systemd, sing-box из PATH запускается в фоне,
# сайт для маскировки может быть с самоподписанным сертификатом, а самопроверка идёт на SELFTEST_URL.
TEST="${NAIVKA_TEST:-0}"
SELFTEST_URL="https://www.gstatic.com/generate_204"
[[ "$TEST" == 1 && -n "${NAIVKA_SELFTEST_URL:-}" ]] && SELFTEST_URL="$NAIVKA_SELFTEST_URL"

log()  { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
trap 'die "Ошибка на строке $LINENO. Подробности выше."' ERR

[[ "${EUID}" -eq 0 ]] || die "Нужны права root (войдите как root или настройте sudo без пароля)."

# ---------- удаление ----------
if [[ "${1:-}" == "--remove" ]]; then
  # shellcheck disable=SC1090
  [[ -r "$STATE_FILE" ]] && . "$STATE_FILE"
  systemctl disable --now sing-box 2>/dev/null || true
  systemctl disable --now naivka-caddy 2>/dev/null || true
  rm -f "$CADDY_UNIT"
  if command -v ufw >/dev/null; then
    [[ -n "${REALITY_PORT:-}" ]] && ufw delete allow "${REALITY_PORT}/tcp" >/dev/null 2>&1 || true
    [[ -n "${HY2_PORT:-}" ]] && ufw delete allow "${HY2_PORT}/udp" >/dev/null 2>&1 || true
  fi
  rm -rf "$DROPIN_DIR" "$SYSCTL_FILE"
  systemctl daemon-reload 2>/dev/null || true
  log "sing-box и Caddy (NaiveProxy) остановлены и убраны из автозапуска, порты закрыты."
  log "Пакет можно удалить полностью: apt remove sing-box. Ключи остались в $STATE_FILE."
  exit 0
fi

# ---------- параметры ----------
SERVER_ADDR="${SERVER_ADDR:-}"
REALITY_SNI="${REALITY_SNI:-}"
REALITY_PORT="${REALITY_PORT:-auto}"
HY2="${HY2:-1}"
HY2_PORT="${HY2_PORT:-auto}"
BBR="${BBR:-1}"
NAME="${NAME:-Naivka}"
NAIVE="${NAIVE:-0}"
DOMAIN="${DOMAIN:-}"

HOST_RE='^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$'
[[ "$SERVER_ADDR" =~ $HOST_RE || "$SERVER_ADDR" =~ ^[0-9.]+$ || "$SERVER_ADDR" =~ ^[0-9A-Fa-f:]+$ ]] \
  || die "Укажите адрес сервера: SERVER_ADDR=1.2.3.4"
[[ "$NAIVE" == 0 || "$NAIVE" == 1 ]] || die "NAIVE — 0 или 1"
if [[ "$NAIVE" == 1 ]]; then
  DOMAIN="${DOMAIN,,}"
  [[ "$DOMAIN" =~ $HOST_RE ]] || die "Для NaiveProxy нужен домен: DOMAIN=proxy.example.com"
  REALITY_SNI="$DOMAIN" # Reality маскируется под свой же сайт на Caddy
fi
[[ "$REALITY_SNI" =~ $HOST_RE ]] || die "Укажите сайт для маскировки Reality: REALITY_SNI=www.example.com"
[[ "$HY2" == 0 || "$HY2" == 1 ]] || die "HY2 — 0 или 1"
[[ "$BBR" == 0 || "$BBR" == 1 ]] || die "BBR — 0 или 1"
[[ "$NAME" =~ ^[^[:cntrl:]]{1,40}$ ]] || die "NAME — до 40 символов"

if [[ "$TEST" != 1 ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  [[ "${ID:-}" == debian || "${ID:-}" == ubuntu || " ${ID_LIKE:-} " == *" debian "* ]] \
    || die "Скрипт рассчитан на Debian или Ubuntu (у вас ${PRETTY_NAME:-неизвестно})."
  command -v systemctl >/dev/null || die "Нужен systemd."
  case "$(uname -m)" in x86_64|aarch64) ;; *) die "Архитектура $(uname -m) не поддерживается.";; esac
fi

# Порт занят другой программой? Свои sing-box и Caddy не считаются: их мы как раз перенастраиваем.
port_busy() { # port_busy t|u порт
  local out
  out="$(ss -H"$1"lnp "( sport = :$2 )" 2>/dev/null)" || true
  [[ -n "$out" && "$out" != *'"sing-box"'* && "$out" != *'"caddy-naive"'* ]]
}
tcp_busy() { port_busy t "$1"; }
udp_busy() { port_busy u "$1"; }

# Повторный запуск: те же ключи и порты.
if [[ -r "$STATE_FILE" ]]; then
  # shellcheck disable=SC1090
  . "$STATE_FILE"
  log "Найдена прошлая установка — использую её ключи и порты."
  # если в прошлый раз Hysteria2 не ставили, а теперь просят — порт выберем ниже
  [[ "$HY2" == 1 && -z "${HY2_PASS:-}" ]] && HY2_PORT=auto
  # 443/tcp теперь нужен Caddy для NaiveProxy — Reality переезжает
  [[ "$NAIVE" == 1 && "${REALITY_PORT:-}" == 443 ]] && REALITY_PORT=auto
fi

pick_port() { # pick_port tcp|udp значение
  local proto="$1" want="$2"
  if [[ "$want" != auto ]]; then
    [[ "$want" =~ ^[0-9]+$ ]] && (( want > 0 && want < 65536 )) || die "Неверный порт $want"
    echo "$want"; return
  fi
  for p in 443 8443 2053 2083; do
    [[ "$proto" == tcp && "$p" == 443 && "$NAIVE" == 1 ]] && continue # 443 — для NaiveProxy
    if [[ "$proto" == tcp ]] && ! tcp_busy "$p"; then echo "$p"; return; fi
    if [[ "$proto" == udp ]] && ! udp_busy "$p"; then echo "$p"; return; fi
  done
  die "Не нашлось свободного порта $proto (443, 8443, 2053, 2083) — укажите свой."
}
REALITY_PORT="$(pick_port tcp "$REALITY_PORT")"
[[ "$HY2" == 1 ]] && HY2_PORT="$(pick_port udp "$HY2_PORT")"
if tcp_busy "$REALITY_PORT"; then die "Порт ${REALITY_PORT}/tcp занят другой программой."; fi
if [[ "$HY2" == 1 ]] && udp_busy "$HY2_PORT"; then die "Порт ${HY2_PORT}/udp занят другой программой."; fi
if [[ "$NAIVE" == 1 ]]; then
  for p in 80 443; do tcp_busy "$p" && die "Для NaiveProxy нужен свободный порт ${p}/tcp, а его занимает другая программа (веб-сервер?)."; done
  [[ "$REALITY_PORT" == 443 ]] && die "Порт 443/tcp нужен NaiveProxy — укажите для Reality другой."
fi
log "Reality: порт ${REALITY_PORT}/tcp, маскировка под ${REALITY_SNI}"
[[ "$HY2" == 1 ]] && log "Hysteria2: порт ${HY2_PORT}/udp"
[[ "$NAIVE" == 1 ]] && log "NaiveProxy: ${DOMAIN}:443/tcp"

# ---------- установка sing-box ----------
if [[ "$TEST" != 1 ]]; then
  export DEBIAN_FRONTEND=noninteractive
  log "Обновляю список пакетов и ставлю нужное (curl, openssl)…"
  apt-get update -y >/dev/null
  apt-get install -y curl ca-certificates openssl iproute2 >/dev/null
  [[ "$NAIVE" == 1 ]] && apt-get install -y xz-utils jq >/dev/null
  if ! command -v sing-box >/dev/null; then
    log "Устанавливаю sing-box из официального репозитория SagerNet…"
    mkdir -p /etc/apt/keyrings
    curl -fsSL https://sing-box.app/gpg.key -o /etc/apt/keyrings/sagernet.asc
    chmod a+r /etc/apt/keyrings/sagernet.asc
    cat > /etc/apt/sources.list.d/sagernet.sources <<'EOF'
Types: deb
URIs: https://deb.sagernet.org/
Suites: *
Components: *
Enabled: yes
Signed-By: /etc/apt/keyrings/sagernet.asc
EOF
    apt-get update -y >/dev/null
    apt-get install -y -o Dpkg::Options::=--force-confold sing-box >/dev/null
  fi
  # Конфиг из пакета поднимает Shadowsocks с паролем из примера — не даём ему поработать.
  systemctl stop sing-box 2>/dev/null || true
  id sing-box >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin sing-box
fi
command -v sing-box >/dev/null || die "sing-box не установился."
log "sing-box: $(sing-box version | head -n1)"

CURL_K=()
[[ "$TEST" == 1 ]] && CURL_K=(-k)

# ---------- NaiveProxy: Caddy с forwardproxy ----------
# Готовая сборка klzgrad (только amd64), проверенная по контрольной сумме, которую хранит GitHub;
# если её нет или сумма не сошлась — сборка из исходников официальным способом (xcaddy), 3–10 минут.
caddy_ok() { [[ -x "$1" ]] && "$1" list-modules 2>/dev/null | grep -qx 'http.handlers.forward_proxy'; }

caddy_prebuilt() {
  [[ "$(uname -m)" == x86_64 ]] || return 1
  local tmp api want got bin
  tmp="$(mktemp -d)"
  log "Скачиваю готовый Caddy с forwardproxy (klzgrad/forwardproxy)…"
  if ! curl -fsSL --max-time 120 -o "$tmp/c.tar.xz" https://github.com/klzgrad/forwardproxy/releases/latest/download/caddy-forwardproxy-naive.tar.xz; then
    rm -rf "$tmp"; return 1
  fi
  api="$(curl -fsSL --max-time 30 https://api.github.com/repos/klzgrad/forwardproxy/releases/latest 2>/dev/null || true)"
  want="$(jq -r '.assets[] | select(.name=="caddy-forwardproxy-naive.tar.xz") | .digest // empty' <<<"$api" 2>/dev/null | sed 's/^sha256://')" || true
  got="$(sha256sum "$tmp/c.tar.xz" | awk '{print $1}')"
  if [[ -z "$want" ]]; then
    warn "GitHub не сообщил контрольную сумму сборки — соберу Caddy из исходников."
    rm -rf "$tmp"; return 1
  fi
  if [[ "$want" != "$got" ]]; then
    warn "Контрольная сумма сборки не совпала — не ставлю её, соберу из исходников."
    rm -rf "$tmp"; return 1
  fi
  tar -xJf "$tmp/c.tar.xz" -C "$tmp"
  bin="$(find "$tmp" -type f -name caddy | head -n1)"
  if ! caddy_ok "$bin"; then rm -rf "$tmp"; return 1; fi
  install -m 755 "$bin" "$CADDY_BIN"
  rm -rf "$tmp"
  log "Готовый Caddy проверен по контрольной сумме и установлен."
}

caddy_build() {
  local arch tmp goinfo gover gofile gosha swap="" avail
  case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
  tmp="$(mktemp -d)"
  log "Собираю Caddy с forwardproxy из исходников (официальный способ NaiveProxy). Это 3–10 минут…"
  goinfo="$(curl -fsSL --max-time 30 'https://go.dev/dl/?mode=json')" || die "Не удалось связаться с go.dev."
  gover="$(jq -r '[.[] | select(.stable)][0].version' <<<"$goinfo")"
  gofile="$(jq -r --arg a "$arch" '[.[] | select(.stable)][0].files[] | select(.os=="linux" and .arch==$a and .kind=="archive") | .filename' <<<"$goinfo")"
  gosha="$(jq -r --arg a "$arch" '[.[] | select(.stable)][0].files[] | select(.os=="linux" and .arch==$a and .kind=="archive") | .sha256' <<<"$goinfo")"
  [[ -n "$gofile" && -n "$gosha" ]] || die "Не удалось узнать последнюю версию Go на go.dev."
  curl -fsSL --max-time 300 -o "$tmp/go.tgz" "https://go.dev/dl/${gofile}"
  [[ "$(sha256sum "$tmp/go.tgz" | awk '{print $1}')" == "$gosha" ]] || die "Архив Go не совпал с контрольной суммой go.dev."
  tar -xzf "$tmp/go.tgz" -C "$tmp"
  log "Go ${gover} скачан и проверен."
  # Сборке нужно около 1 ГБ памяти: на маленьком VPS временно включаем подкачку.
  avail=$(( ( $(awk '/MemAvailable/ {print $2}' /proc/meminfo) + $(awk '/SwapFree/ {print $2}' /proc/meminfo) ) / 1024 ))
  if (( avail < 1200 )) && [[ ! -e /swapfile-naivka ]]; then
    if fallocate -l 1G /swapfile-naivka 2>/dev/null && chmod 600 /swapfile-naivka && mkswap /swapfile-naivka >/dev/null && swapon /swapfile-naivka 2>/dev/null; then
      swap=1; log "Временно включена подкачка 1 ГБ на время сборки."
    else
      rm -f /swapfile-naivka; warn "Памяти мало (${avail} МБ), а подкачку включить не удалось — сборка может не пройти."
    fi
  fi
  if ! (export GOROOT="$tmp/go" GOPATH="$tmp/gopath" GOCACHE="$tmp/cache" PATH="$tmp/go/bin:$PATH" GOTOOLCHAIN=local
        cd "$tmp" && go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest \
        && "$tmp/gopath/bin/xcaddy" build --with github.com/caddyserver/forwardproxy=github.com/klzgrad/forwardproxy@naive --output "$tmp/caddy") >"$tmp/build.log" 2>&1; then
    tail -n 20 "$tmp/build.log" >&2
    if [[ -n "$swap" ]]; then swapoff /swapfile-naivka; rm -f /swapfile-naivka; fi
    die "Сборка Caddy не удалась (журнал выше)."
  fi
  if [[ -n "$swap" ]]; then swapoff /swapfile-naivka; rm -f /swapfile-naivka; fi
  caddy_ok "$tmp/caddy" || die "Собранный Caddy без модуля forwardproxy."
  install -m 755 "$tmp/caddy" "$CADDY_BIN"
  rm -rf "$tmp"
  log "Caddy с forwardproxy собран и установлен."
}

NAIVE_USER="${NAIVE_USER:-}"
NAIVE_PASS="${NAIVE_PASS:-}"
if [[ "$NAIVE" == 1 ]]; then
  if [[ "$TEST" != 1 ]]; then
    # Сертификат Let's Encrypt выдадут, только если домен уже указывает на этот сервер.
    MY_IP="$(curl -4 -fsS --max-time 10 https://api.ipify.org 2>/dev/null || true)"
    DNS_IPS="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ')" || true
    [[ -n "$DNS_IPS" ]] || die "Домен ${DOMAIN} не найден в DNS. Создайте A-запись на IP сервера${MY_IP:+ ($MY_IP)} и подождите 5–15 минут."
    if [[ -n "$MY_IP" && " $DNS_IPS " != *" $MY_IP "* ]]; then
      die "Домен ${DOMAIN} указывает на ${DNS_IPS% }, а у сервера IP ${MY_IP}. Исправьте A-запись (без прокси Cloudflare) и повторите."
    fi
    log "Домен ${DOMAIN} указывает на этот сервер."
    caddy_ok "$CADDY_BIN" || caddy_prebuilt || caddy_build
  else
    caddy_ok "$CADDY_BIN" || CADDY_BIN="$(command -v caddy-naive)" || die "В тестовом режиме нужен caddy-naive в PATH."
  fi
  [[ -n "$NAIVE_USER" ]] || NAIVE_USER="u$(openssl rand -hex 6)"
  [[ -n "$NAIVE_PASS" ]] || NAIVE_PASS="$(openssl rand -hex 16)"

  [[ "$TEST" == 1 ]] && SITE_DIR="$CONF_DIR/www"
  mkdir -p "$SITE_DIR"
  if [[ ! -s "$SITE_DIR/index.html" ]]; then
    printf '%s\n' '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Welcome</title>' \
      '<meta name="viewport" content="width=device-width,initial-scale=1"></head>' \
      '<body style="font-family:system-ui,sans-serif;max-width:640px;margin:60px auto;color:#333">' \
      '<h1>It works</h1><p>This server is up and running.</p></body></html>' > "$SITE_DIR/index.html"
    chmod 644 "$SITE_DIR/index.html"
  fi
  CADDY_CONF_DIR="$CADDY_DIR"
  [[ "$TEST" == 1 ]] && CADDY_CONF_DIR="$CONF_DIR/caddy"
  mkdir -p "$CADDY_CONF_DIR"
  # HTTP/3 выключаем, если UDP 443 занимает Hysteria2.
  H3_OPT=""
  [[ "$HY2" == 1 && "${HY2_PORT:-}" == 443 ]] && H3_OPT=$'\n  servers {\n    protocols h1 h2\n  }'
  TLS_OPT=""
  # Тест: свой учебный центр сертификатов Caddy, без установки его в доверенные системы.
  [[ "$TEST" == 1 ]] && TLS_OPT=$'\n  tls internal' && H3_OPT+=$'\n  skip_install_trust'
  {
    echo "# Создано install-server.sh (Naivka). NaiveProxy: Caddy + forwardproxy (klzgrad)."
    echo "{"
    echo "  order forward_proxy before file_server"
    echo "  log {"
    echo "    exclude http.log.error"
    echo "  }${H3_OPT}"
    echo "}"
    echo ":443, ${DOMAIN} {${TLS_OPT}"
    echo "  encode"
    echo "  forward_proxy {"
    echo "    basic_auth ${NAIVE_USER} ${NAIVE_PASS}"
    echo "    hide_ip"
    echo "    hide_via"
    echo "    probe_resistance"
    # Тест: проверочный сервер на 127.0.0.1, а внутренние адреса forwardproxy по умолчанию запрещает.
    [[ "$TEST" == 1 ]] && printf '    acl {\n      allow 127.0.0.1\n    }\n'
    echo "  }"
    echo "  file_server {"
    echo "    root ${SITE_DIR}"
    echo "  }"
    echo "}"
  } > "$CADDY_CONF_DIR/Caddyfile"
  chmod 640 "$CADDY_CONF_DIR/Caddyfile"
  if ! "$CADDY_BIN" validate --config "$CADDY_CONF_DIR/Caddyfile" --adapter caddyfile >"$CONF_DIR/caddy-validate.log" 2>&1; then
    tail -n 15 "$CONF_DIR/caddy-validate.log" >&2
    die "Конфиг Caddy не прошёл проверку."
  fi

  if [[ "$TEST" != 1 ]]; then
    id naivka-caddy >/dev/null 2>&1 || useradd --system --home /var/lib/naivka-caddy --shell /usr/sbin/nologin naivka-caddy
    chown root:naivka-caddy "$CADDY_CONF_DIR/Caddyfile"
    printf '%s\n' \
      "# Создано install-server.sh (Naivka): Caddy с forwardproxy для NaiveProxy." \
      "[Unit]" \
      "Description=NaiveProxy (Caddy + forwardproxy), installed by Naivka" \
      "After=network-online.target" \
      "Wants=network-online.target" \
      "" \
      "[Service]" \
      "User=naivka-caddy" \
      "Group=naivka-caddy" \
      "ExecStart=${CADDY_BIN} run --config ${CADDY_CONF_DIR}/Caddyfile --adapter caddyfile" \
      "ExecReload=${CADDY_BIN} reload --config ${CADDY_CONF_DIR}/Caddyfile --adapter caddyfile --force" \
      "Environment=XDG_DATA_HOME=/var/lib/naivka-caddy XDG_CONFIG_HOME=/var/lib/naivka-caddy" \
      "StateDirectory=naivka-caddy" \
      "LimitNOFILE=1048576" \
      "AmbientCapabilities=CAP_NET_BIND_SERVICE" \
      "CapabilityBoundingSet=CAP_NET_BIND_SERVICE" \
      "NoNewPrivileges=yes" \
      "ProtectSystem=strict" \
      "ProtectHome=yes" \
      "PrivateTmp=yes" \
      "PrivateDevices=yes" \
      "MemoryMax=400M" \
      "Restart=on-failure" \
      "RestartSec=5s" \
      "" \
      "[Install]" \
      "WantedBy=multi-user.target" > "$CADDY_UNIT"
    systemctl daemon-reload
    systemctl enable naivka-caddy >/dev/null 2>&1
    systemctl restart naivka-caddy
  else
    pkill -f "caddy-naive run --config $CADDY_CONF_DIR" 2>/dev/null || true
    XDG_DATA_HOME="$CONF_DIR/caddy-data" XDG_CONFIG_HOME="$CONF_DIR/caddy-data" \
      nohup "$CADDY_BIN" run --config "$CADDY_CONF_DIR/Caddyfile" --adapter caddyfile >"$CONF_DIR/caddy.log" 2>&1 &
  fi
  log "Жду, пока Caddy получит сертификат для ${DOMAIN} (до 3 минут)…"
  ok=0
  for _ in $(seq 1 90); do
    if curl "${CURL_K[@]}" -s -o /dev/null --max-time 5 --resolve "${DOMAIN}:443:127.0.0.1" "https://${DOMAIN}/"; then ok=1; break; fi
    sleep 2
  done
  if [[ "$ok" != 1 ]]; then
    [[ "$TEST" != 1 ]] && journalctl -u naivka-caddy -n 30 --no-pager >&2
    die "Caddy не получил сертификат. Проверьте, что порты 80 и 443 открыты в панели хостинга и домен указывает на сервер."
  fi
  log "Сайт https://${DOMAIN}/ работает, сертификат есть."
else
  # ---------- сайт для маскировки ----------
  # Reality показывает постороннему настоящий сайт REALITY_SNI, поэтому он должен отвечать по TLS 1.3 с HTTP/2.
  if ! curl "${CURL_K[@]}" -s -o /dev/null --max-time 10 --tlsv1.3 --http2 "https://${REALITY_SNI}/"; then
    die "Сайт ${REALITY_SNI} не отвечает с сервера по TLS 1.3 — выберите другой сайт для маскировки."
  fi
  log "Сайт ${REALITY_SNI} отвечает по TLS 1.3 — подходит."
fi

# ---------- ключи ----------
mkdir -p "$CONF_DIR"
umask 077
if [[ -z "${UUID:-}" ]]; then
  UUID="$(sing-box generate uuid)"
  KEYS="$(sing-box generate reality-keypair)"
  PRIVATE_KEY="$(awk '/PrivateKey/ {print $2}' <<<"$KEYS")"
  PUBLIC_KEY="$(awk '/PublicKey/ {print $2}' <<<"$KEYS")"
  SHORT_ID="$(sing-box generate rand --hex 8)"
fi
if [[ "$HY2" == 1 && -z "${HY2_PASS:-}" ]]; then
  HY2_PASS="$(sing-box generate rand --hex 16)"
  OBFS_PASS="$(sing-box generate rand --hex 16)"
fi
[[ -n "${UUID:-}" && -n "${PRIVATE_KEY:-}" && -n "${PUBLIC_KEY:-}" && -n "${SHORT_ID:-}" ]] || die "Не удалось создать ключи."

HY2_CERT="$CONF_DIR/naivka-hy2.crt"
HY2_KEY="$CONF_DIR/naivka-hy2.key"
if [[ "$HY2" == 1 && ! -s "$HY2_CERT" ]]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
    -keyout "$HY2_KEY" -out "$HY2_CERT" -subj "/CN=${REALITY_SNI}" >/dev/null 2>&1 || die "Не удалось создать сертификат для Hysteria2."
fi
HY2_PIN=""
if [[ "$HY2" == 1 ]]; then
  HY2_PIN="$(openssl x509 -in "$HY2_CERT" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | openssl enc -base64)"
fi

cat > "$STATE_FILE" <<EOF
UUID=$UUID
PRIVATE_KEY=$PRIVATE_KEY
PUBLIC_KEY=$PUBLIC_KEY
SHORT_ID=$SHORT_ID
REALITY_PORT=$REALITY_PORT
HY2_PASS=${HY2_PASS:-}
OBFS_PASS=${OBFS_PASS:-}
HY2_PORT=${HY2_PORT:-}
NAIVE_USER=${NAIVE_USER:-}
NAIVE_PASS=${NAIVE_PASS:-}
DOMAIN=${DOMAIN:-}
EOF
chmod 600 "$STATE_FILE"

# ---------- конфиг ----------
if [[ -e "$CONFIG" ]] && ! grep -q '"tag": "naivka-reality"' "$CONFIG"; then
  BAK="${CONFIG}.bak.$(date +%Y%m%d-%H%M%S)"
  cp -a "$CONFIG" "$BAK"
  warn "Прежний конфиг sing-box сохранён в $BAK"
fi
# С NaiveProxy Reality показывает посторонним свой же сайт с Caddy на этом сервере.
HANDSHAKE_HOST="$REALITY_SNI"
[[ "$NAIVE" == 1 ]] && HANDSHAKE_HOST="127.0.0.1"
HY2_INBOUND=""
if [[ "$HY2" == 1 ]]; then
  HY2_INBOUND=$(cat <<EOF
    ,{
      "type": "hysteria2",
      "tag": "naivka-hy2",
      "listen": "::",
      "listen_port": ${HY2_PORT},
      "users": [ { "name": "naivka", "password": "${HY2_PASS}" } ],
      "obfs": { "type": "salamander", "password": "${OBFS_PASS}" },
      "tls": {
        "enabled": true,
        "alpn": [ "h3" ],
        "certificate_path": "${HY2_CERT}",
        "key_path": "${HY2_KEY}"
      }
    }
EOF
)
fi
# Сервер не пускает к адресам своей сети (защита от доступа к соседям по хостингу).
PRIVATE_RULE=',{ "ip_is_private": true, "action": "reject" }'
[[ "$TEST" == 1 ]] && PRIVATE_RULE=""
NEW_CONFIG="$(mktemp)"
cat > "$NEW_CONFIG" <<EOF
{
  "log": { "level": "warn", "timestamp": true },
  "inbounds": [
    {
      "type": "vless",
      "tag": "naivka-reality",
      "listen": "::",
      "listen_port": ${REALITY_PORT},
      "users": [ { "name": "naivka", "uuid": "${UUID}", "flow": "xtls-rprx-vision" } ],
      "tls": {
        "enabled": true,
        "server_name": "${REALITY_SNI}",
        "reality": {
          "enabled": true,
          "handshake": { "server": "${HANDSHAKE_HOST}", "server_port": 443 },
          "private_key": "${PRIVATE_KEY}",
          "short_id": [ "${SHORT_ID}" ]
        }
      }
    }
    ${HY2_INBOUND}
  ],
  "outbounds": [ { "type": "direct", "tag": "direct" } ],
  "route": {
    "rules": [
      { "action": "resolve" }
      ${PRIVATE_RULE}
    ],
    "final": "direct"
  }
}
EOF
sing-box check -c "$NEW_CONFIG" || { rm -f "$NEW_CONFIG"; die "Конфиг не прошёл проверку sing-box — на сервере ничего не изменено."; }
if [[ "$TEST" == 1 ]]; then
  install -m 600 "$NEW_CONFIG" "$CONFIG"
else
  install -m 640 -o root -g sing-box "$NEW_CONFIG" "$CONFIG"
  [[ "$HY2" == 1 ]] && chown root:sing-box "$HY2_KEY" "$HY2_CERT" && chmod 640 "$HY2_KEY" "$HY2_CERT"
fi
rm -f "$NEW_CONFIG"
# sing-box склеивает все json из папки — чужие отключаем.
find "$CONF_DIR" -maxdepth 1 -name '*.json' ! -name 'config.json' -exec mv {} {}.disabled \;

# ---------- служба, файрвол, BBR ----------
if [[ "$TEST" != 1 ]]; then
  mkdir -p "$DROPIN_DIR"
  cat > "$DROPIN_DIR/naivka.conf" <<'EOF'
# Добавлено install-server.sh (Naivka)
[Service]
MemoryHigh=250M
MemoryMax=350M
Environment=GOMEMLIMIT=200MiB
# Порты ниже 1024 (443) — только это право, больше никаких.
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
Restart=always
RestartSec=5s
EOF
  systemctl daemon-reload

  if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
    ufw allow "${REALITY_PORT}/tcp" >/dev/null
    [[ "$HY2" == 1 ]] && ufw allow "${HY2_PORT}/udp" >/dev/null
    if [[ "$NAIVE" == 1 ]]; then ufw allow 80/tcp >/dev/null; ufw allow 443/tcp >/dev/null; fi
    log "ufw: открыты порты."
  else
    warn "ufw не включён. Если у хостера есть файрвол в панели — откройте там ${REALITY_PORT}/tcp$([[ "$HY2" == 1 ]] && echo " и ${HY2_PORT}/udp")."
  fi

  if [[ "$BBR" == 1 ]] && modprobe tcp_bbr 2>/dev/null; then
    printf 'net.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n' > "$SYSCTL_FILE"
    sysctl -q -p "$SYSCTL_FILE" || true
    log "BBR: $(sysctl -n net.ipv4.tcp_congestion_control)"
  fi

  systemctl enable sing-box >/dev/null 2>&1
  systemctl restart sing-box
  sleep 2
  systemctl is-active --quiet sing-box || { journalctl -u sing-box -n 30 --no-pager; die "sing-box не запустился (журнал выше)."; }
else
  pkill -f "sing-box run -c $CONFIG" 2>/dev/null || true
  nohup sing-box run -c "$CONFIG" >"$CONF_DIR/test.log" 2>&1 &
  sleep 1.5
fi
log "sing-box запущен."

# ---------- самопроверка: подключаемся к себе как клиент ----------
selftest() { # selftest имя outbound-json
  local dir port code pid
  dir="$(mktemp -d)"
  port=$(( 20000 + RANDOM % 20000 ))
  cat > "$dir/c.json" <<EOF
{ "log": { "level": "error" },
  "inbounds": [ { "type": "mixed", "listen": "127.0.0.1", "listen_port": ${port} } ],
  "outbounds": [ $2 ] }
EOF
  sing-box run -c "$dir/c.json" >/dev/null 2>&1 &
  pid=$!
  sleep 1.5
  code="$(curl --noproxy "" -s -o /dev/null -w '%{http_code}' --max-time 15 -x "http://127.0.0.1:${port}" "$SELFTEST_URL")" || true
  kill "$pid" 2>/dev/null || true
  rm -rf "$dir"
  if [[ "$code" == 204 ]]; then log "Самопроверка $1: трафик проходит."; else warn "Самопроверка $1 не прошла (код $code)."; fi
}
selftest Reality "{ \"type\": \"vless\", \"server\": \"127.0.0.1\", \"server_port\": ${REALITY_PORT}, \"uuid\": \"${UUID}\", \"flow\": \"xtls-rprx-vision\",
  \"tls\": { \"enabled\": true, \"server_name\": \"${REALITY_SNI}\", \"utls\": { \"enabled\": true, \"fingerprint\": \"chrome\" },
  \"reality\": { \"enabled\": true, \"public_key\": \"${PUBLIC_KEY}\", \"short_id\": \"${SHORT_ID}\" } } }"
if [[ "$HY2" == 1 ]]; then
  selftest Hysteria2 "{ \"type\": \"hysteria2\", \"server\": \"127.0.0.1\", \"server_port\": ${HY2_PORT}, \"password\": \"${HY2_PASS}\",
    \"obfs\": { \"type\": \"salamander\", \"password\": \"${OBFS_PASS}\" },
    \"tls\": { \"enabled\": true, \"server_name\": \"${REALITY_SNI}\", \"alpn\": [\"h3\"], \"certificate_public_key_sha256\": [\"${HY2_PIN}\"] } }"
fi

if [[ "$NAIVE" == 1 ]]; then
  code="$(curl "${CURL_K[@]}" --noproxy "" --proxy-insecure -s -o /dev/null -w '%{http_code}' --max-time 15 --resolve "${DOMAIN}:443:127.0.0.1" \
    -x "https://${DOMAIN}:443" --proxy-user "${NAIVE_USER}:${NAIVE_PASS}" "$SELFTEST_URL")" || true
  if [[ "$code" == 204 ]]; then log "Самопроверка NaiveProxy: трафик проходит."; else warn "Самопроверка NaiveProxy не прошла (код $code)."; fi
  # Посторонний без пароля должен видеть обычный сайт, а не прокси.
  code="$(curl "${CURL_K[@]}" --noproxy "" --proxy-insecure -s -o /dev/null -w '%{http_code}' --max-time 10 --resolve "${DOMAIN}:443:127.0.0.1" \
    -x "https://${DOMAIN}:443" "$SELFTEST_URL")" || true
  if [[ "$code" != 204 ]]; then log "Маскировка NaiveProxy: без пароля прокси не отвечает."; else warn "Без пароля прокси пропускает трафик!"; fi
fi

# ---------- ссылки ----------
urlenc() { # процентное кодирование для имени в ссылке
  local LC_ALL=C s="$1" out="" c i
  for (( i = 0; i < ${#s}; i++ )); do
    c="${s:i:1}"
    case "$c" in [A-Za-z0-9._~-]) out+="$c" ;; *) printf -v c '%%%02X' "'$c"; out+="$c" ;; esac
  done
  printf '%s' "$out"
}
ADDR="$SERVER_ADDR"
[[ "$ADDR" == *:* ]] && ADDR="[$ADDR]"
echo
log "Готово. Ссылки на серверы (их же Naivka добавит сама):"
echo "NAIVKA-LINK vless://${UUID}@${ADDR}:${REALITY_PORT}?encryption=none&flow=xtls-rprx-vision&security=reality&sni=${REALITY_SNI}&fp=chrome&pbk=${PUBLIC_KEY}&sid=${SHORT_ID}&type=tcp#$(urlenc "$NAME Reality")"
if [[ "$NAIVE" == 1 ]]; then
  echo "NAIVKA-LINK naive+https://${NAIVE_USER}:${NAIVE_PASS}@${DOMAIN}:443#$(urlenc "$NAME Naive")"
fi
if [[ "$HY2" == 1 ]]; then
  echo "NAIVKA-LINK hysteria2://${HY2_PASS}@${ADDR}:${HY2_PORT}/?sni=${REALITY_SNI}&obfs=salamander&obfs-password=${OBFS_PASS}&pubkey-sha256=$(urlenc "$HY2_PIN")#$(urlenc "$NAME Hysteria2")"
fi
echo "Журнал: journalctl -u sing-box -f    Удаление: bash install-server.sh --remove"
