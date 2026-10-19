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
