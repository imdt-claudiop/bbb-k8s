#!/bin/bash
# Build and push the bbb-k8s images, then pin them in chart/values.yaml.
#
# Usage: images/build.sh <component>...
#   components: akka-apps bootstrap hasura hasura-metadata graphql-middleware graphql-actions
#               bbb-web nginx
#
# Env:
#   BBB_REPO   path to a bigbluebutton monorepo clone (required unless SRC_DIR is set)
#   BBB_SHA    monorepo commit to build (default: bbb.sourceSha in chart/values.yaml)
#   SRC_DIR    use this directory as the source tree instead of exporting BBB_SHA from BBB_REPO
#   TAG        image tag (default: <line>-<sha8>-<UTC timestamp>)
#   REGISTRY   default ghcr.io/imdt-claudiop/bbb-k8s
#   PUSH=0     build only, do not push nor touch values.yaml
#
# This script is the only writer of bootstrap.schemaSha: it is the sha256 of the
# bbb_schema.sql copied into the bootstrap image it just built.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
VALUES="$ROOT/chart/values.yaml"
REGISTRY=${REGISTRY:-ghcr.io/imdt-claudiop/bbb-k8s}
PUSH=${PUSH:-1}
LINE=$(sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$ROOT/chart/Chart.yaml")

BBB_SHA=${BBB_SHA:-$(sed -n 's/^  sourceSha: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$VALUES" | head -1)}
TAG=${TAG:-$LINE-${BBB_SHA:0:8}-$(date -u +%Y%m%d%H%M%S)}

[ $# -gt 0 ] || { echo "usage: $0 <component>... (see the header)" >&2; exit 2; }

if [ -z "${SRC_DIR:-}" ]; then
  : "${BBB_REPO:?set BBB_REPO to a bigbluebutton clone, or SRC_DIR}"
  SRC_DIR=$(mktemp -d)
  trap 'rm -rf "$SRC_DIR"' EXIT
  git -C "$BBB_REPO" archive "$BBB_SHA" \
    bbb-common-message akka-bbb-apps bbb-graphql-server bbb-shared-notes-server \
    bbb-graphql-middleware bbb-graphql-actions \
    bbb-common-web bigbluebutton-web bigbluebutton-html5 bbb-learning-dashboard bigbluebutton-config \
    build/packages-template \
    bigbluebutton-html5/private/config/settings.yml | tar -x -C "$SRC_DIR"
fi

# set_value <section> <key> <value>: rewrite "  key: ..." inside the top-level "section:" block.
set_value() {
  awk -v s="$1:" -v k="$2" -v v="$3" '
    /^[^ #]/ { insec = ($1 == s) }
    insec && $1 == k":" { sub(/:.*/, ": \"" v "\""); }
    { print }' "$VALUES" > "$VALUES.tmp" && mv "$VALUES.tmp" "$VALUES"
}

for c in "$@"; do
  img="$REGISTRY/$c:$TAG"
  echo ">> building $img from $BBB_SHA"
  docker build --build-context src="$SRC_DIR" --build-arg SOURCE_SHA="$BBB_SHA" \
    -t "$img" "$ROOT/images/$c"
  [ "$PUSH" = 1 ] || continue
  docker push "$img"
  case $c in
    akka-apps)
      set_value akkaApps tag "$TAG"
      # The client settings ConfigMap ships the same settings.yml as the pinned source.
      cp "$SRC_DIR/bigbluebutton-html5/private/config/settings.yml" "$ROOT/chart/files/settings.yml"
      ;;
    bootstrap)
      set_value bootstrap tag "$TAG"
      set_value bootstrap schemaSha "$(sha256sum "$SRC_DIR/bbb-graphql-server/bbb_schema.sql" | cut -d' ' -f1)"
      ;;
    hasura) set_value hasura tag "$TAG" ;;
    hasura-metadata)
      set_value hasuraMetadata tag "$TAG"
      # Content hash of what the image applies: a change re-runs the metadata Job.
      set_value hasuraMetadata metadataSha "$(cd "$SRC_DIR/bbb-graphql-server" \
        && find metadata config.yaml -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1)"
      ;;
    graphql-middleware) set_value graphqlMiddleware tag "$TAG" ;;
    graphql-actions) set_value graphqlActions tag "$TAG" ;;
    bbb-web) set_value bbbWeb tag "$TAG" ;;
    nginx) set_value nginx tag "$TAG" ;;
  esac
done
if [ "$PUSH" = 1 ]; then echo ">> chart/values.yaml updated (tag $TAG)"; fi
