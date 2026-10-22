# Creating the cluster on DigitalOcean (DOKS)

How the lab cluster (`clusters/doks-hackweek.yaml`) was created, from the DigitalOcean control
panel, up to the point where [install.md](install.md) takes over. The values below are the ones
that cluster runs (read from it with `kubectl`); a step marked **[confirm]** describes the
control panel from the account owner's account and was not checked against the panel itself.

## 1. Create the cluster

In the control panel: **Kubernetes** > **Create Cluster** [confirm].

| Setting | Lab cluster |
|---|---|
| Region | NYC1 |
| Kubernetes version | 1.37 (`v1.37.0`) |
| Node pool | one pool, 4 nodes of `s-4vcpu-8gb` (4 vCPU, 8 GB RAM, 160 GB disk) |
| Network plugin | Cilium, the DOKS default (`livekit.networkPolicy: cilium` in the values) |

Four nodes give each node role its own node (below). The stack requests about 6 vCPU and
7.5 GiB in total, so fewer, larger nodes also work, with several roles pointed at one label
value in the cluster values (`clusters/k3s-local.yaml` does that for a single node). Wait
until the nodes show as running [confirm].

## 2. Download the kubeconfig

From the cluster's page, download the config file [confirm: **Download Config File**, on the
cluster overview]. It holds a token for the cluster: keep it out of this repository.

```bash
export KUBECONFIG=~/.kube/<cluster>.yaml   # the downloaded file
kubectl get nodes -o wide                   # 4 nodes Ready; note each EXTERNAL-IP
kubectl get storageclass                    # do-block-storage (the chart's default) is there
```

## 3. Label the nodes

One role per node (install.md, "Node roles"). The lab cluster:

```bash
kubectl label node <node 1> bbb-k8s/role=storage
kubectl label node <node 2> bbb-k8s/role=media
kubectl label node <node 3> bbb-k8s/role=control
kubectl label node <node 4> bbb-k8s/role=data
```

Labels set this way are lost when DOKS replaces a node (upgrade, pool resize); re-apply them,
and update the media node's IP in the cluster values if that node changed.

## 4. Open the media ports in the firewall

DOKS manages a cloud firewall for the worker nodes. Measured on the lab cluster before any rule
was added: it admits only TCP 80 and 443 from outside (install.md, "What the DOKS firewall lets
through"). For WebRTC media add an inbound rule for **UDP 16384-32768**, sources all IPv4 and
IPv6, to the firewall that covers the workers [confirm: **Networking** > **Firewalls**, the
firewall DOKS created for the cluster, or a new one applied to the node pool's tag]. LiveKit
also accepts media over TCP 443 of the media node, which is already open.

## 5. Write the cluster values

Create `clusters/<cluster>.yaml`, as `clusters/doks-hackweek.yaml`:

- `livekit.nodeIP`: the EXTERNAL-IP of the node labelled `bbb-k8s/role=media`;
- `livekit.config.rtc.ips.includes`: that IP as `/32`, plus the VPC range of the nodes (the
  INTERNAL-IPs; `10.116.0.0/20` on the lab cluster), so LiveKit announces only addresses
  clients and the recorder can use;
- `publicHost`: after the install has created the load balancer (install.md, "Install").

Then follow [install.md](install.md) from "Install".

## Several cells

Each cell gets its own nodes, created before the cell is installed (install.md, "Several cells
in one cluster"). Labels and taints set on a node pool apply to every node of the pool and
survive node replacement, unlike `kubectl label` (section 3), so for cell `<n>` create one pool
of one node per role [confirm: **Add Node Pool** on the cluster's page, labels and taints in the
pool settings]:

| Pool | Labels | Taint |
|---|---|---|
| `cell-<n>-storage` | `bbb-k8s/cell=<n>`, `bbb-k8s/role=storage` | `bbb-k8s/cell=<n>:NoSchedule` |
| `cell-<n>-media` | `bbb-k8s/cell=<n>`, `bbb-k8s/role=media` | same |
| `cell-<n>-control` | `bbb-k8s/cell=<n>`, `bbb-k8s/role=control` | same |
| `cell-<n>-data` | `bbb-k8s/cell=<n>`, `bbb-k8s/role=data` | same |

```bash
kubectl get nodes -l bbb-k8s/cell=<n> -L bbb-k8s/role -o wide   # four nodes, one per role
```

The media ports need the firewall rule of section 4 for the new media node too: apply it by the
pool's tag when creating the pool. Write the cell's values as in section 5, with `cell: "<n>"`
and the EXTERNAL-IP of the cell's media node.
