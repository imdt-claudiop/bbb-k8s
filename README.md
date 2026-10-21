# bbb-k8s

Kubernetes packaging for BigBlueButton: a Helm chart that runs the official BigBlueButton
container images, and the database bootstrap. One branch per BigBlueButton version line; `main` is the **4.1** line.

Phase 1 is a lift-and-shift: one pod per service, the same binaries as a package install, and
only the changes each service needs to run outside a single machine. Changes to BigBlueButton
itself go upstream as pull requests; this repository only holds the packaging.

| Path | Contents |
|---|---|
| `chart/` | Helm chart (`appVersion` = BigBlueButton line) |
| `images/` | The images this repository built before the switch to the official ones; unused by the chart |
| `scripts/` | Cluster helpers (`create-secrets.sh`, `public-host.sh`, `sync-upstream.sh`) |
| `docs/` | Install and operation runbooks |
| `.github/workflows/` | Static checks: helm lint, kubeconform, shellcheck |

Current state: Postgres, Redis, the database bootstrap Job, akka-apps, the GraphQL stack
(Hasura, graphql-actions, graphql-middleware), bbb-web and the front nginx with the html5
client, published through a DigitalOcean LoadBalancer, audio through LiveKit, and recording
with playback (presentation format). `helm test` checks an install through the API. See
[docs/install.md](docs/install.md).

## Images

The components run the images the BigBlueButton monorepo publishes,
`ghcr.io/bigbluebutton/bigbluebutton/bbb-<target>`, at the per-commit tag in
`officialImages.tag` of `chart/values.yaml`. The chart files that must match them (client
settings, recording config, LiveKit config, nginx location files, schema sha) come from the same
commit through `scripts/sync-upstream.sh`; see docs/install.md "Images".
