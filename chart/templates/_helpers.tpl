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
{{- end }}

{{/* An official image: include "bbb.officialImage" (dict "root" . "name" "web") */}}
{{- define "bbb.officialImage" -}}
{{ .root.Values.officialImages.registry }}/bbb-{{ .name }}:{{ required "officialImages.tag is empty" .root.Values.officialImages.tag }}
{{- end }}

{{/* Job name keyed on the schema sha, the bootstrap script and the Postgres image: Job templates
     are immutable. */}}
{{- define "bbb.bootstrapJobName" -}}
bbb-bootstrap-{{ printf "%s%s%s" .Values.bootstrap.schemaSha (.Files.Get "scripts/bootstrap.sh") .Values.postgres.image | sha256sum | trunc 8 }}
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
