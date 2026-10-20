# Installing bbb-k8s (phase 1 skeleton)

This installs the BigBlueButton 4.1 services built so far, in dependency order: Postgres and
Redis, the database bootstrap Job, akka-apps, then the GraphQL stack (Hasura with its metadata
Job, graphql-actions, graphql-middleware), then bbb-web and the front nginx with the html5
client and the learning dashboard, the public HTTPS entry point, and the media path: LiveKit,
bbb-webrtc-sfu (LiveKit module only) and bbb-webrtc-recorder, and the recording processing with
playback (presentation format).

## Prerequisites

- `kubectl` and `helm` (v3) pointing at the cluster.
- A storage class named `do-block-storage-retain` (DigitalOcean Kubernetes ships it), or set
  `postgres.storageClass` / `redis.storageClass`.
- A token that can read the bbb-k8s packages on ghcr.io (`read:packages`).

### Node roles

Workloads are placed by node role (spec D1). Label one node per role; roles can share a node.

```bash
kubectl label node <node> bbb-k8s/role=storage
kubectl label node <node> bbb-k8s/role=media
kubectl label node <node> bbb-k8s/role=control
kubectl label node <node> bbb-k8s/role=data
```

Postgres and Hasura run on `data`; Redis, akka-apps, graphql-actions and graphql-middleware on
`control`; bbb-web, the front nginx, bbb-webrtc-recorder and the recording processing on
`storage`, where the `bbb-data` and `bbb-recorder` volumes are attached; livekit-server (host network) and bbb-webrtc-sfu on
`media`. Labels set with
`kubectl label` are lost when the managed cluster replaces a node (upgrade, pool change):
re-apply them, or set them as node pool labels in the cloud provider.

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
`--set` (for example a wrong `bootstrap.schemaSha`) silently stays in place.

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
  `properties-sha` / `site-sha` pod annotations.
- Deleting the release or the namespace deletes the load balancer; a new install gets a new
  IP and therefore a new host (a DigitalOcean reserved IP would keep it, but needs an API
  token).

## How bbb-web and the front nginx are wired

- **bbb-web** is the package's exploded war started with the `bbb-web.service` command line,
  listening on 0.0.0.0:8090. `/etc/bigbluebutton/bbb-web.properties` is rendered at start from
  the `bbb-web-config` ConfigMap plus `securitySalt` from `bbb-salt` (`bbbWeb.properties` in
  values adds lines). The conversion wrapper `/usr/share/bbb-web/run-in-systemd.sh`, called by
  that fixed path, is replaced by a plain `timeout` (no systemd in a container). Office
  documents are not converted (no LibreOffice, spec L5); PDF and images are.
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
  the cluster wiring (Redis, the SFU webhook, `rtc.node_ip`, the key file) and
  `livekit.config` from values. `livekit.nodeIP` (per cluster) must be the media node's public
  IP; a replaced node gets a new IP. The key id is `livekit.apiKey`; the secret is in
  `bbb-livekit` (`scripts/create-secrets.sh`), mounted as a key file with mode 0440.
- **bbb-webrtc-sfu** runs only its LiveKit module (`SFU_MODULES`): it answers the token
  requests of akka-apps, turns LiveKit webhooks into voice/camera/screen events and drives the
  recorder. Without the mediasoup modules it logs "Reconnecting to MCS" every 2 seconds (spec
  L11); that is expected.
- **bbb-webrtc-recorder** joins the LiveKit room of a recorded meeting as a WebRTC client and
  writes each track under `/var/lib/bbb-webrtc-recorder` (`bbb-recorder` volume). Pods cannot
  reach a node's public IP, so LiveKit runs with `use_external_ip` (STUN maps the public
  interface) and announces the media node's VPC address too, restricted per cluster with
  `livekit.config.rtc.ips.includes`; the recorder connects over UDP to that VPC address.
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
  files in `/var/log/bigbluebutton` (an emptyDir), e.g. `archive-<id>.log`,
  `presentation/process-<id>.log`: `kubectl -n bbb exec deploy/recording -c resque-worker -- ls /var/log/bigbluebutton`.
- **Playback**: the nginx image builds bbb-playback at the tag the monorepo pins and copies it
  to `/var/bigbluebutton/playback/presentation/2.3` (where the package installs it) with an init
  container; `/presentation/<id>` serves the published files from `bbb-data`.
- **Scope**: presentation only (spec L7). No caption inbox (L8). No shared notes service, so
  recordings have no notes (L6); the archive step only warns when a meeting had notes. The
  player asks for optional files that this format does not produce here (notes.html,
  polls.json, external_videos.json, webcams.mp4) and gets 404s; this likely matches a package
  install without those features, but was not compared against one.

## How shared notes and the annotation export are wired

- **bbb-shared-notes** converts a notes PDF export with `wkhtmltopdf`, installed in the image
  (a dependency of the package). The package runs it through `run-in-systemd.sh`, a transient
  systemd user unit with a timeout, memory limits and sandboxing; there is no systemd in a
  container, so `commandExecution.runnerScript` is `/usr/bin/timeout`, which takes the same
  arguments. The conversion keeps the timeout and is bounded by the pod's memory limit
  instead; the extra sandboxing of the unit (private network, read-only system) is lost.
  The same path serves the "Export to PDF" of the notes panel and the notes captured from a
  breakout room into the parent meeting.
- **bbb-export-annotations** writes the merged PDF into the presentation directory on
  `bbb-data` (`<presLocation>/pdfs/<jobId>`) and then uploads it to bbb-web. It mounts the
  volume read-write and runs as `bigbluebutton` (uid/gid 998), the owner of the directories
  bbb-web creates, like the package unit. Its jobs are the whiteboard captured from a
  breakout room and the presentation exports.

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
  name is derived from the schema sha, the metadata content sha and the image tag, so a new
  schema (bootstrap recreates `bbb_graphql`) or new metadata re-runs it.
- **graphql-middleware** reads `/etc/bigbluebutton/bbb-graphql-middleware.yml` (a ConfigMap)
  over its packaged defaults: Service names instead of loopback, and Hasura directly on
  `hasura:8085` instead of the package's nginx load balancer on 8185. Its first hook is
  bbb-web, which arrives in a later slice: until then every client connection is refused with
  close code 4403.
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
- The Job name is derived from the schema sha and the bootstrap image tag, so an upgrade that
  changes either one creates a new Job instead of patching an immutable one.
- akka-apps carries the schema sha as a pod annotation: a new schema recreates akka-apps in the
  same upgrade, after the database. **A schema change ends every meeting in progress**: do it in
  a maintenance window. A bootstrap image bump with the same schema does not restart akka-apps.

## Uninstall and cleanup

```bash
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

- akka-apps `/healthz` answers 503 until bbb-web and the GraphQL middleware exist (later
  slices); the probes are TCP only for that reason.
- One replica per service; restarting akka-apps ends the meetings it holds (spec L1, L2).
- Camera and screen sharing use the same LiveKit path but are not part of the validated set.
- The middleware panics whenever a Redis receive fails (`internal/websrv/rediscli.go`
  dereferences the nil message): an init container holds it until Redis answers at start, but
  a Redis restart still makes it crash and restart until Redis is back (observed: 3 restarts,
  then Running). Clients connected through it reconnect.
- Changing the middleware ConfigMap does not restart the middleware: restart it
  (`kubectl -n bbb rollout restart deploy/gql-middleware`).
