#!/bin/bash
# The daily cleanup of a package install (bigbluebutton-config/cron.daily/bigbluebutton), on the
# volumes of this chart: bbb-data at /var/bigbluebutton (recording logs in log/) and
# bbb-recorder at /var/lib/bbb-webrtc-recorder. The FreeSWITCH, mediasoup and docker steps have
# nothing to clean here. Run by the recording-cleanup CronJob.
# Env: HISTORY, UNRECORDED_DAYS, PUBLISHED_DAYS, LOG_HISTORY (days to keep).
set -uo pipefail

history=${HISTORY:-5}
unrecorded_days=${UNRECORDED_DAYS:-14}
published_days=${PUBLISHED_DAYS:-14}
log_history=${LOG_HISTORY:-28}

data=/var/bigbluebutton
recording_dir=$data/recording
status=$recording_dir/status
log_dir=$data/log
recorder_dir=/var/lib/bbb-webrtc-recorder
types=$(cd /usr/local/bigbluebutton/core/scripts/process && find -- *.rb | sed 's/\.rb$//')
[ -n "$types" ] || types=presentation

# Recording ids are <sha1>-<epoch ms>; nothing else ever reaches an rm below.
valid_id() { [[ $1 =~ ^[0-9a-f]{40}-[0-9]{13}$ ]]; }

echo "presentations of meetings older than $history days"
find "$data/" -maxdepth 1 -type d -name "*-[0-9]*" -mtime +"$history" -print -exec rm -rf '{}' +

echo "recorder files older than $history days"
for app in recordings screenshare audio; do
  app_dir=$recorder_dir/$app
  [ -d "$app_dir" ] || continue
  find "$app_dir" \( -iname '*.mkv' -o -iname '*.webm' \) -mtime +"$history" -print -delete
  find "$app_dir" \( -iname '*.ogg' -o -iname '*.json' \) -mtime +"$history" -print -delete
  find "$app_dir" -mindepth 1 -type d -empty -mtime +"$history" -print -delete
done

echo "recording logs older than $log_history days"
if [ -d "$log_dir" ]; then
  find "$log_dir" -type f -mtime +"$log_history" -print -delete
fi

# bbb-record --delete <id>, on the paths of this stack.
delete_recording() {
  local id=$1 type
  # Status files first, so the processing does not pick the recording up halfway.
  rm -f "$status"/ended/"$id"* "$status"/recorded/"$id"* "$status"/archived/"$id"* \
    "$status"/sanity/"$id"* "$status"/processed/"$id"* "$status"/published/"$id"*
  for type in $types; do
    rm -rf "$data"/published/"$type"/"$id"* "$data"/unpublished/"$type"/"$id"* \
      "$data"/deleted/"$type"/"$id"*
    rm -rf "$recording_dir"/process/"$type"/"$id"* "$recording_dir"/publish/"$type"/"$id"*
    rm -rf "${log_dir:?}"/"$type"/*"$id"*
  done
  rm -rf "$data"/captions/"$id"*
  rm -f "$data"/inbox/"$id"*.json "$data"/inbox/"$id"*.txt
  rm -rf "$recording_dir"/raw/"$id"*
  rm -rf "${data:?}/$id"
}

echo "raw data of meetings without recording marks older than $unrecorded_days days"
find "$status" -name '*.norecord' -mtime +"$unrecorded_days" | while read -r norecord; do
  id=${norecord%.norecord}
  id=${id##*/}
  if valid_id "$id"; then
    echo "deleting $id"
    delete_recording "$id"
  else
    echo "skipping $norecord"
  fi
done

echo "raw data of published recordings older than $published_days days"
if [ -d "$recording_dir/raw" ]; then
  find "$recording_dir/raw" -mindepth 2 -maxdepth 2 -name events.xml -mtime +"$published_days" |
    while read -r events; do
      meeting=$(basename "$(dirname "$events")")
      valid_id "$meeting" || { echo "skipping $events"; continue; }
      published=true
      for type in $types; do
        [ -f "$status/published/$meeting-$type.done" ] || published=false
      done
      if $published; then
        echo "$meeting properly published, removing its raw dir"
        rm -r "${recording_dir:?}/raw/$meeting"
      else
        echo "$meeting not published in every format, keeping its raw dir"
      fi
    done
fi
