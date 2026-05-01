# pkg/plugin

Shared scaffold for TARDAI Tool Bus plugins.

Each plugin's `cmd/<id>/main.go` constructs a `Server`, fills its
`Manifest`, sets `PlanFn` (optional, for two-phase tools) and `InvokeFn`,
optionally `HealthExtra`, then calls `Run(":8000")`.

The library handles:

- Bearer auth (env `BEARER`) on `/plan` and `/invoke`
- `/healthz` (always 200, with optional extras)
- Manifest registration with the Tool Bus on startup, with retry/backoff
  (30 attempts, 2s apart)
- Audit log push to the artefact surface for every plan/invoke
- Status-coded errors via `plugin.Errorf(status, format, args...)`

Env vars consumed by `New()`:

- `BEARER` (required, fatal if absent)
- `BUS_BASE` (default `http://tardai-tool-bus.tardai.svc.cluster.local:8000`)
- `ARTEFACT_BASE` (default `http://tardai-artefacts.tardai.svc.cluster.local:8000`)

The plugin's own `SELF_BASE` is plugin-specific — set in each `main.go`
since it appears in the Manifest as the public endpoint URL.
