# mandate-tracking — STATUS

**State**: shipped. Companion seed file at `_meta/mandates/seed.yaml` ships
with this deployment to give the tool data on first invocation.
**Confirm tier**: OFF (read-only-cluster + none → OFF).

## Schema
Each `_meta/mandates/<id>.yaml` is expected to be:
```yaml
id: <slug>
title: <human title>
opened_at: <ISO timestamp>
deadline: <ISO timestamp, optional>
status: open|completed|blocked
blockers: [array of strings]
owner: <agent name, e.g. claude, sovereign, gemini>
```

## Discovery
The artefact surface listing endpoint is best-effort — the plugin tries
a couple of query-param shapes (`prefix=` and `path=`) and accepts both
list and dict responses with common key names (`files`, `paths`, `items`).

## Gaps
- If the artefact surface doesn't expose a listing endpoint at all, this
  plugin returns an empty list. The seed mandate file would still be
  unreachable until listing works.
- Owner action: confirm artefact surface listing API and harden the
  discovery path in `app.py`.

## Why read-only
Mandates are sovereign-state. Writing belongs in a separate tool with
write-internal blast radius and a confirm gate, e.g. a future
`mandate-update` plugin. Keeping this one read-only avoids accidental
status churn.
