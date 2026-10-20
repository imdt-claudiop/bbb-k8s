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

{{- define "bbb.image" -}}
{{- if not .tag }}{{ fail (printf "%s image tag is empty: run images/build.sh %s" .name .name) }}{{ end -}}
{{ .root.Values.registry }}/{{ .name }}:{{ .tag }}
{{- end }}

{{/* Job name keyed on schema sha + bootstrap image tag: Job templates are immutable. */}}
{{- define "bbb.bootstrapJobName" -}}
bbb-bootstrap-{{ printf "%s%s" .Values.bootstrap.schemaSha .Values.bootstrap.tag | sha256sum | trunc 8 }}
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
