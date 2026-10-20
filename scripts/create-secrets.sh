#!/bin/bash
# Create the namespace and the Secrets the chart references. Idempotent: existing Secrets are
# kept (rotating a database password needs the bootstrap Job to run again).
#
# Env: GHCR_USER, GHCR_TOKEN (a token with read:packages on the bbb-k8s images).
#      TLS_CERT, TLS_KEY (optional): certificate and key for the public host. Without them the
#      *.bbb.imdt.dev wildcard is downloaded from a.bbb.imdt.dev, as bbb-docker-dev does.
set -euo pipefail
NS=${NS:-bbb}
: "${GHCR_USER:?}" "${GHCR_TOKEN:?}"

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

# TLS for the BBB nginx, refreshed every run (certificates expire).
tls_dir=$(mktemp -d); trap 'rm -rf "$tls_dir"' EXIT
if [ -n "${TLS_CERT:-}" ] && [ -n "${TLS_KEY:-}" ]; then
  cp "$TLS_CERT" "$tls_dir/tls.crt"; cp "$TLS_KEY" "$tls_dir/tls.key"
else
  curl -fsS -o "$tls_dir/tls.crt" http://a.bbb.imdt.dev/certs_new/fullchain.pem
  curl -fsS -o "$tls_dir/tls.key" http://a.bbb.imdt.dev/certs_new/privkey.pem
fi
kubectl -n "$NS" create secret tls bbb-tls --cert="$tls_dir/tls.crt" --key="$tls_dir/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

# The pull Secret is refreshed every run, so a rotated token only needs a re-run.
kubectl -n "$NS" create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io --docker-username="$GHCR_USER" --docker-password="$GHCR_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl -n "$NS" get secret bbb-db bbb-salt bbb-hasura bbb-tls ghcr-pull
