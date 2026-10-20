{{- define "bbb.labels" -}}
app.kubernetes.io/part-of: bigbluebutton
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/* Labels of pod templates. No chart version here: a chart release that changes nothing in a
     workload must not restart its pods (restarting akka-apps or bbb-web ends meetings). */}}
{{- define "bbb.podLabels" -}}
app.kubernetes.io/part-of: bigbluebutton
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* selector labels: include "bbb.selector" "akka-apps" */}}
{{- define "bbb.selector" -}}
app.kubernetes.io/name: {{ . }}
{{- end }}

{{/* Placement on a node role (spec D1): the role label, set on the node pool (docs/install.md
     "Node roles"), and a toleration of the NoSchedule taint of the same role, so a pool can be
     kept for its role by tainting it. */}}
{{- define "bbb.placement" -}}
nodeSelector:
  {{ .root.Values.nodeRoleLabel }}: {{ .role }}
tolerations:
  - key: {{ .root.Values.nodeRoleLabel }}
    operator: Equal
    value: {{ .role }}
    effect: NoSchedule
{{- end }}

{{/* The pods that mount bbb-data or bbb-recorder (spec D2). RWO volumes attach to one node, so
     the group has to share it: each pod carries this label and requires a node that already runs
     a pod with it. A pod that matches its own term may start alone, so the first one schedules
     and the others follow it. */}}
{{- define "bbb.storageGroupLabel" -}}
bbb-k8s/volume-group: bbb-data
{{- end }}

{{- define "bbb.storageAffinity" -}}
affinity:
  podAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchLabels:
            {{- include "bbb.storageGroupLabel" . | nindent 12 }}
        topologyKey: kubernetes.io/hostname
{{- end }}

{{/* Pod settings every workload shares. No service-link variables: a ClusterIP Service named
     redis would inject REDIS_PORT=tcp://..., which bbb-webrtc-sfu reads as its Redis port (the
     way LIVEKIT_PORT broke livekit-server). No API token: no pod calls the Kubernetes API. */}}
{{- define "bbb.podDefaults" -}}
enableServiceLinks: false
automountServiceAccountToken: false
{{- end }}

{{/* Pod securityContext. dict: "user" (run every container as this UID/GID) and "fsGroup",
     both optional. */}}
{{- define "bbb.podSecurityContext" -}}
securityContext:
  seccompProfile:
    type: RuntimeDefault
  {{- with .user }}
  runAsNonRoot: true
  runAsUser: {{ . }}
  runAsGroup: {{ . }}
  {{- end }}
  {{- with .fsGroup }}
  fsGroup: {{ . }}
  # Chown the volume only when its root does not carry the group yet, instead of file by file
  # on every start (the recordings only grow).
  fsGroupChangePolicy: OnRootMismatch
  {{- end }}
{{- end }}

{{/* Container securityContext (docs/install.md "Pod security"). dict: "root", optional "drop"
     (default ALL) and "add". */}}
{{- define "bbb.containerSecurity" -}}
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: {{ .root.Values.podSecurity.readOnlyRootFilesystem }}
  capabilities:
    drop: {{ .drop | default (list "ALL") | toJson }}
    {{- with .add }}
    add: {{ toJson . }}
    {{- end }}
{{- end }}

{{/* Resources of the short init containers. Requests equal limits, so they keep a Guaranteed
     pod (bbb-web) Guaranteed. */}}
{{- define "bbb.initResources" -}}
resources:
  requests: { cpu: 100m, memory: 128Mi }
  limits: { cpu: 100m, memory: 128Mi }
{{- end }}

{{/* emptyDir for a scratch path. dict: "name", "size" (sizeLimit). */}}
{{- define "bbb.scratch" -}}
- name: {{ .name }}
  emptyDir:
    sizeLimit: {{ .size }}
{{- end }}

{{- define "bbb.image" -}}
{{- if not .tag }}{{ fail (printf "%s image tag is empty: run images/build.sh %s" .name .name) }}{{ end -}}
{{ .root.Values.registry }}/{{ .name }}:{{ .tag }}
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
  {{- include "bbb.initResources" . | nindent 2 }}
  {{- include "bbb.containerSecurity" (dict "root" .root) | nindent 2 }}
{{- end }}

{{- define "bbb.publicHost" -}}
{{ required "publicHost is empty: set it per cluster (clusters/<name>.yaml, scripts/public-host.sh)" .Values.publicHost }}
{{- end }}

{{/* initContainer: wait until Redis answers. Several services exit (or panic) when their first
     Redis connection fails. The redis Service has a ClusterIP, so its name resolves before Redis
     is ready and one answer is enough. */}}
{{- define "bbb.waitRedis" -}}
- name: wait-redis
  image: {{ .Values.redis.image }}
  command:
    - sh
    - -c
    - |
      until [ "$(redis-cli -h redis ping 2>/dev/null)" = PONG ]; do
        echo waiting for redis; sleep 2
      done
  {{- include "bbb.initResources" . | nindent 2 }}
  {{- include "bbb.containerSecurity" (dict "root" .) | nindent 2 }}
{{- end }}
