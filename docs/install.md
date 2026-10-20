# Installing bbb-k8s (phase 1 skeleton)

This installs the BigBlueButton 4.1 services built so far, in dependency order: Postgres and
Redis, the database bootstrap Job, akka-apps, then the GraphQL stack (Hasura with its metadata
Job, graphql-actions, graphql-middleware), then bbb-web and the front nginx with the html5
client and the learning dashboard. Later slices add the public HTTPS entry point, LiveKit and
the SFU, and recording.

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
`control`; bbb-web and the front nginx on `storage`, where the `bbb-data` volume
(`/var/bigbluebutton`) is attached. Labels set with
`kubectl label` are lost when the managed cluster replaces a node (upgrade, pool change):
re-apply them, or set them as node pool labels in the cloud provider.

## Install

```bash
GHCR_USER=<user> GHCR_TOKEN=<token> scripts/create-secrets.sh   # namespace bbb + Secrets
helm upgrade --install bbb chart -n bbb --reset-values
kubectl -n bbb get pods -w
```

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

## Reaching the client (until the public entry point exists)

Every client and API URL derives from `publicHost` (`bigbluebutton.web.serverURL`), and the
client always uses `https`/`wss` on port 443 of the page's host. Until the public entry point
exists, `publicHost` is `bbbk8s-ip-127-0-0-1.bbb.imdt.dev`, which public DNS resolves to
127.0.0.1, and the front nginx terminates TLS for it. Forward port 443 on the machine with the
browser:

```bash
sudo kubectl -n bbb port-forward svc/bbb-nginx 443:443
# then open https://bbbk8s-ip-127-0-0-1.bbb.imdt.dev/ or a join URL
```

If something already listens on 443 there (another BigBlueButton), run the port-forward and the
browser inside a container, which has its own loopback.

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
  It has no NetworkPolicy ingress allow yet (nothing in the cluster calls it); port-forward
  does not go through NetworkPolicy.

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
- The default presentation is missing: bbb-web downloads `${serverURL}/default.pdf`, and
  inside its pod the provisional host resolves to 127.0.0.1, so the download fails and meetings
  start with no slides. Fixed by the public entry point.
- No audio or video (LiveKit comes later); the audio dialog can be closed.
- The middleware panics whenever a Redis receive fails (`internal/websrv/rediscli.go`
  dereferences the nil message): an init container holds it until Redis answers at start, but
  a Redis restart still makes it crash and restart until Redis is back (observed: 3 restarts,
  then Running). Clients connected through it reconnect.
- Changing the middleware ConfigMap does not restart the middleware: restart it
  (`kubectl -n bbb rollout restart deploy/gql-middleware`).
