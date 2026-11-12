#!/usr/bin/env bash
# Optional independent Mieru installation. Does not replace sing-box or Caddy.
set -Eeuo pipefail
umask 077
[[ $EUID == 0 ]] || { echo 'Нужны права root.' >&2; exit 1; }
for command in curl python3 tar sha256sum systemctl flock timeout; do
  command -v "$command" >/dev/null || { echo "Не найдена команда $command" >&2; exit 1; }
done
exec 9>/run/naivka-mieru-install.lock
flock -n 9 || { echo 'Установка Mieru уже выполняется.' >&2; exit 1; }
BASE=/opt/naivka-mieru
UNIT=/etc/systemd/system/naivka-mita.service
PORT=49997
TAG=v3.39.0
case "$(uname -m)" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; *) echo 'Неподдерживаемая архитектура Mieru.' >&2; exit 1;; esac
ASSET="mita_${TAG#v}_linux_${ARCH}.tar.gz"
mkdir -p "$BASE"
STAGE=$(mktemp -d "$BASE/stage.XXXXXXXX")
BACKUP=$(mktemp -d "$BASE/backup.XXXXXXXX")
ACTIVE=0; ENABLED=0; APPLIED=0; SUCCESS=0; VALIDATOR_PID=
systemctl is-active --quiet naivka-mita && ACTIVE=1 || true
systemctl is-enabled --quiet naivka-mita && ENABLED=1 || true
[[ ! -d "$BASE/current" ]] || cp -a "$BASE/current" "$BACKUP/current"
[[ ! -f "$UNIT" ]] || cp -a "$UNIT" "$BACKUP/unit"
cleanup() {
  status=$?; trap - EXIT; set +e
  if [[ -n "$VALIDATOR_PID" ]]; then kill "$VALIDATOR_PID" 2>/dev/null; wait "$VALIDATOR_PID" 2>/dev/null; fi
  if [[ $APPLIED == 1 && $SUCCESS != 1 ]]; then
    echo 'Mieru не запустился; восстанавливаю прежнюю конфигурацию.' >&2
    systemctl stop naivka-mita
    rm -rf "$BASE/current"
    [[ ! -d "$BACKUP/current" ]] || cp -a "$BACKUP/current" "$BASE/current"
    if [[ -f "$BACKUP/unit" ]]; then cp -a "$BACKUP/unit" "$UNIT"; else rm -f "$UNIT"; fi
    systemctl daemon-reload
    if [[ $ENABLED == 1 ]]; then systemctl enable naivka-mita; else systemctl disable naivka-mita; fi
    [[ $ACTIVE == 0 ]] || systemctl start naivka-mita
  fi
  rm -rf "$STAGE"
  exit "$status"
}
trap cleanup EXIT
echo 'Скачиваю официальный Mieru (mita), проверяю SHA-256…'
curl --fail --silent --show-error --location --connect-timeout 15 --max-time 180 \
  "https://github.com/enfein/mieru/releases/download/$TAG/$ASSET" -o "$STAGE/archive.tar.gz"
curl --fail --silent --show-error --location --connect-timeout 15 --max-time 30 \
  "https://api.github.com/repos/enfein/mieru/releases/tags/$TAG" -o "$STAGE/release.json"
python3 - "$STAGE" "$ASSET" <<'PY'
import hashlib,json,pathlib,sys
stage=pathlib.Path(sys.argv[1]); name=sys.argv[2]
assets=json.loads((stage/'release.json').read_text())['assets']
digest=next((a.get('digest','') for a in assets if a['name']==name),'')
actual='sha256:'+hashlib.sha256((stage/'archive.tar.gz').read_bytes()).hexdigest()
if not digest.startswith('sha256:') or digest!=actual: raise SystemExit('Не удалось подтвердить контрольную сумму Mieru.')
PY
# Extract exactly one regular binary, never archive paths or symbolic links.
python3 - "$STAGE" <<'PY'
import pathlib,tarfile,sys
p=pathlib.Path(sys.argv[1])
with tarfile.open(p/'archive.tar.gz') as archive:
    files=[m for m in archive.getmembers() if pathlib.PurePosixPath(m.name).name=='mita' and m.isfile()]
    if len(files)!=1 or files[0].size>128*1024*1024: raise SystemExit('Неверный архив mita.')
    (p/'mita').write_bytes(archive.extractfile(files[0]).read())
(p/'mita').chmod(0o755)
PY
export SERVER_ADDR NAME
python3 - "$STAGE" "$BASE/current/config.json" "$PORT" <<'PY'
import json,pathlib,secrets,sys,os,urllib.parse,socket
stage=pathlib.Path(sys.argv[1]); old=pathlib.Path(sys.argv[2]); port=int(sys.argv[3])
if old.exists():
    cfg=json.loads(old.read_text())
    if cfg.get('portBindings') != [{'port':port,'protocol':'TCP'},{'port':port,'protocol':'UDP'}]: raise SystemExit('Порты существующего Mieru изменены; автоматическая замена отменена.')
    if len(cfg.get('users',[]))!=1 or not cfg['users'][0].get('password'): raise SystemExit('Существующая конфигурация Mieru нестандартная; автоматическая замена отменена.')
else:
    # Abort before changes if either listener is occupied.
    for kind in (socket.SOCK_STREAM,socket.SOCK_DGRAM):
        with socket.socket(socket.AF_INET,kind) as s: s.bind(('0.0.0.0',port))
    cfg={'portBindings':[{'port':port,'protocol':'TCP'},{'port':port,'protocol':'UDP'}],
         'users':[{'name':'naivka','password':secrets.token_hex(24)}], 'loggingLevel':'WARN','mtu':1400}
(stage/'config.json').write_text(json.dumps(cfg))
user=cfg['users'][0]; addr=os.environ['SERVER_ADDR']; host='['+addr+']' if ':' in addr else addr
q=[('profile',(os.environ.get('NAME') or addr)+' Mieru'),('mtu',str(cfg.get('mtu',1400)))]
for b in cfg['portBindings']:
    q.extend([('port',str(b.get('portRange') or b['port'])),('protocol',b['protocol'])])
url='mierus://'+urllib.parse.quote(user['name'],safe='')+':'+urllib.parse.quote(user['password'],safe='')+'@'+host+'?'+urllib.parse.urlencode(q)
(stage/'link').write_text(url)
PY
# apply config is an RPC command, not an offline validator. Run an isolated,
# idle daemon: its empty configuration opens no public listeners. Neither the
# default mita socket nor the installed Naivka service may receive this request.
# STAGE is root-only (0700); the relaxed UDS ownership is confined to this probe.
printf '{}\n' > "$STAGE/validated.json"
VALIDATOR_SOCKET="$STAGE/validate.sock"
env -u MITA_CONFIG_FILE MITA_CONFIG_JSON_FILE="$STAGE/validated.json" \
  MITA_UDS_PATH="$VALIDATOR_SOCKET" MITA_INSECURE_UDS=1 \
  "$STAGE/mita" run >"$STAGE/validation.log" 2>&1 &
VALIDATOR_PID=$!
READY=0
for _ in {1..30}; do
  if MITA_UDS_PATH="$VALIDATOR_SOCKET" timeout 2 "$STAGE/mita" status >/dev/null 2>&1; then READY=1; break; fi
  kill -0 "$VALIDATOR_PID" 2>/dev/null || break
  sleep 1
done
[[ $READY == 1 ]] || { echo 'Не удалось запустить изолированную проверку Mieru.' >&2; exit 1; }
if ! MITA_UDS_PATH="$VALIDATOR_SOCKET" timeout 10 "$STAGE/mita" apply config "$STAGE/config.json" >"$STAGE/validation-result.log" 2>&1; then
  echo 'Конфигурация Mieru не прошла проверку; установленная служба не изменена.' >&2
  exit 1
fi
kill "$VALIDATOR_PID"
wait "$VALIDATOR_PID" 2>/dev/null || true
VALIDATOR_PID=
getent group mita >/dev/null || groupadd --system mita
id mita >/dev/null 2>&1 || useradd --system --gid mita --no-create-home --shell /usr/sbin/nologin mita
cat > "$STAGE/unit" <<'UNIT'
[Unit]
Description=Naivka Mieru server
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=mita
Group=mita
Environment=MITA_CONFIG_JSON_FILE=/opt/naivka-mieru/current/config.json
Environment=MITA_UDS_PATH=/run/naivka-mita/mita.sock
ExecStart=/opt/naivka-mieru/current/mita run
Restart=on-failure
RestartSec=3
RuntimeDirectory=naivka-mita
RuntimeDirectoryMode=0700
UMask=0077
NoNewPrivileges=true
ProtectHome=true
ProtectSystem=strict
PrivateTmp=true
LimitNOFILE=65536
[Install]
WantedBy=multi-user.target
UNIT
APPLIED=1
systemctl stop naivka-mita 2>/dev/null || true
mkdir -p "$BASE/current"
install -m755 "$STAGE/mita" "$BASE/current/mita"
install -m640 -o root -g mita "$STAGE/config.json" "$BASE/current/config.json"
chmod 755 "$BASE" "$BASE/current"
install -m644 "$STAGE/unit" "$UNIT"
systemctl daemon-reload
systemctl enable --now naivka-mita >/dev/null
READY=0
for _ in {1..30}; do
  # status also exits successfully for IDLE. Only RUNNING confirms proxy startup.
  if STATUS=$(MITA_UDS_PATH=/run/naivka-mita/mita.sock timeout 2 "$BASE/current/mita" status 2>&1) && [[ "$STATUS" == *'mita server status is "RUNNING"'* ]]; then READY=1; break; fi
  sleep 1
done
[[ $READY == 1 ]] || { echo 'Mieru не прошёл проверку запуска.' >&2; exit 1; }
if command -v ufw >/dev/null && ufw status | head -1 | grep -q 'Status: active'; then
  ufw allow "$PORT/tcp" >/dev/null
  ufw allow "$PORT/udp" >/dev/null
fi
SUCCESS=1
echo "Mieru установлен. Резервная копия: $BACKUP"
echo "В файрволе хостинга должны быть открыты $PORT TCP и UDP. Для Mieru важна синхронизация времени сервера."
printf 'NAIVKA-LINK %s\n' "$(cat "$STAGE/link")"
