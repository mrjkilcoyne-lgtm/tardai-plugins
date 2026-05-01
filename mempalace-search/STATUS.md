# mempalace-search — STATUS

**State**: shipped, **needs-mempalace-api-spec**.
**Confirm tier**: OFF (read-only-external + none → OFF).

## Same gap as mempalace-read
The MemPalace HTTP API at `mempalace.substrates:8095` is undocumented.
This plugin will return 502 until either the API is documented or the
deployment exposes `/openapi.json`.

## Probe order
1. POST to `/api/search`, `/search`, `/palace/search`, `/api/semantic`
   with `{query, limit, tags}` body.
2. GET the same paths with `?q=...&limit=...` params.
3. Substring fallback: GET listing endpoint, score each memory by
   substring match count + tag overlap, return top-N.

## Substring scoring
- 1.0 per memory containing the query string.
- +0.1 per additional occurrence within that memory.
- +0.5 per matching tag.
- Memories with score 0 dropped.

## Owner action required
Same as `mempalace-read`. Once the API is known, edit SEARCH_PATHS /
LIST_PATHS in `app.py` to the canonical endpoints and remove the probe
fallback chain.
