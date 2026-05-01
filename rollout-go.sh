#!/usr/bin/env bash
# Roll the 10 Tool Bus plugins from Python to Go, one at a time.
# Verifies each via the Bus's manifest endpoint after rollout.
set -euo pipefail

NS=tardai
PLUGINS=(
  self-artefact-read
  self-artefact-list
  tool-bus-introspect
  http-egress
  mempalace-read
  mempalace-search
  pod-introspect
  time-sense
  cost-sense
  mandate-tracking
)

# Map plugin id to k8s deployment name.
deploy_name() {
  case "$1" in
    tool-bus-introspect) echo "tool-tool-bus-introspect";;
    *) echo "tool-$1";;
  esac
}

BEARER=$(kubectl -n $NS get secret tardai-auth -o jsonpath='{.data.bearer}' | base64 -d)

# Start a port-forward to the Tool Bus once.
kubectl -n $NS port-forward svc/tardai-tool-bus 18080:8000 >/tmp/pf-bus.log 2>&1 &
PF=$!
trap "kill $PF 2>/dev/null || true" EXIT
sleep 3

bus_has_tool() {
  curl -fsS -H "Authorization: Bearer $BEARER" \
    http://127.0.0.1:18080/api/tools/manifest \
    | python3 -c "import sys,json; tools=json.load(sys.stdin); ids=[t.get('id') for t in tools]; sys.exit(0 if '$1' in ids else 1)"
}

bus_invoke() {
  local id="$1"; local body="$2"
  curl -fsS -X POST -H "Authorization: Bearer $BEARER" \
    -H "Content-Type: application/json" \
    --data "$body" \
    "http://127.0.0.1:18080/api/tools/$id/invoke"
}

for id in "${PLUGINS[@]}"; do
  dep=$(deploy_name "$id")
  img="ghcr.io/mrjkilcoyne-lgtm/tardai-plugins-$id:latest"
  echo "===== rolling $id (deployment: $dep) ====="

  # Capture old image for rollback
  old_img=$(kubectl -n $NS get deploy/$dep -o jsonpath='{.spec.template.spec.containers[0].image}')
  echo "  old image: $old_img"

  if ! kubectl -n $NS set image deploy/$dep "$id=$img" 2>/dev/null; then
    # Container name may differ; fall back to "app"
    kubectl -n $NS set image deploy/$dep "app=$img"
  fi

  # Drop resources to the Go profile
  kubectl -n $NS set resources deploy/$dep \
    --requests=cpu=5m,memory=5Mi \
    --limits=cpu=100m,memory=32Mi

  # Force pull of :latest
  kubectl -n $NS rollout restart deploy/$dep

  if ! kubectl -n $NS rollout status deploy/$dep --timeout=90s; then
    echo "  ROLLOUT FAILED — rolling back to $old_img"
    kubectl -n $NS set image deploy/$dep "app=$old_img" || true
    kubectl -n $NS rollout status deploy/$dep --timeout=60s || true
    continue
  fi

  # Re-registration is async; wait up to 30s
  ok=0
  for i in {1..15}; do
    if bus_has_tool "$id"; then ok=1; break; fi
    sleep 2
  done
  if [ "$ok" = "0" ]; then
    echo "  WARN: $id not in bus manifest after 30s"
  else
    echo "  $id present in bus manifest"
  fi

  echo
done

echo "===== final state ====="
kubectl -n $NS top pods 2>/dev/null || echo "metrics-server not ready"
kubectl -n $NS top nodes 2>/dev/null || true
echo
echo "bus manifest tool count:"
curl -fsS -H "Authorization: Bearer $BEARER" \
  http://127.0.0.1:18080/api/tools/manifest \
  | python3 -c "import sys,json; t=json.load(sys.stdin); print(len(t)); print(' '.join(sorted(x.get('id','?') for x in t)))"
