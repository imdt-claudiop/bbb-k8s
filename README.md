# bbb-k8s

Kubernetes packaging for BigBlueButton: container images, a Helm chart and the database
bootstrap. One branch per BigBlueButton version line; `main` is the **4.1** line.

Phase 1 is a lift-and-shift: one pod per service, the same binaries as a package install, and
only the changes each service needs to run outside a single machine. Changes to BigBlueButton
itself go upstream as pull requests; this repository only holds the packaging.

| Path | Contents |
|---|---|
| `chart/` | Helm chart (`appVersion` = BigBlueButton line) |
| `images/` | One Dockerfile per component, built from the pinned monorepo commit by `images/build.sh` |
| `scripts/` | Cluster helpers (`create-secrets.sh`) |
| `docs/` | Install and operation runbooks |

Current state: Postgres, Redis, the database bootstrap Job, akka-apps, the GraphQL stack
(Hasura, graphql-actions, graphql-middleware), bbb-web and the front nginx with the html5
client, published through a DigitalOcean LoadBalancer, and audio through LiveKit. See
[docs/install.md](docs/install.md).

## Building images

```bash
BBB_REPO=/path/to/bigbluebutton images/build.sh bootstrap akka-apps
```

The script builds from `bbb.sourceSha` in `chart/values.yaml`, pushes to
`ghcr.io/imdt-claudiop/bbb-k8s/<component>`, and pins the new tags (and, for the bootstrap
image, `bootstrap.schemaSha`) in `chart/values.yaml`.
