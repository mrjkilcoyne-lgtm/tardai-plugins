#!/usr/bin/env bash
# Deploy all 7 Tier 1 plugins. Idempotent — re-running re-applies manifests.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PLUGINS=(
  http-egress
  mempalace-read
  mempalace-search
  pod-introspect
  time-sense
  cost-sense
  mandate-tracking
)

echo "==> applying policy ConfigMap"
kubectl apply -f "$ROOT/http-egress/k8s/configmap-allowlist.yaml"

echo "==> applying pod-introspect RBAC"
kubectl apply -f "$ROOT/pod-introspect/k8s/rbac.yaml"

for p in "${PLUGINS[@]}"; do
  echo "==> deploying $p"
  kubectl apply -f "$ROOT/$p/k8s/service.yaml"
  kubectl apply -f "$ROOT/$p/k8s/deployment.yaml"
done

echo "==> waiting for rollouts"
for p in "${PLUGINS[@]}"; do
  kubectl -n tardai rollout status "deploy/tool-$p" --timeout=180s || \
    echo "WARN: tool-$p did not reach ready in 180s — check kubectl describe"
done

echo "==> done. Pods:"
kubectl -n tardai get pods -l tardai.tool=true
