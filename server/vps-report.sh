# Fixed read-only Linux probes. No user-supplied commands/paths, sudo, agents or files.
export LC_ALL=C
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
[ "$(uname -s 2>/dev/null)" = Linux ] || exit 2
printf 'protocol=1\n'
date '+server_time=%s'
# Fixed system identity and version probes. Never source os-release as shell code.
awk -F= '$1=="PRETTY_NAME" {v=substr($0,index($0,"=")+1);gsub(/^"|"$/,"",v);print "info_os=" substr(v,1,120);exit}' /etc/os-release 2>/dev/null
uname -r 2>/dev/null | cut -c1-120 | sed 's/^/info_kernel=/'
uname -m 2>/dev/null | cut -c1-120 | sed 's/^/info_arch=/'
hostname 2>/dev/null | cut -c1-120 | sed 's/^/info_hostname=/'
if command -v sing-box >/dev/null 2>&1; then sing-box version 2>/dev/null | head -n1 | cut -c1-120 | sed 's/^/info_singbox=/'; fi
if command -v xray >/dev/null 2>&1; then xray version 2>/dev/null | head -n1 | cut -c1-120 | sed 's/^/info_xray=/'; fi
if [ -x /usr/local/bin/caddy-naive ]; then /usr/local/bin/caddy-naive version 2>/dev/null | head -n1 | cut -c1-120 | sed 's/^/info_caddy=/'; fi
# Identity prevents rates across reboots or changed interface sets.
if [ -r /proc/sys/kernel/random/boot_id ]; then tr -d '\n' < /proc/sys/kernel/random/boot_id | sed 's/^/boot_id=/'; printf '\n'; fi
awk -F: 'NR>2 {n=$1; gsub(/[ \t]/,"",n);if(n!="lo")print n}' /proc/net/dev 2>/dev/null | sort | cksum | awk '{print "network_set=" $1}'
# One second of CPU deltas; guest counters are already included in user/nice.
cpu_before=$(awk '/^cpu / {t=0; for(i=2;i<=9;i++) t+=$i; print t, $5+$6}' /proc/stat 2>/dev/null)
sleep 1
cpu_after=$(awk '/^cpu / {t=0; for(i=2;i<=9;i++) t+=$i; print t, $5+$6}' /proc/stat 2>/dev/null)
printf '%s\n%s\n' "$cpu_before" "$cpu_after" | awk 'NR==1 && NF==2 {t=$1; idle=$2; ok=1} NR==2 && NF==2 && ok && $1>t {printf "cpu_usage=%.1f\n",100*(1-($2-idle)/($1-t))}'
awk '{printf "uptime=%.0f\n", $1}' /proc/uptime 2>/dev/null
awk '{print "load=" $1; print "load5=" $2; print "load15=" $3; split($4,p,"/");print "processes=" p[2]}' /proc/loadavg 2>/dev/null
awk '/^processor[[:space:]]*:/ {n++} END {if(n) print "cpus=" n}' /proc/cpuinfo 2>/dev/null
awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2; found=1} END {if(t && found) {printf "memory_total=%.0f\nmemory_used=%.0f\n", t*1024, (t-a)*1024}}' /proc/meminfo 2>/dev/null
awk '/^SwapTotal:/ {t=$2} /^SwapFree:/ {f=$2; ok=1} END {if(ok)printf "swap_total=%.0f\nswap_used=%.0f\n",t*1024,(t-f)*1024}' /proc/meminfo 2>/dev/null
for resource in cpu memory io; do
  awk -v k="pressure_$resource" '$1=="some" {for(i=2;i<=NF;i++)if($i~/^avg10=/){split($i,a,"=");print k "=" a[2]}}' "/proc/pressure/$resource" 2>/dev/null
done
df -Pi / 2>/dev/null | awk 'NR==2 {print "inodes_total=" $2;print "inodes_used=" $3}'
df -Pk / 2>/dev/null | awk 'NR==2 {printf "disk_total=%.0f\ndisk_used=%.0f\n", $2*1024, $3*1024}'
awk -F ':' 'NR>2 {name=$1; gsub(/[ \t]/,"",name); if(name=="lo") next; data=$2; sub(/^[ \t]+/,"",data); split(data,f,/[ \t]+/); rx+=f[1]; tx+=f[9]} END {if(NR>2) printf "network_rx=%.0f\nnetwork_tx=%.0f\n", rx,tx}' /proc/net/dev 2>/dev/null
# Effective defaults/includes, but NOT connection-specific Match evaluation.
sshd_bin=$(command -v sshd 2>/dev/null)
if [ -n "$sshd_bin" ]; then
  "$sshd_bin" -T 2>/dev/null | awk '$1=="permitrootlogin" {print "root_login=" $2} $1=="passwordauthentication" {print "password_auth=" $2} $1=="kbdinteractiveauthentication" {print "keyboard_auth=" $2} $1=="usepam" {print "pam=" $2} $1=="pubkeyauthentication" {print "publickey_auth=" $2} $1=="permitemptypasswords" {print "empty_passwords=" $2} $1=="maxauthtries" {print "auth_tries=" $2} $1=="x11forwarding" {print "x11_forwarding=" $2}'
fi
if command -v ufw >/dev/null 2>&1; then
  ufw status 2>/dev/null | awk '/^Status: active$/ {print "firewall=active"} /^Status: inactive$/ {print "firewall=inactive"}'
fi
# Evaluate Match for the current SSH connection where its trusted environment is available.
case "$SSH_CONNECTION" in
  *[!0-9a-fA-F:.\ ]*|'') ;;
  *) set -- $SSH_CONNECTION
     if [ "$#" = 4 ] && [ -n "$sshd_bin" ]; then
       ssh_user=$(id -un 2>/dev/null)
       case "$ssh_user" in *[!a-zA-Z0-9_.-]*|'') ;; *)
         "$sshd_bin" -T -C "user=$ssh_user,addr=$1,laddr=$3,lport=$4" 2>/dev/null | awk '$1=="permitrootlogin" {print "match_root=" $2} $1=="passwordauthentication" {print "match_password=" $2} $1=="kbdinteractiveauthentication" {print "match_keyboard=" $2}'
         ;; esac
     fi;;
esac
if command -v firewall-cmd >/dev/null 2>&1; then
  firewall-cmd --state 2>/dev/null | awk '$0=="running" {print "firewalld=active"} $0=="not running" {print "firewalld=inactive"}'
fi
if command -v nft >/dev/null 2>&1; then
  { nft list ruleset 2>/dev/null; printf '__exit=%s\n' "$?"; } | awk '/^__exit=/ {status=substr($0,8);next} /hook input/ {hook=1} /policy drop|policy reject/ {drop=1} END {if(status=="0")print "nft_input=" (hook?(drop?"drop_observed":"hook_observed"):"no_hook")}'
fi
if command -v iptables >/dev/null 2>&1; then
  iptables -S INPUT 2>/dev/null | awk '$1=="-P"&&$2=="INPUT" {if($3=="ACCEPT")print "iptables_input=accept";else if($3=="DROP")print "iptables_input=drop"}'
fi
if [ -x /opt/naivka-mieru/current/mita ]; then /opt/naivka-mieru/current/mita version 2>/dev/null | head -n1 | cut -c1-120 | sed 's/^/info_mieru=/'; fi
for service in sing-box xray naivka-caddy naivka-mita x-ui marzban nginx caddy fail2ban ssh sshd s-ui;  do
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

# Debian/Ubuntu only: use existing APT indexes, never refresh/download/install.
# Do not source os-release as shell code. Disk cache generation is disabled.
os_id=$(awk -F= '$1=="ID" {gsub(/["\047]/,"",$2); print $2; exit}' /etc/os-release 2>/dev/null)
case "$os_id" in
  debian|ubuntu)
    index_times=$(for index in /var/lib/apt/lists/*_Packages*; do
      [ -f "$index" ] && [ -r "$index" ] && stat -c '%Y' "$index" 2>/dev/null
    done | awk '/^[0-9]+$/ {if(!n || $1<oldest) oldest=$1; if($1>newest) newest=$1; n++} END {if(n) printf "apt_index_oldest=%.0f\napt_index_newest=%.0f\n",oldest,newest}')
    if [ -n "$index_times" ] && command -v apt >/dev/null 2>&1; then
      printf '%s\n' "$index_times"
      { apt -o Dir::Cache::pkgcache= -o Dir::Cache::srcpkgcache= -o Debug::NoLocking=1 list --upgradable 2>/dev/null; printf '__exit=%s\n' "$?"; } |
        awk '/^Listing\.\.\./ {header=1; next} /^__exit=/ {status=substr($0,8); next}
          /\[upgradable from: / && $1 ~ /\// {n++; next} NF {bad=1}
          END {if(status=="0" && header && !bad) printf "os_updates=apt_cached\napt_updates=%d\n",n;
               else print "os_updates=cache_error"}'
    else printf 'os_updates=missing_indexes\n'; fi
    ;;
  *) printf 'os_updates=unsupported\n';;
esac

# Local socket inventory, NOT an external reachability scan. No process names/IPs
# leave the server. Retain at most 128 distinct protocol/port/binding tuples.
if command -v ss >/dev/null 2>&1; then
  { ss -H -lntu 2>/dev/null; printf '__exit=%s\n' "$?"; } | awk '
    /^__exit=/ {status=substr($0,8); next}
    ($1=="tcp" && $2=="LISTEN") || ($1=="udp" && $2=="UNCONN") {
      endpoint=$5; port=endpoint; sub(/^.*:/,"",port)
      if(port !~ /^[0-9]+$/ || (port+0)<1 || (port+0)>65535) {bad=1; next}
      address=endpoint; sub(/:[^:]*$/,"",address); gsub(/[\[\]]/,"",address); sub(/%.*/,"",address)
      binding="specific"
      if(address=="*" || address=="0.0.0.0" || address=="::") binding="wildcard"
      else if(address ~ /^127\./ || address=="::1" || address=="0:0:0:0:0:0:0:1" || address ~ /^::ffff:127\./) binding="loopback"
      key=$1 "|" (port+0) "|" binding
      if(key in seen) next
      if(n>=128) {truncated=1; next}
      seen[key]=1; entries[++n]=key; next
    }
    NF {bad=1}
    END {if(status!="0" || bad) exit
         print "local_listeners=" (truncated ? "truncated" : "observed")
         for(i=1;i<=n;i++) print "listener=" entries[i]}'
fi

# Fixed known secret/configuration paths only. ACL output stays on the server.
for item in singbox:/etc/sing-box/config.json xray:/usr/local/etc/xray/config.json panel:/etc/x-ui/x-ui.db ssh:/etc/ssh/sshd_config; do
  label=${item%%:*}; path=${item#*:}
  [ -f "$path" ] || continue
  stat -c '%a %u' "$path" 2>/dev/null | awk -v k="$label" 'NF==2 && $1~/^[0-7]+$/ && $2~/^[0-9]+$/ {print "owner_" k "=" ($2==0?"root":"other");n=length($1);g=substr($1,n-1,1);o=substr($1,n,1);print "access_" k "=" ((g+o)>0?"shared_access":"restricted")}'
  if command -v getfacl >/dev/null 2>&1; then
    { getfacl -cp -- "$path" 2>/dev/null; printf '__exit=%s\n' "$?"; } | awk -v k="$label" '/^__exit=/ {status=substr($0,8);next} /^(default:)?(user|group):[^:]+:/ {named=1} END {if(status=="0")print "acl_" k "=" (named?"extended":"basic")}'
  fi
done
for item in caddy:/etc/caddy/cert.pem panel:/etc/x-ui/cert.pem; do
  label=${item%%:*}; path=${item#*:}
  [ -r "$path" ] || continue
  openssl x509 -in "$path" -noout >/dev/null 2>&1 || continue
  if openssl x509 -in "$path" -noout -checkend 2592000 >/dev/null 2>&1; then
    printf 'cert_%s=valid_30d\n' "$label"
  elif openssl x509 -in "$path" -noout -checkend 0 >/dev/null 2>&1; then
    printf 'cert_%s=expires_30d\n' "$label"
  else printf 'cert_%s=expired\n' "$label"; fi
done
