#!/usr/bin/env bash
# Друзья на своём сервере: отдельный вход для каждого — свои ключи Reality, Hysteria2 и NaiveProxy.
# Работает только на сервере, поставленном через Naivka (install-server.sh): берёт его ключи из
# /etc/sing-box/naivka.env и дописывает друзей к пользователям sing-box и Caddy.
#
# Naivka кладёт этот файл в /usr/local/sbin/naivka-friends и запускает с переменными:
#   ACTION       list | add | remove | pause | resume | renew | expire | apply
#   NAME         имя друга (для add), до 40 символов
#   DAYS         срок в днях (add, renew), 0 — бессрочно
#   FID          id друга (remove, pause, resume, renew)
#   SERVER_ADDR  адрес сервера для ссылок
#
# Друзья хранятся в /etc/naivka-friends/friends.json. Раз в час служба naivka-friends.timer
# отключает тех, у кого кончился срок (ACTION=expire). Повторная установка сервера друзей не теряет.
#
# Что НЕ делается: не трогаются вход владельца, ключи сервера, другие программы.
# Последняя строка вывода «NAIVKA-FRIENDS <base64 JSON>» — список друзей и их ссылки для Naivka.
set -Eeuo pipefail

CONF_DIR="${CONF_DIR:-/etc/sing-box}"
STATE_FILE="$CONF_DIR/naivka.env"
CONFIG="$CONF_DIR/config.json"
# Не в папке sing-box: он склеивает все её json в свой конфиг.
FRIENDS_DIR="/etc/naivka-friends"
CADDYFILE="/etc/naivka-caddy/Caddyfile"
CADDY_BIN="/usr/local/bin/caddy-naive"
SELF="/usr/local/sbin/naivka-friends"
TEST="${NAIVKA_TEST:-0}"
if [[ "$TEST" == 1 ]]; then
  CADDYFILE="$CONF_DIR/caddy/Caddyfile"
  FRIENDS_DIR="$CONF_DIR/friends"
  SELF="$CONF_DIR/naivka-friends"
  command -v caddy-naive >/dev/null && CADDY_BIN="$(command -v caddy-naive)"
fi

log()  { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
trap 'die "Ошибка на строке $LINENO. Подробности выше."' ERR

[[ "${EUID}" -eq 0 || "$TEST" == 1 ]] || die "Нужны права root."
[[ -r "$STATE_FILE" && -r "$CONFIG" ]] || die "Сервер ставился не через Naivka: нет $STATE_FILE. Сначала установите протоколы во вкладке «Свой сервер»."
# shellcheck disable=SC1090
. "$STATE_FILE"

ACTION="${ACTION:-list}"
NAME="${NAME:-}"
DAYS="${DAYS:-0}"
FID="${FID:-}"
SERVER_ADDR="${SERVER_ADDR:-}"

if ! command -v jq >/dev/null; then
  [[ "$TEST" == 1 ]] && die "Нужен jq."
  log "Ставлю jq…"
  apt-get install -y jq >/dev/null 2>&1 || { apt-get update -y >/dev/null && apt-get install -y jq >/dev/null; }
fi

FRIENDS="$FRIENDS_DIR/friends.json"
mkdir -p "$FRIENDS_DIR"
chmod 700 "$FRIENDS_DIR"
# Одновременно с часовой проверкой сроков файл не правим.
exec 9>"$FRIENDS_DIR/lock"
flock -w 30 9 || die "Список друзей занят другой операцией — попробуйте ещё раз."

umask 077
[[ -s "$FRIENDS" ]] || echo '[]' > "$FRIENDS"
NOW="$(date +%s)"

save() { # save <json>
  local tmp
  tmp="$(mktemp "$FRIENDS_DIR/.friends.XXXXXX")"
  printf '%s\n' "$1" > "$tmp"
  mv "$tmp" "$FRIENDS"
  chmod 600 "$FRIENDS"
}

need_fid() {
  [[ "$FID" =~ ^[0-9a-f]{8}$ ]] || die "Неверный id друга."
  jq -e --arg id "$FID" 'any(.[]; .id == $id)' "$FRIENDS" >/dev/null || die "Друга с таким id нет."
}

# apply — переписывает пользователей sing-box и Caddy: владелец + все друзья, у которых доступ открыт.
apply() {
  local active tmp
  active="$(jq --argjson now "$NOW" '[.[] | select((.paused | not) and (.until == 0 or .until > $now))]' "$FRIENDS")"

  tmp="$(mktemp "$FRIENDS_DIR/.config.XXXXXX")"
  jq --argjson f "$active" '
    .inbounds |= map(
      if .tag == "naivka-reality" then
        .users = ([.users[] | select(.name == "naivka")] + [$f[] | {name: ("f-" + .id), uuid: .uuid, flow: "xtls-rprx-vision"}])
      elif .tag == "naivka-hy2" then
        .users = ([.users[] | select(.name == "naivka")] + [$f[] | {name: ("f-" + .id), password: .hy2}])
      else . end)' "$CONFIG" > "$tmp"
  if ! sing-box check -c "$tmp"; then rm -f "$tmp"; die "Конфиг не прошёл проверку sing-box — ничего не изменено."; fi
  if ! cmp -s "$tmp" "$CONFIG"; then
    cat "$tmp" > "$CONFIG" # cat, а не mv: права и владелец файла остаются прежними
    if [[ "${NO_SINGBOX_RESTART:-0}" != 1 ]]; then
      if [[ "$TEST" == 1 ]]; then
        pkill -f "sing-box run -c $CONFIG" 2>/dev/null || true
        nohup sing-box run -c "$CONFIG" >"$CONF_DIR/test.log" 2>&1 9>&- & # без замка: его держит только этот скрипт
        sleep 1.5
      else
        systemctl reload sing-box 2>/dev/null || systemctl restart sing-box
      fi
    fi
  fi
  rm -f "$tmp"

  # NaiveProxy: строки basic_auth друзей помечены «# naivka-friend» и стоят сразу за строкой владельца.
  if [[ -n "${NAIVE_USER:-}" && -f "$CADDYFILE" ]]; then
    tmp="$(mktemp "$FRIENDS_DIR/.caddy.XXXXXX")"
    jq -r '.[] | "    basic_auth \(.nuser) \(.npass) # naivka-friend"' <<<"$active" > "$tmp.add"
    awk -v owner="basic_auth ${NAIVE_USER} " -v addf="$tmp.add" '
      /# naivka-friend$/ { next }
      { print }
      index($0, owner) { while ((getline l < addf) > 0) print l }' "$CADDYFILE" > "$tmp"
    rm -f "$tmp.add"
    if ! cmp -s "$tmp" "$CADDYFILE"; then
      if ! "$CADDY_BIN" validate --config "$tmp" --adapter caddyfile >/dev/null 2>&1; then
        rm -f "$tmp"; die "Конфиг Caddy не прошёл проверку — ничего не изменено."
      fi
      cat "$tmp" > "$CADDYFILE"
      if [[ "$TEST" == 1 ]]; then
        "$CADDY_BIN" reload --config "$CADDYFILE" --adapter caddyfile --force >/dev/null 2>&1 || warn "Caddy не перечитал конфиг."
      else
        systemctl reload naivka-caddy || warn "Caddy не перечитал конфиг."
      fi
    fi
    rm -f "$tmp"
  fi
}

# Часовая проверка сроков — своя служба systemd, ставится один раз.
ensure_timer() {
  [[ "$TEST" == 1 ]] && return 0
  [[ -x "$SELF" ]] || return 0
  if [[ ! -f /etc/systemd/system/naivka-friends.timer ]]; then
    printf '%s\n' "# Создано Naivka: отключает друзей, у которых кончился срок." "[Unit]" "Description=Naivka friends: expire" "" \
      "[Service]" "Type=oneshot" "Environment=ACTION=expire" "ExecStart=$SELF" > /etc/systemd/system/naivka-friends.service
    printf '%s\n' "# Создано Naivka." "[Unit]" "Description=Naivka friends: hourly expiry check" "" \
      "[Timer]" "OnCalendar=hourly" "Persistent=true" "" "[Install]" "WantedBy=timers.target" > /etc/systemd/system/naivka-friends.timer
    systemctl daemon-reload
    systemctl enable --now naivka-friends.timer >/dev/null 2>&1 || warn "Не удалось включить проверку сроков."
  fi
}

urlenc() {
  local LC_ALL=C s="$1" out="" c i
  for (( i = 0; i < ${#s}; i++ )); do
    c="${s:i:1}"
    case "$c" in [A-Za-z0-9._~-]) out+="$c" ;; *) printf -v c '%%%02X' "'$c"; out+="$c" ;; esac
  done
  printf '%s' "$out"
}

# report — список друзей и ссылки на вход для каждого (их видит только владелец в Naivka).
report() {
  local sni pin="" addr links f id name nm
  sni="$(jq -r '.inbounds[] | select(.tag == "naivka-reality") | .tls.server_name' "$CONFIG")"
  [[ -n "${HY2_PORT:-}" && -s "$CONF_DIR/naivka-hy2.crt" ]] && \
    pin="$(openssl x509 -in "$CONF_DIR/naivka-hy2.crt" -pubkey -noout | openssl pkey -pubin -outform der | openssl dgst -sha256 -binary | openssl enc -base64)"
  addr="$SERVER_ADDR"
  [[ "$addr" == *:* ]] && addr="[$addr]"
  links='{}'
  while IFS= read -r f; do
    id="$(jq -r .id <<<"$f")"
    name="$(jq -r .name <<<"$f")"
    nm=()
    if [[ -n "$addr" ]]; then
      nm+=("vless://$(jq -r .uuid <<<"$f")@${addr}:${REALITY_PORT}?encryption=none&flow=xtls-rprx-vision&security=reality&sni=${sni}&fp=chrome&pbk=${PUBLIC_KEY}&sid=${SHORT_ID}&type=tcp#$(urlenc "$name Reality")")
      [[ -n "$pin" ]] && nm+=("hysteria2://$(jq -r .hy2 <<<"$f")@${addr}:${HY2_PORT}/?sni=${sni}&obfs=salamander&obfs-password=${OBFS_PASS}&pubkey-sha256=$(urlenc "$pin")#$(urlenc "$name Hysteria2")")
    fi
    [[ -n "${NAIVE_USER:-}" && -n "${DOMAIN:-}" ]] && nm+=("naive+https://$(jq -r .nuser <<<"$f"):$(jq -r .npass <<<"$f")@${DOMAIN}:443#$(urlenc "$name Naive")")
    links="$(jq --arg id "$id" '.[$id] = $ARGS.positional' --args "${nm[@]}" <<<"$links")"
  done < <(jq -c '.[]' "$FRIENDS")
  printf 'NAIVKA-FRIENDS %s\n' "$(jq -c --argjson now "$NOW" --argjson links "$links" \
    '{friends: [.[] | {id, name, created, until, paused, expired: (.until > 0 and .until <= $now), links: ($links[.id] // [])}]}' "$FRIENDS" | base64 -w0)"
}

case "$ACTION" in
  list) ;;
  add)
    [[ "$NAME" =~ ^[^[:cntrl:]]{1,40}$ ]] || die "Имя — от 1 до 40 символов."
    [[ "$DAYS" =~ ^[0-9]{1,4}$ ]] || die "Срок — число дней (0 — бессрочно)."
    (( $(jq length "$FRIENDS") < 100 )) || die "Больше 100 друзей на одном сервере не поддерживается."
    id="$(openssl rand -hex 4)"
    until=0
    (( DAYS > 0 )) && until=$(( NOW + DAYS * 86400 ))
    save "$(jq --arg id "$id" --arg name "$NAME" --arg uuid "$(sing-box generate uuid)" \
      --arg hy2 "$(openssl rand -hex 16)" --arg nuser "f$(openssl rand -hex 5)" --arg npass "$(openssl rand -hex 16)" \
      --argjson now "$NOW" --argjson until "$until" \
      '. + [{id: $id, name: $name, uuid: $uuid, hy2: $hy2, nuser: $nuser, npass: $npass, created: $now, until: $until, paused: false}]' "$FRIENDS")"
    apply
    ensure_timer
    log "Друг «$NAME» добавлен."
    ;;
  remove)
    need_fid
    save "$(jq --arg id "$FID" 'map(select(.id != $id))' "$FRIENDS")"
    apply
    log "Доступ удалён."
    ;;
  pause|resume)
    need_fid
    save "$(jq --arg id "$FID" --argjson p "$([[ "$ACTION" == pause ]] && echo true || echo false)" 'map(if .id == $id then .paused = $p else . end)' "$FRIENDS")"
    apply
    [[ "$ACTION" == pause ]] && log "Доступ приостановлен." || log "Доступ снова открыт."
    ;;
  renew)
    need_fid
    [[ "$DAYS" =~ ^[0-9]{1,4}$ ]] || die "Срок — число дней (0 — бессрочно)."
    until=0
    (( DAYS > 0 )) && until=$(( NOW + DAYS * 86400 ))
    save "$(jq --arg id "$FID" --argjson until "$until" 'map(if .id == $id then .until = $until | .paused = false else . end)' "$FRIENDS")"
    apply
    log "Срок обновлён."
    ;;
  expire|apply)
    apply
    ;;
  *) die "ACTION — list, add, remove, pause, resume, renew, expire или apply" ;;
esac

[[ "$ACTION" == expire || "$ACTION" == apply ]] || report
