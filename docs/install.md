# Installing bbb-k8s (phase 1 skeleton)

This installs the first BigBlueButton 4.1 services in dependency order: Postgres and Redis,
the database bootstrap Job, then akka-apps. Later slices add bbb-web, Hasura, the GraphQL
middleware and actions, LiveKit and the SFU, recording and the nginx entry point.

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

In this slice Postgres runs on `data`, Redis and akka-apps on `control`. Labels set with
`kubectl label` are lost when the managed cluster replaces a node (upgrade, pool change):
re-apply them, or set them as node pool labels in the cloud provider.

## Install

```bash
GHCR_USER=<user> GHCR_TOKEN=<token> scripts/create-secrets.sh   # namespace bbb + Secrets
helm upgrade --install bbb chart -n bbb --reset-values
kubectl -n bbb get pods -w
```

`create-secrets.sh` generates the database passwords (`bbb-db`) and the shared secret
(`bbb-salt`) once and keeps them on later runs; the pull Secret (`ghcr-pull`) is refreshed
every run. Nothing secret lives in this repository.

Expected order: `postgres-0` and `redis-0` start, the `bbb-bootstrap-<hash>` Job creates the
roles and databases, and `akka-apps` stays in `Init` (container `wait-bootstrap`) until
`bbb_graphql` carries the marker of the current schema, then starts.

**Always pass `--reset-values` (or your own `-f values.yaml`) to `helm upgrade`.** A plain
`helm upgrade` with no values flags reuses the values of the previous release, so a one-off
`--set` (for example a wrong `bootstrap.schemaSha`) silently stays in place.

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
- Hasura metadata is not applied yet (Hasura slice).
