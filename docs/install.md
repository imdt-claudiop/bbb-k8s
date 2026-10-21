# Installing bbb-k8s (phase 1 skeleton)

This installs the BigBlueButton 4.1 services built so far, in dependency order: Postgres and
Redis, the database bootstrap Job, akka-apps, then the GraphQL stack (Hasura with its metadata
Job, graphql-actions, graphql-middleware), then bbb-web and the front nginx with the html5
client and the learning dashboard, the public HTTPS entry point, and the media path: LiveKit,
bbb-webrtc-sfu (LiveKit module only) and bbb-webrtc-recorder, and the recording processing with
playback (presentation format).

## Prerequisites

- A cluster: [doks.md](doks.md) creates one on DigitalOcean, [k3s.md](k3s.md) runs everything
  on one machine for local tests.
- `kubectl` and `helm` (v3) pointing at the cluster.
- A storage class named `do-block-storage-retain` (DigitalOcean Kubernetes ships it), or set
  `postgres.storageClass`, `redis.storageClass`, `bbbData.storageClass` and
  `webrtcRecorder.storageClass`.

### Node roles

Workloads are placed by node role (spec D1). A node carries one `bbb-k8s/role` label, so label
one node per role; to put several roles on one node, point their `*.role` values at the same
label value (`clusters/k3s-local.yaml` does that for a single node).

```bash
kubectl label node <node> bbb-k8s/role=storage
kubectl label node <node> bbb-k8s/role=media
kubectl label node <node> bbb-k8s/role=control
kubectl label node <node> bbb-k8s/role=data
```

Postgres and Hasura run on `data`; Redis, akka-apps, graphql-actions and graphql-middleware on
`control`; bbb-web, the front nginx, bbb-webrtc-recorder and the recording processing on
`storage`, where the `bbb-data` and `bbb-recorder` volumes are attached; livekit-server (host
network) and bbb-webrtc-sfu on `media`. Labels set with `kubectl label` are lost when the managed cluster replaces a node (upgrade, pool change):
re-apply them, or set them as node pool labels in the cloud provider.

## Images

Every BigBlueButton component runs the official image the monorepo publishes
(`docker/README.md` there): `ghcr.io/bigbluebutton/bigbluebutton/bbb-<target>`, at the immutable
per-commit tag in `officialImages.tag` (`<branch>-<sha7>`, e.g. `v4.1.x-develop-5b80a5b`), never
the moving branch tag. Postgres, Redis and LiveKit are their upstream images. The images are
public. The chart adds only deployment configuration: the files mounted at the bare-metal paths,
init containers for what a package postinst does on a volume, and the Service names.

Some chart files must match the images: the client settings, the recording config, the LiveKit
config, the nginx location files and the schema sha. `scripts/sync-upstream.sh` writes them all
from `bbb.sourceSha`. To move to a new commit:

```bash
BBB_REPO=/path/to/bigbluebutton scripts/sync-upstream.sh <commit>
# then set officialImages.tag to <branch>-<sha7> of the same commit, and review the diff
```

## Install

Two paths. Production: your own name and certificate, installed in one step. Lab: iMDT's test
domain, whose resolver derives the name from the load balancer IP. Both run `create-secrets.sh`
first (namespace `bbb` and the Secrets).

### Production: your own domain and certificate

Choose the public name first (e.g. `bbb.example.org`) and have a certificate for it (full chain
and key, PEM). The chart does not need the name to resolve to install: bbb-web needs it on the
first `create` (it downloads `https://<publicHost>/default.pdf`), and so does `helm test`.

```bash
TLS_CERT=fullchain.pem TLS_KEY=privkey.pem scripts/create-secrets.sh   # namespace bbb + Secrets
helm upgrade --install bbb chart -n bbb --reset-values -f clusters/<cluster>.yaml \
  --set publicHost=bbb.example.org        # or publicHost in clusters/<cluster>.yaml
PUBLIC_HOST=bbb.example.org scripts/public-host.sh   # prints the A record to create, waits for it
kubectl -n bbb get pods -w
helm test bbb -n bbb                      # once public-host.sh printed publicHost=...
```

`public-host.sh` waits for the IP of the load balancer and prints the record to create,
`bbb.example.org -> <IP>`, then waits until public DNS answers it. Create the record right away,
before anything else asks for the name: resolvers cache a missing name for the negative TTL of
the zone. Instead of a manual record, [external-dns](https://github.com/kubernetes-sigs/external-dns)
can publish it from an annotation on the Service, in `clusters/<cluster>.yaml` (Helm merges it
with the provider annotations of `values.yaml`):

```yaml
nginx:
  service:
    annotations:
      external-dns.alpha.kubernetes.io/hostname: bbb.example.org
```

`public-host.sh` asks `1.1.1.1` by default; `DNS_SERVER=` uses the system resolver instead, for
networks that block outbound DNS. A new certificate is a re-run of `create-secrets.sh` with the
new files.

### Lab: iMDT's test domain

For iMDT's own test clusters only. `bbb.imdt.dev` has a resolver that answers
`<prefix>-ip-<IP with dashes>.bbb.imdt.dev` with that IP, and iMDT publishes a `*.bbb.imdt.dev`
wildcard certificate with its private key, so whoever installs this way shares that key: never
use it for production. The name depends on the IP, which only exists once the chart created the
load balancer, so the install has two steps:

```bash
TLS_SOURCE=imdt-test scripts/create-secrets.sh   # downloads the wildcard from a.bbb.imdt.dev
# 1. any name, to get the load balancer created
helm upgrade --install bbb chart -n bbb --reset-values -f clusters/<cluster>.yaml \
  --set publicHost=pending.invalid
IP_DOMAIN=bbb.imdt.dev scripts/public-host.sh    # prints publicHost=bbbk8s-ip-<IP>.bbb.imdt.dev
# 2. write that name as publicHost in clusters/<cluster>.yaml, then from now on:
helm upgrade --install bbb chart -n bbb --reset-values -f clusters/<cluster>.yaml
```

`PREFIX` (default `bbbk8s`) sets the label before `-ip-`.

### Both paths

`publicHost` has no default: the chart refuses to render without it.

`create-secrets.sh` generates the database passwords (`bbb-db`), the shared secret
(`bbb-salt`) and the Hasura admin secret (`bbb-hasura`) once and keeps them on later runs; the
TLS Secret (`bbb-tls`, a plain `kubernetes.io/tls`) is refreshed every run, from `TLS_CERT`/
`TLS_KEY` or, with `TLS_SOURCE=imdt-test`, from iMDT's lab wildcard. Without either it stops
before changing anything. The images are public: there is no pull Secret. Nothing secret lives
in this repository.

Expected order: `postgres-0` and `redis-0` start, the `bbb-bootstrap-<hash>` Job creates the
roles and databases, and `akka-apps` stays in `Init` (container `wait-bootstrap`) until
`bbb_graphql` carries the marker of the current schema, then starts. `hasura` waits for the
same marker and applies the metadata of its image before it opens 8085, the
`hasura-metadata-<hash>` Job then checks that the metadata is consistent, and
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
- `publicHost` must resolve to the load balancer IP on public DNS and be covered by the
  certificate in `bbb-tls`. `scripts/public-host.sh` checks the resolution (see "Install").
- bbb-web downloads `https://<publicHost>/default.pdf` when a meeting is created, and only
  accepts a certificate it trusts: one from a public authority, or one signed by the authority
  in `secrets.extraCA` (a Secret with `ca.crt`, also trusted by `helm test`; [k3s.md](k3s.md)
  uses it with a local authority). Cilium
  resolves a pod's connection to the load balancer IP inside the cluster, so the request
  reaches nginx from the bbb-web pod IP (allowed by the `bbb-web` egress rule).
- Changing `publicHost` restarts bbb-web (meetings in progress end) and nginx, through the
  `properties-sha` / `site-sha` pod annotations.
- Deleting the release or the namespace deletes the load balancer; a new install gets a new
  IP: update the A record of your name (with the lab domain, the name itself changes). A
  DigitalOcean reserved IP would keep it, but needs an API token.

## How bbb-web and the front nginx are wired

- **bbb-web** is the official `bbb-web` image: the package's exploded war started with the
  `bbb-web.service` command line, listening on 0.0.0.0:8090, heap from the image's
  `JDK_JAVA_OPTIONS`. A `setup` init container writes `/etc/bigbluebutton/bbb-web.properties`
  from the `bbb-web-config` ConfigMap plus `securitySalt` from `bbb-salt` (`bbbWeb.properties` in
  values adds lines) and creates the directory tree on `bbb-data`. The file is mounted by itself
  (subPath): the image's `/etc/bigbluebutton/nopdfmark.ps`, which the PDF page downscaler passes
  to ghostscript, has to stay visible. The image's conversion wrapper
  `/usr/share/bbb-web/run-in-systemd.sh` keeps only the timeout (no systemd in a container).
  Office documents are not converted (no LibreOffice, spec L5); PDF and images are.
- **nginx** is the official `bbb-nginx` image (the package site and location files) with this
  chart's TLS site over `sites-available/bigbluebutton` and the location files of
  `chart/files/nginx` over `/usr/share/bigbluebutton/nginx`: the image's own point at
  127.0.0.1, these at the Services (`bbb-web:8090`, `gql-middleware:8378`, `hasura:8085`,
  `livekit:7880`, `bbb-webrtc-sfu:3008`, `bbb-shared-notes:8787`, `bbb-webhooks:3005`). The
  image's Hasura balancer site is replaced by a comment (its `upstream hasura` would capture
  `http://hasura:8085`). It runs as 998 to read `bbb-data`, and serves the default assets,
  slides, recordings and dashboard reports from there. Its ingress is the public entry point below.
- **html5-client**, **learning-dashboard** and **playback** are the official static-app images
  (nginx on 8080) that `bbb-nginx` proxies to by these Service names (`static-apps.yaml`).

### API-Mate

The front nginx also serves **API-Mate** at `https://<publicHost>/api-mate/`, a small static page
to build and sign BigBlueButton API calls by hand. It is the upstream
[mconf/api-mate](https://github.com/mconf/api-mate) bundle (gh-pages, pinned to commit
`d058fb77e11113c39ae84b2e9c6831a794cf9bbe`), downloaded by an init container of the front nginx at pod
start and checked against its sha256 (`nginx.apiMate` in values; while it is enabled, the
`bbb-nginx-api-mate` NetworkPolicy lets that pod reach the internet on 443); it is not a BBB package
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
- Images: the official `bbb-webrtc-sfu` and `bbb-webrtc-recorder`, which the monorepo builds from
  their repositories at the tags its placeholders pin. The recorder handles SIGINT but not yet
  SIGTERM, so the chart stops it with SIGINT in a preStop hook.

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
recommended production setup), add an inbound rule for **UDP 16384-32768** (and **TCP 7881**
if LiveKit's default TCP port is ever used) to the firewall that covers the worker droplets,
or to a new firewall applied to the node pool's tag ([doks.md](doks.md), step 4). The lab
cluster's account owner has since added the UDP rule; the measurement above predates it and
was not repeated.

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
  official `bbb-record-and-playback` image (record-and-playback core with the presentation,
  video, notes and screenshare formats; the `steps` of `bigbluebutton.yml` run presentation
  only). A `setup` init container writes `bbb-web.properties` with `securitySalt` for the
  recording-ready callback (mounted by itself, next to the image's `bigbluebutton-release`) and
  creates the directory tree on `bbb-data`. The config is the package's `bigbluebutton.yml`
  (`chart/files/bigbluebutton.yml`) merged with Redis, `playback_host` = `publicHost` and the
  notes endpoint; it is mounted over the scripts' file and as the `recording.yml` override,
  because several scripts read the scripts' file directly.
- **Logs**: the worker and rap-starter log to stdout (without a systemd journal the
  record-and-playback logger falls back to stdout). The stage scripts keep their own
  files in `/var/log/bigbluebutton` (an emptyDir), e.g. `archive-<id>.log`,
  `presentation/process-<id>.log`: `kubectl -n bbb exec deploy/recording -c resque-worker -- ls /var/log/bigbluebutton`.
- **Playback**: the official `playback` image serves the player at `/playback/presentation/2.3`
  (bbb-nginx proxies to it); `/presentation/<id>` serves the published files from `bbb-data`.
- **Daily cleanup**: the `recording-cleanup` CronJob runs `cleanup.rb` from the same image.
- **Scope**: presentation only (spec L7). No caption inbox (L8). Recordings have no notes (L6):
  the notes endpoint of the recording config points at `shared-notes:8787`, while the Service is
  `bbb-shared-notes` and the recording pod has no egress to it; the archive step only warns when
  a meeting had notes. The
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

- **Hasura** is the official `bbb-graphql-server` image: the Community Edition engine of the
  package (taken from Hasura's `v2.49.3-ce` image), the defaults of the package's
  `hasura-config.env` (`hasura.env` in values adds to them), and the BigBlueButton metadata of the
  pinned commit, whose BigBlueButton source reads its URL from `HASURA_GRAPHQL_BBB_DATABASE_URL`.
  The auth hook is `akka-apps:8901/userInfo`, the action handler `gql-actions:8093`.
- **Metadata**: the image's entrypoint applies it on a temporary server before the engine starts,
  but `metadata apply` only warns on inconsistencies. The `hasura-metadata-<hash>` Job (same
  image, hasura-cli) waits for port 8085, reloads, and fails unless Hasura reports the metadata
  as consistent. Its name is derived from the schema sha and the image tag, so a new schema
  (bootstrap recreates `bbb_graphql`) or a new image re-runs it.
- **graphql-middleware** reads `/etc/bigbluebutton/bbb-graphql-middleware.yml` (a ConfigMap)
  over its packaged defaults: Service names instead of loopback, and Hasura directly on
  `hasura:8085` instead of the package's nginx load balancer on 8185. Its first hook is
  bbb-web.
- **graphql-actions** turns client mutations into Redis messages and trusts the session
  variables in the request, so only Hasura and the middleware may reach it.

## How the database bootstrap behaves

`chart/scripts/bootstrap.sh` replaces the postinst of `bbb-graphql-server` and
`bbb-shared-notes-server`, which drop the databases on every install. It runs on the Postgres
image; init containers copy `bbb_schema.sql` and `blocknote_schema.sql` from the official
graphql-server and shared-notes-server images.

- `bootstrap.schemaSha` in `chart/values.yaml` is written by `scripts/sync-upstream.sh` and is
  the only source of the schema marker. The Job refuses to run (`schema sha mismatch: image=...
  values=...`) if it differs from the sha256 of the copied `bbb_schema.sql`; the database is not
  touched in that case.
- `bbb_graphql` is dropped and recreated only when it is missing or its marker
  (`COMMENT ON DATABASE`, `schema-sha:<sha>`) differs. Otherwise the Job only re-asserts roles,
  passwords and grants. The marker is written last.
- `hasura_app` and `blocknote_app` are created when missing and never dropped;
  `blocknote_schema.sql` is loaded only when `blocknote_app` is created.
- The Job name is derived from the schema sha, the script, the Postgres image and the official
  image tag, so an upgrade that changes any of them creates a new Job instead of patching an
  immutable one.
- akka-apps carries the schema sha as a pod annotation: a new schema recreates akka-apps in the
  same upgrade, after the database. **A schema change ends every meeting in progress**: do it in
  a maintenance window. A new image tag with the same schema re-runs the bootstrap, which keeps
  `bbb_graphql` (`already at schema-sha:...: keeping it`).

## Checking an install

```bash
helm test bbb -n bbb
kubectl -n bbb logs api-smoke
```

The `api-smoke` test Pod (`chart/templates/tests/api-smoke.yaml`, script in
`chart/scripts/api-smoke.sh`) goes through the public host the way an integration does: the
API answers, then `create`, `join`, `getMeetingInfo`, `getRecordings` and `end` of a meeting of
its own (`helm-test-<timestamp>`, signed with the shared secret), and finally the html5 client
and the LiveKit signaling behind the front nginx answer 2xx. Each step prints `ok` or `FAIL`.
It never touches other meetings. The pod stays after every run (Completed or Error) until the
next `helm test` replaces it, so its output can always be read with
`kubectl -n bbb logs api-smoke`. Its NetworkPolicy only lets it reach the front nginx.

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

- akka-apps `/healthz` answers 503 in its first minute and whenever bbb-web is away; the
  probes are TCP only for that reason (`chart/templates/akka-apps.yaml`).
- One replica per service; restarting akka-apps ends the meetings it holds (spec L1, L2).
- Camera and screen sharing use the same LiveKit path but are not part of the validated set.
- The middleware waits for its Redis subscription at start and exits when it loses it (by
  design upstream), so a Redis restart restarts the middleware and drops the clients connected
  through it; they reconnect.
- Changing the middleware ConfigMap does not restart the middleware: restart it
  (`kubectl -n bbb rollout restart deploy/gql-middleware`).
