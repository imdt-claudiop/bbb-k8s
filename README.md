# bbb-k8s

Kubernetes packaging for BigBlueButton: a Helm chart that runs the official BigBlueButton
container images, and the database bootstrap. One branch per BigBlueButton version line; `main` is the **4.1** line.

Phase 1 is a lift-and-shift: one pod per service, the same binaries as a package install, and
only the changes each service needs to run outside a single machine. Changes to BigBlueButton
itself go upstream as pull requests; this repository only holds the packaging.

| Path | Contents |
|---|---|
| `chart/` | Helm chart (`appVersion` = BigBlueButton line) |
| `controller/` | The cell controller: CRD `CellSet` + operator ([controller/README.md](controller/README.md)) |
| `scripts/` | Cluster helpers (`create-secrets.sh`, `public-host.sh`, `sync-upstream.sh`) |
| `docs/` | Phase 1 and 2 specifications, install and operation runbooks |
| `.github/workflows/` | Static checks: helm lint, kubeconform, shellcheck, controller tests |

Current state: one complete BigBlueButton 4.1 cell per release, one release per namespace: Postgres,
Redis, the database bootstrap Job, akka-apps, the GraphQL stack (Hasura, graphql-actions,
graphql-middleware), bbb-web and the front nginx with the html5 client, shared notes, the
annotation export, webhooks, audio through LiveKit (bbb-webrtc-sfu, bbb-webrtc-recorder), and
recording with playback (presentation format), behind one public host on a `LoadBalancer`
Service. Several cells share a cluster on their own nodes ([docs/install.md](docs/install.md),
"Several cells in one cluster"). `helm test` checks an install through the API. What runs, the
invariants it depends on, the decisions behind it and its known limitations are in
[docs/phase1.md](docs/phase1.md). To install, see [docs/install.md](docs/install.md); for the cluster itself,
[docs/doks.md](docs/doks.md) (DigitalOcean) or [docs/k3s.md](docs/k3s.md) (one machine, local
tests).

## Roadmap

Three phases:

1. **One cell on Kubernetes** ([docs/phase1.md](docs/phase1.md)).
2. **Multiple cells** ([docs/phase2.md](docs/phase2.md)): the chart installs once per
   namespace, and the `CellSet` controller adds and retires cells behind the balancer the
   integrator already uses (Scalelite in the PoC) - `kubectl scale cellset` is the knob
   ([controller/README.md](controller/README.md)).
3. **Architectural improvements, fault tolerance and scale.**

Phase 2 is additive: a phase 1 install - one release, by hand, no controller - keeps working
exactly as before (a single-cell cluster does not even set the `cell` value). The controller
and the multi-cell values only enter when a cluster runs more than one cell.

## Images

The components run the images the BigBlueButton monorepo publishes,
`ghcr.io/bigbluebutton/bigbluebutton/bbb-<target>`, at the per-commit tag in
`officialImages.tag` of `chart/values.yaml`. The chart files that must match them (client
settings, recording config, LiveKit config, nginx location files, schema sha) come from the same
commit through `scripts/sync-upstream.sh`; see docs/install.md "Images".
