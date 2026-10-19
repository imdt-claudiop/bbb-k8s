#!/bin/bash
# Create the namespace and the Secrets the chart references. Idempotent: existing Secrets are
# kept (rotating a database password needs the bootstrap Job to run again).
#
# Env: GHCR_USER, GHCR_TOKEN (a token with read:packages on the bbb-k8s images).
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

# The pull Secret is refreshed every run, so a rotated token only needs a re-run.
kubectl -n "$NS" create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io --docker-username="$GHCR_USER" --docker-password="$GHCR_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl -n "$NS" get secret bbb-db bbb-salt ghcr-pull
