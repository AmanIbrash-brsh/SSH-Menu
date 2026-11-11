echo __SM__
. /etc/os-release 2>/dev/null && echo "os=${PRETTY_NAME:-$NAME}"
echo "up=$(cut -d. -f1 /proc/uptime)"
echo "load=$(cut -d' ' -f1-3 /proc/loadavg)"
echo "cpu=$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo)"
awk '/^MemTotal:/{t=$2}/^MemAvailable:/{a=$2}END{print "mem=" t-a " " t}' /proc/meminfo
timeout 3 df -PkTl 2>/dev/null | awk 'NR>1 && $2!~/^(tmpfs|devtmpfs|overlay|squashfs|efivarfs|ramfs|nsfs|fuse.*)$/ {print "df=" $4 " " $3 " " $7}'
if command -v systemctl >/dev/null 2>&1; then
  echo units=1
  systemctl --failed --no-legend --plain 2>/dev/null | awk 'NF{print "fail=" $1}'
fi
[ -f /var/run/reboot-required ] && echo reboot=1
if command -v apt-get >/dev/null 2>&1; then
  u=$(timeout 5 apt-get -s -o Debug::NoLocking=1 upgrade 2>/dev/null | grep '^Inst')
  echo "upd=$(printf '%s' "$u" | grep -c .) $(printf '%s' "$u" | grep -ci securi)"
elif command -v dnf >/dev/null 2>&1; then
  echo "upd=$(timeout 5 dnf -C -q check-update 2>/dev/null | grep -c '^[[:alnum:]]') 0"
fi
c=$(timeout 2 docker ps -q 2>/dev/null) && echo "docker=$(printf '%s' "$c" | grep -c .)"
