#!/bin/bash
# Apply the BigBlueButton metadata to Hasura and fail unless it is fully consistent.
# Env: HASURA_ENDPOINT (e.g. http://hasura:8085), HASURA_GRAPHQL_ADMIN_SECRET.
set -euo pipefail
: "${HASURA_ENDPOINT:?}" "${HASURA_GRAPHQL_ADMIN_SECRET:?}"
cd /app
args=(--skip-update-check --endpoint "$HASURA_ENDPOINT" --admin-secret "$HASURA_GRAPHQL_ADMIN_SECRET")
hasura metadata apply "${args[@]}"
# "metadata apply" only warns on inconsistencies; reload so the sources are re-read, then check.
hasura metadata reload "${args[@]}"
hasura metadata inconsistency status "${args[@]}"
echo "metadata applied and consistent"
