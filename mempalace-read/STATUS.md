# mempalace-read — STATUS

**State**: shipped, **needs-mempalace-api-spec**.
**Confirm tier**: OFF (read-only-external + none → OFF).

## Critical gap
The MemPalace HTTP service at `mempalace.substrates.svc.cluster.local:8095`
responds to `/healthz` (200 OK) but every other path probed returns 404 —
including `/api`, `/api/memories`, `/memories`, `/palace`, `/recall`, `/search`,
`/list`, `/store`. **The API surface is undocumented.**

This plugin will return HTTP 502 with `error: MemPalace API surface not
discovered` until either:
1. The MemPalace deployment exposes a `/openapi.json` we can introspect, OR
2. Sovereign tells us the canonical endpoint paths and we update READ_PATHS.

## What it does try (in order)
- `/api/memories`
- `/api/memory`
- `/memories`
- `/palace/list`
- `/list`
- `/recall`

First 200 OK with parsable JSON wins. Response is normalised — accepts a
bare list, or a wrapper object with key `memories|results|items|entries`.

## Filtering
- Sends `q`, `category`, `key`, `limit` as query params (best-effort).
- Falls back to client-side filtering on the returned list.

## Owner action required
Sovereign or repo owner: document the MemPalace API or add a `/openapi.json`
to the mempalace deployment. Until then this plugin is a placeholder with
graceful failure.
