#!/bin/bash
# Keep the DigitalOcean LoadBalancer of svc/bbb-nginx, and with it the IP and the public host,
# when the release or the namespace is deleted: once disowned, the cloud controller no longer
# changes or deletes it. Prints the annotation that makes the next install take it over
# (docs/install.md "Keeping the public IP"). Run it right before the teardown: a disowned
# LoadBalancer stops following node changes. Delete it by hand when it is no longer needed.
#
# Env: NS (default bbb).
set -euo pipefail
NS=${NS:-bbb}

id=$(kubectl -n "$NS" get svc bbb-nginx -o jsonpath='{.metadata.annotations.kubernetes\.digitalocean\.com/load-balancer-id}')
[ -n "$id" ] || { echo "svc/bbb-nginx carries no load-balancer-id yet" >&2; exit 1; }
ip=$(kubectl -n "$NS" get svc bbb-nginx -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
kubectl -n "$NS" annotate svc bbb-nginx --overwrite service.kubernetes.io/do-loadbalancer-disown=true >/dev/null

cat <<EOF
LoadBalancer $id ($ip) disowned: deleting svc/bbb-nginx now leaves it in place.
To change your mind: kubectl -n $NS annotate svc bbb-nginx service.kubernetes.io/do-loadbalancer-disown-
Before the next install, add to clusters/<cluster>.yaml:
nginx:
  service:
    annotations:
      kubernetes.digitalocean.com/load-balancer-id: $id
EOF
