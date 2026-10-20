#!/bin/bash
# Start bbb-web the way bbb-web.service does, after the setup the packages do at install time.
# Env: SHARED_SECRET (securitySalt), optional INITIAL_HEAP_SIZE / MAX_HEAP_SIZE.
set -euo pipefail
: "${SHARED_SECRET:?}"

# Directory tree the bbb-config package creates under /var/bigbluebutton (bbb-data volume).
for d in recording/raw recording/process recording/publish recording/status/recorded \
         recording/status/archived recording/status/processed recording/status/sanity \
         recording/status/ended recording/status/published events captions/inbox published \
         unpublished deleted basic_stats diagnostics learning-dashboard; do
  mkdir -p "/var/bigbluebutton/$d"
done

# /etc/bigbluebutton/bbb-web.properties = the chart's override + the secret.
{
  cat /etc/bigbluebutton-template/bbb-web.properties
  printf '\nsecuritySalt=%s\n' "$SHARED_SECRET"
} > /etc/bigbluebutton/bbb-web.properties

cd /usr/share/bbb-web
exec java -Dgrails.env=prod -Dserver.address=0.0.0.0 -Dserver.port=8090 \
  -Dspring.main.allow-circular-references=true \
  -Xms"${INITIAL_HEAP_SIZE:-384m}" -Xmx"${MAX_HEAP_SIZE:-384m}" \
  -XX:+HeapDumpOnOutOfMemoryError -XX:HeapDumpPath=/var/bigbluebutton/diagnostics \
  -cp 'WEB-INF/lib/*:/:WEB-INF/classes/:.' org.springframework.boot.loader.launch.WarLauncher
