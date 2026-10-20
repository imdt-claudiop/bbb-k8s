#!/bin/bash
# Container replacement for bigbluebutton-web/run-in-systemd.sh, kept at the path the
# conversion code calls (/usr/share/bbb-web/run-in-systemd.sh, fixed in bbb-common-web).
# The package runs each conversion in a hardened transient systemd unit. There is no systemd
# here; what does not need it is kept:
#   RuntimeMaxSec=<timeout>  timeout(1), and SIGKILL 10 s later if the command ignores SIGTERM
#   MemoryMax=640M           an address-space limit (ulimit -v) at the same default, read from
#                            BBB_PRESENTATION_CONVERSION_MEMORY_MAX like upstream
#   UMask=0022               umask 0022
# MemoryHigh (a soft limit) has no equivalent without cgroups; the container covers the
# filesystem and privilege restrictions. The memory cap matters more in a pod than on a server:
# the conversion shares the bbb-web container, and on cgroup v2 the kubelet kills every process
# of a container that passes its memory limit, the JVM with all its meetings included.
# Usage: run-in-systemd.sh <timeout-seconds> <command> [args...]
set -e
if [ $# -lt 2 ]; then
  echo "Usage: $0 <timeout-seconds> <command> [args ...]" >&2
  exit 1
fi
timeout_secs="$1"; shift

# A systemd size (K, M or G suffix, or bytes) in KiB, the unit of ulimit -v.
mem="${BBB_PRESENTATION_CONVERSION_MEMORY_MAX:-640M}"
case "$mem" in
  *K) kib=${mem%K} ;;
  *M) kib=$((${mem%M} * 1024)) ;;
  *G) kib=$((${mem%G} * 1024 * 1024)) ;;
  *) kib=$((mem / 1024)) ;;
esac
ulimit -v "$kib"
umask 0022
exec timeout --kill-after=10s "${timeout_secs}" "$@"
