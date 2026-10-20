#!/bin/bash
# helm test: the BigBlueButton API through the public host, signed the way an integration signs
# it, plus the html5 client and LiveKit signaling behind the front nginx. Run by the api-smoke
# test Pod. Env: PUBLIC_HOST, SHARED_SECRET.
set -euo pipefail
: "${PUBLIC_HOST:?}" "${SHARED_SECRET:?}"

base="https://$PUBLIC_HOST/bigbluebutton/api"
id="helm-test-$(date +%s)"

# call <name> <query>: a signed API call (checksum: sha256 of name, query and secret).
call() {
  local sum
  sum=$(printf '%s%s%s' "$1" "$2" "$SHARED_SECRET" | sha256sum | cut -d' ' -f1)
  curl -fsS --max-time 30 "$base/$1?$2&checksum=$sum" || true
}

# expect <step> <body>: the body is a SUCCESS response.
expect() {
  if grep -q '<returncode>SUCCESS</returncode>' <<<"$2"; then
    echo "ok   $1"
  else
    echo "FAIL $1: $2"
    exit 1
  fi
}

# reachable <step> <url>: the URL answers 2xx.
reachable() {
  if curl -fsS --max-time 30 -o /dev/null "$2"; then
    echo "ok   $1"
  else
    echo "FAIL $1: $2"
    exit 1
  fi
}

expect "api" "$(curl -fsS --max-time 30 "$base" || true)"
expect "create" "$(call create "name=helm-test&meetingID=$id&record=false")"
expect "join" "$(call join "fullName=helm-test&meetingID=$id&role=MODERATOR&redirect=false")"
expect "getMeetingInfo" "$(call getMeetingInfo "meetingID=$id")"
expect "getRecordings" "$(call getRecordings "meetingID=$id")"
expect "end" "$(call end "meetingID=$id")"
reachable "html5 client" "https://$PUBLIC_HOST/html5client/"
reachable "livekit signaling" "https://$PUBLIC_HOST/livekit/"
