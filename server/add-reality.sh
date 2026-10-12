#!/usr/bin/env bash
# Добавляет на сервер с NaiveProxy (Caddy + forwardproxy) второй протокол — VLESS + Reality.
#
# Что НЕ трогается: Caddy, /etc/caddy/Caddyfile, порт 443, сертификат, пользователи Naive.
# Что делается:
#   * ставится официальный sing-box из репозитория SagerNet;
#   * он слушает отдельный порт (по умолчанию 8443);
#   * посторонним на этом порту показывается ваш же сайт-заглушка с вашим сертификатом
#     (Reality пересылает «чужие» соединения в Caddy на 127.0.0.1:443);
#   * память sing-box ограничена, чтобы он никогда не потеснил Caddy;
#   * в конце проверяется, что Naive работает как раньше.
#
# Повторный запуск безопасен: ключи сохраняются, ссылка выводится заново.
# Удаление: bash add-reality.sh --remove
set -Eeuo pipefail

PORT="${PORT:-8443}"
NAME="${NAME:-Reality}"
STATE_FILE="/etc/sing-box/reality.env"
USERS_FILE="/root/reality-users.txt"
CONFIG="/etc/sing-box/config.json"
DROPIN_DIR="/etc/systemd/system/sing-box.service.d"

log()  { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
trap 'die "Ошибка на строке $LINENO. Naive не затронут; подробности выше."' ERR

[[ "${EUID}" -eq 0 ]] || die "Запустите от root."

# ---------- удаление ----------
if [[ "${1:-}" == "--remove" ]]; then
  # shellcheck disable=SC1090
  [[ -r "$STATE_FILE" ]] && . "$STATE_FILE"
  systemctl disable --now sing-box 2>/dev/null || true
  if command -v ufw >/dev/null && [[ -n "${PORT:-}" ]]; then ufw delete allow "${PORT}/tcp" >/dev/null 2>&1 || true; fi
  rm -rf "$DROPIN_DIR"
  systemctl daemon-reload
  log "sing-box остановлен и убран из автозапуска, порт ${PORT} закрыт."
  log "Пакет можно удалить полностью: apt remove sing-box"
  log "Ключи сохранены в $STATE_FILE и $USERS_FILE — удалите их вручную, если не нужны."
  exit 0
fi

# ---------- проверки ----------
[[ -r /etc/caddy/Caddyfile ]] || die "Не найден /etc/caddy/Caddyfile. Скрипт рассчитан на установку Naive через Caddy."
systemctl is-active --quiet caddy || die "Служба caddy не запущена. Сначала почините Naive: systemctl status caddy"

DOMAIN="${DOMAIN:-$(grep -oE '^[[:space:]]*:443,[[:space:]]*[A-Za-z0-9.-]+' /etc/caddy/Caddyfile | head -n1 | sed -E 's/.*,[[:space:]]*//')}"
[[ "$DOMAIN" =~ ^([A-Za-z0-9-]+\.)+[A-Za-z]{2,}$ ]] || die "Не удалось определить домен из Caddyfile. Запустите так: DOMAIN=ваш.домен bash add-reality.sh"
[[ "$PORT" =~ ^[0-9]+$ ]] && (( PORT > 1024 && PORT < 65536 && PORT != 443 )) || die "Порт должен быть от 1025 до 65535."
log "Домен Naive: $DOMAIN, порт для Reality: $PORT"

naive_check() {
  # Ответ сайта-заглушки на 443 — запрос идёт локально, мимо DNS.
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 --resolve "${DOMAIN}:443:127.0.0.1" "https://${DOMAIN}/")" || true
  echo "${code:-000}"
}
NAIVE_BEFORE="$(naive_check)"
[[ "$NAIVE_BEFORE" =~ ^[23] ]] || warn "Сайт на 443 сейчас отвечает кодом $NAIVE_BEFORE — проверьте Naive отдельно. Продолжаю."

if ss -Htln "( sport = :${PORT} )" | grep -q . && ! { systemctl is-active --quiet sing-box && [[ -r "$STATE_FILE" ]]; }; then
  die "Порт ${PORT} уже занят другой программой. Выберите другой: PORT=9443 bash add-reality.sh"
fi

MEM_AVAIL_MB=$(( $(awk '/MemAvailable/ {print $2}' /proc/meminfo) / 1024 ))
SWAP_MB=$(( $(awk '/SwapTotal/ {print $2}' /proc/meminfo) / 1024 ))
log "Доступно памяти: ${MEM_AVAIL_MB} МБ, подкачка: ${SWAP_MB} МБ"
if (( MEM_AVAIL_MB < 150 && SWAP_MB == 0 )); then
  warn "Свободной памяти мало, а подкачки нет."
  read -r -p "Создать файл подкачки 1 ГБ? [Y/n] " ans
  if [[ ! "$ans" =~ ^[Nn] ]]; then
    if fallocate -l 1G /swapfile 2>/dev/null && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile 2>/dev/null; then
      grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
      log "Подкачка включена."
    else
      rm -f /swapfile
      warn "Подкачку включить не удалось (похоже, контейнерный VPS). Продолжаю: sing-box всё равно ограничен по памяти."
    fi
  fi
fi

# ---------- установка sing-box ----------
if ! command -v sing-box >/dev/null; then
  log "Устанавливаю sing-box из официального репозитория SagerNet…"
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y curl ca-certificates >/dev/null
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
# Конфиг из пакета поднимает Shadowsocks с паролем из примера — не даём ему поработать ни секунды.
systemctl stop sing-box 2>/dev/null || true
log "sing-box: $(sing-box version | head -n1)"
id sing-box >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin sing-box

# ---------- ключи (при повторном запуске — те же) ----------
mkdir -p /etc/sing-box
if [[ -r "$STATE_FILE" ]]; then
  # shellcheck disable=SC1090
  . "$STATE_FILE"
  log "Использую сохранённые ключи из $STATE_FILE"
else
  UUID="$(sing-box generate uuid)"
  KEYS="$(sing-box generate reality-keypair)"
  PRIVATE_KEY="$(awk '/PrivateKey/ {print $2}' <<<"$KEYS")"
  PUBLIC_KEY="$(awk '/PublicKey/ {print $2}' <<<"$KEYS")"
  SHORT_ID="$(sing-box generate rand --hex 8)"
fi
[[ -n "${UUID:-}" && -n "${PRIVATE_KEY:-}" && -n "${PUBLIC_KEY:-}" && -n "${SHORT_ID:-}" ]] || die "Не удалось сгенерировать ключи."
umask 077
cat > "$STATE_FILE" <<EOF
UUID=$UUID
PRIVATE_KEY=$PRIVATE_KEY
PUBLIC_KEY=$PUBLIC_KEY
SHORT_ID=$SHORT_ID
DOMAIN=$DOMAIN
PORT=$PORT
EOF
chmod 600 "$STATE_FILE"

# ---------- конфиг ----------
if [[ -e "$CONFIG" ]] && ! grep -q '"tag": "reality-in"' "$CONFIG"; then
  cp -a "$CONFIG" "${CONFIG}.bak.$(date +%Y%m%d-%H%M%S)"
fi
NEW_CONFIG="$(mktemp)"
cat > "$NEW_CONFIG" <<EOF
{
  "log": { "level": "warn", "timestamp": true },
  "inbounds": [
    {
      "type": "vless",
      "tag": "reality-in",
      "listen": "::",
      "listen_port": ${PORT},
      "users": [ { "name": "user1", "uuid": "${UUID}", "flow": "xtls-rprx-vision" } ],
      "tls": {
        "enabled": true,
        "server_name": "${DOMAIN}",
        "reality": {
          "enabled": true,
          "handshake": { "server": "127.0.0.1", "server_port": 443 },
          "private_key": "${PRIVATE_KEY}",
          "short_id": [ "${SHORT_ID}" ]
        }
      }
    }
  ],
  "outbounds": [ { "type": "direct", "tag": "direct" } ],
  "route": {
    "rules": [
      { "action": "resolve" },
      { "ip_is_private": true, "action": "reject" }
    ],
    "final": "direct"
  }
}
EOF
sing-box check -c "$NEW_CONFIG" || { rm -f "$NEW_CONFIG"; die "Конфиг не прошёл проверку sing-box — ничего не изменено."; }
install -m 640 -o root -g sing-box "$NEW_CONFIG" "$CONFIG"
rm -f "$NEW_CONFIG"
# В /etc/sing-box не должно остаться других json — sing-box склеивает все файлы из папки.
find /etc/sing-box -maxdepth 1 -name '*.json' ! -name 'config.json' -exec mv {} {}.disabled \;

# ---------- служба: ограничения и защита ----------
mkdir -p "$DROPIN_DIR"
cat > "$DROPIN_DIR/naivka.conf" <<'EOF'
# Добавлено add-reality.sh
[Service]
# Память: при нехватке система остановит sing-box, а не Caddy.
MemoryHigh=120M
MemoryMax=180M
Environment=GOMEMLIMIT=100MiB
OOMScoreAdjust=500
# Порт выше 1024 — особые права не нужны.
CapabilityBoundingSet=
AmbientCapabilities=
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

# ---------- файрвол ----------
if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
  ufw allow "${PORT}/tcp" >/dev/null
  log "UFW: открыт порт ${PORT}/tcp"
else
  warn "UFW не активен — если у провайдера есть свой файрвол в панели, откройте там порт ${PORT}/tcp."
fi

# ---------- запуск ----------
systemctl enable sing-box >/dev/null 2>&1
systemctl restart sing-box
sleep 2
systemctl is-active --quiet sing-box || { journalctl -u sing-box -n 30 --no-pager; die "sing-box не запустился (журнал выше). Naive не затронут."; }
log "sing-box запущен на порту ${PORT}."

# ---------- самопроверка: подключаемся к себе как клиент ----------
SELFTEST="$(mktemp -d)"
TEST_PORT=$(( 20000 + RANDOM % 20000 ))
cat > "$SELFTEST/client.json" <<EOF
{
  "log": { "level": "error" },
  "inbounds": [ { "type": "mixed", "listen": "127.0.0.1", "listen_port": ${TEST_PORT} } ],
  "outbounds": [ {
    "type": "vless", "server": "127.0.0.1", "server_port": ${PORT},
    "uuid": "${UUID}", "flow": "xtls-rprx-vision",
    "tls": { "enabled": true, "server_name": "${DOMAIN}",
      "utls": { "enabled": true, "fingerprint": "chrome" },
      "reality": { "enabled": true, "public_key": "${PUBLIC_KEY}", "short_id": "${SHORT_ID}" } }
  } ]
}
EOF
sing-box run -c "$SELFTEST/client.json" >/dev/null 2>&1 &
CLIENT_PID=$!
sleep 1.5
TEST_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -x "http://127.0.0.1:${TEST_PORT}" https://www.gstatic.com/generate_204)" || true
kill "$CLIENT_PID" 2>/dev/null || true
rm -rf "$SELFTEST"
if [[ "$TEST_CODE" == "204" ]]; then
  log "Самопроверка: трафик через Reality проходит."
else
  warn "Самопроверка не прошла (код $TEST_CODE). Посмотрите: journalctl -u sing-box -n 50"
fi

# Посторонний на порту Reality должен видеть ваш обычный сайт.
PROBE_CODE="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 --resolve "${DOMAIN}:${PORT}:127.0.0.1" "https://${DOMAIN}:${PORT}/")" || true
[[ "$PROBE_CODE" =~ ^[23] ]] && log "Маскировка: на порту ${PORT} посторонний видит ваш сайт-заглушку (код $PROBE_CODE)." \
  || warn "Маскировка: проверочный запрос на порт ${PORT} вернул $PROBE_CODE."

# ---------- Naive не пострадал? ----------
NAIVE_AFTER="$(naive_check)"
if systemctl is-active --quiet caddy && [[ "$NAIVE_AFTER" == "$NAIVE_BEFORE" ]]; then
  log "Naive работает как раньше (caddy активен, сайт отвечает $NAIVE_AFTER)."
else
  warn "Внимание: caddy=$(systemctl is-active caddy), сайт до=$NAIVE_BEFORE после=$NAIVE_AFTER. Откат: bash add-reality.sh --remove"
fi

# ---------- ссылка ----------
LINK="vless://${UUID}@${DOMAIN}:${PORT}?encryption=none&flow=xtls-rprx-vision&security=reality&sni=${DOMAIN}&fp=chrome&pbk=${PUBLIC_KEY}&sid=${SHORT_ID}&type=tcp#${NAME}"
{
  echo "Reality (VLESS) — $(date '+%Y-%m-%d %H:%M')"
  echo "  server: ${DOMAIN}:${PORT}"
  echo "  uuid: ${UUID}"
  echo "  public key: ${PUBLIC_KEY}"
  echo "  short id: ${SHORT_ID}"
  echo "  sni: ${DOMAIN}, flow: xtls-rprx-vision, fingerprint: chrome"
  echo "  link: ${LINK}"
} > "$USERS_FILE"
chmod 600 "$USERS_FILE"

echo
log "Готово. Ссылка для клиента (сохранена в $USERS_FILE):"
echo
echo "  $LINK"
echo
MEM_NOW="$(systemctl show sing-box -p MemoryCurrent --value 2>/dev/null || true)"
[[ "$MEM_NOW" =~ ^[0-9]+$ ]] && echo "Память sing-box сейчас: $(( MEM_NOW / 1048576 )) МБ (предел 180 МБ)"
echo "Журнал: journalctl -u sing-box -f    Удаление: bash add-reality.sh --remove"
