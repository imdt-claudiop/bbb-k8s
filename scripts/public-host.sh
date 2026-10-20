#!/bin/bash
# Print the public host of the BBB entry point: waits for the DigitalOcean LoadBalancer IP of
# svc/bbb-nginx and checks that <prefix>-ip-<IP with dashes>.bbb.imdt.dev resolves to it on
# public DNS. Output (stdout): "publicHost=<host> publicIP=<ip>".
#
# Env: NS (default bbb), PREFIX (default bbbk8s), TIMEOUT seconds (default 600).
set -euo pipefail
NS=${NS:-bbb}; PREFIX=${PREFIX:-bbbk8s}; TIMEOUT=${TIMEOUT:-600}

ip=""
for _ in $(seq 1 $((TIMEOUT / 10))); do
  ip=$(kubectl -n "$NS" get svc bbb-nginx -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
  [ -n "$ip" ] && break
  echo "waiting for the LoadBalancer IP of svc/bbb-nginx..." >&2; sleep 10
done
[ -n "$ip" ] || { echo "no LoadBalancer IP after ${TIMEOUT}s" >&2; exit 1; }

host="${PREFIX}-ip-${ip//./-}.bbb.imdt.dev"
resolved=$(dig +short "$host" @1.1.1.1 | tail -1)
if [ "$resolved" != "$ip" ]; then
  echo "$host resolves to '${resolved}', expected $ip" >&2; exit 1
fi
echo "publicHost=$host publicIP=$ip"
