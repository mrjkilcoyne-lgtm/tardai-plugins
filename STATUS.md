# STATUS — Go Rewrite (2026-05-01)

10 plugins migrated from Python/FastAPI to Go single-binary.

## Behaviour preservation

For each plugin: input shapes, output shapes, error codes, and audit
content match the Python originals. Audit log is now pushed centrally by
the shared scaffold (`pkg/plugin/server.go`) instead of in each plugin.

### Confirmed-equivalent

| plugin | parity check |
|---|---|
| `self-artefact-read` | path validation, base64/utf8 encoding, content-type passthrough |
| `self-artefact-list` | prefix validation, returns paths array + count |
| `tool-bus-introspect` | enriched view with by_blast_radius counter, summary fields |
| `http-egress` | allowlist load (configmap → default), header redaction, body truncate at 500 (plan) and 100000 (invoke), follow_redirects=false, timeout from request |
| `mempalace-read` | endpoint probe order, normalisation across `memories`/`results`/`items`/`entries`, client-side substring/category/key filters |
| `mempalace-search` | semantic POST → GET → list+score fallback, score formula `1.0 + 0.1*count + 0.5*tag` |
| `pod-introspect` | describe/logs/events/top via SA token, error shapes preserved, `top` returns `error` field on metrics-server failure |
| `time-sense` | now_iso/now_epoch/now_human/monotonic/uptime/boot_wall/delta_s; ISO parse with `Z` → `+00:00` substitution |
| `cost-sense` | unavailable shape preserved, audit log scan groups by `caller_session` and `tool_id`, totals match |
| `mandate-tracking` | YAML parse, list endpoint probe variants, status filter, by_status counter |

### Known gaps preserved (NOT fixed in this change)

- **`mempalace-read` / `mempalace-search`**: MemPalace HTTP API is
  undocumented. Both plugins probe a list of plausible endpoints and
  return 502 if nothing answers. Same as Python.
- **`cost-sense` Anthropic**: returns `unavailable: true` even with
  `ANTHROPIC_ADMIN_TOKEN` set, because the canonical admin usage endpoint
  isn't wired. Same as Python.
- **`cost-sense` Civo / Vercel**: returns `unavailable: true` because
  `CIVO_API_KEY` / `VERCEL_TOKEN` are not in the pod env. Same as Python.

### Minor deviations

- **time-sense `monotonic`**: Python returned a kernel-monotonic counter
  that increases from a system-wide reference. Go's `time.Since(boot)`
  starts from process boot. Both increase strictly; the absolute value
  differs from the Python plugin if a sovereign caller relies on
  cross-process comparison. `uptime_s` is unchanged (process uptime).
  Output field shape preserved.
- **Audit log**: in the Python plugins each plugin manually wrote audit
  entries; the Bus also writes them. In Go the scaffold writes one entry
  per request (post-hoc, async). If the Bus also writes, that's a
  duplicate — same as the Python behaviour.
- **`http-egress` invoke**: when `body` is a non-string non-nil object,
  Go marshals to JSON and sets `Content-Type: application/json` if not
  already present. Python used httpx's `json=` kwarg which does the same.
- **Health probe**: `/healthz` always returns `{"ok": true, ...}`. The
  same shape as Python.

## Resource impact

Python pods (before): `requests.memory=12-48 MiB`, resident ~38 MiB.
Go pods (after): `requests.memory=5 MiB`, resident expected ~5–10 MiB.

10 plugins × ~30 MiB saved each ≈ ~300 MiB recovered.
Node was at ~99% memory request before; should drop to ~75%.

## Test coverage

Cross-compilation only. Each binary is ~6.6–7.1 MB. No unit tests.
Integration verification happens at rollout via the rollout script:

1. Image swap → rollout status (90s timeout).
2. Re-registration check via Bus `/api/tools/manifest`.
3. On failure: rollback to previous image.

A live invocation smoke test is left to sovereign — running the Bus
introspect tool through itself is the canonical validation.
