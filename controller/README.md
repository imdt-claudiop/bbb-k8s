# The cell controller (phase 2, step 3)

A Kubernetes operator reconciling `CellSet` (`bbb-k8s.imdt.dev/v1alpha1`): a set of
BigBlueButton cells of one version behind one balancer. `spec.replicas` up creates cells
(numbered by a global counter that never reuses a number; `status.nextCell` tells the
operator which node pool to label next); down retires the cell with the fewest participants
(tie: the oldest) without ending meetings. Six phases per cell: Provisioning, Ready,
Registered, Retiring, Retired, Failed. The balancer is behind an interface
(`internal/balancer`); the only implementation is Scalelite (Servers API, >= 1.5.1).

The controller carries the chart of the commit it was built from (`--chart`) and refuses a
`spec.version.chart` it does not carry. Publishing charts to a registry (and consuming them
by version) is planned, not built.

- Build: `go build ./...` - test: `go test ./...` (envtest needs `KUBEBUILDER_ASSETS`, see
  the CI workflow; without it that one test skips).
- CRD and RBAC: `config/crd`, `config/manager` (regenerate with controller-gen, see CI).
- Copy-paste examples: `config/samples/` (a CellSet and its frozen values ConfigMap).
- Run locally against a kubeconfig (handy while developing):
  `go run ./cmd --chart ../chart --vpc-cidr <the nodes' VPC range>`.

## Deploying in-cluster

The image is PRIVATE (the repository is private; the code never goes to a public registry).
From the repo root:

```bash
# 1. Build and push the image, tagged by commit.
docker build -f controller/Dockerfile -t ghcr.io/imdt-claudiop/bbb-k8s-controller:<sha> .
docker login ghcr.io -u imdt-claudiop    # a PAT with write:packages
docker push ghcr.io/imdt-claudiop/bbb-k8s-controller:<sha>
# First push: confirm the package is PRIVATE (the GitHub packages page, or the API).

# 2. CRD, RBAC and the controller namespace.
kubectl apply -f controller/config/crd -f controller/config/manager

# 3. The pull Secret the manifest references.
kubectl -n bbb-k8s-controller create secret docker-registry ghcr-pull   --docker-server=ghcr.io --docker-username=imdt-claudiop --docker-password=<PAT with read:packages>

# 4. Pin the image tag and the cluster's shared-services pool (docs/doks.md, "The shared
#    services pool") - the controller must never land on cell nodes.
kubectl -n bbb-k8s-controller patch deploy bbb-k8s-controller --type merge -p '{
  "spec": {"template": {"spec": {
    "nodeSelector": {"doks.digitalocean.com/node-pool": "<services pool name>"},
    "containers": [{"name": "controller",
      "image": "ghcr.io/imdt-claudiop/bbb-k8s-controller:<sha>",
      "args": ["--vpc-cidr", "<the nodes VPC range, e.g. 10.116.0.0/20>"]}]
  }}}}'
kubectl -n bbb-k8s-controller get pods     # Running
```

Two prerequisites that bite when skipped:

- **`nginx.clientNamespaces` in the frozen values** must list the controller's namespace (and
  the balancer's, when it runs in-cluster): both reach the cells' public hosts from inside
  the cluster, where the chart's public ipBlock rule cannot admit them (`config/samples/values-configmap.yaml`).
- **The counter seed**: adopting a cluster whose cell history predates the controller, set
  the `bbb-k8s.imdt.dev/next-cell` annotation on the CellSet so deleted cells' numbers are
  never reused (`config/samples/cellset.yaml`).

## What the operator provides per CellSet

In the CellSet's namespace (`config/samples/` has the CellSet and ConfigMap):

```bash
kubectl create namespace cellsets
# The frozen values of the version (cell-agnostic; see config/samples/values-configmap.yaml).
kubectl -n cellsets apply -f controller/config/samples/values-configmap.yaml
# The TLS certificate covering every cell host, copied to each cell.
kubectl -n cellsets create secret tls bbb-wildcard-tls --cert=<fullchain.pem> --key=<privkey.pem>
# The balancer credential (scalelite: the LOADBALANCER_SECRET). Never copied to cells.
kubectl -n cellsets create secret generic scalelite-api --from-literal=loadbalancer-secret=<secret>
# With recording delivery: the SSH key the cells deliver with (key id; known_hosts optional).
kubectl -n cellsets create secret generic spool-ssh --from-file=id=<private key> --from-file=known_hosts=<known_hosts>
```

Per cell, BEFORE scaling up: the node pool labelled
`bbb-k8s/cell=<status.nextCell>` plus one `bbb-k8s/role` per node, tainted
`bbb-k8s/cell=<n>:NoSchedule` (docs/doks.md, "Several cells"). A test balancer for a cluster
without one: [docs/scalelite-test.md](../docs/scalelite-test.md).

## Upgrading (phase 2, step 4)

A cell is immutable: born with one version, dies with it. Changing version is a commit of
this repository (chart, synced upstream files, frozen values), never a `helm upgrade` of a
running cell. The upgrade flow, operator-driven as the spec defines it:

1. Deploy the controller built at the new commit (it carries the new chart). CellSets
   pinning an older `spec.version.chart` are then FROZEN FOR GROWTH - no new cells - but
   their existing cells still register, drain and retire (`VersionSupported: False` names
   it); a version whose change is only in the frozen values keeps the same chart and needs
   no controller swap.
2. Create a NEW CellSet with the new version (`spec.version.chart` + a new frozen values
   ConfigMap) against the same balancer, and scale it up (node pools first, as always).
3. When the new set has as many `Registered` cells as the old one, set the old set's
   `replicas: 0`: its cells go through Retiring one at a time (drain grace, zero meetings,
   recordings delivered, deregister, uninstall). The scale-to-zero refusal does not fire,
   because the balancer still has the new set's Registered cells; zeroing the LAST
   registered set remains refused.
4. Delete the old CellSet object once its cells are gone. Node pools are the operator's to
   reuse or remove (cell numbers are never reused; pools are relabelled for new numbers).

No meeting is ended by the process: a cell with meetings waits in Retiring until they finish.
