#!/bin/bash
# Create the namespace and the Secrets the chart references. Idempotent: existing Secrets are
# kept (rotating a database password needs the bootstrap Job to run again).
#
# The images are the public official ones: no pull Secret.
#
# Env: NS (default bbb): the namespace of the cell. Each cell has its own Secrets; run once per
#      cell (NS=cell-<n>) with a certificate that covers the cell's public host.
#      TLS_CERT, TLS_KEY: certificate (full chain) and key for the public host, PEM files. The
#      TLS Secret is refreshed from them on every run (certificates expire).
#      TLS_SOURCE=imdt-test: lab shortcut instead of TLS_CERT/TLS_KEY. Downloads iMDT's
#      *.bbb.imdt.dev wildcard from a.bbb.imdt.dev, as bbb-docker-dev does. Its private key is
#      handed to everyone who installs this way: lab only, never production.
#      LIVEKIT_API_KEY (optional, default bbbk8s): must match values livekit.apiKey.
set -euo pipefail
NS=${NS:-bbb}

# Check the certificate source before changing anything.
if [ -n "${TLS_CERT:-}" ] && [ -n "${TLS_KEY:-}" ]; then
  tls_source=files
elif [ "${TLS_SOURCE:-}" = imdt-test ]; then
  tls_source=imdt-test
else
  cat >&2 <<'EOF'
no certificate for the public host. Either:
  TLS_CERT=<fullchain.pem> TLS_KEY=<privkey.pem>   the certificate of your public host (production)
  TLS_SOURCE=imdt-test                             lab only: iMDT's *.bbb.imdt.dev wildcard, whose
                                                   private key every such installer receives
EOF
  exit 1
fi

kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"

rand() { openssl rand -hex 24; }

if ! kubectl -n "$NS" get secret bbb-db >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic bbb-db \
    --from-literal=postgres-password="$(rand)" \
    --from-literal=bbb-core-password="$(rand)" \
    --from-literal=bbb-hasura-password="$(rand)" \
    --from-literal=hasura-app-password="$(rand)" \
    --from-literal=blocknote-app-password="$(rand)"
fi

if ! kubectl -n "$NS" get secret bbb-salt >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic bbb-salt --from-literal=security-salt="$(rand)"
fi

if ! kubectl -n "$NS" get secret bbb-hasura >/dev/null 2>&1; then
  kubectl -n "$NS" create secret generic bbb-hasura --from-literal=admin-secret="$(rand)"
fi

# LiveKit API secret, generated once like the bbb-livekit postinst. The key id is not secret
# (values livekit.apiKey, default bbbk8s); keys.yaml is the key file livekit-server reads.
if ! kubectl -n "$NS" get secret bbb-livekit >/dev/null 2>&1; then
  lk_secret=$(openssl rand -hex 32)
  kubectl -n "$NS" create secret generic bbb-livekit \
    --from-literal=api-secret="$lk_secret" \
    --from-literal=keys.yaml="${LIVEKIT_API_KEY:-bbbk8s}: $lk_secret"
fi

# TLS for the BBB nginx, refreshed every run (certificates expire).
tls_dir=$(mktemp -d); trap 'rm -rf "$tls_dir"' EXIT
if [ "$tls_source" = files ]; then
  cp "$TLS_CERT" "$tls_dir/tls.crt"; cp "$TLS_KEY" "$tls_dir/tls.key"
else
  curl -fsS -o "$tls_dir/tls.crt" http://a.bbb.imdt.dev/certs_new/fullchain.pem
  curl -fsS -o "$tls_dir/tls.key" http://a.bbb.imdt.dev/certs_new/privkey.pem
fi
kubectl -n "$NS" create secret tls bbb-tls --cert="$tls_dir/tls.crt" --key="$tls_dir/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl -n "$NS" get secret bbb-db bbb-salt bbb-hasura bbb-livekit bbb-tls
