# bbb-k8s phase 1: specification

## 0. Conventions

- **Audience**: whoever operates this chart or builds on it (phase 2 included) without the
  conversations that produced it. Install steps live in [install.md](install.md); this document
  states what the system is, what must hold, and why.
- **State described**: repository `main` with `chart/values.yaml` at `bbb.sourceSha`
  `5b80a5ba64442b43246e4708cecd751b905d91a4` and `officialImages.tag`
  `v4.1.x-develop-5b80a5b` (`chart/values.yaml:5,11`). When those values move, the facts below
  that cite upstream code are to be re-checked against the new commit.
- **Normative language**: the key words MUST, MUST NOT, SHOULD, SHOULD NOT and MAY are to be
  interpreted as described in BCP 14 (RFC 2119, RFC 8174) when, and only when, they appear in
  all capitals.
- **Citations**: `path:line` is a file of this repository; `upstream:path:line` is
  `bigbluebutton/bigbluebutton` at `bbb.sourceSha`.
- **Identifiers**: decisions are `D<n>` (section 14), limitations `L<n>` (section 15). The
  chart's comments and install.md cite them by these identifiers.
- **Glossary**: *cell*, one complete BigBlueButton deployment (one release in one namespace,
  one public host); *pin*, the pair `bbb.sourceSha` and `officialImages.tag`; *schema marker*,
  the `schema-sha:<sha>` comment on the `bbb_graphql` database.

## 1. Purpose, scope and non-goals

Purpose: run one BigBlueButton 4.1 cell on Kubernetes from the official container images
(`ghcr.io/bigbluebutton/bigbluebutton/bbb-<target>`), with only the deployment configuration a
service needs to run outside a single machine (README.md, "Phase 1 is a lift-and-shift").

In scope: the Helm chart (`chart/`), the cluster helpers (`scripts/`), per-cluster values
(`clusters/`), and the runbooks (`docs/`).

Non-goals of phase 1:

- more than one cell, or routing meetings between cells (phase 2, a separate effort);
- high availability or fault tolerance: every workload is a single replica (D9, L2);
- autoscaling;
- telephony and the FreeSWITCH/mediasoup media path: media is LiveKit only (L4);
- changes to BigBlueButton itself, which go upstream as pull requests (README.md).

## 2. Context

```
 browser (html5 client)  ── HTTPS 443 / WSS ──▶  public host ── TCP LB ──▶  cell (namespace bbb)
 browser media           ── UDP 16384-32768, TCP fallback ──▶  media node (LiveKit, host network)
 API integrator          ── HTTPS /bigbluebutton/api (checksum) ──▶  public host
 operator                ── kubectl / helm ──▶  cluster API
 cell pods               ── pull ──▶  ghcr.io official images, livekit/livekit-server, postgres, redis
```

Legend: `──▶` is the direction of the connection; the targets on the right are the cell's entry
points (sections 3 and 9).

External dependencies: a DNS name for the public host that resolves to the load balancer IP,
a TLS certificate for it (`bbb-tls`), a cluster with a `LoadBalancer` implementation and a
storage class for ReadWriteOnce volumes (install.md, "Prerequisites").

## 3. Containers

Rendered from `helm template bbb chart -n bbb -f clusters/doks-hackweek.yaml --api-versions
cilium.io/v2/CiliumNetworkPolicy`. Every workload has exactly one replica.

| Workload (kind) | Image | Node role | Ports | Role in the cell |
|---|---|---|---|---|
| postgres (StatefulSet) | `postgres:16` | data | 5432 | `bbb_graphql`, `hasura_app`, `blocknote_app` |
| redis (StatefulSet) | `redis:7.4` | control | 6379 | message bus and durable queues (section 4) |
| bbb-bootstrap-&lt;hash&gt; (Job) | `postgres:16` + init from graphql-server, shared-notes-server | any | - | roles, databases, schema marker (section 11) |
| akka-apps | `bbb-apps-akka` | control | 8901 | meeting state machine |
| hasura | `bbb-graphql-server` | data | 8085 | GraphQL engine over `bbb_graphql` |
| hasura-metadata-&lt;hash&gt; (Job) | `bbb-graphql-server` | any | - | fails unless the metadata is consistent |
| gql-actions | `bbb-graphql-actions` | control | 8093 | client mutations to Redis |
| gql-middleware | `bbb-graphql-middleware` | control | 8378 | client GraphQL connections |
| bbb-web | `bbb-web` | storage | 8090 | API, presentations, recording trigger |
| bbb-nginx | `bbb-nginx` | storage | 8443, 8080 | TLS, routing, auth_request |
| html5-client, learning-dashboard, playback | `bbb-<name>` | control | 8080 | static apps behind bbb-nginx |
| bbb-shared-notes | `bbb-shared-notes-server` | control | 8787 | shared notes |
| bbb-export-annotations | `bbb-export-annotations` | storage | - | annotated PDF export |
| bbb-webhooks | `bbb-webhooks` | control | 3005 | hooks API and callbacks |
| livekit (host network) | `livekit/livekit-server:v1.9.12` | media | 7880, 7881 or 443 TCP, UDP 16384-32768 | WebRTC media |
| bbb-webrtc-sfu | `bbb-webrtc-sfu` | media | 3040, 3008 | LiveKit module only (L11) |
| bbb-webrtc-recorder | `bbb-webrtc-recorder` | storage | - | records tracks of recorded meetings |
| recording (2 containers) | `bbb-record-and-playback` | storage | - | rap-starter and resque workers |
| recording-cleanup (CronJob) | `bbb-record-and-playback` | storage | - | `cleanup.rb` at 03:00 (`recordingCleanup.schedule`) |
| api-smoke (helm test Pod) | `bbb-apps-akka` | any | - | end-to-end API check (section 13) |

Images without a registry prefix are `officialImages.registry` with `officialImages.tag`
(`chart/templates/_helpers.tpl:18-20`). The only Service of type `LoadBalancer` is `bbb-nginx`
(443, 80); the others are ClusterIP.

## 4. State and data

| State | Where | Durable | Owner |
|---|---|---|---|
| `bbb_graphql` | Postgres volume (10Gi) | no: derived from akka-apps, recreated when the schema marker changes (section 11) | bootstrap Job |
| `hasura_app`, `blocknote_app` (notes) | Postgres volume | yes: created when missing, never dropped (`chart/scripts/bootstrap.sh:5-6`) | bootstrap Job |
| Redis data | Redis volume (5Gi), AOF (`chart/templates/redis.yaml:43`) | yes: resque and export queues, recording events, LiveKit routing, registered hooks | redis |
| presentations, recordings, published playback | `bbb-data` PVC, 20Gi, ReadWriteOnce, `/var/bigbluebutton` | yes | uid/gid 998 |
| raw recorder tracks | `bbb-recorder` PVC, 20Gi, ReadWriteOnce | until archived (the archive step deletes them, install.md "Recording and playback") | uid/gid 998 |
| secrets | `bbb-db`, `bbb-salt`, `bbb-hasura`, `bbb-livekit`, `bbb-tls`, optional `secrets.extraCA` | created once by `scripts/create-secrets.sh` (TLS refreshed every run) | operator |

The default storage class is `do-block-storage-retain` (`chart/values.yaml`, `storageClass`
keys): deleting the release keeps the cloud volumes (install.md, "Uninstall and cleanup").

## 5. Invariants

Each invariant is a rule the code imposes; breaking it breaks the cell.

- **I1. A restart of akka-apps or bbb-web MUST be treated as ending every meeting in progress.**
  akka-apps marks every previous meeting as ended when it starts
  (`upstream:akka-bbb-apps/src/main/scala/org/bigbluebutton/core/BigBlueButtonActor.scala:74-79`,
  `setAllMeetingsEnded`). bbb-web writes the `.done` files that start recording processing
  (`upstream:bbb-common-web/src/main/java/org/bigbluebutton/api/service/impl/RecordingServiceFileImpl.java:119-153`);
  a meeting that ends while bbb-web is away is not queued. Verify: end of section 12 lists the
  changes that restart them.
- **I2. `bbb_graphql` MUST be recreated only when the schema marker differs.** A recreation
  MUST be followed by a restart of akka-apps. `chart/scripts/bootstrap.sh:61-69` drops and
  recreates only on a marker mismatch; akka-apps carries the schema sha as a pod annotation
  (`chart/templates/akka-apps.yaml:78`), so a new schema restarts it in the same upgrade.
  Verify: `kubectl -n bbb logs job/<bbb-bootstrap-…>` prints `already at schema-sha:…: keeping
  it` when the schema did not change.
- **I3. A cell MUST NOT share its Postgres, its Redis or its public host with another cell.**
  Two akka-apps on one database end each other's meetings (I1); the Redis channel names are
  fixed defaults (`upstream:akka-bbb-apps/src/main/scala/org/bigbluebutton/SystemConfiguration.scala:86`),
  so two akka-apps on one Redis would both handle every message; the client derives the LiveKit
  and notes URLs from the page's host
  (`upstream:bigbluebutton-html5/imports/ui/components/livekit/primary-room/component.tsx:197`,
  `upstream:bigbluebutton-html5/imports/ui/components/bn-shared-notes/hooks.ts:65`). Verify: one
  release per namespace, `publicHost` unique per release.
- **I4. Every pod that writes `bbb-data` or `bbb-recorder` MUST run as uid/gid 998.** The
  directories belong to `bigbluebutton` (998), and bbb-web, nginx, recording,
  export-annotations and the recorder read each other's files (`chart/templates/webrtc-recorder.yaml:38-40`,
  install.md "How shared notes and the annotation export are wired"). Verify: `kubectl -n bbb get pods -o
  jsonpath='{..securityContext.runAsUser}'`.
- **I5. Every BigBlueButton image MUST be the immutable per-commit tag in
  `officialImages.tag`, the same for all of them, never the moving branch tag.** Moving the pin
  is a new release of the cell and MUST repeat the validation of section 13. Verify: section 10.
- **I6. A change to a running cell MUST have its rollback revision known before it is
  applied.** Verify: `helm -n bbb history bbb` names the deployed revision before `helm
  upgrade`; section 12 has the rollback.

## 6. Cell isolation

A cell owns, and MUST NOT share (I3): its namespace, its Postgres StatefulSet and volume, its
Redis StatefulSet and volume, its `bbb-data` and `bbb-recorder` volumes, its secrets, its
`publicHost` and load balancer, and its media node's LiveKit (host network, one LiveKit per
node because of the fixed ports). Inside the namespace a default-deny NetworkPolicy admits only
the listed flows (`chart/templates/networkpolicy.yaml`, D6). The chart renders one cell per
release; it does not route meetings between cells.

## 7. Interfaces

- **Public host** (`publicHost`, required, `chart/templates/_helpers.tpl:63`): every client
  and API URL derives from it (`bigbluebutton.web.serverURL`). It MUST resolve to the load
  balancer IP and MUST be covered by `bbb-tls`.
- **BigBlueButton API**: `https://<publicHost>/bigbluebutton/api/<call>?<query>&checksum=<sha1>`,
  signed with `bbb-salt`; webhooks at `/bigbluebutton/api/hooks/*` through `bbb-webhooks:3005`.
- **Paths behind bbb-nginx**: `chart/files/nginx/*.nginx`, the official location files with
  the Service names of this chart (`scripts/sync-upstream.sh:44-80`).
- **Values an operator sets per cluster**: `publicHost`, `livekit.nodeIP`,
  `livekit.config` (e.g. `rtc.ips.includes`, `rtc.tcp_port`), the `role` and `storageClass`
  keys, `nginx.service`, `secrets.extraCA` (`clusters/*.yaml` are examples).
- **Values an operator MUST NOT edit by hand**: `bbb.sourceSha`, `officialImages.tag` and
  `bootstrap.schemaSha` move together through `scripts/sync-upstream.sh` (section 10), and
  `chart/files/*` is its output.

## 8. Runtime flows

- **Create and join**: the API call reaches bbb-web through bbb-nginx; bbb-web downloads
  `https://<publicHost>/default.pdf` through the public host and only accepts a certificate it
  trusts (install.md, "Public entry point"; D15); akka-apps holds the meeting; the client
  connects to gql-middleware and Hasura through bbb-nginx (`/graphql`).
- **Media**: bbb-webrtc-sfu requests tokens for akka-apps; the client connects to LiveKit at
  `wss://<publicHost>/livekit` for signaling and to the media node for media (D4).
- **Recording**: bbb-webrtc-recorder joins the LiveKit room and writes tracks to
  `bbb-recorder`; when the meeting ends bbb-web writes the `.done` file, `rap-starter` enqueues
  the archive job in Redis, and the workers archive, process and publish the presentation
  format into `bbb-data` (install.md, "Recording and playback").
- **Notes and annotations**: install.md, "How shared notes and the annotation export are
  wired".

## 9. Deployment

- **Placement** (D1): `nodeSelector` `bbb-k8s/role=<role>` from the `role` keys
  (`chart/templates/_helpers.tpl:12-15`, `nodeRoleLabel`). A node carries one value; several
  roles share a node by pointing their `role` keys at the same value.
- **Entry** (D12): `bbb-nginx` Service `LoadBalancer`, `externalTrafficPolicy: Local`
  (`chart/values.yaml:141`), TLS terminated by nginx; no Ingress.
- **Media network** (D4): LiveKit with `hostNetwork: true` (`chart/templates/livekit.yaml:59`).
- **Network policy** (D6, D14): default-deny plus named allows; LiveKit's flows by Cilium node
  entities when the cluster has Cilium, by `livekit.nodeIP` otherwise
  (`chart/templates/networkpolicy.yaml:216,277`).
- **Clusters described**: DigitalOcean ([doks.md](doks.md), `clusters/doks-hackweek.yaml`) and
  a single k3s machine ([k3s.md](k3s.md), `clusters/k3s-local.yaml`).

## 10. Image policy

- The images are those of `officialImages.registry` at `officialImages.tag`
  (`<branch>-<sha7>`), plus `postgres:16`, `redis:7.4` and `livekit/livekit-server:v1.9.12`
  (D11).
- Moving the pin MUST go through `BBB_REPO=<clone> scripts/sync-upstream.sh <commit>`, which
  writes `bbb.sourceSha`, `bootstrap.schemaSha` and the upstream files of `chart/files/`, with
  `officialImages.tag` moved to the same commit in the same change (`scripts/sync-upstream.sh:13-15`).
- The tag MUST exist for every image before the move:
  `docker manifest inspect ghcr.io/bigbluebutton/bigbluebutton/bbb-<target>:<tag>`.
- Jobs whose template carries the tag are named from it (D17), so a pin move creates new Jobs.

## 11. Bootstrap and schema

`bbb-bootstrap-<hash>` (`chart/templates/bootstrap-job.yaml`, `chart/scripts/bootstrap.sh`):

- MUST refuse to run when the sha256 of the image's `bbb_schema.sql` differs from
  `bootstrap.schemaSha` (`chart/scripts/bootstrap.sh:23-24`); the database is untouched.
- Recreates `bbb_graphql` only when its marker differs or it is missing, and writes the marker
  last (`chart/scripts/bootstrap.sh:61-86`).
- Its name hashes the schema sha, the script, the Postgres image and the official image tag
  (`chart/templates/_helpers.tpl:22-27`): Job templates are immutable, so any change creates a
  new Job; a re-run with an unchanged marker keeps the database (I2).
- akka-apps waits for the marker in an init container (`wait-bootstrap`,
  `chart/templates/_helpers.tpl:39`) and starts after it.

## 12. Operations

- **Install**: [install.md](install.md), "Install" (production path or lab path).
- **Upgrade**: `helm upgrade --install bbb chart -n bbb --reset-values -f
  clusters/<cluster>.yaml`. `--reset-values` (or an explicit `-f`) MUST be passed: a plain
  upgrade reuses the previous release's values.
- **Before any upgrade** (I6): record `helm -n bbb history bbb --max 1`.
- **Rollback**: `helm -n bbb rollback bbb <revision> --wait`. If the rollback fails on a Job
  (`field is immutable`), delete the Completed Jobs named in the error (they only re-run
  checks or the idempotent bootstrap) and roll back again.
- **Changes that end meetings in progress** (I1): any change to akka-apps or bbb-web pods
  (image, config, `publicHost`, a new schema), a Redis restart (it restarts gql-middleware and
  drops its clients, install.md "Known limitations of this slice"). These SHOULD run in an
  agreed maintenance window.
- **Diagnosis**: `helm test bbb -n bbb` then `kubectl -n bbb logs api-smoke` (section 13);
  per-component logs with `kubectl -n bbb logs deploy/<name>`.

## 13. Validation

A cell is accepted when every criterion holds:

| Criterion | Evidence |
|---|---|
| The chart lints and renders valid manifests | CI `.github/workflows/chart.yaml`: `helm lint --strict`, render, kubeconform, shellcheck |
| Every pod is Running (the Jobs Completed) | `kubectl -n bbb get pods` |
| The API, a meeting lifecycle, the client and LiveKit signaling answer through the public host | `helm test bbb -n bbb` passes; `kubectl -n bbb logs api-smoke` prints only `ok` lines (`chart/scripts/api-smoke.sh`) |
| Two browsers join one meeting with audio | `getMeetingInfo` reports `participantCount` 2 and `voiceParticipantCount` 2 |
| The default presentation loads | the slide shows in the client; bbb-web logs no `SSLHandshakeException` |
| A recorded meeting is published and plays | `getRecordings` reports `published` with 2 participants; the playback URL plays and shows the chat |
| Webhooks answer | `hooks/create`, `hooks/list`, `hooks/destroy` return `SUCCESS` through the public host |

A pin move (I5) MUST also pass: an upgrade from the previous revision, a rollback to it, and
the upgrade forward again, each with `helm test` passing.

## 14. Decisions

Each decision: context, decision, status, consequences. A superseded decision keeps its
identifier.

- **D1 Placement by node role.** Context: volumes and the hottest query path bind components
  together. Decision: four roles (`storage`, `media`, `control`, `data`) by node label. Status:
  accepted. Consequences: a missing label leaves pods Pending; labels set with `kubectl label`
  are lost when a managed node is replaced (L13).
- **D2 Shared volumes between pods on one node.** Context: bbb-web, nginx, recording,
  export-annotations and the recorder share files and inotify triggers. Decision: two
  ReadWriteOnce volumes (`bbb-data`, `bbb-recorder`) used only by pods on the `storage` node.
  Status: accepted (cloud block volumes, not local PVs). Consequences: no ReadWriteMany; all
  writers run as 998 (I4).
- **D3 Single Redis with AOF on a volume.** Context: Redis holds queues and recording events,
  not only messages. Decision: one Redis, append-only file, no password, reachable only through
  NetworkPolicy. Status: accepted. Consequences: a Redis restart restarts gql-middleware.
- **D4 Media on the node's network.** Context: LiveKit announces the address it binds.
  Decision: host network on the `media` node, `livekit.nodeIP` per cluster, UDP
  16384-32768, ICE-TCP fallback on `rtc.tcp_port` (443 on DOKS, 7881 on a single machine).
  Status: accepted (the TCP fallback replaced the original media-TCP-off, L10). Consequences:
  one LiveKit per node; the firewall MUST admit the UDP range for clients behind strict NATs.
- **D5 TLS at an Ingress.** Status: superseded by D12.
- **D6 bbb-nginx stays the authentication layer.** Context: notes and the SFU trust the headers
  bbb-nginx injects after `auth_request`. Decision: everything public goes through bbb-nginx;
  default-deny NetworkPolicy protects the surfaces without authentication (graphql-actions,
  the notes loopback, akka `/analytics`). Status: accepted.
- **D7 Secrets in Secrets.** Decision: `bbb-db`, `bbb-salt`, `bbb-hasura`, `bbb-livekit`,
  `bbb-tls`, created by `scripts/create-secrets.sh`; the client `settings.yml` is a ConfigMap
  read by akka-apps and bbb-web. Status: accepted.
- **D8 Idempotent bootstrap Job** instead of the package postinst, which drops the databases.
  Status: accepted. Consequences: section 11.
- **D9 One replica, `Recreate`, for every stateful or singleton workload.** Context: a new
  akka-apps starting before the old one stops ends all meetings (I1). Status: accepted.
  Consequences: L1, L2.
- **D10 TCP probes on singletons.** Context: akka-apps `/healthz` answers 503 in its first
  minute and while bbb-web is away (`chart/templates/akka-apps.yaml:105-106`); as liveness it
  would end meetings. Status: accepted.
- **D11 Official images at a per-commit tag.** Context: the upstream monorepo publishes every
  component as an image tagged `<branch>-<sha7>`. Decision: run those images, pinned (I5),
  with the matching upstream files synced by `scripts/sync-upstream.sh`. Status: accepted.
  Consequences: no image is built here; a needed image change is an upstream pull request.
- **D12 TCP load balancer to bbb-nginx, TLS in nginx, no Ingress.** Context: bbb-nginx already
  routes every path and authenticates. Decision: Service `LoadBalancer` in TCP mode with
  `externalTrafficPolicy: Local`. Status: accepted, supersedes D5. Consequences: the client IP
  reaches nginx; the certificate is a plain `kubernetes.io/tls` Secret.
- **D14 LiveKit policies follow the network plugin.** Context: a host-network pod cannot be
  selected by a plain NetworkPolicy; not every cluster runs Cilium. Decision:
  CiliumNetworkPolicies by node entity when the API has `cilium.io/v2/CiliumNetworkPolicy`,
  plain NetworkPolicies by `livekit.nodeIP` otherwise. Status: accepted. Consequences: on
  clusters without Cilium the rule is exact only where pods reach the media node at
  `livekit.nodeIP`; `helm template` needs `--api-versions` to render the Cilium variant.
- **D15 Optional extra certificate authority.** Context: bbb-web and `helm test` call the
  public host and refuse an untrusted certificate. Decision: `secrets.extraCA` names a Secret
  whose `ca.crt` both trust; empty by default. Status: accepted. Consequences: a local test can
  use a certificate from its own authority ([k3s.md](k3s.md)).
- **D16 The cell is the unit of isolation** (I3). Status: accepted. Consequences: scaling past
  one cell is a new design (phase 2), not more replicas.
- **D17 Job names hash their template's inputs.** Context: a Job's pod template is immutable.
  Decision: `bbb-bootstrap-<hash>` and `hasura-metadata-<hash>` include the image tag and the
  schema sha. Status: accepted. Consequences: a pin move re-runs both Jobs.

(D13 is not assigned.)

## 15. Limitations and known debt

- **L1** A restart of akka-apps, bbb-web, LiveKit, the SFU or the recorder interrupts meetings
  or recordings (I1).
- **L2** One replica per service; no high availability.
- **L3** The `storage` and `data` workloads are bound to their volumes' node; moving them to a
  replacement node was not exercised.
- **L4** No telephony.
- **L5** Office documents are not converted (no LibreOffice); PDF and images are.
- **L6** Recordings have no notes: the recording config points at `shared-notes:8787` and the
  recording pod has no egress to the notes Service (`chart/templates/recording.yaml:8`).
- **L7** Only the presentation recording format runs.
- **L8** No caption inbox.
- **L9** No live transcription.
- **L10** Lifted: media has a TCP fallback (D4).
- **L11** bbb-webrtc-sfu logs "Reconnecting to MCS" every 2 seconds (no mediasoup module); expected.
- **L12** Ejecting a user from the media path is not covered by a test.
- **L13** Node labels set with `kubectl label` are lost on node replacement.
- **L14** Camera and screen sharing use the LiveKit path but are not part of the validated set
  (section 13).
- **L15** external-dns publication of the public host was rendered, not run against a provider.
- **L16** Scale limits per cell (meetings, participants) were not measured.
