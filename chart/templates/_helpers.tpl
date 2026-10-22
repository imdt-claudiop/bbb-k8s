{{- define "bbb.labels" -}}
app.kubernetes.io/part-of: bigbluebutton
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/* selector labels: include "bbb.selector" "akka-apps" */}}
{{- define "bbb.selector" -}}
app.kubernetes.io/name: {{ . }}
{{- end }}

{{- define "bbb.nodeSelector" -}}
nodeSelector:
  {{ .root.Values.nodeRoleLabel }}: {{ .role }}
  {{- with .root.Values.cell }}
  {{ $.root.Values.cellLabel }}: {{ include "bbb.cell" $.root | quote }}
  {{- end }}
{{- include "bbb.cellTolerations" .root }}
{{- end }}

{{/* The cell's own nodes, for the pods without a role (the Jobs and helm test): the cell label
     and the toleration of its taint. Empty without values cell. */}}
{{- define "bbb.cellPlacement" -}}
{{- with .Values.cell }}
nodeSelector:
  {{ $.Values.cellLabel }}: {{ include "bbb.cell" $ | quote }}
{{- include "bbb.cellTolerations" $ }}
{{- end }}
{{- end }}

{{- define "bbb.cellTolerations" -}}
{{- with .Values.cell }}
tolerations:
  - key: {{ $.Values.cellLabel }}
    operator: Equal
    value: {{ include "bbb.cell" $ | quote }}
    effect: NoSchedule
{{- end }}
{{- end }}

{{/* values cell, checked: it becomes a label value. */}}
{{- define "bbb.cell" -}}
{{- $c := toString .Values.cell }}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$" $c) }}
{{- fail (printf "cell is %q: lowercase letters, digits and dashes, a label value" $c) }}
{{- end }}
{{- $c }}
{{- end }}

{{/* An official image: include "bbb.officialImage" (dict "root" . "name" "web") */}}
{{- define "bbb.officialImage" -}}
{{ .root.Values.officialImages.registry }}/bbb-{{ .name }}:{{ required "officialImages.tag is empty" .root.Values.officialImages.tag }}
{{- end }}

{{/* Job name keyed on everything its template carries (the schema sha, the bootstrap script, the
     Postgres image and the official image tag of its init containers): Job templates are
     immutable. A new name re-runs the bootstrap, which keeps bbb_graphql when the marker matches. */}}
{{- define "bbb.bootstrapJobName" -}}
bbb-bootstrap-{{ printf "%s%s%s%s" .Values.bootstrap.schemaSha (.Files.Get "scripts/bootstrap.sh") .Values.postgres.image .Values.officialImages.tag | sha256sum | trunc 8 }}
{{- end }}

{{- define "bbb.dbPasswordEnv" -}}
- name: {{ .env }}
  valueFrom:
    secretKeyRef:
      name: {{ .root.Values.secrets.db }}
      key: {{ .key }}
{{- end }}

{{/* initContainer: wait until bbb_graphql carries the marker of the current schema (slice 1). */}}
{{- define "bbb.waitBootstrap" -}}
- name: wait-bootstrap
  image: {{ .root.Values.postgres.image }}
  env:
    - name: PGHOST
      value: postgres
    - name: PGUSER
      value: {{ .user }}
    - name: PGDATABASE
      value: postgres
    - name: EXPECTED
      value: {{ printf "schema-sha:%s" .root.Values.bootstrap.schemaSha | quote }}
    {{- include "bbb.dbPasswordEnv" (dict "root" .root "env" "PGPASSWORD" "key" .key) | nindent 4 }}
  command:
    - sh
    - -c
    - |
      q="SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = 'bbb_graphql'"
      until [ "$(psql -tAc "$q" 2>/dev/null)" = "$EXPECTED" ]; do
        echo "waiting for bbb_graphql at $EXPECTED"; sleep 3
      done
      echo "bbb_graphql is at $EXPECTED"
{{- end }}

{{- define "bbb.publicHost" -}}
{{ required "publicHost is empty: set it per cluster (clusters/<name>.yaml, scripts/public-host.sh)" .Values.publicHost }}
{{- end }}

{{/* initContainer: wait until Redis answers. Several services exit (or panic) when their first
     Redis connection fails, and the headless Service has no DNS record until Redis is ready. A
     single answer is not enough: lookups made before Redis was ready kept failing with "no such
     host" for a few seconds afterwards (likely CoreDNS negative caching, not confirmed), so
     require answers spanning 10 seconds. */}}
{{- define "bbb.waitRedis" -}}
- name: wait-redis
  image: {{ .Values.redis.image }}
  command:
    - sh
    - -c
    - |
      ok=0
      while [ "$ok" -lt 6 ]; do
        if [ "$(redis-cli -h redis ping 2>/dev/null)" = PONG ]; then ok=$((ok + 1)); else ok=0; echo waiting for redis; fi
        sleep 2
      done
{{- end }}

{{/* Shell command creating the tree under /var/bigbluebutton (the bbb-data volume) that the
     package postinsts create (bbb-config, bbb-web, bbb-record-core and the playback formats). The
     official images create it in their own filesystem, which the mounted volume hides. */}}
{{- define "bbb.dataTree" -}}
cd /var/bigbluebutton && mkdir -p recording/raw recording/process recording/status/recorded recording/status/archived recording/status/processed recording/status/sanity recording/status/ended recording/status/published events captions/inbox unpublished deleted basic_stats diagnostics learning-dashboard published/presentation published/video published/notes published/screenshare recording/publish/presentation recording/publish/video recording/publish/notes recording/publish/screenshare
{{- end }}
