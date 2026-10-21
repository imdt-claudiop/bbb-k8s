# Running on a single machine with k3s (local tests)

A whole bbb-k8s install on one Linux machine, with [k3s](https://k3s.io) as the cluster, to try
the chart without a cloud account. Every command below was run as written on Ubuntu 24.04
(16 vCPU, 32 GB RAM, k3s v1.36.5+k3s1), followed by `helm test` and a recorded meeting with two
participants in audio. It is a test setup: one node, a certificate from a local authority, and
media reachable only from where the machine's IP is.

## What the machine needs

- Linux with `curl`, `openssl`, `kubectl` (k3s brings one) and `helm` (v3).
- About 6 vCPU and 8 GiB of memory free for the pods (their requests add up to 6 vCPU and
  7.5 GiB).
- Free ports on the machine: TCP 80 and 443 (the front nginx), TCP 7880 and 7881 and UDP
  16384-32768 (LiveKit, on the host network). Another web server or BigBlueButton on the same
  machine takes them.
- An IP that the browsers can reach; below `10.111.12.105`, replace it with yours everywhere.

## 1. Install k3s

Without Traefik: the chart brings its own nginx on 80 and 443.

```bash
curl -sfL https://get.k3s.io -o k3s-install.sh
sudo INSTALL_K3S_EXEC="--disable traefik" sh k3s-install.sh
mkdir -p ~/.kube
sudo cat /etc/rancher/k3s/k3s.yaml > ~/.kube/k3s-local && chmod 600 ~/.kube/k3s-local
export KUBECONFIG=~/.kube/k3s-local
kubectl get nodes              # one node, Ready
kubectl get storageclass       # local-path (default)
```

k3s answers a `LoadBalancer` Service with its own load balancer (ServiceLB) on the node's
ports, uses `local-path` volumes, and runs flannel, without Cilium: the k3s values set
`livekit.networkPolicy: plain`, so the LiveKit network policies are plain NetworkPolicies by
`livekit.nodeIP`.

## 2. Label the node and write the values

One node carries every role:

```bash
kubectl label node <node name> bbb-k8s/role=all
```

`clusters/k3s-local.yaml` points every role at `all`, every volume at `local-path`, and LiveKit
at the machine. Copy it and replace `10.111.12.105` (in `publicHost` and `livekit.nodeIP`). The
public name comes from [sslip.io](https://sslip.io), a public DNS service that answers
`<anything>.<IP with dashes>.sslip.io` with that IP, so no DNS record is needed:
`bbb.10-111-12-105.sslip.io`. LiveKit's TCP fallback moves to 7881, because 443 of the node
belongs to the front nginx.

## 3. Create a local certificate authority and the certificate

bbb-web downloads the default presentation from the public name and only accepts a certificate
it trusts, so a plain self-signed certificate is not enough: a meeting starts without its
slides and `helm test` fails. Create a small authority, sign the certificate with it, and give
the authority to the chart (`secrets.extraCA`):

In a directory outside the repository (the key must never be committed):

```bash
mkdir -p ~/bbb-local-ca && cd ~/bbb-local-ca
H=bbb.10-111-12-105.sslip.io
openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj "/CN=bbb-k8s local CA" \
  -keyout ca.key -out ca.crt
openssl req -newkey rsa:2048 -nodes -subj "/CN=$H" -keyout privkey.pem -out req.csr
printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\n' "$H" > ext.cnf
openssl x509 -req -in req.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 365 \
  -extfile ext.cnf -out cert.pem
cat cert.pem ca.crt > fullchain.pem
```

Keep `ca.key` private: anything it signs is trusted wherever `ca.crt` is.

## 4. Install

From the repository:

```bash
TLS_CERT=~/bbb-local-ca/fullchain.pem TLS_KEY=~/bbb-local-ca/privkey.pem scripts/create-secrets.sh
kubectl -n bbb create secret generic bbb-local-ca --from-file=ca.crt=$HOME/bbb-local-ca/ca.crt
helm upgrade --install bbb chart -n bbb --reset-values -f clusters/k3s-local.yaml
kubectl -n bbb get pods -w       # until every pod is Running (the two Jobs: Completed)
helm test bbb -n bbb
```

The first install pulls every image: allow a few minutes (a reinstall with the images already
pulled was ready in about two minutes).

## 5. Join a meeting

Create a meeting and join it with an API-Mate outside the cell (install.md, "API-Mate"), with
`server=https://bbb.10-111-12-105.sslip.io/bigbluebutton/`; that path was not exercised here
(the validation signed the API calls with a script). The browser does not know the local authority: import `ca.crt` as a trusted
authority in the browser or the operating system, or accept the certificate warning. The
validation here ran its browsers with certificate errors ignored; importing the authority into
a browser was not exercised.

## Limits

- One node, no redundancy; volumes live on the machine's disk (`local-path`).
- Media reaches LiveKit at `livekit.nodeIP`: browsers on other machines need that IP and UDP
  16384-32768 (or TCP 7881) reachable; a firewall on the machine was not part of this setup.
- sslip.io is a third-party service. Another name works if it resolves to the machine's IP
  both for the browsers and inside the cluster (bbb-web resolves it through the cluster DNS,
  which forwards to the machine's resolver), for example an A record; not validated here.

### Alternative: a certificate from a public authority

If you own a domain, you can point a name at the machine's IP and use a certificate from a
public authority for it (for example Let's Encrypt with a DNS challenge, since the machine is
usually not reachable from the internet), then install without `secrets.extraCA`. **Not
validated here.**

## Removing it

`helm uninstall bbb -n bbb` and `kubectl delete namespace bbb` remove the install (install.md,
"Uninstall and cleanup"; `local-path` volumes are deleted with their claims). k3s itself ships
`/usr/local/bin/k3s-uninstall.sh` (not run here).
