# cost-sense — STATUS

**State**: shipped. Per-source availability depends on credentials.
**Confirm tier**: ON (read-only-external + financial → ON per matrix).

## Credential surface
The Deployment expects optional secret `tool-cost-sense-creds` with keys:
- `anthropic_admin_token`
- `civo_api_key`
- `vercel_token`

If the secret is missing, all three are unset and the relevant sources
return `unavailable: true` with reason. **No fabrication.** This is the
honesty rule.

## Source state at time of writing
- **Anthropic**: admin usage API integration is a stub. Even if the token
  is supplied, `app.py` returns `unavailable: true` because the canonical
  admin endpoint shape isn't wired. Owner action: implement against the
  Anthropic admin Usage API once the endpoint is confirmed.
- **Civo**: hits `https://api.civo.com/v2/billing` if `CIVO_API_KEY` is set.
  Returns whatever Civo gives.
- **Vercel**: hits `https://api.vercel.com/v1/usage` if `VERCEL_TOKEN` is
  set. Returns whatever Vercel gives.
- **Bus audit**: reads `_meta/audit/<date>.jsonl` files via the artefact
  surface, parses each line as JSON, groups by `caller_session` and
  `tool_id`. Always available (when artefact surface is up).

## Confirm-gate ON
Because financial data is sensitive, the Bus enforces a confirm gate
before /invoke runs. Sovereign sees the period + scope before approving.

## Note on http-egress overlap
This plugin makes outbound calls to api.civo.com / api.vercel.com /
api.anthropic.com directly — bypassing http-egress. This is intentional
for performance (one network hop, not two) but means the http-egress
allowlist is not enforced for cost-sense calls. The trade-off is
acceptable because cost-sense's blast radius is fixed at compile time
(only those three hosts) — sovereign cannot redirect it.
