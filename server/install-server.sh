#!/usr/bin/env bash
# Ставит на свой VPS (Debian 11+ / Ubuntu 20.04+) sing-box с двумя протоколами:
#   * VLESS + Reality (TCP) — основной: маскируется под обычный HTTPS чужого сайта;
#   * Hysteria2 (UDP, по желанию) — запасной: быстрый на плохом мобильном интернете.
# Домен не нужен. Hysteria2 работает со своим сертификатом, а клиент проверяет его по отпечатку
# ключа (не «без проверки»), поэтому подменить сервер незаметно нельзя.
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
#
# Что делается на сервере:
#   * ставится официальный sing-box из репозитория SagerNet (apt);
#   * пишется /etc/sing-box/config.json (чужой конфиг сохраняется рядом как .bak);
#   * служба sing-box включается в автозапуск, память ограничивается;
#   * в ufw (если он включён) открываются выбранные порты; при BBR=1 — настройка в /etc/sysctl.d.
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
  if command -v ufw >/dev/null; then
    [[ -n "${REALITY_PORT:-}" ]] && ufw delete allow "${REALITY_PORT}/tcp" >/dev/null 2>&1 || true
    [[ -n "${HY2_PORT:-}" ]] && ufw delete allow "${HY2_PORT}/udp" >/dev/null 2>&1 || true
  fi
  rm -rf "$DROPIN_DIR" "$SYSCTL_FILE"
  systemctl daemon-reload 2>/dev/null || true
  log "sing-box остановлен и убран из автозапуска, порты закрыты."
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

HOST_RE='^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$'
[[ "$SERVER_ADDR" =~ $HOST_RE || "$SERVER_ADDR" =~ ^[0-9.]+$ || "$SERVER_ADDR" =~ ^[0-9A-Fa-f:]+$ ]] \
  || die "Укажите адрес сервера: SERVER_ADDR=1.2.3.4"
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

# Порт занят другой программой? Сам sing-box не считается: его мы как раз перенастраиваем.
port_busy() { # port_busy t|u порт
  local out
  out="$(ss -H"$1"lnp "( sport = :$2 )" 2>/dev/null)" || true
  [[ -n "$out" && "$out" != *'"sing-box"'* ]]
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
fi

pick_port() { # pick_port tcp|udp значение
  local proto="$1" want="$2"
  if [[ "$want" != auto ]]; then
    [[ "$want" =~ ^[0-9]+$ ]] && (( want > 0 && want < 65536 )) || die "Неверный порт $want"
    echo "$want"; return
  fi
  for p in 443 8443 2053 2083; do
    if [[ "$proto" == tcp ]] && ! tcp_busy "$p"; then echo "$p"; return; fi
    if [[ "$proto" == udp ]] && ! udp_busy "$p"; then echo "$p"; return; fi
  done
  die "Не нашлось свободного порта $proto (443, 8443, 2053, 2083) — укажите свой."
}
REALITY_PORT="$(pick_port tcp "$REALITY_PORT")"
[[ "$HY2" == 1 ]] && HY2_PORT="$(pick_port udp "$HY2_PORT")"
if tcp_busy "$REALITY_PORT"; then die "Порт ${REALITY_PORT}/tcp занят другой программой."; fi
if [[ "$HY2" == 1 ]] && udp_busy "$HY2_PORT"; then die "Порт ${HY2_PORT}/udp занят другой программой."; fi
log "Reality: порт ${REALITY_PORT}/tcp, маскировка под ${REALITY_SNI}"
[[ "$HY2" == 1 ]] && log "Hysteria2: порт ${HY2_PORT}/udp"

# ---------- установка sing-box ----------
if [[ "$TEST" != 1 ]]; then
  export DEBIAN_FRONTEND=noninteractive
  log "Обновляю список пакетов и ставлю нужное (curl, openssl)…"
  apt-get update -y >/dev/null
  apt-get install -y curl ca-certificates openssl iproute2 >/dev/null
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

# ---------- сайт для маскировки ----------
# Reality показывает постороннему настоящий сайт REALITY_SNI, поэтому он должен отвечать по TLS 1.3 с HTTP/2.
CURL_K=()
[[ "$TEST" == 1 ]] && CURL_K=(-k)
if ! curl "${CURL_K[@]}" -s -o /dev/null --max-time 10 --tlsv1.3 --http2 "https://${REALITY_SNI}/"; then
  die "Сайт ${REALITY_SNI} не отвечает с сервера по TLS 1.3 — выберите другой сайт для маскировки."
fi
log "Сайт ${REALITY_SNI} отвечает по TLS 1.3 — подходит."

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
EOF
chmod 600 "$STATE_FILE"

# ---------- конфиг ----------
if [[ -e "$CONFIG" ]] && ! grep -q '"tag": "naivka-reality"' "$CONFIG"; then
  BAK="${CONFIG}.bak.$(date +%Y%m%d-%H%M%S)"
  cp -a "$CONFIG" "$BAK"
  warn "Прежний конфиг sing-box сохранён в $BAK"
fi
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
          "handshake": { "server": "${REALITY_SNI}", "server_port": 443 },
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
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -x "http://127.0.0.1:${port}" "$SELFTEST_URL")" || true
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
if [[ "$HY2" == 1 ]]; then
  echo "NAIVKA-LINK hysteria2://${HY2_PASS}@${ADDR}:${HY2_PORT}/?sni=${REALITY_SNI}&obfs=salamander&obfs-password=${OBFS_PASS}&pubkey-sha256=$(urlenc "$HY2_PIN")#$(urlenc "$NAME Hysteria2")"
fi
echo "Журнал: journalctl -u sing-box -f    Удаление: bash install-server.sh --remove"
