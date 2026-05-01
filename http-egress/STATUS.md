# http-egress — STATUS

**State**: shipped, allowlist enforced from ConfigMap or fallback default.
**Confirm tier**: ON (write-external + none → ON per sovereign matrix).

## Allowlist source
- Primary: ConfigMap `tardai-tool-bus-policy` key `egress-allowlist`, mounted
  at `/etc/tardai-policy/egress-allowlist`. Edit the ConfigMap to add hosts;
  pod re-reads on every request (no restart needed).
- Fallback: hardcoded default if ConfigMap absent (9 hosts: github, stripe,
  cal, vercel, cloudflare, civo, slack hooks, anthropic, openai).

## Hardening
- Host check is exact match (no wildcard, no suffix). Sub-paths under an
  allowlisted host are allowed.
- Auth/cookie/api-key headers redacted in /plan output for confirm display.
- Response body truncated at 100kB.
- `follow_redirects=False` — explicit redirects must be re-requested so the
  allowlist is reapplied per hop.

## Gaps / known limits
- No per-host rate limit; only global plugin rate limit (60/min).
- No request signing / mTLS (relies on sovereign's bearer for inbound auth).
- Body size cap is enforced only on response, not request.
