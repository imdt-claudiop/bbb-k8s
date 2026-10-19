#!/bin/bash
# Idempotent database bootstrap for BigBlueButton (spec D8).
#
# Port of the postinst of bbb-graphql-server and bbb-shared-notes-server, minus their
# unconditional DROP DATABASE. bbb_graphql is recreated only when it is missing or when
# its schema marker differs from SCHEMA_SHA; hasura_app and blocknote_app are never dropped.
#
# Required env: PGHOST, PGPASSWORD (superuser "postgres"), SCHEMA_SHA,
#   BBB_CORE_PASSWORD, BBB_HASURA_PASSWORD, HASURA_APP_PASSWORD, BLOCKNOTE_APP_PASSWORD.
set -euo pipefail

: "${PGHOST:?}" "${PGPASSWORD:?}" "${SCHEMA_SHA:?}"
: "${BBB_CORE_PASSWORD:?}" "${BBB_HASURA_PASSWORD:?}" "${HASURA_APP_PASSWORD:?}" "${BLOCKNOTE_APP_PASSWORD:?}"
export PGUSER="${PGUSER:-postgres}"

SCHEMA=/bootstrap/bbb_schema.sql
BLOCKNOTE_SCHEMA=/bootstrap/blocknote_schema.sql
MARKER="schema-sha:${SCHEMA_SHA}"

# values.bootstrap.schemaSha is the single source of truth for the marker the akka-apps
# initContainer waits for. The image's own checksum is only a guard against a values/image mismatch.
computed=$(sha256sum "$SCHEMA" | cut -d' ' -f1)
if [ "$computed" != "$SCHEMA_SHA" ]; then
  echo "schema sha mismatch: image=${computed} values=${SCHEMA_SHA}" >&2
  exit 1
fi

psql_q() { psql -v ON_ERROR_STOP=1 -q "$@"; }
# psql expands :'var' only in statements read from stdin/files, not in -c.
psql_var() { # name value sql
  local name=$1 value=$2; shift 2
  printf '%s\n' "$1" | psql_q -v "$name=$value"
}

ensure_role() { # name password
  if [ "$(psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='$1'")" != "1" ]; then
    psql_q -c "CREATE ROLE \"$1\" LOGIN"
    echo "role $1 created"
  fi
  psql_var pw "$2" "ALTER ROLE \"$1\" WITH LOGIN PASSWORD :'pw'"
}

db_exists() { [ "$(psql -tAc "SELECT 1 FROM pg_database WHERE datname='$1'")" = "1" ]; }

ensure_role bbb_core "$BBB_CORE_PASSWORD"
ensure_role bbb_hasura "$BBB_HASURA_PASSWORD"
ensure_role hasura_app "$HASURA_APP_PASSWORD"
ensure_role blocknote_app "$BLOCKNOTE_APP_PASSWORD"

# --- bbb_graphql: derived data, recreated only on a new schema ---------------------------
current=""
if db_exists bbb_graphql; then
  current=$(psql -tAc "SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname='bbb_graphql'")
fi
if [ "$current" != "$MARKER" ]; then
  echo "bbb_graphql marker is '${current:-<missing>}', expected '$MARKER': recreating"
  psql_q -c "DROP DATABASE IF EXISTS bbb_graphql WITH (FORCE)"
  psql_q -c "CREATE DATABASE bbb_graphql WITH TEMPLATE template0 LC_COLLATE 'C.UTF-8'"
  psql_q -c "ALTER DATABASE bbb_graphql SET timezone TO 'UTC'"
  psql_q -d bbb_graphql -f "$SCHEMA"
  recreated=1
else
  echo "bbb_graphql already at $MARKER: keeping it"
  recreated=0
fi

psql_q -c "GRANT CONNECT ON DATABASE bbb_graphql TO bbb_core, bbb_hasura"
psql_q -d bbb_graphql <<'SQL'
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO bbb_core;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO bbb_core;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO bbb_core;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO bbb_core;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO bbb_hasura;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO bbb_hasura;
SQL

# The marker is written last: akka-apps starts only once it matches.
if [ "$recreated" = 1 ]; then
  psql_var marker "$MARKER" "COMMENT ON DATABASE bbb_graphql IS :'marker'"
  echo "bbb_graphql recreated at $MARKER"
fi

# --- hasura_app: Hasura metadata, durable --------------------------------------------------
if ! db_exists hasura_app; then
  psql_q -c "CREATE DATABASE hasura_app OWNER hasura_app"
  echo "hasura_app created"
fi

# --- blocknote_app: shared notes content, durable -----------------------------------------
# blocknote_schema.sql is a plain CREATE TABLE, so it is loaded only when the database is new.
if ! db_exists blocknote_app; then
  psql_q -c "CREATE DATABASE blocknote_app OWNER blocknote_app"
  # Loaded as the superuser, as the original postinst does.
  psql_q -d blocknote_app -f "$BLOCKNOTE_SCHEMA"
  echo "blocknote_app created"
fi

echo "bootstrap done ($MARKER)"
