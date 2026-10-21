#!/bin/bash
# Copy into the chart the upstream files that must match the official images it runs, from the
# monorepo commit those images were built from (values bbb.sourceSha):
#   chart/files/settings.yml      bigbluebutton-html5/private/config/settings.yml, with the client
#                                 build id stamped as the html5 and akka-apps images stamp it
#   chart/files/bigbluebutton.yml record-and-playback/core/scripts/bigbluebutton.yml
#   chart/files/livekit.yaml      build/packages-template/bbb-livekit/livekit.yaml
#   chart/files/nginx/*.nginx     the location files of the official bbb-nginx image, with the
#                                 loopback upstreams of a single host replaced by this chart's
#                                 Service names (the image's own files point at 127.0.0.1)
#   values bootstrap.schemaSha    sha256 of bbb-graphql-server/bbb_schema.sql
#
# Usage: BBB_REPO=/path/to/bigbluebutton scripts/sync-upstream.sh [<commit>]
#   With a commit, it is written to bbb.sourceSha first. Move officialImages.tag to the same
#   commit (<branch>-<sha7>) in the same change.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
VALUES="$ROOT/chart/values.yaml"
: "${BBB_REPO:?set BBB_REPO to a bigbluebutton clone}"

# set_value <section> <key> <value>: rewrite "  key: ..." inside the top-level "section:" block.
set_value() {
  awk -v s="$1:" -v k="$2" -v v="$3" '
    /^[^ #]/ { insec = ($1 == s) }
    insec && $1 == k":" { sub(/:.*/, ": \"" v "\""); }
    { print }' "$VALUES" > "$VALUES.tmp" && mv "$VALUES.tmp" "$VALUES"
}

if [ $# -gt 0 ]; then
  set_value bbb sourceSha "$(git -C "$BBB_REPO" rev-parse --verify "$1^{commit}")"
fi
SHA=$(sed -n 's/^  sourceSha: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$VALUES" | head -1)
git -C "$BBB_REPO" cat-file -e "$SHA^{commit}"
show() { git -C "$BBB_REPO" show "$SHA:$1"; }

# Same id as CLIENT_BUILD in .github/workflows/build-all-images.yml (build/setup.sh COMMIT_DATE).
build=$(git -C "$BBB_REPO" log -n1 --pretty=format:%cd --date=format:%Y%m%dT%H%M%S "$SHA")
show bigbluebutton-html5/private/config/settings.yml \
  | sed "s/HTML5_CLIENT_VERSION/$build/" > "$ROOT/chart/files/settings.yml"
show record-and-playback/core/scripts/bigbluebutton.yml > "$ROOT/chart/files/bigbluebutton.yml"
show build/packages-template/bbb-livekit/livekit.yaml > "$ROOT/chart/files/livekit.yaml"
set_value bootstrap schemaSha "$(show bbb-graphql-server/bbb_schema.sql | sha256sum | cut -d' ' -f1)"

# The location files of docker/nginx/Dockerfile (bbb-nginx), plus webhooks.nginx, which that
# image does not ship. api-mate.nginx is this chart's own file and is left alone.
out="$ROOT/chart/files/nginx"
mkdir -p "$out"
find "$out" -name '*.nginx' ! -name api-mate.nginx -delete
while read -r src dst; do
  show "$src" > "$out/$dst"
done <<'EOF'
bigbluebutton-web/bbb-web.nginx web.nginx
bigbluebutton-web/loadbalancer.nginx loadbalancer.nginx
bigbluebutton-web/nginx-confs/presentation-slides.nginx presentation-slides.nginx
build/packages-template/bbb-config/plugins-assets-cors.nginx plugins-assets-cors.nginx
build/packages-template/bbb-graphql-middleware/graphql.nginx graphql.nginx
build/packages-template/bbb-livekit/livekit.nginx livekit.nginx
build/packages-template/bbb-shared-notes-server/bbb-shared-notes-server.nginx bbb-shared-notes-server.nginx
build/packages-template/bbb-webrtc-sfu/webrtc-sfu.nginx webrtc-sfu.nginx
build/packages-template/bbb-webhooks/webhooks.nginx webhooks.nginx
record-and-playback/notes/scripts/notes-playback.nginx notes-playback.nginx
record-and-playback/presentation/scripts/presentation.nginx presentation.nginx
record-and-playback/screenshare/scripts/recording-screenshare.nginx recording-screenshare.nginx
record-and-playback/video/scripts/playback-video.nginx playback-video.nginx
docker/nginx/bbb-html5.nginx bbb-html5.nginx
docker/nginx/learning-dashboard.nginx learning-dashboard.nginx
docker/nginx/playback.nginx playback.nginx
EOF
# The 8185 Hasura balancer of the package becomes the GraphQL server's Service.
sed -i -e 's|http://127.0.0.1:8090|http://bbb-web:8090|g' \
       -e 's|http://127.0.0.1:8378|http://gql-middleware:8378|g' \
       -e 's|http://127.0.0.1:8185|http://hasura:8085|g' \
       -e 's|http://127.0.0.1:7880|http://livekit:7880|g' \
       -e 's|http://127.0.0.1:3008|http://bbb-webrtc-sfu:3008|g' \
       -e 's|http://localhost:8787|http://bbb-shared-notes:8787|g' \
       -e 's|http://127.0.0.1:3005|http://bbb-webhooks:3005|g' "$out"/*.nginx
if grep -n '^[^#]*\(127\.0\.0\.1\|localhost\)' "$out"/*.nginx; then
  echo "a location file still points at the local host (above): add its rewrite" >&2
  exit 1
fi
echo ">> chart files synced from $SHA (client build $build)"
