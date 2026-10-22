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
- Run locally against a kubeconfig (the PoC path):
  `go run ./cmd --chart ../chart --vpc-cidr <the nodes' VPC range>`.
- In-cluster: build the image at the REPO ROOT (`docker build -f controller/Dockerfile .`),
  push it PRIVATE to ghcr (`ghcr.io/imdt-claudiop/bbb-k8s-controller:<sha>`; the repository is
  private, so the image must be too), create the `ghcr-pull` docker-registry Secret in the
  controller namespace, then apply `config/crd` and `config/manager` (set the image tag and a
  nodeSelector for the cluster's shared-services pool). The controller reaches the cells'
  public hosts from inside the cluster, so each cell's frozen values must admit its namespace
  in `nginx.clientNamespaces` (e.g. `[scalelite, bbb-k8s-controller]`).

What the operator provides per CellSet, in its namespace: the frozen values ConfigMap
(`spec.version.valuesRef`, key `values.yaml`, without per-cell values), the TLS Secret
(`spec.host.tlsSecretRef`), the balancer credential (`spec.balancer.secretRef`, key
`loadbalancer-secret`) and, with recording delivery, the SSH key
(`spec.recordingSecretRef`). Per cell, BEFORE scaling up: the node pool labelled
`bbb-k8s/cell=<status.nextCell>` plus one `bbb-k8s/role` per node, tainted
`bbb-k8s/cell=<n>:NoSchedule` (docs/doks.md, "Several cells").

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
