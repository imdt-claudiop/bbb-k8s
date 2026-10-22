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
  then apply `config/crd` and `config/manager`. No published image yet.

What the operator provides per CellSet, in its namespace: the frozen values ConfigMap
(`spec.version.valuesRef`, key `values.yaml`, without per-cell values), the TLS Secret
(`spec.host.tlsSecretRef`), the balancer credential (`spec.balancer.secretRef`, key
`loadbalancer-secret`) and, with recording delivery, the SSH key
(`spec.recordingSecretRef`). Per cell, BEFORE scaling up: the node pool labelled
`bbb-k8s/cell=<status.nextCell>` plus one `bbb-k8s/role` per node, tainted
`bbb-k8s/cell=<n>:NoSchedule` (docs/doks.md, "Several cells").
