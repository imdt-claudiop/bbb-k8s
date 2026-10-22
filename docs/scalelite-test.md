# A test Scalelite for the phase 2 PoC

The controller's only balancer implementation is Scalelite (1.5.1+, the Servers API), and the
recording delivery needs its spool. A production integrator brings their own; a test cluster
can run one inside itself with the manifests in [scalelite-test/](scalelite-test/): the
official images (`blindsidenetwks/scalelite`), their own Postgres and Redis, an SSH ingest for
the recording spool, and the nginx front with the playback player. Everything runs on the
shared services pool (doks.md, "The shared services pool").

This is a TEST bench: one replica of everything, basic-auth-less Redis, and the playback
player copied from the official `bbb-playback` image. Not production guidance.

## 1. Namespace, secrets and keys

```bash
kubectl create namespace scalelite
# App secrets.
kubectl -n scalelite create secret generic scalelite-secrets \
  --from-literal=secret-key-base=$(openssl rand -hex 64) \
  --from-literal=loadbalancer-secret=$(openssl rand -hex 24) \
  --from-literal=postgres-password=$(openssl rand -hex 16)
# TLS for the public host (any certificate that covers it; the lab wildcard works).
kubectl -n scalelite create secret tls scalelite-tls --cert=<fullchain.pem> --key=<privkey.pem>
# The spool SSH ingest: a host key (pinned by clients), the client keypair's public half.
ssh-keygen -q -t ed25519 -N '' -f spool_id       # the CELLS' key: private half goes to the CellSet
ssh-keygen -q -t ed25519 -N '' -f spool_hostkey
kubectl -n scalelite create secret generic spool-ssh-hostkey --from-file=ssh_host_ed25519_key=spool_hostkey
kubectl -n scalelite create secret generic spool-ssh-authorized --from-file=authorized_keys=spool_id.pub
```

## 2. The LoadBalancer first (the host derives from its IP on the lab resolver)

```bash
kubectl apply -f docs/scalelite-test/scalelite.yaml   # after the sed of step 3 if you prefer one pass
kubectl -n scalelite get svc scalelite-nginx -w       # until EXTERNAL-IP; that IP names the host
# Lab resolver: the host is scalelite-ip-<IP with dashes>.bbb.imdt.dev. Your own DNS: any name
# that resolves to the IP and is covered by the TLS Secret.
```

## 3. Fill the placeholders and apply the app

```bash
sed -i "s/SERVICES_POOL/<your services pool name>/" docs/scalelite-test/*.yaml
sed -i "s/SCALELITE_HOST/<the host from step 2>/" docs/scalelite-test/scalelite-app.yaml
kubectl apply -f docs/scalelite-test/scalelite.yaml -f docs/scalelite-test/scalelite-app.yaml
kubectl -n scalelite get pods    # api, poller, importer (2 containers), nginx, postgres, redis
```

## 4. Smoke

```bash
H=<the host>; S=$(kubectl -n scalelite get secret scalelite-secrets -o jsonpath='{.data.loadbalancer-secret}' | base64 -d)
curl -s "https://$H/bigbluebutton/api" | grep SUCCESS        # the BBB-facing API
CS=$(printf 'getServers%s' "$S" | sha256sum | awk '{print $1}')
curl -s "https://$H/scalelite/api/getServers?checksum=$CS"   # "No servers are configured" = healthy
```

## 5. What the CellSet consumes

- `spec.balancer.url`: `https://<the host>/scalelite/api`; `spec.balancer.secretRef`: a Secret
  in the CELLSET's namespace with that same `loadbalancer-secret` value.
- Recording delivery (the frozen values): spool host
  `spool@spool-ssh.scalelite.svc.cluster.local`, path `/var/bigbluebutton/spool`, port `2222`,
  `namespace: scalelite` (in-cluster spool; the namespace egress form).
- `spec.recordingSecretRef`: a Secret in the CellSet's namespace with key `id` = the
  `spool_id` PRIVATE key of step 1 (and optionally `known_hosts` pinning the host key as
  `[spool-ssh.scalelite.svc.cluster.local]:2222 <spool_hostkey.pub>`).
- The cells' frozen values must admit the balancer at their public entry:
  `nginx.clientNamespaces: [scalelite, ...]` - the poller times out otherwise.

The recording flow's "delivered" signal: the tar vanishes from the spool (the importer eats
it) and the recording answers on `getRecordings` of the Scalelite host, playback included.
