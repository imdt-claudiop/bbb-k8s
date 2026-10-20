#!/bin/bash
# Create the namespace and the Secrets the chart references. Idempotent: existing Secrets are
# kept (rotating a database password needs the bootstrap Job to run again).
#
# Env: GHCR_USER, GHCR_TOKEN (a token with read:packages on the bbb-k8s images).
#      LIVEKIT_API_KEY (optional, default bbbk8s): must match values livekit.apiKey.
#      TLS_CERT, TLS_KEY (optional): certificate and key for the public host. Without them the
#      *.bbb.imdt.dev wildcard is downloaded from a.bbb.imdt.dev, as bbb-docker-dev does.
set -euo pipefail
NS=${NS:-bbb}
: "${GHCR_USER:?}" "${GHCR_TOKEN:?}"

kubectl get namespace "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"
# Pod Security Admission in warn and audit mode: every apply lists what a pod still does that
# the restricted profile forbids (LiveKit's host network, the root entrypoints of Postgres and
# Redis), without blocking anything (docs/install.md "Pod security").
kubectl label namespace "$NS" --overwrite \
  pod-security.kubernetes.io/warn=restricted pod-security.kubernetes.io/audit=restricted >/dev/null

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
if [ -n "${TLS_CERT:-}" ] && [ -n "${TLS_KEY:-}" ]; then
  cp "$TLS_CERT" "$tls_dir/tls.crt"; cp "$TLS_KEY" "$tls_dir/tls.key"
else
  curl -fsS -o "$tls_dir/tls.crt" http://a.bbb.imdt.dev/certs_new/fullchain.pem
  curl -fsS -o "$tls_dir/tls.key" http://a.bbb.imdt.dev/certs_new/privkey.pem
fi
old_crt=$(kubectl -n "$NS" get secret bbb-tls -o jsonpath='{.data.tls\.crt}' 2>/dev/null || true)
kubectl -n "$NS" create secret tls bbb-tls --cert="$tls_dir/tls.crt" --key="$tls_dir/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# nginx reads its certificate only at start: the kubelet updates the mounted files, but nginx
# would keep serving the old one. A restart of nginx does not end meetings (clients reconnect).
if [ -n "$old_crt" ] && [ "$old_crt" != "$(base64 < "$tls_dir/tls.crt" | tr -d '\n')" ] \
  && kubectl -n "$NS" get deployment bbb-nginx >/dev/null 2>&1; then
  echo "certificate changed: restarting bbb-nginx"
  kubectl -n "$NS" rollout restart deployment bbb-nginx >/dev/null
fi

# The pull Secret is refreshed every run, so a rotated token only needs a re-run.
kubectl -n "$NS" create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io --docker-username="$GHCR_USER" --docker-password="$GHCR_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl -n "$NS" get secret bbb-db bbb-salt bbb-hasura bbb-livekit bbb-tls ghcr-pull
