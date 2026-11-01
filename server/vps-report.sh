# Fixed read-only Linux probes. No user-supplied commands/paths, sudo, agents or files.
export LC_ALL=C
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
[ "$(uname -s 2>/dev/null)" = Linux ] || exit 2
printf 'protocol=1\n'
# One second of CPU deltas; guest counters are already included in user/nice.
cpu_before=$(awk '/^cpu / {t=0; for(i=2;i<=9;i++) t+=$i; print t, $5+$6}' /proc/stat 2>/dev/null)
sleep 1
cpu_after=$(awk '/^cpu / {t=0; for(i=2;i<=9;i++) t+=$i; print t, $5+$6}' /proc/stat 2>/dev/null)
printf '%s\n%s\n' "$cpu_before" "$cpu_after" | awk 'NR==1 && NF==2 {t=$1; idle=$2; ok=1} NR==2 && NF==2 && ok && $1>t {printf "cpu_usage=%.1f\n",100*(1-($2-idle)/($1-t))}'
awk '{printf "uptime=%.0f\n", $1}' /proc/uptime 2>/dev/null
awk '{print "load=" $1}' /proc/loadavg 2>/dev/null
awk '/^processor[[:space:]]*:/ {n++} END {if(n) print "cpus=" n}' /proc/cpuinfo 2>/dev/null
awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2; found=1} END {if(t && found) {printf "memory_total=%.0f\nmemory_used=%.0f\n", t*1024, (t-a)*1024}}' /proc/meminfo 2>/dev/null
df -Pk / 2>/dev/null | awk 'NR==2 {printf "disk_total=%.0f\ndisk_used=%.0f\n", $2*1024, $3*1024}'
awk -F ':' 'NR>2 {name=$1; gsub(/[ \t]/,"",name); if(name=="lo") next; data=$2; sub(/^[ \t]+/,"",data); split(data,f,/[ \t]+/); rx+=f[1]; tx+=f[9]} END {if(NR>2) printf "network_rx=%.0f\nnetwork_tx=%.0f\n", rx,tx}' /proc/net/dev 2>/dev/null
# Effective defaults/includes, but NOT connection-specific Match evaluation.
sshd_bin=$(command -v sshd 2>/dev/null)
if [ -n "$sshd_bin" ]; then
  "$sshd_bin" -T 2>/dev/null | awk '$1=="permitrootlogin" {print "root_login=" $2} $1=="passwordauthentication" {print "password_auth=" $2}'
fi
if command -v ufw >/dev/null 2>&1; then
  ufw status 2>/dev/null | awk '/^Status: active$/ {print "firewall=active"} /^Status: inactive$/ {print "firewall=inactive"}'
fi
for service in sing-box xray naivka-caddy x-ui; do
  [ "$(systemctl show "$service" -p LoadState --value 2>/dev/null)" = loaded ] || continue
  state=$(systemctl is-active "$service" 2>/dev/null)
  case "$state" in active|inactive|failed) printf 'service_%s=%s\n' "$service" "$state";; esac
  state=$(systemctl is-enabled "$service" 2>/dev/null)
  case "$state" in enabled|disabled|static|masked) printf 'enabled_%s=%s\n' "$service" "$state";; esac
done
# Report only permission mode, never configuration contents (which contain keys).
if [ -f /etc/sing-box/config.json ]; then
  mode=$(stat -c '%a' /etc/sing-box/config.json 2>/dev/null)
  case "$mode" in [0-7][0-7][0-7]) printf 'config_mode=%s\n' "$mode";; esac
fi
# Only the certificate installed at Naivka's fixed Hysteria2 path is in scope.
if [ -r /etc/sing-box/naivka-hy2.crt ] && command -v openssl >/dev/null 2>&1; then
  if openssl x509 -in /etc/sing-box/naivka-hy2.crt -noout >/dev/null 2>&1; then
    if openssl x509 -in /etc/sing-box/naivka-hy2.crt -noout -checkend 0 >/dev/null 2>&1; then
      if openssl x509 -in /etc/sing-box/naivka-hy2.crt -noout -checkend 2592000 >/dev/null 2>&1; then
        printf 'certificate=valid_30d\n'
      else printf 'certificate=expires_30d\n'; fi
    else printf 'certificate=expired\n'; fi
  fi
fi
