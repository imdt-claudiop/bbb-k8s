#!/bin/sh
# Container replacement for bigbluebutton-web/run-in-systemd.sh, kept at the path the
# conversion code calls (/usr/share/bbb-web/run-in-systemd.sh, fixed in bbb-common-web).
# The package wraps each conversion in a transient systemd user unit with a timeout and memory
# limits; there is no systemd here, so this keeps the timeout and relies on the pod's limits.
# Usage: run-in-systemd.sh <timeout-seconds> <command> [args...]
set -e
if [ $# -lt 2 ]; then
  echo "Usage: $0 <timeout-seconds> <command> [args ...]" >&2
  exit 1
fi
timeout_secs="$1"; shift
exec timeout "${timeout_secs}" "$@"
