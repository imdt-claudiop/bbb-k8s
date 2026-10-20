#!/bin/bash
# Prepare what the packages set up at install time, then run the given process
# (rap-starter or the resque workers).
# Env: SHARED_SECRET (securitySalt, for the recording-ready callback).
set -euo pipefail
: "${SHARED_SECRET:?}"
# The post-publish callback reads securitySalt from bbb-web.properties.
printf 'securitySalt=%s\n' "$SHARED_SECRET" > /etc/bigbluebutton/bbb-web.properties
# Directories the bbb-record-core postinst creates under the shared volume and the log dir.
for d in recording/raw recording/process recording/publish recording/status/recorded \
         recording/status/archived recording/status/processed recording/status/sanity \
         recording/status/ended recording/status/published events captions/inbox published \
         unpublished deleted; do
  mkdir -p "/var/bigbluebutton/$d"
done
mkdir -p /var/log/bigbluebutton/presentation
exec "$@"
