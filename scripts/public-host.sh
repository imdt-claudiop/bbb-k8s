#!/bin/bash
# Wait for the LoadBalancer IP of svc/bbb-nginx and check that the public host resolves to it on
# public DNS. Output (stdout): "publicHost=<host> publicIP=<ip>".
#
# The host to check, one of:
#   PUBLIC_HOST  the name you chose for the install (production): create its A record pointing to
#                the IP this script prints, or let external-dns do it.
#   IP_DOMAIN    lab shortcut for a domain whose resolver answers <name>-ip-<IP with dashes>.<domain>
#                with that IP (e.g. bbb.imdt.dev, iMDT's test resolver): the host becomes
#                ${PREFIX}-ip-<IP with dashes>.${IP_DOMAIN}. No default.
#
# Env: NS (default bbb), PREFIX (default bbbk8s, IP_DOMAIN only), TIMEOUT seconds for each wait
#      (default 600), DNS_SERVER (default 1.1.1.1; empty: the system resolver, for networks that
#      block outbound DNS).
set -euo pipefail
NS=${NS:-bbb}; PREFIX=${PREFIX:-bbbk8s}; TIMEOUT=${TIMEOUT:-600}; DNS_SERVER=${DNS_SERVER-1.1.1.1}

if [ -z "${PUBLIC_HOST:-}" ] && [ -z "${IP_DOMAIN:-}" ]; then
  cat >&2 <<'EOF'
set the host to check:
  PUBLIC_HOST=<your name>   the name of the install; its A record must point to the LoadBalancer IP
  IP_DOMAIN=<domain>        lab shortcut: <PREFIX>-ip-<IP with dashes>.<domain>, for a resolver that
                            answers those names with the IP (e.g. bbb.imdt.dev)
EOF
  exit 1
fi

ip=""
for _ in $(seq 1 $((TIMEOUT / 10))); do
  ip=$(kubectl -n "$NS" get svc bbb-nginx -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
  [ -n "$ip" ] && break
  echo "waiting for the LoadBalancer IP of svc/bbb-nginx..." >&2; sleep 10
done
[ -n "$ip" ] || { echo "no LoadBalancer IP after ${TIMEOUT}s" >&2; exit 1; }

host=${PUBLIC_HOST:-${PREFIX}-ip-${ip//./-}.${IP_DOMAIN:-}}

# The last IPv4 address of the answer (after any CNAME). A failed query (no answer, server
# unreachable) counts as "not resolved yet".
resolve() {
  dig +short "$host" ${DNS_SERVER:+"@$DNS_SERVER"} 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+){3}$' | tail -1 || true
}
resolved=$(resolve)
if [ "$resolved" != "$ip" ] && [ -n "${PUBLIC_HOST:-}" ]; then
  # Create the record before anything queries the name: resolvers cache a missing name for the
  # negative TTL of the zone, which can outlast the wait.
  echo "create an A record: $host -> $ip (waiting up to ${TIMEOUT}s for it on public DNS)" >&2
fi
for _ in $(seq 1 $((TIMEOUT / 10))); do
  [ "$resolved" = "$ip" ] && break
  sleep 10; resolved=$(resolve)
done
if [ "$resolved" != "$ip" ]; then
  echo "$host resolves to '${resolved}' on ${DNS_SERVER:-the system resolver}, expected $ip" >&2
  [ -z "$resolved" ] && [ -n "$DNS_SERVER" ] && \
    echo "(if this network blocks outbound DNS, retry with DNS_SERVER= to use the system resolver)" >&2
  exit 1
fi
echo "publicHost=$host publicIP=$ip"
