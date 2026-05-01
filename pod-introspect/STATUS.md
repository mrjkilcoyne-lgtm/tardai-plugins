# pod-introspect — STATUS

**State**: shipped.
**Confirm tier**: OFF (read-only-cluster + none → OFF).

## RBAC
Dedicated ServiceAccount `tool-pod-introspect` with Role bound to the
`tardai` namespace ONLY. Verbs: get, list, watch on pods, pods/log,
events. Plus get on `metrics.k8s.io/v1beta1` pods (for `top`).

NO cluster-wide read. NO write verbs.

## Implementation
Talks to in-cluster k8s API at `https://kubernetes.default.svc` using
the SA token mounted at `/var/run/secrets/kubernetes.io/serviceaccount/token`.
No `kubectl` binary in the image — smaller, faster, fewer attack surfaces.

## Actions
- `describe` — pod phase, node, container statuses, conditions, startTime
- `logs` — last N lines (default 100), optional container filter
- `events` — namespace events, optionally filtered by pod
- `top` — CPU/memory per container (requires metrics-server; degrades gracefully)

## Gaps
- No support for previous-container logs (`previous=true`) — could add.
- No streaming logs.
- Other namespaces blocked by RBAC (intentional).
