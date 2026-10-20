# Installing bbb-k8s (phase 1 skeleton)

This installs the BigBlueButton 4.1 services built so far, in dependency order: Postgres and
Redis, the database bootstrap Job, akka-apps, then the GraphQL stack (Hasura with its metadata
Job, graphql-actions, graphql-middleware), then bbb-web and the front nginx with the html5
client and the learning dashboard, the public HTTPS entry point, and the media path: LiveKit,
bbb-webrtc-sfu (LiveKit module only) and bbb-webrtc-recorder, and the recording processing with
playback (presentation format).

## Prerequisites

- `kubectl` and `helm` (v3 or v4) pointing at the cluster.
- A storage class named `do-block-storage-retain` (DigitalOcean Kubernetes ships it), or set
  `postgres.storageClass` / `redis.storageClass`.
- A token that can read the bbb-k8s packages on ghcr.io (`read:packages`).

### Node roles

Workloads are placed by node role (spec D1), a `nodeSelector` on `bbb-k8s/role=<role>`.
Postgres, Hasura and the bootstrap and metadata Jobs run on `data`; Redis, akka-apps,
graphql-actions and graphql-middleware on `control`; bbb-web, the front nginx,
bbb-webrtc-recorder, the recording processing and its cleanup CronJob on `storage`, where the
`bbb-data` and `bbb-recorder` volumes are attached; livekit-server (host network) and
bbb-webrtc-sfu on `media`.

Set the roles as **node pool labels**, one pool per role: DOKS puts a pool's labels on every node
it creates for the pool (upgrade, repair, resize), while a label set with `kubectl label` is lost
with its node and leaves that role's pods Pending.

```bash
doctl kubernetes cluster node-pool create <cluster> --name media --size s-4vcpu-8gb --count 1 \
  --label bbb-k8s/role=media --taint bbb-k8s/role=media:NoSchedule
doctl kubernetes cluster node-pool create <cluster> --name storage --size s-4vcpu-8gb --count 1 \
  --label bbb-k8s/role=storage
# the same for control and data
```

- Every pod tolerates the `NoSchedule` taint of its own role, so tainting a pool keeps everything
  else off it. The media pool is the one worth tainting: CoreDNS, the Jobs or a monitoring stack
  then never compete with LiveKit for CPU.
- `doctl kubernetes cluster node-pool update` replaces the whole label and taint set of a pool:
  pass all of them every time.
- Roles can share a node: point several roles at one label value in the values (for example
  `postgres.role: control`).
- On a cluster without pools per role, `kubectl label node <node> bbb-k8s/role=<role>` works,
  but has to be repeated whenever DOKS replaces the node.

The pods of `storage` follow the two RWO volumes to one node by pod affinity (label
`bbb-k8s/volume-group: bbb-data`), not only by the node label: a second `storage` node does not
split them across nodes, where the late ones would hang on a Multi-Attach error.

## Install

The public host is derived from the IP of the LoadBalancer, which only exists once the chart
created it, so the first install has two steps:

```bash
GHCR_USER=<user> GHCR_TOKEN=<token> scripts/create-secrets.sh   # namespace bbb + Secrets
# 1. any host to get the LoadBalancer created
helm upgrade --install bbb chart -n bbb --reset-values --set publicHost=pending.bbb.imdt.dev
scripts/public-host.sh            # waits for the IP, checks public DNS, prints publicHost=...
# 2. write that host in clusters/<cluster>.yaml, then from now on:
helm upgrade --install bbb chart -n bbb --reset-values -f clusters/<cluster>.yaml
kubectl -n bbb get pods -w
```

`publicHost` has no default: the chart refuses to render without it.

`create-secrets.sh` generates the database passwords (`bbb-db`), the shared secret
(`bbb-salt`) and the Hasura admin secret (`bbb-hasura`) once and keeps them on later runs; the
pull Secret (`ghcr-pull`) and the TLS Secret (`bbb-tls`) are refreshed every run. Without
`TLS_CERT`/`TLS_KEY` the TLS Secret holds the `*.bbb.imdt.dev` wildcard, downloaded from
`a.bbb.imdt.dev` as bbb-docker-dev does (over plain HTTP, as there). Nothing secret lives in this repository.

Expected order: `postgres-0` and `redis-0` start, the `bbb-bootstrap-<hash>` Job creates the
roles and databases, and `akka-apps` stays in `Init` (container `wait-bootstrap`) until
`bbb_graphql` carries the marker of the current schema, then starts. `hasura` waits for the
same marker, the `hasura-metadata-<hash>` Job waits for Hasura and applies the metadata, and
`gql-actions` / `gql-middleware` start right away (they only need Redis and Hasura at request
time).

**Always pass `--reset-values` (or your own `-f values.yaml`) to `helm upgrade`.** A plain
`helm upgrade` with no values flags reuses the values of the previous release, so a one-off
`--set` (for example a wrong `bootstrap.schemaSha`) silently stays in place. `values.schema.json`
rejects unknown keys, so a misspelled key in a cluster file fails the render instead of being
ignored.

After the first install, keep the load balancer across reinstalls ("Keeping the public IP"
below) and every later install is a single `helm upgrade --install`.

### Checking an install

```bash
helm test bbb -n bbb
kubectl -n bbb logs api-smoke     # the steps, ok or FAIL
```

The test pod calls the API the way an integration does, through the public host and signed with
the shared secret: `create`, `join`, `getMeetingInfo`, `getRecordings` and `end` must answer
SUCCESS, and the html5 client and LiveKit signaling (`/livekit/`) must answer through the front
nginx (`chart/scripts/api-smoke.sh`). A failed pod stays for its log; the next run replaces it.

### Upgrading a release installed with chart 0.1.0

- `svc/redis` and `svc/postgres` were headless and now have a ClusterIP, which Kubernetes does
  not change in place. Delete both right before the upgrade, which recreates them seconds later
  (open connections keep working meanwhile): `kubectl -n bbb delete service redis postgres`
- Every pod template changes once (labels, security settings, probes), so every pod restarts:
  meetings in progress end. Do it in a maintenance window.
- The bootstrap and metadata Jobs get new names and run once more; with an unchanged schema they
  only re-assert roles, grants and the metadata.

## Public entry point

Every client and API URL derives from `publicHost` (`bigbluebutton.web.serverURL`), and the
client always uses `https`/`wss` on port 443 of the page's host.

- `svc/bbb-nginx` is a `LoadBalancer`: the DigitalOcean cloud controller creates a load
  balancer in TCP mode (`nginx.service.annotations`) and the BBB nginx terminates TLS itself
  with the `bbb-tls` Secret, as on a package install. There is no Ingress controller:
  ingress-nginx is archived upstream, and an L7 layer in front would duplicate TLS and add
  WebSocket and upload timeouts to tune. The Cilium Gateway API of the cluster is the natural
  L7 entry for a later multi-cell router.
- `externalTrafficPolicy: Local` keeps the client IP, so the nginx access log shows real
  clients and the NetworkPolicy `bbb-nginx-public` admits them with an `ipBlock`. The node
  IPs and NodePorts stay closed from outside.
- `<prefix>-ip-<IP with dashes>.bbb.imdt.dev` resolves to that IP on public DNS, and the
  `*.bbb.imdt.dev` wildcard covers it. `scripts/public-host.sh` prints the name and checks the
  resolution.
- bbb-web downloads `https://<publicHost>/default.pdf` when a meeting is created. Cilium
  resolves a pod's connection to the load balancer IP inside the cluster, so the request
  reaches nginx from the bbb-web pod IP (allowed by the `bbb-web` egress rule).
- Changing `publicHost` restarts bbb-web (meetings in progress end) and nginx, through the
  `config-sha` / `site-sha` pod annotations.
- Deleting the release or the namespace deletes the load balancer, unless it was kept: a new
  load balancer gets a new IP and therefore a new host.
- A renewed certificate is served once nginx restarts: `scripts/create-secrets.sh` restarts it
  when the certificate in `bbb-tls` changed (meetings survive an nginx restart; clients
  reconnect).

### Keeping the public IP

The load balancer, its IP and with them the public host live as long as `svc/bbb-nginx`. To
reinstall from scratch and keep the host:

1. Before deleting the release or the namespace, run `scripts/keep-loadbalancer.sh`. It marks the
   load balancer as disowned (`service.kubernetes.io/do-loadbalancer-disown: "true"`), so the
   DigitalOcean cloud controller stops changing it and does not delete it with the Service, and
   prints its UUID.
2. Add that UUID to the cluster file, as the annotation
   `kubernetes.digitalocean.com/load-balancer-id` under `nginx.service.annotations`. The Service
   of the next install takes that load balancer over: same IP, same `publicHost`, one
   `helm upgrade --install`.

No API token is involved; the cluster's own cloud controller does the work. A disowned load
balancer stops following node changes, so reinstall soon after, and delete it in the
DigitalOcean panel when it is no longer needed. For `helm uninstall` / `helm install` cycles that
keep the namespace, `nginx.service.keep: true` (`helm.sh/resource-policy: keep`) leaves the
Service, and so the load balancer, in place.

## How bbb-web and the front nginx are wired

- **bbb-web** is the package's exploded war started with the `bbb-web.service` command line,
  listening on 0.0.0.0:8090. `/etc/bigbluebutton/bbb-web.properties` is rendered at start from
  the `bbb-web-config` ConfigMap plus `securitySalt` from `bbb-salt` (`bbbWeb.properties` in
  values adds lines). The conversion wrapper `/usr/share/bbb-web/run-in-systemd.sh`, called by
  that fixed path, keeps what the package's transient systemd unit does without systemd: the
  timeout (plus SIGKILL 10 s later), umask 0022, and the 640M memory cap as an address-space
  limit (`bbbWeb.conversionMemoryMax`). The cap matters more in a pod: on cgroup v2 the kubelet
  kills every process of a container that passes its memory limit, so an uncapped conversion
  would take the JVM, and every meeting, down with it. Office documents are not converted (no
  LibreOffice, spec L5); PDF and images are.
- bbb-web logs to stdout only (the war's `logback.xml` without its file appender, from the
  ConfigMap, through `JAVA_OPTS`), and runs with requests equal to limits (Guaranteed QoS): on
  the storage node it is the last pod the kubelet evicts and the kernel kills when the recording
  worker's ffmpeg takes memory. Its heap is `bbbWeb.heap`; the rest of the limit is for the
  conversion tools.
- **nginx** serves the html5 client and the learning dashboard built from the pinned commit,
  the default assets of bigbluebutton-config, and the package nginx fragments with their
  loopback upstreams rewritten to `bbb-web:8090`, `gql-middleware:8378` and `hasura:8085`. The
  dashboard app is copied into `/var/bigbluebutton/learning-dashboard` by an init container.
  Its ingress is the public entry point below.

### API-Mate

The front nginx also serves **API-Mate** at `https://<publicHost>/api-mate/`, a small static page
to build and sign BigBlueButton API calls by hand. It is the upstream
[mconf/api-mate](https://github.com/mconf/api-mate) bundle (gh-pages, pinned to commit
`d058fb77e11113c39ae84b2e9c6831a794cf9bbe` and fetched at image build time); it is not a BBB package
(`bbb-conf` only links to `mconf.github.io/api-mate`). Its serving fragment uses a trailing-slash
`location /api-mate/` + `alias` on purpose, to avoid the nginx off-by-slash alias traversal.

Build a ready-to-use link by filling the server and the shared secret in the URL fragment:

```
https://<publicHost>/api-mate/#server=https://<publicHost>/bigbluebutton/&sharedSecret=<secret>
# secret:
kubectl -n bbb get secret bbb-salt -o jsonpath='{.data.security-salt}' | base64 -d
```

The shared secret stays in the browser URL **fragment** (after `#`), so it is never sent to the
server; api-mate reads it client-side to sign the calls.

## Media: LiveKit

- **livekit-server** runs the upstream image of the version the `bbb-livekit` package builds
  (v1.9.12), with host networking on the `media` node so WebRTC reaches the node's public IP
  directly. Its config is the package's `livekit.yaml` (`chart/files/livekit.yaml`) merged with
  the cluster wiring (Redis, the SFU webhook, the key file, `prometheus_port`) and
  `livekit.config` from values. There is no `rtc.node_ip`: with `use_external_ip` LiveKit
  replaces it with what STUN reports. The key id is `livekit.apiKey`; the secret is in
  `bbb-livekit` (`scripts/create-secrets.sh`), mounted as a key file with mode 0440. The
  container runs as root only to bind port 443 of the host and keeps no capability but
  `NET_BIND_SERVICE`; it has no CPU limit (the media node is its own, and throttling at a limit
  delays live media).
- **Announced addresses**: when a cluster restricts them (`livekit.config.rtc.ips.includes`), the
  media node's own public IP has to be in the list. The init container `config` adds it at pod
  start: `livekit.nodeIP` if set, otherwise the droplet metadata service (`169.254.169.254`,
  reachable on the host network). A replaced media node therefore needs no change in the
  values; if neither source answers, the pod stays in `Init` with a message saying so.
- **bbb-webrtc-sfu** runs only its LiveKit module (`SFU_MODULES`): it answers the token
  requests of akka-apps, turns LiveKit webhooks into voice/camera/screen events and drives the
  recorder. Without the mediasoup modules it logs "Reconnecting to MCS" every 2 seconds (spec
  L11); that is expected.
- **bbb-webrtc-recorder** joins the LiveKit room of a recorded meeting as a WebRTC client and
  writes each track under `/var/lib/bbb-webrtc-recorder` (`bbb-recorder` volume). Pods cannot
  reach a node's public IP, so LiveKit runs with `use_external_ip` (STUN maps the public
  interface) and announces the media node's VPC address too, restricted per cluster with
  `livekit.config.rtc.ips.includes` (the VPC range; the public IP is added at start); the
  recorder connects over UDP to that VPC address.
- Images: the SFU and the recorder are built from their upstream repositories at the tags the
  monorepo placeholders pin, with their own Dockerfiles (`images/webrtc-*/upstream`).

### What the DOKS firewall lets through

Measured from outside with a listener on the media node: the DigitalOcean firewall of the
workers admits only TCP 80 and 443 that nobody asked for; every other TCP port and all
unsolicited UDP is dropped. WebRTC still works over UDP for most clients, because ICE opens the
path from both sides and the firewall is stateful (two browsers on the internet connected over
UDP to the media node, ports in 16384-32768). For clients whose UDP cannot get through (a
network that blocks UDP, some NATs), LiveKit also accepts media over **TCP on port 443** of the
media node (`livekit.config.rtc.tcp_port`, `allow_tcp_fallback`), which is open: a browser with
all UDP blocked joined audio that way.

To make UDP reachable without relying on ICE from both sides (better for strict NATs and the
recommended production setup), someone with access to the DigitalOcean account should add an
inbound rule for **UDP 16384-32768** (and **TCP 7881** if LiveKit's default TCP port is ever
used) to the firewall that covers the worker droplets, or to a new firewall applied to the node
pool's tag.

### NetworkPolicy and the host network

livekit-server carries the node identity, so plain NetworkPolicies cannot select it: the chart
adds CiliumNetworkPolicies naming the node entities on LiveKit's ports (nginx, the SFU and the
recorder to 7880; LiveKit to Redis and to the SFU webhook; the recorder to the media ports).
Policies do not protect a host-network pod itself: any pod in the cluster can open LiveKit's
port 7880 (its API still needs a token signed with the secret). From the internet 7880 is
filtered.

## Outbound calls

The namespace is default-deny in both directions. Besides the flows between the services,
bbb-web and the recording workers may open TCP 80 and 443 to the internet
(`externalEgress`), for what an API caller asks of them: presentations passed by URL to `create`
and `insertDocument`, `meta_endCallbackUrl`, and the recording-ready callback
(`post_publish_recording_ready_callback.rb`). The private, CGNAT and link-local ranges are
excluded, so such a URL cannot reach the VPC, the pods or the droplet metadata service. Add the
port of an integration that listens elsewhere to `externalEgress.ports`.

## Recording and playback

- **Pipeline**: bbb-web writes `recording/status/recorded/<id>.done` on `bbb-data` when a
  recorded meeting ends; `rap-starter` sees it (inotify, same filesystem) and enqueues the
  archive job in Redis; the resque workers run archive (events from Redis, audio from the
  recorder volume, which archive then deletes), sanity, captions, process and publish of the
  presentation format into `/var/bigbluebutton/published/presentation/<id>`. getRecordings
  (bbb-web) reads the published metadata.
- **One pod, two containers** (`recording`): `rap-starter` and `resque-worker`, from the
  `recording` image (record-and-playback core + presentation scripts of the pinned commit on
  Ubuntu 24.04, mkclean built from source). The config is the package's `bigbluebutton.yml`
  (`chart/files/bigbluebutton.yml`) merged with Redis, `playback_host` = `publicHost` and the
  notes endpoint; it is mounted over the scripts' file and as the `recording.yml` override,
  because several scripts read the scripts' file directly.
- **Logs**: the worker and rap-starter log to stdout (the package logs to journald, which a
  container does not have: the image swaps that one line). The stage scripts keep their own
  files in `/var/log/bigbluebutton`, which is `log/` on `bbb-data`, so the log of a failed
  recording survives a pod restart, e.g. `archive-<id>.log`, `presentation/process-<id>.log`:
  `kubectl -n bbb exec deploy/recording -c resque-worker -- ls /var/log/bigbluebutton`.
- **Cleanup**: the CronJob `recording-cleanup` runs the steps of the package's daily cron
  (`bigbluebutton-config/cron.daily/bigbluebutton`) that apply here, with its defaults
  (`recording.cleanup`): presentations of past meetings and recorder files after 5 days, raw
  data of meetings without recording marks and of published recordings after 14, recording logs
  after 28. It runs next to the volumes (`chart/scripts/recording-cleanup.sh`); a run by hand:
  `kubectl -n bbb create job --from=cronjob/recording-cleanup cleanup-now`.
- **Playback**: the nginx image builds bbb-playback at the tag the monorepo pins and copies it
  to `/var/bigbluebutton/playback/presentation/2.3` (where the package installs it) with an init
  container; `/presentation/<id>` serves the published files from `bbb-data`.
- **Scope**: presentation only (spec L7). No caption inbox (L8). No shared notes service, so
  recordings have no notes (L6); the archive step only warns when a meeting had notes. The
  player asks for optional files that this format does not produce here (notes.html,
  polls.json, external_videos.json, webcams.mp4) and gets 404s; this likely matches a package
  install without those features, but was not compared against one.

## How the GraphQL stack is wired

- **Hasura** runs the Community Edition engine the `bbb-graphql-server` package installs (the
  binary published in `iMDT/hasura-graphql-engine`, checksum-pinned in `images/hasura`); the
  official `hasura/graphql-engine` image, which ships the Pro Edition binary, only provides the
  runtime libraries. Tuning comes from the package's `hasura-config.env` (`hasura.env` in values).
  The auth hook is `akka-apps:8901/userInfo`, the action handler `gql-actions:8093`.
- **Metadata**: `images/hasura-metadata` carries `bbb-graphql-server/metadata` of the pinned
  commit, with the BigBlueButton source rewritten at build time to read its URL from
  `HASURA_GRAPHQL_BBB_DATABASE_URL` (upstream it is a literal with `127.0.0.1` and a password).
  The Job applies it, reloads, and fails unless Hasura reports the metadata as consistent. Its
  name is the hash of its rendered pod template (schema sha, metadata content sha and image tag
  included), so a new schema (bootstrap recreates `bbb_graphql`) or new metadata re-runs it.
- **graphql-middleware** reads `/etc/bigbluebutton/bbb-graphql-middleware.yml` (a ConfigMap)
  over its packaged defaults: Service names instead of loopback, and Hasura directly on
  `hasura:8085` instead of the package's nginx load balancer on 8185. A change to that
  ConfigMap restarts the middleware (`config-sha` annotation); clients reconnect.
- **graphql-actions** turns client mutations into Redis messages and trusts the session
  variables in the request, so only Hasura and the middleware may reach it.

## How the database bootstrap behaves

`images/bootstrap/bootstrap.sh` replaces the postinst of `bbb-graphql-server` and
`bbb-shared-notes-server`, which drop the databases on every install.

- `bootstrap.schemaSha` in `chart/values.yaml` is written by `images/build.sh` and is the only
  source of the schema marker. The Job refuses to run (`schema sha mismatch: image=...
  values=...`) if it differs from the sha256 of the `bbb_schema.sql` inside the image; the
  database is not touched in that case.
- `bbb_graphql` is dropped and recreated only when it is missing or its marker
  (`COMMENT ON DATABASE`, `schema-sha:<sha>`) differs. Otherwise the Job only re-asserts roles,
  passwords and grants. The marker is written last.
- `hasura_app` and `blocknote_app` are created when missing and never dropped;
  `blocknote_schema.sql` is loaded only when `blocknote_app` is created.
- The Job name is the hash of its rendered pod template (schema sha and bootstrap image tag
  included), so an upgrade that changes anything in it creates a new Job instead of patching an
  immutable one. A retry starts only once the previous pod is gone (`podReplacementPolicy:
  Failed`), so two bootstraps never run at the same time.
- akka-apps carries the schema sha as a pod annotation: a new schema recreates akka-apps in the
  same upgrade, after the database. **A schema change ends every meeting in progress**: do it in
  a maintenance window. A bootstrap image bump with the same schema does not restart akka-apps.

## Metrics

The chart exposes what the services publish; the collector is installed once per cluster, for
example kube-prometheus-stack in the namespace `monitoring` (`metrics.namespace`):

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install kube-prometheus-stack prometheus-community/kube-prometheus-stack -n monitoring \
  --create-namespace --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false
helm upgrade --install bbb chart -n bbb --reset-values -f clusters/<cluster>.yaml   # PodMonitors
```

- With the Prometheus Operator CRDs present, the chart adds PodMonitors for livekit-server
  (`prometheus_port` 6789 on the media node, `livekit.prometheusPort`) and graphql-middleware
  (`/metrics` on 8378), and a NetworkPolicy lets the `monitoring` namespace scrape the
  middleware. A Prometheus that selects PodMonitors by label needs `metrics.podMonitorLabels`.
- The kubelet metrics in that stack answer the questions a load test asks: CPU throttling
  (`container_cpu_cfs_throttled_periods_total`), OOM kills and restarts per container, node
  memory and disk pressure.
- The in-process ceilings are values too, so a limit hit shows up on the right component:
  `akkaApps.heap` (the package's 256m, passed as `-J-Xmx`), `bbbWeb.heap` and
  `redis.maxmemory` (below the container limit, `noeviction`).

## Pod security

- No pod mounts a ServiceAccount token or gets service-link variables; every pod uses the
  runtime's default seccomp profile, and every container runs without privilege escalation and
  drops its capabilities. Postgres and Redis keep the default capabilities minus `NET_RAW` (their
  entrypoints chown the data directory as root, then drop to their user); livekit-server keeps
  only `NET_BIND_SERVICE`.
- The namespace carries `pod-security.kubernetes.io/warn` and `audit` at `restricted`
  (`scripts/create-secrets.sh`): every apply lists what still does not fit (LiveKit's host
  network, the root entrypoints) without blocking it.
- `podSecurity.readOnlyRootFilesystem: true` makes every root filesystem read-only. The chart
  already mounts the scratch directories it knows of (`/tmp`, nginx's cache, the config and home
  directories some services write), so what still fails names a path a service writes and loses
  on every restart. It is off by default and was not run on the cluster yet.

## Uninstall and cleanup

```bash
scripts/keep-loadbalancer.sh      # optional: keep the load balancer, its IP and the host
helm uninstall bbb -n bbb
kubectl delete namespace bbb
```

The storage class keeps the volumes (`Retain`): the PersistentVolumes stay `Released` and the
cloud volumes are still billed. Reinstalling creates new volumes and does not reuse them.
When the data is no longer needed, switch them to `Delete` so the CSI driver removes the cloud
volumes together with the PVs:

```bash
kubectl get pv | grep Released
kubectl get pv --no-headers | awk '$5=="Released" && $6 ~ /^bbb\// {print $1}' \
  | xargs -r -I{} kubectl patch pv {} -p '{"spec":{"persistentVolumeReclaimPolicy":"Delete"}}'
```

Then confirm in the DigitalOcean panel or with `doctl compute volume list` that the volumes are gone; this step was exercised only up to the removal of the PersistentVolumes.

## Known limitations of this slice

- akka-apps `/healthz` answers 503 in its first minute and whenever bbb-web is away, so its
  probes stay TCP: as liveness a hiccup would end meetings, and as readiness it would take the
  Hasura auth hook down too. bbb-web and akka-apps get a startup probe instead of a fixed
  initial delay.
- One replica per service; restarting akka-apps ends the meetings it holds (spec L1, L2).
- Camera and screen sharing use the same LiveKit path but are not part of the validated set.
- The middleware panics whenever a Redis receive fails (`internal/websrv/rediscli.go`
  dereferences the nil message): an init container holds it until Redis answers at start, but
  a Redis restart still makes it crash and restart until Redis is back (observed: 3 restarts,
  then Running). Clients connected through it reconnect.
